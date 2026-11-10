package ownership

import (
	"fmt"
	"go/token"
	"sort"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// checkGlobals reports SAFE006 at the declaration of every package variable of this package that is
// written outside initialisation and is not //safe:guarded_by. A package variable is accessed in
// place by whatever goroutine touches it, so a write after initialisation is shared mutable state
// whatever its type; sync and atomic types are written through method calls, which are not writes
// here. The writes are attached as related positions, so a reader (or the race-corpus scorer) can
// see which code makes the variable mutable. A //safe:unsafe on the declaration accepts it.
//
// Initialisation is init, package variable initialisers, and init-only helpers: functions every
// caller of which is initialisation and whose value is never taken.
func (st *state) checkGlobals(fns []*ssa.Function) {
	initOnly := st.initFuncs(fns)
	writes := map[*ssa.Global][]token.Pos{}
	for _, fn := range fns {
		if initOnly[fn] {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			if w, ok := writeOf(ins); ok {
				if w.grows {
					return // append changes a package variable only through the store of its result
				}
				addr := w.addr
				g, ok := globalRoot(addr)
				if !ok || g.Pkg != st.ssa.Pkg {
					return
				}
				if _, guarded := st.slotOf(addr); guarded {
					return // SAFE009 checks guarded variables and fields at every access
				}
				writes[g] = append(writes[g], posOf(ins, ins))
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			if _, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
				return
			}
			if cc.IsInvoke() {
				for _, v := range append([]ssa.Value{cc.Value}, cc.Args...) {
					if g, ok := st.globalMutArg(v); ok {
						writes[g] = append(writes[g], posOf(ins, ins))
					}
				}
				return
			}
			callee := cc.StaticCallee()
			if callee == nil {
				for _, v := range cc.Args {
					if g, ok := st.globalMutArg(v); ok {
						writes[g] = append(writes[g], posOf(ins, ins))
					}
				}
				return
			}
			if callee.Pkg == st.ssa.Pkg && initOnly[callee] {
				return
			}
			if callee.Pkg != nil && callee.Pkg.Pkg != nil && callee.Pkg.Pkg.Path() == "testing" && callee.Name() == "MainStart" {
				return
			}
			sum := st.summary(callee)
			for i, a := range cc.Args {
				if !sum.writes(i) {
					continue
				}
				if g, ok := st.globalMutArg(a); ok {
					writes[g] = append(writes[g], posOf(ins, ins))
				}
			}
		})
	}
	globals := make([]*ssa.Global, 0, len(writes))
	for g := range writes {
		globals = append(globals, g)
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i].Pos() < globals[j].Pos() })
	for _, g := range globals {
		ws := writes[g]
		sort.Slice(ws, func(i, j int) bool { return ws[i] < ws[j] })
		related := make([]analysis.RelatedInformation, 0, len(ws))
		for _, w := range ws {
			related = append(related, analysis.RelatedInformation{Pos: w, Message: "written here"})
		}
		st.reportRelated(g.Pos(), GlobalUse, fmt.Sprintf(
			"package variable %s is written outside init%s; guard it with //safe:guarded_by or write it only during initialisation",
			g.Name(), st.at(ws[0])), related)
	}
}

// initFuncs returns the functions that run only during initialisation: init functions (and closures
// inside them), the package initialiser, and functions whose every static caller is one of those and
// whose value is never taken.
func (st *state) initFuncs(fns []*ssa.Function) map[*ssa.Function]bool {
	out := map[*ssa.Function]bool{}
	for _, fn := range fns {
		if isInit(fn) {
			out[fn] = true
		}
	}
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil {
		out[pkgInit] = true
	}
	callers := map[*ssa.Function][]*ssa.Function{}
	taken := map[*ssa.Function]bool{}
	for _, fn := range fns {
		forEachInstr(fn, func(ins ssa.Instruction) {
			var static *ssa.Function
			if c, ok := ins.(ssa.CallInstruction); ok && !c.Common().IsInvoke() {
				if f, ok := c.Common().Value.(*ssa.Function); ok {
					static = f
					callers[f] = append(callers[f], fn)
				}
			}
			for _, op := range ins.Operands(nil) {
				if op == nil {
					continue
				}
				if f, ok := (*op).(*ssa.Function); ok && f != static {
					taken[f] = true
				}
			}
		})
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			if out[fn] || taken[fn] || len(callers[fn]) == 0 || fn.Pkg != st.ssa.Pkg {
				continue
			}
			all := true
			for _, c := range callers[fn] {
				if !out[c] {
					all = false
					break
				}
			}
			if all {
				out[fn] = true
				changed = true
			}
		}
	}
	return out
}

func isInit(fn *ssa.Function) bool {
	for f := fn; f != nil; f = f.Parent() {
		if f.Name() == "init" && f.Signature.Recv() == nil && f.Parent() == nil {
			return true
		}
		if len(f.Name()) > 5 && f.Name()[:5] == "init#" {
			return true
		}
	}
	return false
}

func forEachInstr(fn *ssa.Function, f func(ssa.Instruction)) {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			f(ins)
		}
	}
}

func (st *state) globalMutArg(v ssa.Value) (*ssa.Global, bool) {
	if v == nil || st.cls.Shareable(v.Type()) {
		return nil, false
	}
	if _, guarded := st.slotOf(v); guarded {
		return nil, false
	}
	g, ok := globalRoot(v)
	if !ok || g.Pkg != st.ssa.Pkg {
		return nil, false
	}
	return g, true
}

// globalRoot follows field, index, slice, phi, local cell store, and getter call steps back to a package variable.
func globalRoot(v ssa.Value) (*ssa.Global, bool) {
	return globalRootSeen(v, map[ssa.Value]bool{})
}

func globalRootSeen(v ssa.Value, seen map[ssa.Value]bool) (*ssa.Global, bool) {
	for range 64 {
		if v == nil || seen[v] || len(seen) >= 64 {
			return nil, false
		}
		seen[v] = true
		switch x := v.(type) {
		case *ssa.Global:
			return x, true
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Field:
			v = x.X
		case *ssa.Index:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.TypeAssert:
			v = x.X
		case *ssa.Phi:
			for _, e := range x.Edges {
				if g, ok := globalRootSeen(e, seen); ok {
					return g, true
				}
			}
			return nil, false
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			var alloc *ssa.Alloc
			if a, ok := x.X.(*ssa.Alloc); ok {
				alloc = a
			} else if fa, ok := x.X.(*ssa.FieldAddr); ok {
				alloc, _ = fa.X.(*ssa.Alloc)
			}
			if alloc != nil && alloc.Referrers() != nil {
				for _, ref := range *alloc.Referrers() {
					if st, ok := ref.(*ssa.Store); ok && st.Addr == alloc {
						if g, ok := globalRootSeen(st.Val, seen); ok {
							return g, true
						}
					}
					if fa, ok := ref.(*ssa.FieldAddr); ok && fa.Referrers() != nil {
						for _, r2 := range *fa.Referrers() {
							if st, ok := r2.(*ssa.Store); ok && st.Addr == fa {
								if g, ok := globalRootSeen(st.Val, seen); ok {
									return g, true
								}
							}
						}
					}
					if ia, ok := ref.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
						for _, r2 := range *ia.Referrers() {
							if st, ok := r2.(*ssa.Store); ok && st.Addr == ia {
								if g, ok := globalRootSeen(st.Val, seen); ok {
									return g, true
								}
							}
						}
					}
				}
			}
			v = x.X
		case *ssa.Call:
			callee := x.Call.StaticCallee()
			if callee == nil || len(callee.Blocks) == 0 {
				return nil, false
			}
			for _, b := range callee.Blocks {
				for _, ins := range b.Instrs {
					if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
						if g, ok := globalRootSeen(ret.Results[0], seen); ok {
							return g, true
						}
					}
				}
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}
