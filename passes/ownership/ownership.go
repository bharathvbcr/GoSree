// Package ownership checks that memory handed to another goroutine is owned by the sender and not
// used by it afterwards, that package variables are not mutated across goroutines, and that
// //gosree:imm types are never written after construction.
//
// Rules:
//
//	SAFE002  a value crossing to another goroutine is not owned (it may alias a parameter, a package
//	         variable, or memory a callee kept)
//	SAFE003  a value is used after it was moved to another goroutine
//	SAFE004  a moved value also escapes somewhere else (stored, returned, retained by a callee)
//	SAFE005  a //gosree:imm type is malformed or written after construction
//	SAFE006  a package variable is written outside initialisation and not //gosree:guarded_by
//	SAFE008  a handler writes memory it shares with its other invocations (handlerwrite.go)
//	SAFE009  an access to //gosree:guarded_by memory without holding its mutex (lockset.go)
//	CAP003   a stack-scoped borrow (\mub or \rob) crosses a goroutine boundary
//
// A value crosses to another goroutine when it is an argument of a go statement, the closure a go
// statement starts, a callback handed to a function that runs it on another goroutine (spawn.go), a
// value sent on a channel, or an argument for a //gosree:iso parameter. A closure's captured variables
// cross only when the closure itself does.
package ownership

import (
	"go/token"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/kinds"
)

// Rule categories.
const (
	NotOwned  = "SAFE002"
	UseAfter  = "SAFE003"
	Escapes   = "SAFE004"
	Imm       = "SAFE005"
	GlobalUse = "SAFE006"
	// HandlerWrite is a write through memory a handler shares with its other invocations.
	HandlerWrite = "SAFE008"
	// GuardViolation is an access to //gosree:guarded_by memory without its mutex (lockset.go).
	GuardViolation = "SAFE009"
	// BorrowEscape is a stack-scoped borrow crossing a goroutine boundary.
	BorrowEscape = "CAP003"
	// UseAfterSynced is reported for SAFE003 uses that every path reaches only after a
	// synchronisation point that is not a join for the move (forkjoin.go): a receive from another
	// channel, a select, a mutex or cond, or a join shape the rule does not accept. The separate
	// category keeps that class measurable.
	UseAfterSynced = "SAFE003-forkjoin"
)

// TransferRelated is the message of the related position a finding carries when the transfer that
// caused it is elsewhere: a SAFE003 use, a SAFE008 write, a deferred use.
const TransferRelated = "the transfer that causes this finding"

// Analyzer is the ownership checker.
var Analyzer = &analysis.Analyzer{
	Name:      "safeownership",
	Doc:       "check moves of memory between goroutines, package-variable mutation, //gosree:imm types, handler writes, and borrow escapes (SAFE002-SAFE006, SAFE008, SAFE009, CAP003)",
	Run:       run,
	Requires:  []*analysis.Analyzer{annot.Analyzer, buildssa.Analyzer},
	FactTypes: []analysis.Fact{new(FuncFact), new(ImmFact)},
	// Stats is returned so harnesses can count escape hatches without parsing output.
	ResultType: reflect.TypeFor[*Stats](),
}

// Stats counts what the pass saw in one package. SuppressedTransfers / Transfers is how often the
// escape hatch was needed where memory crosses goroutines.
type Stats struct {
	// Transfers counts places a value crosses to another goroutine or to an owned parameter.
	Transfers int `json:"transfers"`
	// FlaggedTransfers counts transfers whose check produced at least one finding, muted or not, so
	// SuppressedTransfers <= FlaggedTransfers and Transfers - FlaggedTransfers are clean. A transfer
	// whose only findings were muted at their own line (a use after the move) is flagged but not
	// suppressed: the hatch is not on the transfer.
	FlaggedTransfers int `json:"flagged_transfers"`
	// SuppressedTransfers counts transfers inside a //gosree:unsafe scope that had a finding to mute.
	SuppressedTransfers int `json:"suppressed_transfers"`
	// Suppressed counts distinct findings (position and rule) of this pass muted by //gosree:unsafe.
	Suppressed int `json:"suppressed_findings"`
	// UnsafeScope counts the //gosree:unsafe directives in the package. A finding is charged to the
	// innermost scope around its own position, or, when the transfer that caused it is in a scope, to the
	// transfer's scope. Which scopes muted nothing is not counted here: other passes mute through the
	// same scopes, so only the stale analyzer (internal/annot/stale), which runs after every pass that
	// mutes, has the final answer.
	UnsafeScope int `json:"unsafe_directives"`
	// Guarded counts fields and package variables annotated //gosree:guarded_by, and Holds the
	// //gosree:holds directives. Like imm and iso they are checked claims, not escape hatches: neither
	// counts toward the hatch ratio or the override budget.
	Guarded int `json:"guarded_directives"`
	Holds   int `json:"holds_directives"`
	// Imm counts the //gosree:imm types of the package that satisfy the imm rules, and Iso the parameters
	// named by //gosree:iso. Both are checked claims too.
	Imm int `json:"imm_types"`
	Iso int `json:"iso_params"`
}

type state struct {
	pass    *analysis.Pass
	ix      *annot.Index
	ssa     *buildssa.SSA
	cls     *kinds.Classifier
	imm     map[*types.TypeName]bool
	sums    map[*ssa.Function]*summary
	stats   *Stats
	reports map[reportKey]bool
	// suppressed holds the findings muted by //gosree:unsafe, so each counts once in Stats.
	suppressed map[reportKey]bool
	// muted is set while checking a transfer that lies in a //gosree:unsafe scope: every finding it
	// produces is muted, wherever it is. mutedFindings counts findings muted that way.
	muted         bool
	mutedFindings int
	// findings counts every finding the pass produced, muted or not, before de-duplication; a
	// transfer whose check raised it is flagged.
	findings int
	// transferAt is the position of the transfer being checked, if any.
	transferAt token.Pos
	// graphs caches the alias graph of each function once summaries are final; the fork-join rule
	// reads a goroutine body's graph while checking its parent.
	graphs    map[*ssa.Function]*graph
	joinCache map[ssa.Instruction]map[ssa.Instruction]bool
	// localGuards maps the cells of guarded local variables (their Alloc, and the free variables of
	// closures that capture them) to their guard.
	localGuards    map[ssa.Value]localGuard
	guardSums      *guardState
	globalWrites   map[*ssa.Global][]token.Pos
	mutatedGlobals map[*ssa.Global]bool
	// immRets caches what each callee's single-result returns trace to (see immReturns).
	immRets map[*ssa.Function][]immRet
}

// graphOf returns fn's alias graph. Call it only after computeSummaries: graphs built during the
// summary fixpoint see provisional summaries.
func (st *state) graphOf(fn *ssa.Function) *graph {
	if g, ok := st.graphs[fn]; ok {
		return g
	}
	g := st.buildGraph(fn)
	st.graphs[fn] = g
	return g
}

type reportKey struct {
	pos   token.Pos
	cat   string
	extra string
}

func run(pass *analysis.Pass) (any, error) {
	st := &state{
		pass:    pass,
		ix:      pass.ResultOf[annot.Analyzer].(*annot.Index),
		ssa:     pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA),
		sums:    map[*ssa.Function]*summary{},
		stats:   &Stats{},
		reports: map[reportKey]bool{},

		suppressed: map[reportKey]bool{},

		graphs:    map[*ssa.Function]*graph{},
		joinCache: map[ssa.Instruction]map[ssa.Instruction]bool{},
	}
	if err := checkCallTables(pass.Pkg); err != nil {
		return nil, err
	}
	st.stats.UnsafeScope = st.ix.UnsafeScopes()
	st.stats.Guarded = len(st.ix.Guarded)
	st.stats.Holds = st.ix.HoldsDirectives()
	st.stats.Iso = len(st.ix.IsoVars)
	st.validateImm()
	for _, ok := range st.imm {
		if ok {
			st.stats.Imm++
		}
	}
	st.cls = st.newClassifier()

	fns := sourceFuncs(st.ssa)
	st.localGuards = st.guardedCells(fns)
	st.computeSummaries(fns)
	st.exportFacts(fns)
	st.collectGlobalMutations(fns)

	for _, fn := range fns {
		if len(fn.Blocks) == 0 {
			continue
		}
		g := st.graphOf(fn)
		st.checkTransfers(g)
		st.checkImmStores(fn)
	}
	st.checkSharedLoadWrites(fns)
	st.checkGlobals()
	st.checkGuarded(fns)
	return st.stats, nil
}

func (st *state) newClassifier() *kinds.Classifier {
	return kinds.NewClassifier(func(tn *types.TypeName) bool {
		if tn.Pkg() == st.pass.Pkg {
			return st.imm[tn]
		}
		var f ImmFact
		return st.pass.ImportObjectFact(tn, &f) && f.Imm
	})
}

func sourceFuncs(ssaRes *buildssa.SSA) []*ssa.Function {
	out := make([]*ssa.Function, 0, len(ssaRes.SrcFuncs)+4)
	seen := make(map[*ssa.Function]bool, len(ssaRes.SrcFuncs)+4)
	var add func(fn *ssa.Function)
	add = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		out = append(out, fn)
		for _, anon := range fn.AnonFuncs {
			add(anon)
		}
	}
	for _, fn := range ssaRes.SrcFuncs {
		add(fn)
	}
	if pkgInit := ssaRes.Pkg.Func("init"); pkgInit != nil {
		for _, anon := range pkgInit.AnonFuncs {
			add(anon)
		}
	}
	return out
}

func (st *state) ensureGuardSummaries(fns []*ssa.Function) *guardState {
	if st.guardSums != nil {
		return st.guardSums
	}
	gs := &guardState{
		releases: st.releaseSummaries(fns),
		before:   map[*ssa.Function]map[ssa.Instruction]lockset{},
		initOnly: st.initFuncs(fns),
		visiting: map[*ssa.Function]bool{},
	}
	st.acquireSummaries(gs, fns)
	st.guardSums = gs
	return gs
}

func (st *state) exportFacts(fns []*ssa.Function) {
	if len(st.pass.Analyzer.FactTypes) == 0 {
		return
	}
	hasExported := false
	for _, fn := range fns {
		if obj, ok := fn.Object().(*types.Func); ok && fn.Synthetic == "" && obj.Pkg() == st.pass.Pkg && obj.Exported() {
			hasExported = true
			break
		}
	}
	var acq map[*ssa.Function][]acquire
	if hasExported && st.hasLockOps(fns) {
		acq = st.ensureGuardSummaries(fns).acquires
	}
	for _, fn := range fns {
		obj, ok := fn.Object().(*types.Func)
		// Only exported functions can be called statically from another package; calls that reach an
		// unexported function some other way fall back to the pessimistic summary.
		if !ok || fn.Synthetic != "" || obj.Pkg() != st.pass.Pkg || !obj.Exported() {
			continue
		}
		s := st.sums[fn]
		f := &FuncFact{Iso: make([]bool, len(fn.Params))}
		for i, p := range fn.Params {
			f.Iso[i] = st.isIsoParam(p)
		}
		if s.pessimistic {
			f.Unknown = true
			f.Leaks = make([]bool, len(fn.Params))
			f.Writes = make([]bool, len(fn.Params))
			for i := range f.Leaks {
				f.Leaks[i] = true
				f.Writes[i] = true
			}
		} else {
			f.Leaks = append([]bool(nil), s.leakParam...)
			f.Spawns = append([]bool(nil), s.spawnParam...)
			f.Writes = append([]bool(nil), s.writeParam...)
			f.Links = append([]Link(nil), s.links...)
			f.Fresh = s.fresh
			f.FreshResult = append([]bool(nil), s.freshResult...)
			if acq != nil {
				f.Acquires = append([]acquire(nil), acq[fn]...)
			}
		}
		st.pass.ExportObjectFact(obj, f)
	}
	for tn, ok := range st.imm {
		if ok && tn.Exported() {
			st.pass.ExportObjectFact(tn, &ImmFact{Imm: true})
		}
	}
}

func (st *state) report(pos token.Pos, cat, msg string) {
	st.reportRelated(pos, cat, msg, nil)
}

// reportRelated reports a finding with related positions (the writes behind a declaration-site
// SAFE006, say). Suppression goes by the finding's own position and rule. So does de-duplication of a
// use after a transfer (SAFE003) and a handler's write (SAFE008): a use site is one defect however
// many transfers or captured variables reach it, reported for the first, while their messages name
// the transfer and the variable, so keying on the message would repeat it once per transfer (a go
// statement in a loop reaches itself on the next iteration once per captured variable). Any other
// finding is keyed on its message as well, since distinct messages at one position are distinct
// defects (two arguments moved by one call; two borrows one go statement captures; an address taken
// and a field written).
func (st *state) reportRelated(pos token.Pos, cat, msg string, related []analysis.RelatedInformation) {
	k := reportKey{pos, cat, msg}
	switch cat {
	case UseAfter, UseAfterSynced, HandlerWrite:
		k.extra = ""
	}
	st.findings++
	// A finding a transfer causes away from the transfer (a use after it, a handler's write) points
	// back at it, so a reader, and a hatch, can find the transfer.
	if st.transferAt.IsValid() && pos != st.transferAt && related == nil {
		related = []analysis.RelatedInformation{{Pos: st.transferAt, Message: TransferRelated}}
	}
	// A finding muted by the transfer's scope is recorded against that scope (checkTransfers);
	// otherwise the scope around the finding's own position mutes it.
	if st.muted || st.ix.Mute(pos) {
		if st.muted {
			st.mutedFindings++
		}
		sk := reportKey{pos, cat, ""}
		if !st.suppressed[sk] {
			st.suppressed[sk] = true
			st.stats.Suppressed++
		}
		return
	}
	if st.reports[k] {
		return
	}
	st.reports[k] = true
	ruleID := cat
	if cat == UseAfterSynced {
		ruleID = UseAfter
	}
	st.pass.Report(analysis.Diagnostic{Pos: pos, Category: cat, Message: ruleID + ": " + msg, Related: related})
}
