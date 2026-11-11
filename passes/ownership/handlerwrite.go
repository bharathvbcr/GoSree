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
// loads to any write (writeOf) into them. It follows the roots into static calls of this package and
// into closures the body calls directly. At a static call into another package (or to a function
// with no body) it reads the callee's summary instead: passing shared memory to a parameter the
// callee may write is a write. At an interface method call or a dynamic call it cannot see the callee,
// so passing shared, non-shareable memory to one is reported as a possible write. It stops at spawn
// sites (the transfer rules own a goroutine the handler starts). It runs inside the registration's
// transfer check, so a //gosree:unsafe on the registration mutes every write it finds.

// checkHandlerWrites reports SAFE008 for the handler registered at sp.
func (st *state) checkHandlerWrites(sp spawnSite, regPos token.Pos) {
	shared := map[*ssa.Function]map[ssa.Value]string{}
	var queue []*ssa.Function
	for _, h := range handlerRoots(st.ssa.Pkg.Prog, sp) {
		if len(h.roots) > 0 {
			shared[h.fn] = h.roots
			queue = append(queue, h.fn)
		}
	}
	if len(queue) == 0 {
		return
	}
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
	once := func(ins ssa.Instruction) bool {
		p := st.pass.Fset.Position(posOf(ins, ins))
		if reported[line{p.Filename, p.Line}] {
			return false
		}
		reported[line{p.Filename, p.Line}] = true
		return true
	}
	for steps := 0; len(queue) > 0 && steps < 10000; steps++ {
		fn := queue[0]
		queue = queue[1:]
		r := shared[fn]
		forEachInstr(fn, func(ins ssa.Instruction) {
			if _, ok := spawnOf(ins); ok {
				return
			}
			if w, ok := writeOf(ins); ok {
				if _, guarded := st.slotOf(w.addr); guarded {
					return // SAFE009 checks guarded memory at every access
				}
				if root, ok := sharedRoot(w.addr, r); ok && once(ins) {
					st.report(posOf(ins, ins), HandlerWrite, fmt.Sprintf(
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
				for _, v := range append([]ssa.Value{cc.Value}, cc.Args...) {
					if _, guarded := st.slotOf(v); guarded {
						continue
					}
					if root, ok := sharedRoot(v, r); ok && !st.cls.Shareable(v.Type()) && once(ins) {
						st.report(posOf(ins, ins), HandlerWrite, fmt.Sprintf(
							"%s is passed to interface method %s, which may write it, by a handler registered with %s%s, which shares it with every concurrent request",
							r[root], cc.Method.Name(), sp.via, st.at(regPos)))
						return
					}
				}
				return
			}
			callee, bindings := directCallee(cc.Value)
			if callee == nil {
				for _, v := range append(cc.Args, cc.Value) {
					if _, guarded := st.slotOf(v); guarded {
						continue
					}
					if root, ok := sharedRoot(v, r); ok && !st.cls.Shareable(v.Type()) && once(ins) {
						st.report(posOf(ins, ins), HandlerWrite, fmt.Sprintf(
							"%s is passed to a dynamic function call, which may write it, by a handler registered with %s%s, which shares it with every concurrent request",
							r[root], sp.via, st.at(regPos)))
						return
					}
				}
				return
			}
			for _, a := range cc.Args {
				if mc := resolveClosureVal(a, 0); mc != nil {
					if argFn, ok := mc.Fn.(*ssa.Function); ok && len(argFn.Blocks) > 0 && st.followable(argFn) {
						for j, b := range mc.Bindings {
							if root, ok := sharedRoot(b, r); ok && j < len(argFn.FreeVars) {
								add(argFn, argFn.FreeVars[j], r[root])
							}
						}
					}
				}
			}
			if len(callee.Blocks) == 0 || !st.followable(callee) {
				st.checkCalleeWrites(ins, cc, callee, r, sp, regPos, once)
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

// checkCalleeWrites reports SAFE008 at a call the write check does not follow into (a function of
// another package, or one with no body): an argument that leads back to shared memory is written when
// the callee's summary says it may write that parameter. Shareable arguments (a mutex, an atomic, an
// imm value) are exempt: their methods synchronise or never write.
func (st *state) checkCalleeWrites(ins ssa.Instruction, cc *ssa.CallCommon, callee *ssa.Function, r map[ssa.Value]string, sp spawnSite, regPos token.Pos, once func(ssa.Instruction) bool) {
	sum := st.summary(callee)
	for i, a := range cc.Args {
		root, ok := sharedRoot(a, r)
		if !ok || !sum.writes(i) || st.cls.Shareable(a.Type()) || !once(ins) {
			continue
		}
		st.report(posOf(ins, ins), HandlerWrite, fmt.Sprintf(
			"%s is passed to %s, which may write it%s, by a handler registered with %s%s, which shares it with every concurrent request",
			r[root], calleeName(callee), sum.unavailable(), sp.via, st.at(regPos)))
		return
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

// handlerBody is a function a repeated spawner may run and the values in it that every invocation
// shares, each with its description.
type handlerBody struct {
	fn    *ssa.Function
	roots map[ssa.Value]string
}

// handlerRoots returns the functions a repeated spawner may run, each with its shared roots.
// Handlers that do not resolve to a function body of this program give nothing. A factory is
// followed through every one of its returns, so an early `return nil` guard does not hide the
// handler it builds further down; a result that is not a function (nil, an error) adds nothing.
func handlerRoots(prog *ssa.Program, sp spawnSite) []handlerBody {
	if !sp.repeated {
		return nil
	}
	var out []handlerBody
	index := map[*ssa.Function]int{}
	add := func(fn *ssa.Function, roots map[ssa.Value]string) {
		if i, ok := index[fn]; ok {
			for v, d := range roots {
				if _, dup := out[i].roots[v]; !dup {
					out[i].roots[v] = d
				}
			}
			return
		}
		index[fn] = len(out)
		out = append(out, handlerBody{fn, roots})
	}
	seen := map[ssa.Value]bool{}
	var resolve func(v ssa.Value, depth int)
	resolve = func(v ssa.Value, depth int) {
		if v == nil || depth > 16 || seen[v] {
			return
		}
		seen[v] = true
		switch x := v.(type) {
		case *ssa.ChangeType:
			resolve(x.X, depth+1)
		case *ssa.Call:
			if callee := x.Call.StaticCallee(); callee != nil && len(callee.Blocks) > 0 {
				returned := false
				for _, b := range callee.Blocks {
					for _, ins := range b.Instrs {
						if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
							returned = true
							resolve(ret.Results[0], depth+1)
						}
					}
				}
				if returned {
					return
				}
			}
			for _, a := range x.Call.Args {
				if spawnedFunc(prog, a, true) != nil {
					resolve(a, depth+1)
					return
				}
			}
		case *ssa.MakeInterface:
			if fn := spawnedFunc(prog, x, true); fn != nil {
				if _, isClosure := unwrapToClosure(x); !isClosure {
					// A handler value: ServeHTTP shares its receiver.
					if len(fn.Params) > 0 {
						add(fn, map[ssa.Value]string{fn.Params[0]: "receiver " + fn.Params[0].Name()})
					}
					return
				}
			}
			resolve(x.X, depth+1)
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
			add(fn, roots)
		}
	}
	resolve(sp.fn, 0)
	return out
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
	if mc := resolveClosureVal(v, 0); mc != nil {
		return mc.Fn.(*ssa.Function), mc.Bindings
	}
	return nil, nil
}

// write is one instruction writing memory: what it writes through (a pointer, map or slice).
type write struct {
	addr ssa.Value
	// grows marks append: it writes the backing array of its first argument only when that slice has
	// room, and the new length is visible only through its result.
	grows bool
}

// writeOf is the one definition of "a write" that every rule shares: a store, a map update, and the
// builtins delete, clear, copy and append on their first argument. SAFE006 leaves out append (a
// package variable is only changed by storing the result back); SAFE008 and the summaries' writes
// bit count it, because it writes the shared backing array.
func writeOf(ins ssa.Instruction) (write, bool) {
	switch ins := ins.(type) {
	case *ssa.Store:
		return write{addr: ins.Addr}, true
	case *ssa.MapUpdate:
		return write{addr: ins.Map}, true
	case *ssa.Call:
		if b, ok := ins.Call.Value.(*ssa.Builtin); ok && len(ins.Call.Args) > 0 {
			switch b.Name() {
			case "delete", "clear", "copy":
				return write{addr: ins.Call.Args[0]}, true
			case "append":
				return write{addr: ins.Call.Args[0], grows: true}, true
			}
		}
	}
	return write{}, false
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
		if v == nil || seen[v] || len(seen) >= 64 {
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
			var alloc *ssa.Alloc
			if a, ok := x.X.(*ssa.Alloc); ok {
				alloc = a
			} else if fa, ok := x.X.(*ssa.FieldAddr); ok {
				alloc, _ = fa.X.(*ssa.Alloc)
			}
			if alloc != nil && alloc.Referrers() != nil {
				for _, ref := range *alloc.Referrers() {
					if st, ok := ref.(*ssa.Store); ok && st.Addr == alloc {
						if r, ok := sharedRootSeen(st.Val, roots, seen); ok {
							return r, true
						}
					}
					if fa, ok := ref.(*ssa.FieldAddr); ok && fa.Referrers() != nil {
						for _, r2 := range *fa.Referrers() {
							if st, ok := r2.(*ssa.Store); ok && st.Addr == fa {
								if r, ok := sharedRootSeen(st.Val, roots, seen); ok {
									return r, true
								}
							}
						}
					}
					if ia, ok := ref.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
						for _, r2 := range *ia.Referrers() {
							if st, ok := r2.(*ssa.Store); ok && st.Addr == ia {
								if r, ok := sharedRootSeen(st.Val, roots, seen); ok {
									return r, true
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
			paramRoots := map[ssa.Value]string{}
			paramToRoot := map[ssa.Value]ssa.Value{}
			for i, a := range x.Call.Args {
				if i < len(callee.Params) {
					if r, ok := sharedRootSeen(a, roots, seen); ok {
						paramRoots[callee.Params[i]] = roots[r]
						paramToRoot[callee.Params[i]] = r
					}
				}
			}
			if len(paramRoots) == 0 {
				return nil, false
			}
			for _, b := range callee.Blocks {
				for _, ins := range b.Instrs {
					if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
						subSeen := make(map[ssa.Value]bool, len(seen))
						for k, val := range seen {
							subSeen[k] = val
						}
						if pRoot, ok := sharedRootSeen(ret.Results[0], paramRoots, subSeen); ok {
							if orig, ok := paramToRoot[pRoot]; ok {
								return orig, true
							}
						}
					}
				}
			}
			return nil, false
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
