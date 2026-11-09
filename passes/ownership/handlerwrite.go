package ownership

import (
	"fmt"
	"go/token"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// SAFE008: a callback that may run concurrently with itself (a repeated spawner such as an HTTP
// handler) must not write memory it shares with its other invocations: what it captured, or its
// receiver. Two requests running the handler at once race on that write, and the code that registered
// the handler cannot see it, so the transfer rules do not catch it.
//
// The check starts from the handler body's shared roots and follows them through derivations and
// loads to any store, map update, or delete/clear/copy/append into them. It follows the roots into
// static calls of this package and into closures the body calls directly, and stops at spawn sites
// (the transfer rules own a goroutine the handler starts), interface and dynamic calls, and calls
// into other packages: those are stated gaps. It runs inside the registration's transfer check, so a
// //safe:unsafe on the registration mutes every write it finds.

// checkHandlerWrites reports SAFE008 for the handler registered at sp.
func (st *state) checkHandlerWrites(sp spawnSite, regPos token.Pos) {
	body, roots := handlerRoots(st.ssa.Pkg.Prog, sp)
	if body == nil || len(roots) == 0 {
		return
	}
	shared := map[*ssa.Function]map[ssa.Value]string{body: roots}
	queue := []*ssa.Function{body}
	add := func(fn *ssa.Function, v ssa.Value, desc string) {
		if shared[fn] == nil {
			shared[fn] = map[ssa.Value]string{}
		}
		if _, ok := shared[fn][v]; ok {
			return
		}
		shared[fn][v] = desc
		queue = append(queue, fn)
	}
	// One finding per source line: `t = append(t, x)` is an append into t and a store back to it.
	type line struct {
		file string
		line int
	}
	reported := map[line]bool{}
	for steps := 0; len(queue) > 0 && steps < 10000; steps++ {
		fn := queue[0]
		queue = queue[1:]
		r := shared[fn]
		forEachInstr(fn, func(ins ssa.Instruction) {
			if _, ok := spawnOf(ins); ok {
				return
			}
			if addr, ok := writeTarget(ins); ok {
				if _, guarded := st.slotOf(addr); guarded {
					return // SAFE009 checks guarded memory at every access
				}
				if root, ok := sharedRoot(addr, r); ok {
					pos := posOf(ins, ins)
					p := st.pass.Fset.Position(pos)
					if reported[line{p.Filename, p.Line}] {
						return
					}
					reported[line{p.Filename, p.Line}] = true
					st.report(pos, HandlerWrite, fmt.Sprintf(
						"%s is written here by a handler registered with %s%s, which shares it with every concurrent request",
						r[root], sp.via, st.at(regPos)))
				}
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			if cc.IsInvoke() {
				return
			}
			callee, bindings := directCallee(cc.Value)
			if callee == nil || len(callee.Blocks) == 0 || !st.followable(callee) {
				return
			}
			for i, a := range cc.Args {
				if i >= len(callee.Params) {
					break
				}
				if root, ok := sharedRoot(a, r); ok {
					add(callee, callee.Params[i], r[root])
				}
			}
			for j, b := range bindings {
				if root, ok := sharedRoot(b, r); ok && j < len(callee.FreeVars) {
					add(callee, callee.FreeVars[j], r[root])
				}
			}
		})
	}
}

// followable reports whether the write check follows a call into fn: a function of this package,
// or a synthetic wrapper (a bound or promoted method) whose own calls are checked the same way.
func (st *state) followable(fn *ssa.Function) bool {
	if fn.Pkg == st.ssa.Pkg {
		return true
	}
	return fn.Pkg == nil && fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information")
}

// handlerRoots returns the function a repeated spawner runs and the values in it that every
// invocation shares, each with its description. Handlers that do not resolve to a function body of
// this program give nothing.
func handlerRoots(prog *ssa.Program, sp spawnSite) (*ssa.Function, map[ssa.Value]string) {
	if !sp.repeated {
		return nil, nil
	}
	v := sp.fn
	for range 16 {
		switch x := v.(type) {
		case *ssa.ChangeType:
			v = x.X
			continue
		case *ssa.MakeInterface:
			if fn := spawnedFunc(prog, x, true); fn != nil {
				if _, isClosure := unwrapToClosure(x); !isClosure {
					// A handler value: ServeHTTP shares its receiver.
					if len(fn.Params) == 0 {
						return nil, nil
					}
					return fn, map[ssa.Value]string{fn.Params[0]: "receiver " + fn.Params[0].Name()}
				}
			}
			v = x.X
			continue
		case *ssa.MakeClosure:
			fn := x.Fn.(*ssa.Function)
			roots := map[ssa.Value]string{}
			for _, fv := range fn.FreeVars {
				if strings.HasPrefix(fn.Synthetic, "bound method wrapper") {
					roots[fv] = "the method value's receiver"
				} else {
					roots[fv] = "captured variable " + fv.Name()
				}
			}
			return fn, roots
		}
		return nil, nil
	}
	return nil, nil
}

// unwrapToClosure reports whether a boxed handler is a converted function value (http.HandlerFunc).
func unwrapToClosure(mi *ssa.MakeInterface) (ssa.Value, bool) {
	v := mi.X
	for range 16 {
		ct, ok := v.(*ssa.ChangeType)
		if !ok {
			break
		}
		v = ct.X
	}
	switch v.(type) {
	case *ssa.MakeClosure, *ssa.Function:
		return v, true
	}
	return nil, false
}

// directCallee returns the function a call runs when it is known statically, with the closure's
// bindings when the callee is a closure built in place.
func directCallee(v ssa.Value) (*ssa.Function, []ssa.Value) {
	switch x := v.(type) {
	case *ssa.Function:
		return x, nil
	case *ssa.MakeClosure:
		return x.Fn.(*ssa.Function), x.Bindings
	}
	return nil, nil
}

// writeTarget returns the memory an instruction writes into, if it writes.
func writeTarget(ins ssa.Instruction) (ssa.Value, bool) {
	switch ins := ins.(type) {
	case *ssa.Store:
		return ins.Addr, true
	case *ssa.MapUpdate:
		return ins.Map, true
	case *ssa.Call:
		if b, ok := ins.Call.Value.(*ssa.Builtin); ok && len(ins.Call.Args) > 0 {
			switch b.Name() {
			case "delete", "clear", "copy", "append":
				return ins.Call.Args[0], true
			}
		}
	}
	return nil, false
}

// sharedRoot follows v back through derivations and loads to one of roots.
func sharedRoot(v ssa.Value, roots map[ssa.Value]string) (ssa.Value, bool) {
	return sharedRootSeen(v, roots, map[ssa.Value]bool{})
}

func sharedRootSeen(v ssa.Value, roots map[ssa.Value]string, seen map[ssa.Value]bool) (ssa.Value, bool) {
	for range 64 {
		if _, ok := roots[v]; ok {
			return v, true
		}
		if seen[v] {
			return nil, false
		}
		seen[v] = true
		switch x := v.(type) {
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Field:
			v = x.X
		case *ssa.Index:
			v = x.X
		case *ssa.Lookup:
			v = x.X
		case *ssa.Extract:
			v = x.Tuple
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.TypeAssert:
			v = x.X
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			v = x.X
		case *ssa.Phi:
			for _, e := range x.Edges {
				if r, ok := sharedRootSeen(e, roots, seen); ok {
					return r, true
				}
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}
