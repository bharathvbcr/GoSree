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
			w, ok := writeOf(ins)
			if !ok || w.grows {
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

// globalRoot follows field, index, slice and load steps back to a package variable.
func globalRoot(v ssa.Value) (*ssa.Global, bool) {
	for range 64 {
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
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			v = x.X
		default:
			return nil, false
		}
	}
	return nil, false
}
