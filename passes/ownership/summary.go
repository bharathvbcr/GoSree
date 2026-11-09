package ownership

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// FuncFact summarises how a function treats its arguments, so callers in other packages can check
// moves across the call. Index i follows SSA parameter order: the receiver, when there is one, is 0.
type FuncFact struct {
	// Leaks[i]: the function may retain, share, return or move argument i, or mix it with memory
	// the caller does not own.
	Leaks []bool
	// Iso[i]: parameter i is //safe:iso, so passing an argument to it moves the argument.
	Iso []bool
	// Fresh: every pointer-bearing result is a fresh allocation that nothing else references.
	Fresh bool
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
	leakParam []bool
	isoParam  []bool
	leakFree  []bool
	fresh     bool
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

func (s *summary) freeLeaks(j int) bool {
	if s.pessimistic || j >= len(s.leakFree) {
		return true
	}
	return s.leakFree[j]
}

var pessimistic = &summary{pessimistic: true}

func optimistic(fn *ssa.Function, st *state) *summary {
	s := &summary{
		leakParam: make([]bool, len(fn.Params)),
		isoParam:  make([]bool, len(fn.Params)),
		leakFree:  make([]bool, len(fn.FreeVars)),
		fresh:     true,
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
	var f FuncFact
	if !st.pass.ImportObjectFact(obj, &f) {
		return pessimistic
	}
	return &summary{leakParam: f.Leaks, isoParam: f.Iso, fresh: f.Fresh}
}

func (st *state) isIsoParam(p *ssa.Parameter) bool {
	v, ok := p.Object().(*types.Var)
	return ok && st.ix.IsoVars[v]
}

// summarise computes a function's summary from its graph.
func summarise(fn *ssa.Function, g *graph, st *state) *summary {
	s := optimistic(fn, st)
	for i, p := range fn.Params {
		if n := g.val(p); n != nil {
			s.leakParam[i] = regionHas(g, n, func(e event) bool { return e.kind != evForeign || e.param != i })
		}
	}
	for j, fv := range fn.FreeVars {
		if n := g.val(fv); n != nil {
			s.leakFree[j] = regionHas(g, n, func(e event) bool { return e.kind != evForeign || e.free != j })
		}
	}
	for _, b := range fn.Blocks {
		ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return)
		if !ok {
			continue
		}
		for _, r := range ret.Results {
			if n := g.val(r); n != nil && regionHas(g, n, func(e event) bool { return e.kind != evReturn }) {
				s.fresh = false
			}
		}
	}
	return s
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

func sameSummary(a, b *summary) bool {
	if a.fresh != b.fresh || len(a.leakParam) != len(b.leakParam) || len(a.leakFree) != len(b.leakFree) {
		return false
	}
	for i := range a.leakParam {
		if a.leakParam[i] != b.leakParam[i] {
			return false
		}
	}
	for i := range a.leakFree {
		if a.leakFree[i] != b.leakFree[i] {
			return false
		}
	}
	return true
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
	out.leakParam = append([]bool(nil), next.leakParam...)
	for i := range out.leakParam {
		out.leakParam[i] = out.leakParam[i] || prev.leakParam[i]
	}
	out.leakFree = append([]bool(nil), next.leakFree...)
	for i := range out.leakFree {
		out.leakFree[i] = out.leakFree[i] || prev.leakFree[i]
	}
	return &out
}
