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
				if root, ok := st.sharedRoot(w.addr, r); ok && once(ins) {
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
					if root, ok := st.sharedRoot(v, r); ok && !st.cls.Shareable(v.Type()) && once(ins) {
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
					if root, ok := st.sharedRoot(v, r); ok && !st.cls.Shareable(v.Type()) && once(ins) {
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
							if root, ok := st.sharedRoot(b, r); ok && j < len(argFn.FreeVars) {
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
				if root, ok := st.sharedRoot(a, r); ok {
					add(callee, callee.Params[i], r[root])
				}
			}
			for j, b := range bindings {
				if root, ok := st.sharedRoot(b, r); ok && j < len(callee.FreeVars) {
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
		root, ok := st.sharedRoot(a, r)
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
	if fn.Pkg == nil && fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information") {
		if o := fn.Origin(); o != nil && o.Pkg != nil {
			return o.Pkg == st.ssa.Pkg
		}
		if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
			return obj.Pkg() == st.pass.Pkg
		}
		return false
	}
	return false
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
		case *ssa.Call:
			if callee := x.Call.StaticCallee(); callee != nil && len(callee.Blocks) > 0 {
				var retVal ssa.Value
				for _, b := range callee.Blocks {
					for _, ins := range b.Instrs {
						if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
							if _, isConst := ret.Results[0].(*ssa.Const); !isConst {
								retVal = ret.Results[0]
								break
							}
						}
					}
					if retVal != nil {
						break
					}
				}
				if retVal != nil {
					v = retVal
					continue
				}
			}
			var innerArg ssa.Value
			for _, a := range x.Call.Args {
				if spawnedFunc(prog, a, true) != nil {
					innerArg = a
					break
				}
			}
			if innerArg != nil {
				v = innerArg
				continue
			}
			return nil, nil
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
	return resolveFuncVal(v, 0, map[ssa.Value]bool{})
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

type callKey struct {
	call   *ssa.Call
	resIdx int
}

type getterSearch struct {
	st       *state
	roots    map[ssa.Value]string
	visited  map[ssa.Value]bool
	valMemo  map[ssa.Value]ssa.Value
	valFail  map[ssa.Value]bool
	callMemo map[callKey]ssa.Value
	callFail map[callKey]bool
}

// sharedRoot follows v back through derivations and loads to one of roots.
func (st *state) sharedRoot(v ssa.Value, roots map[ssa.Value]string) (ssa.Value, bool) {
	gs := &getterSearch{
		st:       st,
		roots:    roots,
		visited:  map[ssa.Value]bool{},
		valMemo:  map[ssa.Value]ssa.Value{},
		valFail:  map[ssa.Value]bool{},
		callMemo: map[callKey]ssa.Value{},
		callFail: map[callKey]bool{},
	}
	return gs.search(v)
}

func sharedRoot(v ssa.Value, roots map[ssa.Value]string) (ssa.Value, bool) {
	var st *state
	return st.sharedRoot(v, roots)
}

func (gs *getterSearch) search(v ssa.Value) (ssa.Value, bool) {
	for range 64 {
		if v == nil {
			return nil, false
		}
		if _, ok := gs.roots[v]; ok {
			return v, true
		}
		if gs.valFail[v] {
			return nil, false
		}
		if r, ok := gs.valMemo[v]; ok {
			return r, true
		}
		if gs.visited[v] || len(gs.visited) >= 128 {
			return nil, false
		}
		gs.visited[v] = true

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
			if call, ok := x.Tuple.(*ssa.Call); ok {
				if r, ok := gs.searchCall(call, x.Index); ok {
					gs.valMemo[v] = r
					return r, true
				}
				gs.valFail[v] = true
				return nil, false
			}
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
				gs.valFail[v] = true
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
					if stInst, ok := ref.(*ssa.Store); ok && stInst.Addr == alloc {
						if r, ok := gs.search(stInst.Val); ok {
							gs.valMemo[v] = r
							return r, true
						}
					}
					if fa, ok := ref.(*ssa.FieldAddr); ok && fa.Referrers() != nil {
						for _, r2 := range *fa.Referrers() {
							if stInst, ok := r2.(*ssa.Store); ok && stInst.Addr == fa {
								if r, ok := gs.search(stInst.Val); ok {
									gs.valMemo[v] = r
									return r, true
								}
							}
						}
					}
					if ia, ok := ref.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
						for _, r2 := range *ia.Referrers() {
							if stInst, ok := r2.(*ssa.Store); ok && stInst.Addr == ia {
								if r, ok := gs.search(stInst.Val); ok {
									gs.valMemo[v] = r
									return r, true
								}
							}
						}
					}
				}
			}
			v = x.X
		case *ssa.Call:
			if r, ok := gs.searchCall(x, 0); ok {
				gs.valMemo[v] = r
				return r, true
			}
			gs.valFail[v] = true
			return nil, false
		case *ssa.Phi:
			for _, e := range x.Edges {
				if r, ok := gs.search(e); ok {
					gs.valMemo[v] = r
					return r, true
				}
			}
			gs.valFail[v] = true
			return nil, false
		default:
			gs.valFail[v] = true
			return nil, false
		}
	}
	gs.valFail[v] = true
	return nil, false
}

func (gs *getterSearch) searchCall(call *ssa.Call, resIdx int) (ssa.Value, bool) {
	k := callKey{call, resIdx}
	if gs.callFail[k] {
		return nil, false
	}
	if r, ok := gs.callMemo[k]; ok {
		return r, true
	}

	callee := call.Call.StaticCallee()
	if callee == nil {
		callee, _ = directCallee(call.Call.Value)
	}
	if callee == nil {
		gs.callFail[k] = true
		return nil, false
	}
	np := len(callee.Params)
	if np == 0 {
		if callee.Signature != nil {
			np = callee.Signature.Params().Len()
			if callee.Signature.Recv() != nil {
				np++
			}
		}
		if np == 0 && gs.st != nil {
			sum := gs.st.summary(callee)
			np = len(sum.writeParam)
		}
	}

	// 1. Local callee blocks in same package
	if len(callee.Blocks) > 0 {
		paramRoots := map[ssa.Value]string{}
		paramToRoot := map[ssa.Value]ssa.Value{}
		for i, a := range call.Call.Args {
			if i < len(callee.Params) {
				if r, ok := gs.search(a); ok {
					paramRoots[callee.Params[i]] = gs.roots[r]
					paramToRoot[callee.Params[i]] = r
				}
			}
		}
		if len(paramRoots) > 0 {
			subGS := &getterSearch{
				st:       gs.st,
				roots:    paramRoots,
				visited:  map[ssa.Value]bool{},
				valMemo:  map[ssa.Value]ssa.Value{},
				valFail:  map[ssa.Value]bool{},
				callMemo: gs.callMemo,
				callFail: gs.callFail,
			}
			for _, b := range callee.Blocks {
				for _, ins := range b.Instrs {
					if ret, ok := ins.(*ssa.Return); ok && resIdx < len(ret.Results) {
						if pRoot, ok := subGS.search(ret.Results[resIdx]); ok {
							if orig, ok := paramToRoot[pRoot]; ok {
								gs.callMemo[k] = orig
								return orig, true
							}
						}
					}
				}
			}
		}
	} else if gs.st != nil {
		// 2. Cross-package getters when callee.Blocks == 0 using summary links
		sum := gs.st.summary(callee)
		targetB := np + resIdx
		for _, l := range sum.links {
			paramIdx := -1
			if l.B == targetB && l.A < np {
				paramIdx = l.A
			} else if l.A == targetB && l.B < np {
				paramIdx = l.B
			}
			if paramIdx >= 0 && paramIdx < len(call.Call.Args) {
				if r, ok := gs.search(call.Call.Args[paramIdx]); ok {
					gs.callMemo[k] = r
					return r, true
				}
			}
		}
	}

	gs.callFail[k] = true
	return nil, false
}
