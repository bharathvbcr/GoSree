// Package ownership checks that memory handed to another goroutine is owned by the sender and not
// used by it afterwards, that package variables are not mutated across goroutines, and that
// //safe:imm types are never written after construction.
//
// Rules:
//
//	SAFE002  a value crossing to another goroutine is not owned (it may alias a parameter, a package
//	         variable, or memory a callee kept)
//	SAFE003  a value is used after it was moved to another goroutine
//	SAFE004  a moved value also escapes somewhere else (stored, returned, retained by a callee)
//	SAFE005  a //safe:imm type is malformed or written after construction
//	SAFE006  a package variable written outside init is accessed from code that runs on a goroutine
//
// A value crosses to another goroutine when it is an argument of a go statement, the closure a go
// statement starts, a value sent on a channel, or an argument for a //safe:iso parameter. A closure's
// captured variables cross only when the closure itself does.
package ownership

import (
	"go/token"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gocap/internal/annot"
	"github.com/bharathvbcr/gocap/internal/kinds"
)

// Rule categories.
const (
	NotOwned     = "SAFE002"
	UseAfter     = "SAFE003"
	Escapes      = "SAFE004"
	Imm          = "SAFE005"
	GlobalUse    = "SAFE006"
	BorrowEscape = "CAP003"
	// UseAfterSynced is reported for SAFE003 uses that every path reaches only after a
	// synchronisation point that is not a join for the move (forkjoin.go): a receive from another
	// channel, a select, a mutex or cond, or a join shape the rule does not accept. The separate
	// category keeps that class measurable.
	UseAfterSynced = "SAFE003-forkjoin"
)

// Analyzer is the ownership checker.
var Analyzer = &analysis.Analyzer{
	Name:      "safeownership",
	Doc:       "check moves of memory between goroutines, package-variable sharing and //safe:imm types (SAFE002-SAFE006)",
	Run:       run,
	Requires:  []*analysis.Analyzer{annot.Analyzer, buildssa.Analyzer},
	FactTypes: []analysis.Fact{new(FuncFact), new(ImmFact)},
	// Stats is returned so harnesses can count escape hatches without parsing output.
	ResultType: reflect.TypeFor[*Stats](),
}

// Stats counts what the pass saw in one package.
type Stats struct {
	Transfers   int
	Suppressed  int
	UnsafeScope int
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
	// graphs caches the alias graph of each function once summaries are final; the fork-join rule
	// reads a goroutine body's graph while checking its parent.
	graphs    map[*ssa.Function]*graph
	joinCache map[*ssa.Go]map[ssa.Instruction]bool
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
	pos token.Pos
	cat string
	msg string
}

func run(pass *analysis.Pass) (any, error) {
	st := &state{
		pass:    pass,
		ix:      pass.ResultOf[annot.Analyzer].(*annot.Index),
		ssa:     pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA),
		sums:    map[*ssa.Function]*summary{},
		stats:   &Stats{},
		reports: map[reportKey]bool{},

		graphs:    map[*ssa.Function]*graph{},
		joinCache: map[*ssa.Go]map[ssa.Instruction]bool{},
	}
	st.stats.UnsafeScope = st.ix.UnsafeScopes()
	st.validateImm()
	st.cls = st.newClassifier()

	fns := st.ssa.SrcFuncs
	st.computeSummaries(fns)
	st.exportFacts(fns)

	for _, fn := range fns {
		if len(fn.Blocks) == 0 {
			continue
		}
		g := st.graphOf(fn)
		st.checkTransfers(g)
		st.checkImmStores(fn)
	}
	st.checkGlobals(fns)
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

func (st *state) exportFacts(fns []*ssa.Function) {
	if len(st.pass.Analyzer.FactTypes) == 0 {
		return
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
			f.Leaks = make([]bool, len(fn.Params))
			for i := range f.Leaks {
				f.Leaks[i] = true
			}
		} else {
			f.Leaks = append([]bool(nil), s.leakParam...)
			f.Fresh = s.fresh
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
	if st.ix.Suppressed(pos) {
		st.stats.Suppressed++
		return
	}
	k := reportKey{pos, cat, msg}
	if st.reports[k] {
		return
	}
	st.reports[k] = true
	ruleID := cat
	if cat == UseAfterSynced {
		ruleID = UseAfter
	}
	st.pass.Report(analysis.Diagnostic{Pos: pos, Category: cat, Message: ruleID + ": " + msg})
}
