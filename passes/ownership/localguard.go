package ownership

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// Guarded local variables. A local annotated //safe:guarded_by lives in a cell: an Alloc in the
// function that declares it and, in each closure that captures it, a free variable bound to that
// Alloc. Its mutex has a cell the same way, or is a package variable. A cell is the root of a guard
// path, so a closure that locks its captured mu holds the guard of its captured x.
//
// A guarded cell has no alias class: like a final cell it is not moved by the goroutines that capture
// it, because SAFE009 checks every access to it instead.

type localGuard struct {
	v, mu *types.Var
	// muCell is the mutex's cell in the same function as the guarded cell, or nil when that function
	// cannot reach the mutex (a closure that does not capture it), so it can never hold it.
	muCell ssa.Value
}

// guardedCells finds the cells of guarded locals in fns and in the closures that capture them.
func (st *state) guardedCells(fns []*ssa.Function) map[ssa.Value]localGuard {
	pkgScope := st.pass.Pkg.Scope()
	byPos := map[token.Pos]*types.Var{} // guarded locals and their local mutexes, by declaration
	for v, mu := range st.ix.Guarded {
		if v.IsField() || v.Parent() == pkgScope {
			continue
		}
		byPos[v.Pos()] = v
		if mu.Parent() != pkgScope {
			byPos[mu.Pos()] = mu
		}
	}
	if len(byPos) == 0 {
		return nil
	}
	cells := map[*ssa.Function]map[*types.Var]ssa.Value{}
	set := func(fn *ssa.Function, v *types.Var, c ssa.Value) bool {
		m := cells[fn]
		if m == nil {
			m = map[*types.Var]ssa.Value{}
			cells[fn] = m
		}
		if m[v] == c {
			return false
		}
		m[v] = c
		return true
	}
	for _, fn := range fns {
		forEachInstr(fn, func(ins ssa.Instruction) {
			// A source variable's Alloc carries its declaration position and name.
			if a, ok := ins.(*ssa.Alloc); ok {
				if v := byPos[a.Pos()]; v != nil && a.Comment == v.Name() {
					set(fn, v, a)
				}
			}
		})
	}
	// Carry cells into closures through their bindings, as deep as closures nest.
	for changed, rounds := true, 0; changed && rounds < 32; rounds++ {
		changed = false
		for _, fn := range fns {
			forEachInstr(fn, func(ins ssa.Instruction) {
				mc, ok := ins.(*ssa.MakeClosure)
				if !ok {
					return
				}
				child := mc.Fn.(*ssa.Function)
				for j, b := range mc.Bindings {
					for v, c := range cells[fn] {
						if c == b && set(child, v, child.FreeVars[j]) {
							changed = true
						}
					}
				}
			})
		}
	}
	out := map[ssa.Value]localGuard{}
	for fn, m := range cells {
		for v, c := range m {
			mu := st.ix.Guarded[v]
			if mu == nil || v.IsField() || v.Parent() == pkgScope {
				continue // a mutex's own cell
			}
			lg := localGuard{v: v, mu: mu}
			if mu.Parent() == pkgScope {
				if g, ok := st.ssa.Pkg.Members[mu.Name()].(*ssa.Global); ok {
					lg.muCell = g
				}
			} else if mc, ok := m[mu]; ok {
				lg.muCell = mc
			} else {
				for p := fn.Parent(); p != nil; p = p.Parent() {
					if pm := cells[p]; pm != nil {
						if mc, ok := pm[mu]; ok {
							lg.muCell = mc
							break
						}
					}
				}
			}
			out[c] = lg
		}
	}
	return out
}

// localSlot is the guarded memory a local cell names.
func (st *state) localSlot(cell ssa.Value) (slot, bool) {
	lg, ok := st.localGuards[cell]
	if !ok {
		return slot{}, false
	}
	out := slot{v: lg.v, mu: lg.mu, base: guardPath{root: cell}}
	if lg.muCell != nil {
		out.muPath, out.known = guardPath{root: lg.muCell}, true
	}
	return out, true
}

// sharedBefore reports whether the guarded local cell a may be seen by another function before ins
// runs: a closure captures it, or its address goes anywhere but a direct load or store, on a path
// that reaches ins. Until then the declaring function is the only one that can touch it.
func sharedBefore(a *ssa.Alloc, ins ssa.Instruction) bool {
	for _, r := range *a.Referrers() {
		if cellAccess(a, r) {
			continue
		}
		if r == ins || reachableAfter(r)(ins) {
			return true
		}
	}
	return false
}

// cellAccess reports whether r only reads or writes the memory of cell (directly, or through a field
// or element address used only for that) without passing its address on.
func cellAccess(cell ssa.Value, r ssa.Instruction) bool {
	switch r := r.(type) {
	case *ssa.Store:
		return r.Addr == cell
	case *ssa.UnOp:
		return r.Op == token.MUL
	case *ssa.DebugRef:
		return true
	case *ssa.FieldAddr:
		return onlyAccessed(r)
	case *ssa.IndexAddr:
		return onlyAccessed(r)
	}
	return false
}

func onlyAccessed(addr ssa.Value) bool {
	for _, r := range *addr.Referrers() {
		if !cellAccess(addr, r) {
			return false
		}
	}
	return true
}
