package ownership

import (
	"fmt"
	"go/token"
	"go/types"
	"slices"
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
// so passing shared, non-shareable memory to one is reported as a possible write. A value a static
// call returns is followed back to the call's arguments: through the callee's returns where its body
// is here, through the links of its summary where it is not (rootWalk). It stops at spawn
// sites (the transfer rules own a goroutine the handler starts). It runs inside the registration's
// transfer check, so a //gosree:unsafe on the registration mutes every write it finds.

// The check is bounded in two places, and says so when it reaches either bound: a handler it could
// not check to the end is reported (HandlerWriteCut), never passed as clean. They are variables so a
// test can lower them.
var (
	// handlerStepLimit is how many function bodies one handler's check scans. A body is scanned
	// again only when memory newly found to be shared reaches it.
	handlerStepLimit = 10000
	// rootWalkBudget is how many values one walk back from a value to shared memory visits. The
	// largest walk in the standard library and the reference service visits 110.
	rootWalkBudget = 4096
)

// A frame is a function the check follows the shared memory into. A function go/ssa synthesises (a
// bound-method or instantiation wrapper) has instructions with no source position, so it is followed
// once per call that enters it from source, and a finding in it is reported at that call (site). A
// function with source is one frame however it is reached.
type frame struct {
	fn   *ssa.Function
	site token.Pos
}

// at is where a finding at ins, an instruction of fr.fn, is reported: the instruction's own position,
// else the call that led into this synthesised function, else last (the registration the check
// started from). Never no position: a finding nobody can locate cannot be fixed or hatched, and every
// such finding in a package would be taken for one (reportRelated keys on the position).
func (fr frame) at(ins ssa.Instruction, last token.Pos) token.Pos {
	if p := posOf(ins, ins); p.IsValid() {
		return p
	}
	if fr.site.IsValid() {
		return fr.site
	}
	return last
}

// into is the frame for following a call at ins, an instruction of fr.fn, into callee.
func (fr frame) into(callee *ssa.Function, ins ssa.Instruction, last token.Pos) frame {
	if callee.Synthetic == "" {
		return frame{fn: callee}
	}
	return frame{fn: callee, site: fr.at(ins, last)}
}

// checkHandlerWrites reports SAFE008 for the handler registered at sp and returns whether any
// finding (or incomplete bound cut) was raised.
func (st *state) checkHandlerWrites(sp spawnSite, regPos token.Pos) bool {
	flagged := false
	raise := func(pos token.Pos, cat, msg string) {
		flagged = true
		if !st.dryRun {
			st.report(pos, cat, msg)
		}
	}
	walk := newRootWalk(st.summary)
	ctorFieldSpec := st.specializeHandlerCtorFields(sp)
	shared := map[frame]map[ssa.Value]string{}
	// A frame waits in the queue once however many values are added to it meanwhile: queueing it
	// per value scanned a function with a hundred shared parameters a hundred times.
	var queue []frame
	queued := map[frame]bool{}
	for _, h := range handlerRoots(st.ssa.Pkg.Prog, sp) {
		if start := (frame{fn: h.fn}); len(h.roots) > 0 {
			shared[start] = h.roots
			queue = append(queue, start)
			queued[start] = true
		}
	}
	if len(queue) == 0 {
		return false
	}
	add := func(fr frame, v ssa.Value, desc string) {
		if shared[fr] == nil {
			shared[fr] = map[ssa.Value]string{}
		}
		if _, ok := shared[fr][v]; ok {
			return
		}
		shared[fr][v] = desc
		if !queued[fr] {
			queued[fr] = true
			queue = append(queue, fr)
		}
	}
	// One finding per source line: `t = append(t, x)` is an append into t and a store back to it.
	type line struct {
		file string
		line int
	}
	reported := map[line]bool{}
	once := func(pos token.Pos) bool {
		p := st.pass.Fset.Position(pos)
		if reported[line{p.Filename, p.Line}] {
			return false
		}
		reported[line{p.Filename, p.Line}] = true
		return true
	}
	steps := 0
	for ; len(queue) > 0 && steps < handlerStepLimit; steps++ {
		cur := queue[0]
		queue = queue[1:]
		queued[cur] = false
		fn, r := cur.fn, shared[cur]
		at := func(ins ssa.Instruction) token.Pos { return cur.at(ins, regPos) }
		into := func(callee *ssa.Function, ins ssa.Instruction) frame { return cur.into(callee, ins, regPos) }
		followArgClosures := func(ins ssa.Instruction, args []ssa.Value) {
			for _, a := range args {
				for _, mc := range closureCandidates(a) {
					if argFn, ok := mc.Fn.(*ssa.Function); ok && len(argFn.Blocks) > 0 && st.followable(argFn) {
						for j, b := range mc.Bindings {
							if _, isDirectRoot := r[b]; !isDirectRoot && st.isSafeHandlerValue(b) {
								continue
							}
							if root, ok := walk.root(b, r); ok && j < len(argFn.FreeVars) {
								add(into(argFn, ins), argFn.FreeVars[j], r[root])
							}
						}
					}
				}
			}
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			// A walk back from a value this instruction uses that ran out of budget did not find
			// shared memory and did not rule it out: say so here, where it gave up.
			walk.cut = false
			defer func() {
				if walk.cut {
					raise(at(ins), HandlerWriteCut, fmt.Sprintf(
						"where a value used here comes from could not be followed to the end (more than %d values lead to it), so a write here to memory a handler shares with every concurrent request may be unreported",
						rootWalkBudget))
				}
			}()
			if _, ok := st.spawnSiteOf(ins); ok {
				return
			}
			if w, ok := writeOf(ins); ok {
				if _, guarded := st.slotOf(w.addr); guarded {
					return // SAFE009 checks guarded memory at every access
				}
				if root, ok := walk.root(w.addr, r); ok && !st.isSafeHandlerValue(root) && once(at(ins)) {
					raise(at(ins), HandlerWrite, fmt.Sprintf(
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
				if methodSums, resolved := st.resolveInvokeMethodSummariesWithSpec(fn, cc, ctorFieldSpec); resolved {
					followArgClosures(ins, cc.Args)
					fullArgs := append([]ssa.Value{cc.Value}, cc.Args...)
					for _, sum := range methodSums {
						for i, v := range fullArgs {
							root, ok := walk.root(v, r)
							if !ok || !sum.writes(i) || st.isHandlerCallGuarded(v) || st.isSafeHandlerValue(v) || st.isSafeHandlerValue(root) {
								continue
							}
							if once(at(ins)) {
								raise(at(ins), HandlerWrite, fmt.Sprintf(
									"%s is passed to interface method %s, which may write it, by a handler registered with %s%s, which shares it with every concurrent request",
									r[root], cc.Method.Name(), sp.via, st.at(regPos)))
							}
							return
						}
					}
					return
				}
				for _, v := range append([]ssa.Value{cc.Value}, cc.Args...) {
					root, ok := walk.root(v, r)
					if !ok || st.isHandlerCallGuarded(v) || st.isSafeHandlerValue(v) || st.isSafeHandlerValue(root) {
						continue
					}
					if once(at(ins)) {
						raise(at(ins), HandlerWrite, fmt.Sprintf(
							"%s is passed to interface method %s, which may write it, by a handler registered with %s%s, which shares it with every concurrent request",
							r[root], cc.Method.Name(), sp.via, st.at(regPos)))
						return
					}
				}
				return
			}
			if b, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
				switch b.Name() {
				case "len", "cap", "min", "max", "ssa:wrapnilchk", "print", "println", "recover", "close":
					return
				}
			}
			// Every function the call may run is followed. Where the called value may also be
			// something that could not be resolved to a function (rest), that something may
			// write what the call hands it, and what it is itself: a function read from shared
			// memory may be a closure over that memory. What a known target captures is its own
			// and is followed with it.
			targets, rest := callTargets(cc.Value)
			if len(targets) == 0 && len(rest) == 1 && rest[0] == cc.Value {
				if fieldFns, ok := st.resolveLoadedFieldCallables(cc.Value); ok {
					rest = nil
					for _, fnTarget := range fieldFns {
						targets = append(targets, callTarget{fn: fnTarget})
					}
				} else if fvTargets, ok := st.resolveFreeVarCallables(cc.Value); ok {
					rest = nil
					for i, a := range cc.Args {
						root, hasRoot := walk.root(a, r)
						if !hasRoot || st.isHandlerCallGuarded(a) || st.isSafeHandlerValue(a) || st.isSafeHandlerValue(root) {
							continue
						}
						mayWriteArg := false
						for _, ft := range fvTargets {
							if st.summary(ft.fn).writes(i) {
								mayWriteArg = true
								break
							}
						}
						if mayWriteArg {
							if once(at(ins)) {
								raise(at(ins), HandlerWrite, fmt.Sprintf(
									"%s is passed to a dynamic function call, which may write it, by a handler registered with %s%s, which shares it with every concurrent request",
									r[root], sp.via, st.at(regPos)))
							}
							return
						}
					}
					followArgClosures(ins, cc.Args)
					for _, ft := range fvTargets {
						if ft.mc == nil {
							continue
						}
						sum := st.summary(ft.fn)
						needsFollow := false
						for j, b := range ft.mc.Bindings {
							_, isDirectRoot := r[b]
							if !st.isHandlerCallGuarded(b) && (isDirectRoot || !st.isSafeHandlerValue(b)) && sum.writesFree(j) {
								needsFollow = true
								break
							}
						}
						if !needsFollow {
							continue
						}
						if len(ft.fn.Blocks) == 0 || !st.followable(ft.fn) {
							if once(at(ins)) {
								raise(at(ins), HandlerWrite, fmt.Sprintf(
									"captured closure is invoked at a dynamic function call, which may write shared memory, by a handler registered with %s%s",
									sp.via, st.at(regPos)))
							}
							return
						}
						for j, b := range ft.mc.Bindings {
							_, isDirectRoot := r[b]
							if j >= len(ft.fn.FreeVars) || st.isHandlerCallGuarded(b) || (!isDirectRoot && st.isSafeHandlerValue(b)) || !sum.writesFree(j) {
								continue
							}
							desc := describe(b)
							if root, hasRoot := walk.root(b, r); hasRoot {
								desc = r[root]
							}
							add(into(ft.fn, ins), ft.fn.FreeVars[j], desc)
						}
					}
					return
				}
			}
			if len(rest) > 0 {
				handed := append(append([]ssa.Value(nil), cc.Args...), rest...)
				for _, v := range handed {
					root, ok := walk.root(v, r)
					if !ok || st.isHandlerCallGuarded(v) || st.isSafeHandlerValue(v) || st.isSafeHandlerValue(root) {
						continue
					}
					if once(at(ins)) {
						raise(at(ins), HandlerWrite, fmt.Sprintf(
							"%s is passed to a dynamic function call, which may write it, by a handler registered with %s%s, which shares it with every concurrent request",
							r[root], sp.via, st.at(regPos)))
						return
					}
				}
			}
			if len(targets) == 0 {
				return
			}
			// A closure handed on as an argument may be called by the callee: every closure the
			// argument may be is followed with what it captures.
			followArgClosures(ins, cc.Args)
			for _, t := range targets {
				callee := t.fn
				if len(callee.Blocks) == 0 || !st.followable(callee) {
					if st.checkCalleeWrites(at(ins), cc, callee, r, walk, sp, regPos, once) {
						flagged = true
					}
					continue
				}
				for i, a := range cc.Args {
					if i >= len(callee.Params) {
						break
					}
					if _, isDirectRoot := r[a]; !isDirectRoot && st.isSafeHandlerValue(a) {
						continue
					}
					if root, ok := walk.root(a, r); ok {
						add(into(callee, ins), callee.Params[i], r[root])
					}
				}
				if t.mc == nil {
					continue
				}
				for j, b := range t.mc.Bindings {
					if _, isDirectRoot := r[b]; !isDirectRoot && st.isSafeHandlerValue(b) {
						continue
					}
					if root, ok := walk.root(b, r); ok && j < len(callee.FreeVars) {
						add(into(callee, ins), callee.FreeVars[j], r[root])
					}
				}
			}
		})
	}
	if len(queue) > 0 {
		raise(regPos, HandlerWriteCut, fmt.Sprintf(
			"the write check of the handler registered with %s stopped after scanning %d function bodies with shared memory still to follow, so a write to memory the handler shares with every concurrent request may be unreported",
			sp.via, steps))
	}
	return flagged
}

func (st *state) isSafeHandlerValue(v ssa.Value) bool {
	if v == nil {
		return true
	}
	return st.cls.Shareable(v.Type()) || st.isAutoSyncType(v.Type()) || st.isAutoSyncCallResultValue(v)
}

// specializeHandlerCtorFields resolves concrete interface types injected at same-package
// constructor calls (`h := NewHandler(dep)`) in the function registering sp for unexported
// read-only-post-construction (`roLocalFields`) interface fields of `h`.
func (st *state) specializeHandlerCtorFields(sp spawnSite) map[*types.Var][]types.Type {
	if sp.ins == nil || sp.ins.Parent() == nil || sp.fn == nil || st.pass == nil {
		return nil
	}
	callerFn := sp.ins.Parent()
	mc, isMC := unwrapClosureValue(sp.fn)
	if !isMC || len(mc.Bindings) == 0 {
		return nil
	}
	spec := map[*types.Var][]types.Type{}
	hasSpec := map[*types.Var]bool{}
	poisoned := map[*types.Var]bool{}
	visited := map[ssa.Value]bool{}

	addConcrete := func(f *types.Var, ct types.Type) {
		if ct == nil || poisoned[f] {
			return
		}
		hasSpec[f] = true
		for _, ex := range spec[f] {
			if types.Identical(ex, ct) {
				return
			}
		}
		spec[f] = append(spec[f], ct)
	}
	poisonStructFields := func(t types.Type) {
		_, s := derefNamedStruct(t)
		if s == nil {
			return
		}
		for i := range s.NumFields() {
			poisoned[s.Field(i).Origin()] = true
		}
	}
	singleStoredVal := func(alloc *ssa.Alloc) (ssa.Value, bool) {
		uses := usesOfCell(alloc)
		if uses.open || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 || len(uses.stores) != 1 {
			return nil, false
		}
		return uses.stores[0].Val, true
	}
	unwrapLocalVal := func(v ssa.Value) ssa.Value {
		seen := map[ssa.Value]bool{}
		for v != nil && !seen[v] {
			seen[v] = true
			switch x := v.(type) {
			case *ssa.ChangeType:
				v = x.X
			case *ssa.UnOp:
				if x.Op == token.MUL {
					if alloc, isAlloc := x.X.(*ssa.Alloc); isAlloc && alloc.Parent() == callerFn {
						if sv, ok := singleStoredVal(alloc); ok {
							v = sv
							continue
						}
					}
				}
				return v
			case *ssa.Alloc:
				if x.Parent() == callerFn {
					if sv, ok := singleStoredVal(x); ok {
						v = sv
						continue
					}
				}
				return v
			default:
				return v
			}
		}
		return v
	}

	var inspectVal func(v ssa.Value)
	inspectVal = func(v ssa.Value) {
		v = unwrapLocalVal(v)
		if v == nil || visited[v] {
			return
		}
		visited[v] = true
		named, s := derefNamedStruct(v.Type())
		if named == nil || s == nil || named.Origin().Obj() == nil || named.Origin().Obj().Pkg() != st.pass.Pkg {
			return
		}
		ctorCall, isCall := v.(*ssa.Call)
		if !isCall || ctorCall.Parent() != callerFn || ctorCall.Call.IsInvoke() {
			poisonStructFields(v.Type())
			return
		}
		ctor := ctorCall.Call.StaticCallee()
		if ctor == nil || ctor.Pkg != st.ssa.Pkg || len(ctor.Blocks) == 0 {
			poisonStructFields(v.Type())
			return
		}
		// Verify ctor returns only nil or a fresh *ssa.Alloc of this struct constructed inside ctor.
		var ctorAllocs []*ssa.Alloc
		for _, b := range ctor.Blocks {
			if len(b.Instrs) == 0 {
				continue
			}
			ret, isRet := b.Instrs[len(b.Instrs)-1].(*ssa.Return)
			if !isRet {
				continue
			}
			if len(ret.Results) != 1 {
				poisonStructFields(v.Type())
				return
			}
			rv := ret.Results[0]
			if c, isConst := rv.(*ssa.Const); isConst && c.IsNil() {
				continue
			}
			alloc, isAlloc := rv.(*ssa.Alloc)
			if !isAlloc || alloc.Parent() != ctor {
				poisonStructFields(v.Type())
				return
			}
			if !slices.Contains(ctorAllocs, alloc) {
				ctorAllocs = append(ctorAllocs, alloc)
			}
		}
		if len(ctorAllocs) == 0 {
			poisonStructFields(v.Type())
			return
		}

		// Recursively inspect same-package struct arguments passed into ctor (e.g. SynthesizeHandler(llmHandler)).
		for _, arg := range ctorCall.Call.Args {
			inspectVal(arg)
		}

		for fi := range s.NumFields() {
			sf := s.Field(fi)
			fOrigin := sf.Origin()
			if poisoned[fOrigin] || sf.Exported() || sf.Embedded() || !st.roLocalFields[fOrigin] {
				continue
			}
			if _, isIface := sf.Type().Underlying().(*types.Interface); !isIface {
				continue
			}
			hasSpec[fOrigin] = true
			var evalStoreVal func(sv ssa.Value, seen map[ssa.Value]bool) bool
			evalStoreVal = func(sv ssa.Value, seen map[ssa.Value]bool) bool {
				if sv == nil {
					return false
				}
				if seen[sv] {
					return true
				}
				seen[sv] = true
				if c, isConst := sv.(*ssa.Const); isConst && c.IsNil() {
					return true
				}
				if st.isAutoSyncCallResultValue(sv) {
					return true
				}
				switch x := sv.(type) {
				case *ssa.Parameter:
					pIdx := slices.Index(ctor.Params, x)
					if pIdx < 0 || pIdx >= len(ctorCall.Call.Args) {
						return false
					}
					arg := ctorCall.Call.Args[pIdx]
					if c, isConst := arg.(*ssa.Const); isConst && c.IsNil() {
						return true
					}
					if st.isAutoSyncCallResultValue(arg) {
						return true
					}
					q := st.ask()
					ts, incomplete := q.resolveInterfaceTypes(callerFn, arg)
					if incomplete || q.cut || len(ts) == 0 {
						return false
					}
					for _, ct := range ts {
						addConcrete(fOrigin, ct)
					}
					return true
				case *ssa.ChangeInterface:
					return evalStoreVal(x.X, seen)
				case *ssa.Phi:
					if len(x.Edges) == 0 {
						return false
					}
					for _, edge := range x.Edges {
						if !evalStoreVal(edge, seen) {
							return false
						}
					}
					return true
				default:
					q := st.ask()
					ts, incomplete := q.resolveInterfaceTypes(ctor, sv)
					if incomplete || q.cut || len(ts) == 0 {
						return false
					}
					for _, ct := range ts {
						addConcrete(fOrigin, ct)
					}
					return true
				}
			}

			for _, alloc := range ctorAllocs {
				if alloc.Referrers() == nil {
					continue
				}
				for _, r := range *alloc.Referrers() {
					fa, isFA := r.(*ssa.FieldAddr)
					if !isFA || fa.X != alloc || fa.Field != fi || fa.Referrers() == nil {
						continue
					}
					for _, fr := range *fa.Referrers() {
						stIns, isStore := fr.(*ssa.Store)
						if !isStore || stIns.Addr != fa {
							continue
						}
						if !evalStoreVal(stIns.Val, map[ssa.Value]bool{}) {
							poisoned[fOrigin] = true
							break
						}
					}
				}
			}
		}
	}

	for _, b := range mc.Bindings {
		inspectVal(b)
	}
	out := map[*types.Var][]types.Type{}
	for f := range hasSpec {
		if !poisoned[f] {
			out[f] = spec[f]
		}
	}
	return out
}

func (st *state) isHandlerCallGuarded(v ssa.Value) bool {
	slot, guarded := st.slotOf(v)
	if !guarded {
		return false
	}
	if slot.inferred {
		return st.isCancelFuncValue(v)
	}
	return true
}

// checkCalleeWrites reports SAFE008 at a call the write check does not follow into (a function of
// another package, or one with no body): an argument that leads back to shared memory is written when
// the callee's summary says it may write that parameter. Shareable arguments (a mutex, an atomic, an
// imm value) are exempt: their methods synchronise or never write. pos is where the call is
// reported, which is not the call's own position when it sits in a synthesised function.
func (st *state) checkCalleeWrites(pos token.Pos, cc *ssa.CallCommon, callee *ssa.Function, r map[ssa.Value]string, walk *rootWalk, sp spawnSite, regPos token.Pos, once func(token.Pos) bool) bool {
	sum := st.summary(callee)
	for i, a := range cc.Args {
		root, ok := walk.root(a, r)
		if !ok || !sum.writes(i) || st.isSafeHandlerValue(a) || st.isSafeHandlerValue(root) {
			continue
		}
		if !st.dryRun && once(pos) {
			st.report(pos, HandlerWrite, fmt.Sprintf(
				"%s is passed to %s, which may write it%s, by a handler registered with %s%s, which shares it with every concurrent request",
				r[root], calleeName(callee), sum.unavailable(), sp.via, st.at(regPos)))
		}
		return true
	}
	return false
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
// Each value is looked at once, so the walk ends without a count: a constructor that leads back
// to a value already visited (one that only ever returns its own result) builds no handler.
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
	var resolve func(v ssa.Value)
	resolve = func(v ssa.Value) {
		if v == nil || seen[v] {
			return
		}
		seen[v] = true
		switch x := v.(type) {
		case *ssa.ChangeType:
			resolve(x.X)
		case *ssa.Call:
			if callee := x.Call.StaticCallee(); callee != nil && len(callee.Blocks) > 0 {
				returned := false
				for _, b := range callee.Blocks {
					for _, ins := range b.Instrs {
						if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
							returned = true
							resolve(ret.Results[0])
						}
					}
				}
				if returned {
					return
				}
			}
			for _, a := range x.Call.Args {
				if spawnedFunc(prog, a, true) != nil {
					resolve(a)
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
			resolve(x.X)
		case *ssa.Phi:
			for _, e := range x.Edges {
				resolve(e)
			}
		case *ssa.UnOp:
			if x.Op == token.MUL {
				if alloc, ok := x.X.(*ssa.Alloc); ok {
					uses := usesOfCell(alloc)
					if !uses.open && len(uses.fields) == 0 && len(uses.elems) == 0 && len(uses.slices) == 0 {
						for _, stIns := range uses.stores {
							resolve(stIns.Val)
						}
					}
				}
			}
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
	resolve(sp.fn)
	return out
}

// hasCompleteHandlerRoots reports whether every non-nil branch of sp.fn resolves completely to
// known function/handler bodies in prog (with at least one such body and zero unknown leaves).
func hasCompleteHandlerRoots(prog *ssa.Program, sp spawnSite) bool {
	if !sp.repeated || sp.fn == nil {
		return false
	}
	foundBody := false
	seen := map[ssa.Value]bool{}
	var check func(v ssa.Value) bool
	check = func(v ssa.Value) bool {
		if v == nil {
			return false
		}
		if seen[v] {
			return true
		}
		seen[v] = true
		switch x := v.(type) {
		case *ssa.Const:
			return x.IsNil()
		case *ssa.ChangeType:
			return check(x.X)
		case *ssa.Phi:
			if len(x.Edges) == 0 {
				return false
			}
			for _, e := range x.Edges {
				if !check(e) {
					return false
				}
			}
			return true
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return false
			}
			alloc, ok := x.X.(*ssa.Alloc)
			if !ok {
				return false
			}
			uses := usesOfCell(alloc)
			if uses.open || len(uses.stores) == 0 || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 {
				return false
			}
			for _, stIns := range uses.stores {
				if !check(stIns.Val) {
					return false
				}
			}
			return true
		case *ssa.Call:
			if callee := x.Call.StaticCallee(); callee != nil && len(callee.Blocks) > 0 {
				returned := false
				for _, b := range callee.Blocks {
					for _, ins := range b.Instrs {
						if ret, ok := ins.(*ssa.Return); ok {
							if len(ret.Results) != 1 {
								return false
							}
							returned = true
							if !check(ret.Results[0]) {
								return false
							}
						}
					}
				}
				if returned {
					return true
				}
			}
			for _, a := range x.Call.Args {
				if spawnedFunc(prog, a, true) != nil {
					return check(a)
				}
			}
			return false
		case *ssa.MakeInterface:
			if fn := spawnedFunc(prog, x, true); fn != nil {
				if _, isClosure := unwrapToClosure(x); !isClosure {
					if len(fn.Blocks) == 0 {
						return false
					}
					foundBody = true
					return true
				}
			}
			return check(x.X)
		case *ssa.MakeClosure:
			fn, ok := x.Fn.(*ssa.Function)
			if !ok || len(fn.Blocks) == 0 {
				return false
			}
			foundBody = true
			return true
		case *ssa.Function:
			if len(x.Blocks) == 0 {
				return false
			}
			foundBody = true
			return true
		default:
			return false
		}
	}
	return check(sp.fn) && foundBody
}

// unwrapToClosure reports whether a boxed handler is a converted function value (http.HandlerFunc).
func unwrapToClosure(mi *ssa.MakeInterface) (ssa.Value, bool) {
	// A conversion's operand is defined before it, so a chain of them ends.
	v := mi.X
	for {
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

// directCallee returns the function a call runs when it can be only one: the function itself, a
// closure built in place, or a variable that holds one function and nothing else (callTargets).
// The second result is the closure's bindings. A value that may be one of several functions has
// no direct callee: it used to answer with the first closure it found, and whoever asked took
// that one for all of them. The callers that can work with several ask for the targets.
func directCallee(v ssa.Value) (*ssa.Function, []ssa.Value) {
	switch x := v.(type) {
	case *ssa.Function:
		return x, nil
	case *ssa.MakeClosure:
		return x.Fn.(*ssa.Function), x.Bindings
	}
	if targets, rest := callTargets(v); len(rest) == 0 && len(targets) == 1 {
		if mc := targets[0].mc; mc != nil {
			return targets[0].fn, mc.Bindings
		}
		return targets[0].fn, nil
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

// rootWalk is what a walk back to shared memory reads besides the value: the summaries of callees
// whose bodies are not here, and a memo of which parameters each result of a function leads back
// to. The memo makes a chain of getters cost one walk per function instead of one per path: without
// it a chain of N functions with two returns each is walked 2^N times when it leads nowhere.
type rootWalk struct {
	// sums returns a callee's summary; nil for a walk that reads none, where the result of a call
	// into another package ends the walk.
	sums func(*ssa.Function) *summary
	// derive[fn, k] are the parameters result k of fn may be memory of.
	derive map[resultKey][]int
	// busy holds, for each result being computed, the parameters found for it so far: what a
	// recursive call to the function is taken to return until the computation settles.
	busy map[resultKey][]int
	// met are the busy results a walk read since the set was last cleared. A result computed from
	// another's unsettled answer is not kept.
	met map[resultKey]bool
	// cut is set when a walk stopped on its budget (rootWalkBudget) instead of at an answer: its
	// "no" then means "not found", not "not there". The caller clears it before the walks it asks
	// about and reads it after. cutResults are the memoised results a cut walk went into, so reading
	// one of them later sets cut again.
	cut        bool
	cutResults map[resultKey]bool
	isSafe     func(ssa.Value) bool
}

type resultKey struct {
	fn *ssa.Function
	k  int
}

// newRootWalk returns a walk that reads callee summaries from sums; with nil it reads none, and the
// result of a call into another package ends the walk.
func newRootWalk(sums func(*ssa.Function) *summary) *rootWalk {
	return &rootWalk{sums: sums, derive: map[resultKey][]int{}, busy: map[resultKey][]int{}, met: map[resultKey]bool{}, cutResults: map[resultKey]bool{}}
}

func newRootWalkWithSafe(sums func(*ssa.Function) *summary, isSafe func(ssa.Value) bool) *rootWalk {
	rw := newRootWalk(sums)
	rw.isSafe = isSafe
	return rw
}

// root follows v back through derivations, loads and call results to one of roots.
func (w *rootWalk) root(v ssa.Value, roots map[ssa.Value]string) (ssa.Value, bool) {
	return sharedRootSeen(v, roots, map[ssa.Value]bool{}, w)
}

// resultParams returns the parameters of fn that its result k may be memory of, by following every
// return of its body. A result that only contains a parameter's memory (a new wrapper around it) is
// not memory of it.
//
// A recursive function is solved as a fixpoint: while its result is being computed, a call to it
// returns the parameters found so far, and the returns are walked again until that set stops growing
// (it only grows, and has at most one entry per parameter). So f(x, y) that returns f(y, x) finds
// both. A result computed while another function's result was still unsettled is returned to that
// computation and not kept, since the other may yet grow.
func (w *rootWalk) resultParams(fn *ssa.Function, k int) []int {
	key := resultKey{fn, k}
	if out, ok := w.derive[key]; ok {
		if w.cutResults[key] {
			w.cut = true
		}
		return out
	}
	if out, ok := w.busy[key]; ok {
		w.met[key] = true
		return out
	}
	w.busy[key] = nil
	outer := w.met
	w.met = map[resultKey]bool{}
	// Whether this result's own walks were cut is kept with it (cutResults); the caller's flag is
	// put back, and set as well if they were.
	cutBefore := w.cut
	w.cut = false
	defer func() {
		if w.cut {
			w.cutResults[key] = true
		}
		w.cut = w.cut || cutBefore
	}()
	index := make(map[ssa.Value]int, len(fn.Params))
	for i, p := range fn.Params {
		index[p] = i
	}
	found := map[int]bool{}
	var out []int
	for {
		delete(w.met, key)
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				ret, ok := ins.(*ssa.Return)
				if !ok || k >= len(ret.Results) {
					continue
				}
				// A walk stops at the first parameter it reaches, and a Phi may reach several: walk
				// again without the ones found until none is left.
				params := make(map[ssa.Value]string, len(fn.Params))
				for _, p := range fn.Params {
					if !found[index[p]] {
						params[p] = ""
					}
				}
				for len(params) > 0 {
					p, ok := w.root(ret.Results[k], params)
					if !ok {
						break
					}
					found[index[p]] = true
					delete(params, p)
				}
			}
		}
		if len(found) == len(out) || !w.met[key] {
			break // nothing new, or nothing read the unsettled set: settled
		}
		out = out[:0:0]
		for i := range found {
			out = append(out, i)
		}
		slices.Sort(out)
		w.busy[key] = out
	}
	out = out[:0:0]
	for i := range found {
		out = append(out, i)
	}
	slices.Sort(out)
	delete(w.busy, key)
	delete(w.met, key)
	if len(w.met) == 0 {
		w.derive[key] = out
	}
	for other := range w.met {
		outer[other] = true
	}
	w.met = outer
	return out
}

// callResultRoot reports whether result k of call is memory of one of roots: the callee returns
// memory of an argument that leads back to a root. With the callee's body it asks resultParams.
// Without it (a function of another package) it reads the callee's summary, when the walk has
// summaries: a link that joins result k itself (depth 0) to an argument says the same thing. Both
// answer one question, so a getter is treated alike wherever it is declared and however many
// results it has.
func callResultRoot(call *ssa.Call, k int, roots map[ssa.Value]string, seen map[ssa.Value]bool, w *rootWalk) (ssa.Value, bool) {
	callee := call.Call.StaticCallee()
	if callee == nil {
		return nil, false
	}
	var from []int
	if len(callee.Blocks) > 0 {
		from = w.resultParams(callee, k)
	} else if w.sums != nil {
		if sum := w.sums(callee); sum != nil {
			nArgs := len(call.Call.Args)
			for _, l := range sum.links {
				// Positions index the arguments, then the results, and A is the smaller: a link
				// between an argument and a result has the argument at A.
				if l.B == nArgs+k && l.DepthB == 0 && l.A < nArgs {
					from = append(from, l.A)
				}
			}
		}
	}
	for _, i := range from {
		if i >= len(call.Call.Args) {
			continue
		}
		if r, ok := sharedRootSeen(call.Call.Args[i], roots, seen, w); ok {
			return r, true
		}
	}
	return nil, false
}

// sharedRootSeen is one walk: seen holds the values it has visited. Each turn of the loop visits a
// value not visited before, so the walk ends; rootWalkBudget bounds what one walk costs, and a walk
// that reaches it sets w.cut instead of answering no in silence.
func sharedRootSeen(v ssa.Value, roots map[ssa.Value]string, seen map[ssa.Value]bool, w *rootWalk) (ssa.Value, bool) {
	first := len(seen) == 0
	for {
		if _, ok := roots[v]; ok {
			return v, true
		}
		if v == nil || seen[v] {
			return nil, false
		}
		if !first && w.isSafe != nil && w.isSafe(v) {
			return nil, false
		}
		first = false
		if len(seen) >= rootWalkBudget {
			w.cut = true
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
			if call, ok := x.Tuple.(*ssa.Call); ok {
				return callResultRoot(call, x.Index, roots, seen, w)
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
				return nil, false
			}
			var alloc *ssa.Alloc
			targetField := -1
			hasTargetIndex := false
			var targetIndex int64
			if a, ok := x.X.(*ssa.Alloc); ok {
				alloc = a
			} else if fa, ok := x.X.(*ssa.FieldAddr); ok {
				if a, isA := fa.X.(*ssa.Alloc); isA {
					alloc = a
					targetField = fa.Field
				}
			} else if ia, ok := x.X.(*ssa.IndexAddr); ok {
				if a, isA := ia.X.(*ssa.Alloc); isA {
					alloc = a
					targetIndex, hasTargetIndex = constIndex(ia.Index)
				}
			}
			if alloc != nil && alloc.Referrers() != nil {
				var checkAddrStores func(addr ssa.Value) (ssa.Value, bool)
				checkAddrStores = func(addr ssa.Value) (ssa.Value, bool) {
					if addr.Referrers() == nil {
						return nil, false
					}
					for _, r2 := range *addr.Referrers() {
						switch sub := r2.(type) {
						case *ssa.Store:
							if sub.Addr == addr {
								if r, ok := sharedRootSeen(sub.Val, roots, seen, w); ok {
									return r, true
								}
							}
						case *ssa.FieldAddr:
							if sub.X == addr {
								if r, ok := checkAddrStores(sub); ok {
									return r, true
								}
							}
						case *ssa.IndexAddr:
							if sub.X == addr {
								if r, ok := checkAddrStores(sub); ok {
									return r, true
								}
							}
						}
					}
					return nil, false
				}
				for _, ref := range *alloc.Referrers() {
					if st, ok := ref.(*ssa.Store); ok && st.Addr == alloc {
						if r, ok := sharedRootSeen(st.Val, roots, seen, w); ok {
							return r, true
						}
					}
					if fa, ok := ref.(*ssa.FieldAddr); ok && fa.X == alloc {
						if targetField >= 0 && fa.Field != targetField {
							continue
						}
						if r, ok := checkAddrStores(fa); ok {
							return r, true
						}
					}
					if ia, ok := ref.(*ssa.IndexAddr); ok && ia.X == alloc {
						if targetField >= 0 {
							continue
						}
						if hasTargetIndex {
							if ci, ok := constIndex(ia.Index); ok && ci != targetIndex {
								continue
							}
						}
						if r, ok := checkAddrStores(ia); ok {
							return r, true
						}
					}
				}
			}
			v = x.X
		case *ssa.Call:
			if x.Call.Signature().Results().Len() != 1 {
				return nil, false // a tuple is not memory; its elements are reached through Extract
			}
			return callResultRoot(x, 0, roots, seen, w)
		case *ssa.Phi:
			for _, e := range x.Edges {
				if r, ok := sharedRootSeen(e, roots, seen, w); ok {
					return r, true
				}
			}
			return nil, false
		default:
			return nil, false
		}
	}
}
