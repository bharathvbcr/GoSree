package ownership

import (
	"cmp"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gocap/internal/kinds"
)

// FuncFact summarises how a function treats its arguments, so callers in other packages can check
// moves across the call. Index i follows SSA parameter order: the receiver, when there is one, is 0.
type FuncFact struct {
	// Leaks[i]: the function may retain, share or move argument i, or mix it with memory the caller
	// does not own, other than by linking it to another argument or a result (Links).
	Leaks []bool
	// Iso[i]: parameter i is //safe:iso, so passing an argument to it moves the argument.
	Iso []bool
	// Spawns[i]: parameter i is a function value that the callee starts on another goroutine or sends
	// on a channel, so passing a callback to it is a goroutine boundary.
	Spawns []bool
	// Fresh: every pointer-bearing result is a fresh allocation that nothing else references, or
	// memory of the arguments it is linked to (Links).
	Fresh bool
	// FreshResult[k]: result k is a fresh allocation that nothing else references, or memory of the
	// arguments it is linked to (Links).
	FreshResult []bool
	// Writes[i]: the function may write memory reachable from argument i (through its pointers, map
	// or slice), itself or through functions it calls.
	Writes []bool
	// Links are the parameters and results whose memory the function joins, as root.Add(child) or
	// root.First() do (see Link).
	Links []Link
	// Acquires are the parameter mutexes the function returns holding (lockset.go).
	Acquires []acquire
	// Unknown: gocap could not analyse the function (it has no body, or depends on one that could
	// not be), so Leaks and Writes are all set by assumption, not because it was seen to do either.
	Unknown bool
}

// AFact marks FuncFact as an analysis fact.
func (*FuncFact) AFact() {}

func (f *FuncFact) String() string { return "ownership summary" }

// ImmFact marks a named type whose //safe:imm annotation passed validation.
type ImmFact struct{ Imm bool }

// AFact marks ImmFact as an analysis fact.
func (*ImmFact) AFact() {}

func (f *ImmFact) String() string { return "imm" }

type summary struct {
	leakParam  []bool
	isoParam   []bool
	spawnParam []bool
	leakFree   []bool
	// writeParam[i] / writeFree[j]: the function may write memory reachable from parameter i or free
	// variable j.
	writeParam  []bool
	writeFree   []bool
	fresh       bool
	freshResult []bool
	// links are the parameters and results the function joins (FuncFact.Links).
	links    []Link
	acquires []acquire
	// pessimistic marks a summary for a function gocap could not analyse.
	pessimistic bool
}

func (s *summary) leaks(i int) bool {
	if s.pessimistic || i >= len(s.leakParam) {
		return true
	}
	return s.leakParam[i]
}

func (s *summary) isIso(i int) bool { return i < len(s.isoParam) && s.isoParam[i] }

func (s *summary) spawns(i int) bool { return i < len(s.spawnParam) && s.spawnParam[i] }

func (s *summary) freeLeaks(j int) bool {
	if s.pessimistic || j >= len(s.leakFree) {
		return true
	}
	return s.leakFree[j]
}

// writes reports whether calling the function may write memory reachable from argument i.
func (s *summary) writes(i int) bool {
	if s.pessimistic || i >= len(s.writeParam) {
		return true
	}
	return s.writeParam[i]
}

func (s *summary) writesFree(j int) bool {
	if s.pessimistic || j >= len(s.writeFree) {
		return true
	}
	return s.writeFree[j]
}

func (s *summary) isFreshResult(k int) bool {
	if s.pessimistic {
		return false
	}
	if k >= 0 && k < len(s.freshResult) {
		return s.freshResult[k]
	}
	return s.fresh
}

// unavailable is the note diagnostics add when a callee's behaviour is assumed rather than known, so
// a reader can tell a callee gocap could not analyse from one it saw retain or write.
func (s *summary) unavailable() string {
	if s.pessimistic {
		return " (summary unavailable)"
	}
	return ""
}

var pessimistic = &summary{pessimistic: true}

func optimistic(fn *ssa.Function, st *state) *summary {
	nRes := 0
	if fn.Signature != nil && fn.Signature.Results() != nil {
		nRes = fn.Signature.Results().Len()
	}
	fr := make([]bool, nRes)
	for k := range fr {
		fr[k] = true
	}
	s := &summary{
		leakParam:  make([]bool, len(fn.Params)),
		isoParam:   make([]bool, len(fn.Params)),
		spawnParam: make([]bool, len(fn.Params)),
		leakFree:   make([]bool, len(fn.FreeVars)),

		writeParam:  make([]bool, len(fn.Params)),
		writeFree:   make([]bool, len(fn.FreeVars)),
		fresh:       true,
		freshResult: fr,
	}
	for i, p := range fn.Params {
		s.isoParam[i] = st.isIsoParam(p)
	}
	return s
}

// summary returns what gocap knows about calling fn.
func (st *state) summary(fn *ssa.Function) *summary {
	if o := fn.Origin(); o != nil {
		fn = o
	}
	if s, ok := st.sums[fn]; ok {
		return s
	}
	obj, ok := fn.Object().(*types.Func)
	// Functions of other packages are built from type information; wrappers, thunks and bound
	// methods have their own parameter layout and get no summary.
	if !ok || (fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information")) {
		return pessimistic
	}
	obj = obj.Origin()
	if obj.Pkg() == st.pass.Pkg {
		return pessimistic
	}
	if o, ok := kinds.FuncOverrideFor(obj.FullName()); ok {
		// An assumption replaces the computed summary (internal/kinds/funcs.go).
		return &summary{leakParam: o.Leaks, writeParam: o.Writes, fresh: o.Fresh}
	}
	var f FuncFact
	if !st.pass.ImportObjectFact(obj, &f) || f.Unknown {
		if obj.Pkg().Path() == atomicPkg {
			return atomicSummary(kinds.Params(obj))
		}
		return pessimistic
	}
	s := &summary{
		leakParam:   f.Leaks,
		isoParam:    f.Iso,
		spawnParam:  f.Spawns,
		writeParam:  f.Writes,
		fresh:       f.Fresh,
		freshResult: f.FreshResult,
		links:       f.Links,
		acquires:    f.Acquires,
	}
	if obj.Pkg().Path() == atomicPkg {
		s.writeParam = make([]bool, len(f.Writes))
	}
	return s
}

// atomicSummary is the summary of a sync/atomic function with n parameters and no summary (a
// function without a Go body has none; see tables.go): it may keep
// what it is given and its result is not fresh, as for any function gocap cannot see, but it writes
// nothing a race check counts. A sync/atomic function with a summary keeps it, with its writes
// cleared.
func atomicSummary(n int) *summary {
	leaks := make([]bool, n)
	for i := range leaks {
		leaks[i] = true
	}
	return &summary{leakParam: leaks, writeParam: make([]bool, n)}
}

func (st *state) isIsoParam(p *ssa.Parameter) bool {
	v, ok := p.Object().(*types.Var)
	return ok && st.ix.IsIso(v)
}

// summarise computes a function's summary from its graph.
//
// A parameter whose region holds another parameter's foreign event is linked to it, not leaked: the
// function joined their memory (root.Add(child) stores child into root). A result whose region holds
// a parameter's foreign event is linked to that parameter (root.First() returns memory of root). A
// link records the depth at which the two values' pointee chains meet in the callee's graph, so the
// caller joins the same memory and no more (graph.link): a copy `cp := *c; return &cp` shares *c's
// pointees, not c itself. Two owned arguments then stay owned, as one region, and a foreign one makes
// the other foreign. Anything else in a parameter's region is a leak, and anything else in a result's
// region makes it not fresh.
//
// A write is a write event on the forward pointee chain (fwdHas), not the region: storing a pointer
// to a parameter somewhere does not write it.
func summarise(fn *ssa.Function, g *graph, st *state) *summary {
	s := optimistic(fn, st)
	np := len(fn.Params)
	var links []Link
	// linkTo records that the value at pos (with class n) holds memory of a parameter, and reports
	// whether that is explained: a parameter's own memory, or a link at the depth where the two meet.
	// Chains that do not meet cannot be linked exactly, so that stays a leak (or a result that is not
	// fresh), never a dropped fact.
	linkTo := func(pos int, n *node, param int) bool {
		if pos == param {
			return true
		}
		pn := g.val(fn.Params[param])
		if pn == nil {
			return false
		}
		d, e, ok := g.meetDepth(n, pn)
		if ok {
			links = append(links, newLink(pos, d, param, e))
		}
		return ok
	}
	isWrite := func(e event) bool { return e.kind == evWrite }
	for i, p := range fn.Params {
		if n := g.val(p); n != nil {
			s.leakParam[i] = regionScan(g, n, func(e event) bool {
				switch {
				case e.kind == evWrite:
					return false
				case e.kind == evForeign && e.param >= 0:
					return !linkTo(i, n, e.param)
				case e.kind == evReturn:
					// Returned memory of the parameter links it to the result (the result's region
					// holds the parameter's foreign event). An owned parameter has no foreign event to
					// link by, so returning it stays a leak.
					return s.isoParam[i]
				}
				return true
			})
			s.writeParam[i] = fwdHas(g, n, isWrite)
			if _, isSig := p.Type().Underlying().(*types.Signature); isSig && !s.isoParam[i] {
				s.spawnParam[i] = regionHas(g, n, func(e event) bool { return e.kind == evTransfer })
			}
		}
	}
	for j, fv := range fn.FreeVars {
		if n := g.val(fv); n != nil {
			s.leakFree[j] = regionHas(g, n, func(e event) bool {
				switch e.kind {
				case evWrite:
					return false
				case evForeign:
					return e.free != j
				}
				return true
			})
			s.writeFree[j] = fwdHas(g, n, isWrite)
		}
	}
	for _, b := range fn.Blocks {
		ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return)
		if !ok {
			continue
		}
		for k, r := range ret.Results {
			n := g.val(r)
			if n == nil {
				continue
			}
			for k2 := k + 1; k2 < len(ret.Results); k2++ {
				if n2 := g.val(ret.Results[k2]); n2 != nil {
					if d, e, ok := g.meetDepth(n, n2); ok {
						links = append(links, newLink(np+k, d, np+k2, e))
					}
				}
			}
			if regionScan(g, n, func(e event) bool {
				switch {
				case e.kind == evReturn || e.kind == evWrite:
					return false
				case e.kind == evForeign && e.param >= 0:
					return !linkTo(np+k, n, e.param)
				}
				return true
			}) {
				s.fresh = false
				if k < len(s.freshResult) {
					s.freshResult[k] = false
				}
			}
		}
	}
	s.links = mergeLinks(nil, links)
	return s
}

// Link says a function joins the memory of two of its parameters or results: positions index the
// parameters followed by the results, and the memory DepthA pointers below A is the memory DepthB
// pointers below B. A is the smaller position.
type Link struct {
	A, DepthA, B, DepthB int
}

func newLink(a, da, b, db int) Link {
	if a > b {
		a, da, b, db = b, db, a, da
	}
	return Link{a, da, b, db}
}

// mergeLinks returns the links of a and b, sorted and without duplicates.
func mergeLinks(a, b []Link) []Link {
	out := slices.Concat(a, b)
	slices.SortFunc(out, func(x, y Link) int {
		return cmp.Or(cmp.Compare(x.A, y.A), cmp.Compare(x.B, y.B), cmp.Compare(x.DepthA, y.DepthA), cmp.Compare(x.DepthB, y.DepthB))
	})
	return slices.Compact(out)
}

// regionScan applies f to every event in n's region and reports whether f returned true for any.
// Unlike regionHas it never stops early: f records links as it goes, and the region is a map, so
// stopping at the first bad event would record a different subset of links on every run.
func regionScan(g *graph, n *node, f func(event) bool) bool {
	found := false
	for c := range g.region(n) {
		for _, e := range c.events {
			if f(e) {
				found = true
			}
		}
	}
	return found
}

func regionHas(g *graph, n *node, bad func(event) bool) bool {
	for c := range g.region(n) {
		for _, e := range c.events {
			if bad(e) {
				return true
			}
		}
	}
	return false
}

// fwdHas reports whether an event bad lies on n's forward pointee chain (pointsTo).
func fwdHas(g *graph, n *node, bad func(event) bool) bool {
	for _, c := range g.pointsTo(n) {
		for _, e := range c.events {
			if bad(e) {
				return true
			}
		}
	}
	return false
}

func sameSummary(a, b *summary) bool {
	return a.fresh == b.fresh && slices.Equal(a.freshResult, b.freshResult) &&
		slices.Equal(a.leakParam, b.leakParam) && slices.Equal(a.spawnParam, b.spawnParam) &&
		slices.Equal(a.leakFree, b.leakFree) &&
		slices.Equal(a.writeParam, b.writeParam) && slices.Equal(a.writeFree, b.writeFree) && slices.Equal(a.links, b.links)
}

// orInto returns next with every flag prev set also set.
func orInto(prev, next []bool) []bool {
	out := append([]bool(nil), next...)
	for i := range out {
		out[i] = out[i] || prev[i]
	}
	return out
}

func andInto(prev, next []bool) []bool {
	out := append([]bool(nil), next...)
	for i := range out {
		if i < len(prev) {
			out[i] = out[i] && prev[i]
		}
	}
	return out
}

// computeSummaries iterates to the greatest fixpoint: every function starts optimistic and only
// ever becomes more pessimistic, so the loop terminates.
func (st *state) computeSummaries(fns []*ssa.Function) {
	for _, fn := range fns {
		st.sums[fn] = optimistic(fn, st)
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			if len(fn.Blocks) == 0 {
				st.sums[fn] = pessimistic
				continue
			}
			next := summarise(fn, st.buildGraph(fn), st)
			if !sameSummary(st.sums[fn], next) {
				merged := mergePessimistic(st.sums[fn], next)
				if !sameSummary(st.sums[fn], merged) {
					changed = true
				}
				st.sums[fn] = merged
			}
		}
	}
}

// mergePessimistic keeps the iteration monotone: a fact once lost is never regained.
func mergePessimistic(prev, next *summary) *summary {
	if prev.pessimistic || next.pessimistic {
		return pessimistic
	}
	out := *next
	out.fresh = prev.fresh && next.fresh
	out.freshResult = andInto(prev.freshResult, next.freshResult)
	out.leakParam = orInto(prev.leakParam, next.leakParam)
	out.spawnParam = orInto(prev.spawnParam, next.spawnParam)
	out.leakFree = orInto(prev.leakFree, next.leakFree)
	out.writeParam = orInto(prev.writeParam, next.writeParam)
	out.writeFree = orInto(prev.writeFree, next.writeFree)
	out.links = mergeLinks(prev.links, next.links)
	return &out
}
