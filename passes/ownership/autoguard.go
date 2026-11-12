package ownership

import (
	"go/token"
	"go/types"
	"slices"
	"strconv"

	"golang.org/x/tools/go/ssa"
)

// autoHoldInfo records an unexported, closed-call-site method proven to be entered with one of
// its receiver struct's value mutex fields already held on every call path.
type autoHoldInfo struct {
	mu   *types.Var
	mode lockMode
}

func isSyncMutexType(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	if named.Obj().Pkg().Path() != "sync" {
		return false
	}
	name := named.Obj().Name()
	return name == "Mutex" || name == "RWMutex"
}

func isSignatureType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Signature)
	return ok
}

// isCancelFuncOrigin reports whether v originates exclusively from nil constants and stdlib
// context.WithCancel / WithTimeout / WithDeadline / WithCancelCause cancel results.
// A context.CancelFunc / CancelCauseFunc is thread-safe to invoke from any goroutine outside the
// lock that guards the map or struct field holding it, and writes no user memory.
func isCancelFuncOrigin(v ssa.Value, seen map[ssa.Value]bool) bool {
	if v == nil {
		return true
	}
	if seen == nil {
		seen = map[ssa.Value]bool{}
	}
	if seen[v] {
		return true
	}
	seen[v] = true
	switch x := v.(type) {
	case *ssa.Const:
		return x.IsNil()
	case *ssa.Extract:
		if x.Index != 1 {
			return false
		}
		c, ok := x.Tuple.(*ssa.Call)
		if !ok || c.Call.IsInvoke() {
			return false
		}
		callee := c.Call.StaticCallee()
		if callee == nil || callee.Pkg == nil || callee.Pkg.Pkg == nil || callee.Pkg.Pkg.Path() != "context" {
			return false
		}
		switch callee.Name() {
		case "WithCancel", "WithCancelCause", "WithTimeout", "WithTimeoutCause", "WithDeadline", "WithDeadlineCause":
			return true
		}
		return false
	case *ssa.ChangeType:
		return isCancelFuncOrigin(x.X, seen)
	case *ssa.Phi:
		for _, edge := range x.Edges {
			if !isCancelFuncOrigin(edge, seen) {
				return false
			}
		}
		return len(x.Edges) > 0
	case *ssa.UnOp:
		if x.Op == token.MUL {
			if sv := singleStableStore(x); sv != nil {
				return isCancelFuncOrigin(sv, seen)
			}
		}
	}
	return false
}

func singleStableStore(load *ssa.UnOp) ssa.Value {
	cell, ok := load.X.(*ssa.Alloc)
	if !ok || !stableLoad(load) || cell.Referrers() == nil {
		return nil
	}
	var store *ssa.Store
	for _, r := range *cell.Referrers() {
		if s, isStore := r.(*ssa.Store); isStore && s.Addr == cell {
			if store != nil {
				return nil
			}
			store = s
		}
	}
	if store != nil && store.Block() != nil && dominates(store, load) {
		return store.Val
	}
	return nil
}

// isCancelFuncValue reports whether v is a function value read from or tracing back to stdlib
// context cancellation closures only.
func (st *state) isCancelFuncValue(v ssa.Value) bool {
	if !isSignatureType(v.Type()) {
		return false
	}
	if isCancelFuncOrigin(v, nil) {
		return true
	}
	s, ok := st.slotOf(v)
	if !ok || !s.inferred {
		return false
	}
	return st.cancelSafeVars[s.v]
}

// inferAutoGuards proves unannotated mutex-encapsulated struct fields and package variables
// (the Go equivalent of Rust's Mutex<T>: Sync interior-mutability firewall) directly from SSA
// lockset dominance and alias/escape containment, before function summaries are computed.
//
// Soundness invariants:
//  1. Unexported encapsulation: only unexported non-embedded struct fields (or fields of an
//     unexported struct never exposed across package boundaries) on structs declared in this
//     package with no struct-type conversions, no value-receiver methods, and no whole-struct
//     loads/overwrites; or unexported package variables guarded by an uncopied package mutex.
//  2. Exclusive write dominance: >= 1 post-construction write, and 100% of post-construction
//     writes hold the candidate mutex in lockWrite mode.
//  3. Read dominance: 100% of post-construction reads hold the SAME mutex in >= lockRead mode.
//  4. Unique interior ownership: every non-shareable reference (map, slice, pointer) stored into
//     the guarded slot originates from a fresh local allocation (never an unowned caller/global
//     alias whose backing memory could be mutated outside the mutex).
//  5. No unlocked reference escape: &slot never escapes, and no non-shareable reference read from
//     the slot leaves the critical section.
func (st *state) inferAutoGuards(fns []*ssa.Function) {
	st.autoGuarded = map[*types.Var]*types.Var{}
	st.paramAutoGuarded = map[*types.Var]*types.Var{}
	st.autoHolds = map[*types.Func]autoHoldInfo{}
	st.cancelSafeVars = map[*types.Var]bool{}
	if st.contracts == nil || st.ssa == nil || st.ssa.Pkg == nil {
		return
	}

	scanFns := fns
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil && !slices.Contains(scanFns, pkgInit) {
		scanFns = append(append([]*ssa.Function(nil), fns...), pkgInit)
	}
	if !st.hasAutoGuardCandidates() || !st.hasLockOps(scanFns) {
		return
	}

	gs := st.ensureGuardSummaries(fns)
	st.inferAutoHolds(gs, fns, scanFns)
	st.inferAutoGuardedFields(gs, scanFns)
	st.inferAutoGuardedGlobals(gs, scanFns)
}

// refineAutoGuardsPostSummaries re-evaluates unguarded candidate fields after computeSummaries so
// same-package fresh-cloning helpers (e.g. cloneViaJSON) have non-pessimistic summaries available
// in isOwnedInteriorVal and verifyLoadedValueUses. Returns true if any new field joined autoGuarded.
func (st *state) refineAutoGuardsPostSummaries(fns []*ssa.Function) bool {
	if st.contracts == nil || st.ssa == nil || st.ssa.Pkg == nil {
		return false
	}
	scanFns := fns
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil && !slices.Contains(scanFns, pkgInit) {
		scanFns = append(append([]*ssa.Function(nil), fns...), pkgInit)
	}
	if !st.hasAutoGuardCandidates() || !st.hasLockOps(scanFns) {
		return false
	}
	prev := len(st.autoGuarded)
	prevParam := len(st.paramAutoGuarded)
	gs := st.ensureGuardSummaries(fns)
	st.inferAutoGuardedFields(gs, scanFns)
	st.inferAutoGuardedGlobals(gs, scanFns)
	return len(st.autoGuarded) > prev || len(st.paramAutoGuarded) > prevParam
}

func (st *state) hasAutoGuardCandidates() bool {
	for _, named := range st.contracts.localAllNamedTypes {
		s, ok := named.Underlying().(*types.Struct)
		if !ok || s.NumFields() == 0 {
			continue
		}
		hasMu, hasData := false, false
		for k := range s.NumFields() {
			sf := s.Field(k)
			if isSyncMutexType(sf.Type()) {
				hasMu = true
				continue
			}
			hasData = true
		}
		if hasMu && hasData {
			return true
		}
	}
	hasGlobalMu, hasGlobalData := false, false
	isMain := st.pass != nil && st.pass.Pkg != nil && st.pass.Pkg.Name() == "main"
	for _, mem := range st.ssa.Pkg.Members {
		g, ok := mem.(*ssa.Global)
		if !ok {
			continue
		}
		ptr, ok := g.Type().Underlying().(*types.Pointer)
		if !ok {
			continue
		}
		if isSyncMutexType(ptr.Elem()) {
			if !token.IsExported(g.Name()) || isMain {
				hasGlobalMu = true
			}
			continue
		}
		if !token.IsExported(g.Name()) || isMain {
			if gv, ok := g.Object().(*types.Var); ok && st.ix.Guarded[gv] == nil {
				hasGlobalData = true
			}
		}
	}
	return hasGlobalMu && hasGlobalData
}

func (st *state) inferAutoHolds(gs *guardState, fns, scanFns []*ssa.Function) {
	ci := st.contracts
	invokedNames := map[string]bool{}
	for _, fn := range scanFns {
		forEachInstr(fn, func(ins ssa.Instruction) {
			if c, ok := ins.(ssa.CallInstruction); ok && c.Common().IsInvoke() && c.Common().Method != nil {
				invokedNames[c.Common().Method.Name()] = true
			}
		})
	}

	type holdCand struct {
		fn     *ssa.Function
		obj    *types.Func
		s      *types.Struct
		muIdxs []int
	}
	var cands []holdCand
	active := map[*types.Func]map[int]lockMode{}

	for _, fn := range fns {
		if fn.Synthetic != "" || len(fn.Blocks) == 0 || len(fn.Params) == 0 {
			continue
		}
		obj, ok := fn.Object().(*types.Func)
		if !ok || obj.Exported() {
			continue
		}
		orig := obj.Origin()
		if st.ix.Holds[orig] != nil || ci.openCallers[fn] || invokedNames[orig.Name()] {
			continue
		}
		if fn.Signature == nil || fn.Signature.Recv() == nil {
			continue
		}
		ptr, ok := fn.Params[0].Type().Underlying().(*types.Pointer)
		if !ok {
			continue
		}
		named, ok := types.Unalias(ptr.Elem()).(*types.Named)
		if !ok || named.Obj() == nil || named.Obj().Pkg() != st.pass.Pkg {
			continue
		}
		s, ok := named.Underlying().(*types.Struct)
		if !ok || s.NumFields() == 0 {
			continue
		}
		sites := ci.callSitesOf[fn]
		if len(sites) == 0 {
			continue
		}
		allDirectCalls := true
		for _, cs := range sites {
			call, isCall := cs.ins.(*ssa.Call)
			if !isCall || call.Call.IsInvoke() || canonicalBodyFn(call.Call.StaticCallee()) != fn || usedAsArg(call, fn, -1) {
				allDirectCalls = false
				break
			}
		}
		if !allDirectCalls {
			continue
		}
		var muIdxs []int
		modes := map[int]lockMode{}
		for k := range s.NumFields() {
			if !isSyncMutexType(s.Field(k).Type()) {
				continue
			}
			if fnReleasesRecvField(gs.releases[fn], k) {
				continue
			}
			muIdxs = append(muIdxs, k)
			modes[k] = lockWrite
		}
		if len(muIdxs) == 0 {
			continue
		}
		cands = append(cands, holdCand{fn: fn, obj: orig, s: s, muIdxs: muIdxs})
		active[orig] = modes
	}

	if len(cands) == 0 {
		return
	}

	syncAutoHolds := func() {
		clear(st.autoHolds)
		for _, c := range cands {
			modes := active[c.obj]
			for _, k := range c.muIdxs {
				if m := modes[k]; m >= lockRead {
					st.autoHolds[c.obj] = autoHoldInfo{mu: c.s.Field(k).Origin(), mode: m}
					break
				}
			}
		}
	}

	syncAutoHolds()
	st.acquireSummaries(gs, fns)
	for changed := true; changed; {
		changed = false
		clear(gs.before)
		for _, c := range cands {
			modes := active[c.obj]
			if len(modes) == 0 {
				continue
			}
			for _, k := range c.muIdxs {
				cur, ok := modes[k]
				if !ok {
					continue
				}
				next := cur
				for _, cs := range ci.callSitesOf[c.fn] {
					call := cs.ins.(*ssa.Call)
					if len(call.Call.Args) == 0 {
						next = 0
						break
					}
					bp, ok := pathOf(call.Call.Args[0])
					if !ok {
						next = 0
						break
					}
					before := st.locksOf(gs, cs.caller)
					at, reach := before[call]
					if !reach {
						continue
					}
					p := guardPath{bp.root, bp.steps + "." + strconv.Itoa(k)}
					held := at[p]
					if held < next {
						next = held
					}
					if next < lockRead {
						break
					}
				}
				if next < lockRead {
					delete(modes, k)
					changed = true
				} else if next != cur {
					modes[k] = next
					changed = true
				}
			}
		}
		if changed {
			syncAutoHolds()
		}
	}
}

func fnReleasesRecvField(rels []release, fieldIdx int) bool {
	want := "." + strconv.Itoa(fieldIdx)
	for _, r := range rels {
		if r.param != 0 {
			continue
		}
		if !r.prefix && r.steps == want {
			return true
		}
		if r.prefix && hasStepPrefix(want, r.steps) {
			return true
		}
	}
	return false
}

func derefNamedStruct(t types.Type) (*types.Named, *types.Struct) {
	if t == nil {
		return nil, nil
	}
	p, ok := t.Underlying().(*types.Pointer)
	if !ok {
		return nil, nil
	}
	named, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok {
		return nil, nil
	}
	s, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, nil
	}
	return named, s
}

func (st *state) typeExposedOutsidePkg(target *types.Named) bool {
	if target == nil || target.Origin().Obj() == nil {
		return true
	}
	if st.pass == nil || st.pass.Pkg == nil {
		return true
	}
	if st.pass.Pkg.Name() == "main" {
		return false
	}
	st.ensureExposedLocalTypes()
	return st.exposedLocalTypes[target.Origin().Obj()]
}

func (st *state) ensureExposedLocalTypes() {
	if st.exposedLocalTypes != nil {
		return
	}
	st.exposedLocalTypes = map[*types.TypeName]bool{}
	if st.pass == nil || st.pass.Pkg == nil || st.pass.Pkg.Name() == "main" {
		return
	}
	pkg := st.pass.Pkg
	seen := map[types.Type]bool{}
	var walk func(t types.Type)
	walk = func(t types.Type) {
		if t == nil {
			return
		}
		t = types.Unalias(t)
		if seen[t] {
			return
		}
		seen[t] = true
		if named, ok := t.(*types.Named); ok {
			if obj := named.Origin().Obj(); obj != nil && obj.Pkg() == pkg {
				st.exposedLocalTypes[obj] = true
			}
			for i := range named.TypeArgs().Len() {
				walk(named.TypeArgs().At(i))
			}
			for i := range named.NumMethods() {
				if m := named.Method(i); m.Exported() {
					walk(m.Type())
				}
			}
		}
		switch u := t.Underlying().(type) {
		case *types.Pointer:
			walk(u.Elem())
		case *types.Slice:
			walk(u.Elem())
		case *types.Array:
			walk(u.Elem())
		case *types.Map:
			walk(u.Key())
			walk(u.Elem())
		case *types.Chan:
			walk(u.Elem())
		case *types.Struct:
			for i := range u.NumFields() {
				if u.Field(i).Exported() {
					walk(u.Field(i).Type())
				}
			}
		case *types.Signature:
			for i := range u.Params().Len() {
				walk(u.Params().At(i).Type())
			}
			for i := range u.Results().Len() {
				walk(u.Results().At(i).Type())
			}
		case *types.Interface:
			for i := range u.NumMethods() {
				walk(u.Method(i).Type())
			}
		}
	}
	if st.contracts != nil {
		for _, bt := range st.contracts.boxedTypes {
			walk(bt)
		}
	}
	if scope := pkg.Scope(); scope != nil {
		for _, name := range scope.Names() {
			if obj := scope.Lookup(name); obj != nil && obj.Exported() {
				walk(obj.Type())
			}
		}
	}
}

// autoPrePub reports whether ins accesses a freshly allocated struct root in fn before any
// instruction in fn can publish, return, spawn, or share root with another goroutine or callee.
// It deliberately avoids st.graphOf(fn) because inferAutoGuards runs prior to computeSummaries.
func (st *state) autoPrePub(fn *ssa.Function, ins ssa.Instruction, basePtr ssa.Value) bool {
	bp, ok := pathOf(basePtr)
	if !ok {
		return false
	}
	if bp.steps != "" {
		// Allow initializing an embedded/nested value struct inside a fresh composite literal
		// (`&outer.embedded = newEmbedded(...)` or `&outer.embedded.f = ...`).
		for _, part := range splitSteps(bp.steps) {
			if part == "*" {
				return false
			}
		}
	}
	var root ssa.Value
	switch r := bp.root.(type) {
	case *ssa.Alloc:
		if r.Parent() != fn {
			return false
		}
		if r.Comment == "complit" {
			pos := ins.Pos()
			if store, ok := ins.(*ssa.Store); ok && !pos.IsValid() {
				pos = store.Addr.Pos()
			}
			if pos.IsValid() && st.inComplit(r, pos) {
				return true
			}
		}
		root = r
	case *ssa.Call:
		if r.Parent() != fn || r.Call.IsInvoke() {
			return false
		}
		callee := r.Call.StaticCallee()
		if callee == nil {
			return false
		}
		freshCall := st.contracts != nil && st.contracts.directFreshRet[canonicalBodyFn(callee)]
		if !freshCall {
			if sum := st.summary(callee); !sum.pessimistic && sum.isFreshResult(0) && !hasLinkTouchingResult(sum, len(r.Call.Args), 0) {
				freshCall = true
			}
		}
		if !freshCall {
			return false
		}
		root = r
	default:
		return false
	}
	derived := map[ssa.Value]bool{}
	var collectDerived func(v ssa.Value)
	collectDerived = func(v ssa.Value) {
		if v == nil || derived[v] {
			return
		}
		derived[v] = true
		if v.Referrers() == nil {
			return
		}
		for _, r := range *v.Referrers() {
			switch x := r.(type) {
			case *ssa.ChangeType, *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.Phi:
				if val, ok := x.(ssa.Value); ok {
					collectDerived(val)
				}
			case *ssa.Store:
				if x.Val == v {
					if a, ok := x.Addr.(*ssa.Alloc); ok && !a.Heap && a.Parent() == fn && !allocCaptured(a) {
						collectDerived(a)
					}
				}
			case *ssa.UnOp:
				if x.Op == token.MUL {
					if a, ok := v.(*ssa.Alloc); ok && a != root {
						collectDerived(x)
					}
				}
			}
		}
	}
	collectDerived(root)

	for d := range derived {
		if a, ok := d.(*ssa.Alloc); ok && a != root {
			continue
		}
		if d.Referrers() == nil {
			continue
		}
		for _, r := range *d.Referrers() {
			if r == ins {
				continue
			}
			switch x := r.(type) {
			case *ssa.DebugRef, *ssa.FieldAddr, *ssa.ChangeType, *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.Phi:
				continue
			case *ssa.Store:
				if x.Val == d {
					if a, ok := x.Addr.(*ssa.Alloc); ok && !a.Heap && a.Parent() == fn && !allocCaptured(a) {
						continue
					}
					if reachableAfter(x)(ins) {
						return false
					}
				}
			default:
				if reachableAfter(x)(ins) {
					return false
				}
			}
		}
	}
	return true
}

func splitSteps(steps string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(steps); i++ {
		if steps[i] == '.' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(steps[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func hasLinkTouchingResult(sum *summary, nArgs, resIdx int) bool {
	target := nArgs + resIdx
	for _, l := range sum.links {
		if (l.A == target && l.B < nArgs) || (l.B == target && l.A < nArgs) {
			return true
		}
	}
	return false
}

// isFreshValueCtorLoad reports whether load (`*t0` of a named struct) is the sole return read of a
// fresh local composite-literal allocation inside an unexported helper constructor whose every
// caller stores the returned struct directly into a fresh pre-publication address (such as
// `newFolderIngestJobStore` embedding `newAsyncJobStore`).
func (st *state) isFreshValueCtorLoad(fn *ssa.Function, load *ssa.UnOp) bool {
	alloc, ok := load.X.(*ssa.Alloc)
	if !ok || alloc.Parent() != fn || !allocValueOnly(alloc) || !st.autoPrePub(fn, load, alloc) {
		return false
	}
	if load.Referrers() == nil {
		return true
	}
	for _, r := range *load.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if x.Val != load || !st.autoPrePub(fn, x, x.Addr) {
				return false
			}
		case *ssa.Return:
			obj, ok := fn.Object().(*types.Func)
			if !ok || obj.Exported() || st.contracts == nil || st.contracts.openCallers[fn] {
				return false
			}
			bodyFn := canonicalBodyFn(fn)
			sites := st.contracts.callSitesOf[bodyFn]
			if len(sites) == 0 {
				return false
			}
			for _, cs := range sites {
				call, isCall := cs.ins.(*ssa.Call)
				if !isCall || call.Referrers() == nil {
					return false
				}
				for _, cr := range *call.Referrers() {
					switch cx := cr.(type) {
					case *ssa.DebugRef:
					case *ssa.Store:
						if cx.Val != call || !st.autoPrePub(cs.caller, cx, cx.Addr) {
							return false
						}
					default:
						return false
					}
				}
			}
		default:
			return false
		}
	}
	return true
}

func allocValueOnly(addr ssa.Value) bool {
	if addr.Referrers() == nil {
		return true
	}
	for _, r := range *addr.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if x.Addr != addr {
				return false
			}
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != addr {
				return false
			}
		case *ssa.FieldAddr:
			if x.X != addr || !allocValueOnly(x) {
				return false
			}
		case *ssa.IndexAddr:
			if x.X != addr || !allocValueOnly(x) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func allocCaptured(a *ssa.Alloc) bool {
	if a.Referrers() == nil {
		return false
	}
	for _, r := range *a.Referrers() {
		switch x := r.(type) {
		case *ssa.Store:
			if x.Addr != a {
				return true
			}
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return true
			}
		case *ssa.DebugRef:
		default:
			return true
		}
	}
	return false
}

func isTypeParamType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := types.Unalias(t).(*types.TypeParam)
	return ok
}

func typeMentionsTypeParam(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if _, ok := t.(*types.TypeParam); ok {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	if named, ok := t.(*types.Named); ok {
		for i := range named.TypeArgs().Len() {
			if typeMentionsTypeParam(named.TypeArgs().At(i), seen) {
				return true
			}
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return typeMentionsTypeParam(u.Elem(), seen)
	case *types.Slice:
		return typeMentionsTypeParam(u.Elem(), seen)
	case *types.Array:
		return typeMentionsTypeParam(u.Elem(), seen)
	case *types.Map:
		return typeMentionsTypeParam(u.Key(), seen) || typeMentionsTypeParam(u.Elem(), seen)
	case *types.Chan:
		return typeMentionsTypeParam(u.Elem(), seen)
	case *types.Struct:
		for i := range u.NumFields() {
			if typeMentionsTypeParam(u.Field(i).Type(), seen) {
				return true
			}
		}
	case *types.Signature:
		for i := range u.Params().Len() {
			if typeMentionsTypeParam(u.Params().At(i).Type(), seen) {
				return true
			}
		}
		for i := range u.Results().Len() {
			if typeMentionsTypeParam(u.Results().At(i).Type(), seen) {
				return true
			}
		}
	}
	return false
}

// isOwnedInteriorVal checks Invariant 4: a non-shareable value stored into an auto-guarded slot
// must originate from fresh allocations or reads of the same guarded slot, never an external
// caller parameter or global that could be mutated outside the mutex.
func (st *state) isOwnedInteriorVal(v ssa.Value, selfField *types.Var, selfGlobal *ssa.Global, allowArbitraryFunc bool, allowTypeParam bool, seen map[ssa.Value]bool) (ok bool, cancelOnly bool) {
	if v == nil || st.cls.Shareable(v.Type()) || st.isAutoSyncType(v.Type()) || (allowTypeParam && isTypeParamType(v.Type())) {
		return true, true
	}
	if isSignatureType(v.Type()) {
		if isCancelFuncOrigin(v, nil) {
			return true, true
		}
		if allowArbitraryFunc {
			return true, false
		}
		return false, false
	}
	if seen == nil {
		seen = map[ssa.Value]bool{}
	}
	if seen[v] {
		return true, true
	}
	seen[v] = true

	switch x := v.(type) {
	case *ssa.Const:
		return x.IsNil(), true
	case *ssa.MakeMap, *ssa.MakeSlice:
		return st.checkFreshContainerStores(x, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	case *ssa.Alloc:
		return st.checkFreshContainerStores(x, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	case *ssa.Slice:
		return st.isOwnedInteriorVal(x.X, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	case *ssa.ChangeType:
		return st.isOwnedInteriorVal(x.X, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	case *ssa.MakeInterface:
		return st.isOwnedInteriorVal(x.X, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	case *ssa.ChangeInterface:
		return st.isOwnedInteriorVal(x.X, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	case *ssa.Phi:
		allCancel := true
		for _, edge := range x.Edges {
			eOK, eCancel := st.isOwnedInteriorVal(edge, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
			if !eOK {
				return false, false
			}
			allCancel = allCancel && eCancel
		}
		return len(x.Edges) > 0, allCancel
	case *ssa.Call:
		if b, isBuiltin := x.Call.Value.(*ssa.Builtin); isBuiltin && b.Name() == "append" && len(x.Call.Args) == 2 {
			dstOK, dstCancel := st.isOwnedInteriorVal(x.Call.Args[0], selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
			if !dstOK {
				return false, false
			}
			srcOK, srcCancel := st.isOwnedSliceSource(x.Call.Args[1], selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
			return srcOK, dstCancel && srcCancel
		}
		if callee := x.Call.StaticCallee(); callee != nil && !x.Call.IsInvoke() {
			if callee.Pkg != nil && callee.Pkg.Pkg != nil {
				switch callee.Pkg.Pkg.Path() {
				case "slices":
					if callee.Name() == "Clone" && len(x.Call.Args) == 1 {
						if slice, ok := x.Call.Args[0].Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || (allowTypeParam && isTypeParamType(slice.Elem()))) {
							return true, true
						}
					}
				case "maps":
					if callee.Name() == "Clone" && len(x.Call.Args) == 1 {
						if mp, ok := x.Call.Args[0].Type().Underlying().(*types.Map); ok && st.cls.Shareable(mp.Key()) && (st.cls.Shareable(mp.Elem()) || (allowTypeParam && isTypeParamType(mp.Elem()))) {
							return true, true
						}
					}
				}
			}
			if sum := st.summary(callee); !sum.pessimistic && sum.isFreshResult(0) && !hasLinkTouchingResult(sum, len(x.Call.Args), 0) {
				return true, true
			}
		}
		return false, false
	case *ssa.UnOp:
		if x.Op != token.MUL {
			return false, false
		}
		if sv := uncapturedSingleStore(x); sv != nil {
			return st.isOwnedInteriorVal(sv, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
		}
		if a, ok := x.X.(*ssa.Alloc); ok && !a.Heap && a.Parent() != nil && allocValueOnly(a) {
			return st.checkFreshContainerStores(a, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
		}
		if a, ok := x.X.(*ssa.Alloc); ok && a.Comment == "complit" && a.Parent() != nil && allocValueOnly(a) {
			return st.checkFreshContainerStores(a, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
		}
		if selfField != nil {
			if fa, ok := x.X.(*ssa.FieldAddr); ok {
				if _, s := derefNamedStruct(fa.X.Type()); s != nil && fa.Field < s.NumFields() && s.Field(fa.Field).Origin() == selfField {
					return true, true
				}
			}
		}
		if selfGlobal != nil && x.X == selfGlobal {
			return true, true
		}
		return false, false
	case *ssa.Extract:
		if x.Index == 0 {
			if lk, ok := x.Tuple.(*ssa.Lookup); ok {
				return st.isOwnedInteriorVal(lk.X, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
			}
		}
		if c, ok := x.Tuple.(*ssa.Call); ok && !c.Call.IsInvoke() {
			if callee := c.Call.StaticCallee(); callee != nil {
				if sum := st.summary(callee); !sum.pessimistic && sum.isFreshResult(x.Index) && !hasLinkTouchingResult(sum, len(c.Call.Args), x.Index) {
					return true, true
				}
			}
		}
		return false, false
	case *ssa.Lookup:
		return st.isOwnedInteriorVal(x.X, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
	}
	return false, false
}

func (st *state) isOwnedSliceSource(v ssa.Value, selfField *types.Var, selfGlobal *ssa.Global, allowArbitraryFunc bool, allowTypeParam bool, seen map[ssa.Value]bool) (bool, bool) {
	if slice, ok := v.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem()) || (allowTypeParam && isTypeParamType(slice.Elem()))) {
		return true, true
	}
	return st.isOwnedInteriorVal(v, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
}

func (st *state) checkFreshContainerStores(container ssa.Value, selfField *types.Var, selfGlobal *ssa.Global, allowArbitraryFunc bool, allowTypeParam bool, seen map[ssa.Value]bool) (bool, bool) {
	if container.Referrers() == nil {
		return true, true
	}
	allCancel := true
	for _, r := range *container.Referrers() {
		switch x := r.(type) {
		case *ssa.Store:
			if x.Addr == container {
				ok, cancel := st.isOwnedInteriorVal(x.Val, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
				if !ok {
					return false, false
				}
				allCancel = allCancel && cancel
			}
		case *ssa.MapUpdate:
			if x.Map == container {
				kOK, kCancel := st.isOwnedInteriorVal(x.Key, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
				vOK, vCancel := st.isOwnedInteriorVal(x.Value, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
				if !kOK || !vOK {
					return false, false
				}
				allCancel = allCancel && kCancel && vCancel
			}
		case *ssa.FieldAddr, *ssa.IndexAddr, *ssa.Slice:
			if val, ok := r.(ssa.Value); ok {
				subOK, subCancel := st.checkFreshContainerStores(val, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, seen)
				if !subOK {
					return false, false
				}
				allCancel = allCancel && subCancel
			}
		}
	}
	return true, allCancel
}

func (st *state) inferAutoGuardedFields(gs *guardState, scanFns []*ssa.Function) {
	ci := st.contracts
	rejectedTypes := map[*types.TypeName]bool{}

	for _, named := range ci.localAllNamedTypes {
		tn := named.Origin().Obj()
		typeKey := types.TypeString(named.Origin(), nil)
		if len(ci.structConv[typeKey]) > 0 {
			rejectedTypes[tn] = true
			continue
		}
		for i := range named.NumMethods() {
			sig, ok := named.Method(i).Type().(*types.Signature)
			if !ok || sig.Recv() == nil {
				continue
			}
			if _, isPtr := sig.Recv().Type().Underlying().(*types.Pointer); !isPtr {
				rejectedTypes[tn] = true
				break
			}
		}
	}

	for _, fn := range scanFns {
		if len(fn.Blocks) == 0 {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			switch x := ins.(type) {
			case *ssa.UnOp:
				if x.Op == token.MUL {
					if named, ok := types.Unalias(x.Type()).(*types.Named); ok && named.Obj() != nil {
						if _, isStruct := named.Underlying().(*types.Struct); isStruct && !st.isFreshValueCtorLoad(fn, x) {
							rejectedTypes[named.Origin().Obj()] = true
						}
					}
				}
			case *ssa.Field:
				if named, ok := types.Unalias(x.X.Type()).(*types.Named); ok && named.Obj() != nil {
					rejectedTypes[named.Origin().Obj()] = true
				}
			case *ssa.Store:
				if named, ok := types.Unalias(x.Val.Type()).(*types.Named); ok && named.Obj() != nil {
					if _, isStruct := named.Underlying().(*types.Struct); isStruct {
						if !st.autoPrePub(fn, x, x.Addr) {
							rejectedTypes[named.Origin().Obj()] = true
						}
					}
				}
			}
		})
	}

	// Collect all FieldAddrs per (TypeName, fieldIdx).
	type fieldSite struct {
		fn *ssa.Function
		fa *ssa.FieldAddr
	}
	sitesByField := map[*types.Var][]fieldSite{}
	for _, fn := range scanFns {
		if len(fn.Blocks) == 0 {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			fa, ok := ins.(*ssa.FieldAddr)
			if !ok {
				return
			}
			named, s := derefNamedStruct(fa.X.Type())
			if named == nil || named.Origin().Obj().Pkg() != st.pass.Pkg || rejectedTypes[named.Origin().Obj()] || fa.Field >= s.NumFields() {
				return
			}
			f := s.Field(fa.Field).Origin()
			sitesByField[f] = append(sitesByField[f], fieldSite{fn: fn, fa: fa})
		})
	}

	for _, named := range ci.localAllNamedTypes {
		tn := named.Origin().Obj()
		if rejectedTypes[tn] {
			continue
		}
		s, ok := named.Underlying().(*types.Struct)
		if !ok || s.NumFields() == 0 {
			continue
		}
		var muIdxs []int
		for k := range s.NumFields() {
			if isSyncMutexType(s.Field(k).Type()) {
				muIdxs = append(muIdxs, k)
			}
		}
		if len(muIdxs) == 0 {
			continue
		}
		unexposedType := !tn.Exported() && !st.typeExposedOutsidePkg(named)
		isGeneric := named.Origin().TypeParams() != nil && named.Origin().TypeParams().Len() > 0

		for i := range s.NumFields() {
			sf := s.Field(i)
			f := sf.Origin()
			if isSyncMutexType(sf.Type()) || st.autoGuarded[f] != nil {
				continue
			}
			declMu := st.ix.Guarded[f]
			if !((!sf.Exported() && !sf.Embedded()) || unexposedType) {
				continue
			}
			sites := sitesByField[f]
			if len(sites) == 0 {
				if declMu != nil && st.cls.Shareable(sf.Type()) {
					st.autoGuarded[f] = declMu
				}
				continue
			}

			tryGuard := func(allowTP bool) (*types.Var, bool) {
				var candMus []int
				if declMu != nil {
					for _, muIdx := range muIdxs {
						if s.Field(muIdx).Origin() == declMu {
							candMus = append(candMus, muIdx)
						}
					}
					if len(candMus) == 0 {
						return nil, false
					}
				} else {
					candMus = append([]int(nil), muIdxs...)
				}
				postWrites := 0
				allCancelFuncs := true
				usedCancelEscape := false
				valid := true

				narrow := func(fn *ssa.Function, ins ssa.Instruction, base ssa.Value, need lockMode) bool {
					bp, ok := pathOf(base)
					if !ok {
						return false
					}
					before := st.locksOf(gs, fn)
					at, reach := before[ins]
					if !reach {
						return true
					}
					kept := candMus[:0]
					for _, muIdx := range candMus {
						mp := guardPath{bp.root, bp.steps + "." + strconv.Itoa(muIdx)}
						if at[mp] >= need {
							kept = append(kept, muIdx)
						}
					}
					candMus = kept
					return len(candMus) > 0
				}

				for _, site := range sites {
					if !st.verifySlotAddrUses(site.fn, site.fa, func(ins ssa.Instruction) bool {
						return st.autoPrePub(site.fn, ins, site.fa.X)
					}, func(ins ssa.Instruction, need lockMode) bool {
						if need == lockWrite {
							postWrites++
						}
						return narrow(site.fn, ins, site.fa.X, need)
					}, f, nil, false, allowTP, &allCancelFuncs, &usedCancelEscape) {
						valid = false
						break
					}
				}
				if valid && (!usedCancelEscape || allCancelFuncs) && (postWrites >= 1 || declMu != nil) && len(candMus) > 0 {
					return s.Field(candMus[0]).Origin(), allCancelFuncs
				}
				return nil, false
			}

			if muVar, allCancel := tryGuard(false); muVar != nil {
				st.autoGuarded[f] = muVar
				if allCancel {
					st.cancelSafeVars[f] = true
				}
			} else if isGeneric && typeMentionsTypeParam(sf.Type(), map[types.Type]bool{}) {
				if muVar, _ := tryGuard(true); muVar != nil {
					st.paramAutoGuarded[f] = muVar
				}
			}
		}
	}
}

func (st *state) inferAutoGuardedGlobals(gs *guardState, scanFns []*ssa.Function) {
	var candMusAll []*ssa.Global
	candMuSet := map[*ssa.Global]bool{}
	var dataGlobals []*ssa.Global
	dataSet := map[*ssa.Global]bool{}
	isMain := st.pass != nil && st.pass.Pkg != nil && st.pass.Pkg.Name() == "main"

	for _, mem := range st.ssa.Pkg.Members {
		g, ok := mem.(*ssa.Global)
		if !ok {
			continue
		}
		ptr, ok := g.Type().Underlying().(*types.Pointer)
		if !ok {
			continue
		}
		valType := ptr.Elem()
		if isSyncMutexType(valType) {
			if !token.IsExported(g.Name()) || isMain {
				candMusAll = append(candMusAll, g)
				candMuSet[g] = true
			}
			continue
		}
		if !token.IsExported(g.Name()) || isMain {
			if gv, ok := g.Object().(*types.Var); ok && st.ix.Guarded[gv] == nil && st.autoGuarded[gv] == nil {
				dataGlobals = append(dataGlobals, g)
				dataSet[g] = true
			}
		}
	}
	if len(candMusAll) == 0 || len(dataGlobals) == 0 {
		return
	}

	badMu := map[*ssa.Global]bool{}
	fnsByGlobal := map[*ssa.Global][]*ssa.Function{}
	for _, fn := range scanFns {
		if len(fn.Blocks) == 0 {
			continue
		}
		seenInFn := map[*ssa.Global]bool{}
		forEachInstr(fn, func(ins ssa.Instruction) {
			var opBuf [8]*ssa.Value
			for _, op := range ins.Operands(opBuf[:0]) {
				if op == nil {
					continue
				}
				g, ok := (*op).(*ssa.Global)
				if !ok {
					continue
				}
				if candMuSet[g] && !badMu[g] && !isValidGlobalMutexInstr(ins, g) {
					badMu[g] = true
				}
				if dataSet[g] && !seenInFn[g] {
					seenInFn[g] = true
					fnsByGlobal[g] = append(fnsByGlobal[g], fn)
				}
			}
		})
	}

	var pkgMus []*ssa.Global
	for _, g := range candMusAll {
		if !badMu[g] {
			pkgMus = append(pkgMus, g)
		}
	}
	if len(pkgMus) == 0 {
		return
	}
	slices.SortFunc(pkgMus, func(a, b *ssa.Global) int {
		if a.Name() < b.Name() {
			return -1
		}
		if a.Name() > b.Name() {
			return 1
		}
		return 0
	})

	for _, dg := range dataGlobals {
		refFns := fnsByGlobal[dg]
		if len(refFns) == 0 {
			continue
		}
		gv := dg.Object().(*types.Var)
		candMus := append([]*ssa.Global(nil), pkgMus...)
		postWrites := 0
		allCancelFuncs := true
		valid := true

		narrow := func(fn *ssa.Function, ins ssa.Instruction, need lockMode) bool {
			before := st.locksOf(gs, fn)
			at, reach := before[ins]
			if !reach {
				return true
			}
			kept := candMus[:0]
			for _, mg := range candMus {
				if at[guardPath{root: mg}] >= need {
					kept = append(kept, mg)
				}
			}
			candMus = kept
			return len(candMus) > 0
		}

		for _, fn := range refFns {
			usedCancelEscape := false
			if !st.verifyGlobalUsesInFn(fn, dg, func(ins ssa.Instruction) bool {
				return gs.initOnly[fn]
			}, func(ins ssa.Instruction, need lockMode) bool {
				if need == lockWrite {
					postWrites++
				}
				return narrow(fn, ins, need)
			}, gv, &allCancelFuncs, &usedCancelEscape) {
				valid = false
				break
			}
			_ = usedCancelEscape
		}

		if valid && postWrites >= 1 && len(candMus) > 0 {
			st.autoGuarded[gv] = candMus[0].Object().(*types.Var)
			if allCancelFuncs {
				st.cancelSafeVars[gv] = true
			}
		}
	}
}

func isValidGlobalMutexInstr(ins ssa.Instruction, mg *ssa.Global) bool {
	switch x := ins.(type) {
	case *ssa.DebugRef:
		return true
	case *ssa.Call:
		if k, recv := lockOp(&x.Call); k != 0 && recv == mg && !usedAsArg(x, mg, 0) {
			return true
		}
	case *ssa.Defer:
		if k, recv := lockOp(&x.Call); k != 0 && recv == mg && !usedAsArg(&ssa.Call{Call: x.Call}, mg, 0) {
			return true
		}
	}
	return false
}

func (st *state) verifyGlobalUsesInFn(
	fn *ssa.Function,
	dg *ssa.Global,
	isPrePub func(ssa.Instruction) bool,
	requireLock func(ssa.Instruction, lockMode) bool,
	gv *types.Var,
	allCancelFuncs *bool,
	usedCancelEscape *bool,
) bool {
	valid := true
	forEachInstr(fn, func(ins ssa.Instruction) {
		if !valid {
			return
		}
		var opBuf [8]*ssa.Value
		uses := false
		for _, op := range ins.Operands(opBuf[:0]) {
			if op != nil && *op == dg {
				uses = true
			}
		}
		if !uses {
			return
		}
		if !st.checkSingleAddrReferrer(fn, dg, ins, isPrePub, requireLock, nil, dg, true, false, allCancelFuncs, usedCancelEscape) {
			valid = false
		}
	})
	_ = gv
	return valid
}

// verifySlotAddrUses verifies every referrer of addr (&x.f, or nested value-struct/array projection
// of &x.f) inside fn.
func (st *state) verifySlotAddrUses(
	fn *ssa.Function,
	addr ssa.Value,
	isPrePub func(ssa.Instruction) bool,
	requireLock func(ssa.Instruction, lockMode) bool,
	selfField *types.Var,
	selfGlobal *ssa.Global,
	allowArbitraryFunc bool,
	allowTypeParam bool,
	allCancelFuncs *bool,
	usedCancelEscape *bool,
) bool {
	if addr.Referrers() == nil {
		return true
	}
	for _, r := range *addr.Referrers() {
		if r.Parent() != fn {
			continue
		}
		if !st.checkSingleAddrReferrer(fn, addr, r, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape) {
			return false
		}
	}
	return true
}

func (st *state) checkSingleAddrReferrer(
	fn *ssa.Function,
	addr ssa.Value,
	r ssa.Instruction,
	isPrePub func(ssa.Instruction) bool,
	requireLock func(ssa.Instruction, lockMode) bool,
	selfField *types.Var,
	selfGlobal *ssa.Global,
	allowArbitraryFunc bool,
	allowTypeParam bool,
	allCancelFuncs *bool,
	usedCancelEscape *bool,
) bool {
	switch x := r.(type) {
	case *ssa.DebugRef:
		return true
	case *ssa.Store:
		if x.Addr != addr {
			return false
		}
		valOK, cancel := st.isOwnedInteriorVal(x.Val, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, nil)
		if !valOK {
			return false
		}
		if !cancel {
			*allCancelFuncs = false
		}
		if isPrePub(x) {
			return true
		}
		return requireLock(x, lockWrite)
	case *ssa.UnOp:
		if x.Op != token.MUL || x.X != addr {
			return false
		}
		if !isPrePub(x) && !requireLock(x, lockRead) {
			return false
		}
		return st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, map[ssa.Value]bool{})
	case *ssa.FieldAddr:
		if x.X != addr {
			return false
		}
		return st.verifySlotAddrUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape)
	case *ssa.IndexAddr:
		if x.X != addr {
			return false
		}
		// Array value field (&x.arr[i]).
		return st.verifySlotAddrUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape)
	case *ssa.Slice:
		if x.X != addr {
			return false
		}
		return st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, map[ssa.Value]bool{})
	}
	return false
}

// verifyLoadedValueUses tracks a value loaded from an auto-guarded candidate slot and ensures:
//   - Shareable values (and verified CancelFuncs, or function descriptors on globals) may be copied
//     out freely once the load itself held >= lockRead.
//   - Non-shareable interior references (maps, slices, struct pointers) are NEVER read or written
//     after the lock is released and NEVER escape the critical section.
func (st *state) verifyLoadedValueUses(
	fn *ssa.Function,
	val ssa.Value,
	isPrePub func(ssa.Instruction) bool,
	requireLock func(ssa.Instruction, lockMode) bool,
	selfField *types.Var,
	selfGlobal *ssa.Global,
	allowArbitraryFunc bool,
	allowTypeParam bool,
	allCancelFuncs *bool,
	usedCancelEscape *bool,
	seen map[ssa.Value]bool,
) bool {
	if val == nil || st.cls.Shareable(val.Type()) || st.isAutoSyncType(val.Type()) || (allowTypeParam && isTypeParamType(val.Type())) {
		return true
	}
	if isSignatureType(val.Type()) {
		if !allowArbitraryFunc && usedCancelEscape != nil {
			*usedCancelEscape = true
		}
		return allowArbitraryFunc || *allCancelFuncs
	}
	if seen[val] {
		return true
	}
	seen[val] = true
	if val.Referrers() == nil {
		return true
	}

	for _, r := range *val.Referrers() {
		if r.Parent() != fn {
			return false
		}
		switch x := r.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.BinOp:
			// Pointer/map/slice nil comparison (`v == nil`, `v != nil`).
			if x.Op == token.EQL || x.Op == token.NEQ {
				continue
			}
			return false
		case *ssa.Lookup:
			if x.X != val {
				return false
			}
			if !isPrePub(x) && !requireLock(x, lockRead) {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.Extract:
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.Range:
			if x.X != val {
				return false
			}
			if !isPrePub(x) && !requireLock(x, lockRead) {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.Next:
			if !isPrePub(x) && !requireLock(x, lockRead) {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.MapUpdate:
			if x.Map != val {
				return false
			}
			kOK, kCancel := st.isOwnedInteriorVal(x.Key, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, nil)
			vOK, vCancel := st.isOwnedInteriorVal(x.Value, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, nil)
			if !kOK || !vOK {
				return false
			}
			if !kCancel || !vCancel {
				*allCancelFuncs = false
			}
			if !isPrePub(x) && !requireLock(x, lockWrite) {
				return false
			}
		case *ssa.IndexAddr:
			if x.X != val {
				return false
			}
			if !st.verifySlotAddrUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape) {
				return false
			}
		case *ssa.FieldAddr:
			if x.X != val {
				return false
			}
			if !st.verifySlotAddrUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape) {
				return false
			}
		case *ssa.Index:
			if x.X != val {
				return false
			}
			if !isPrePub(x) && !requireLock(x, lockRead) {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.Field:
			if x.X != val {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.Slice:
			if x.X != val {
				return false
			}
			if !isPrePub(x) && !requireLock(x, lockRead) {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != val {
				return false
			}
			if !isPrePub(x) && !requireLock(x, lockRead) {
				return false
			}
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.Phi:
			if !st.verifyLoadedValueUses(fn, x, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
				return false
			}
		case *ssa.ChangeType, *ssa.MakeInterface, *ssa.ChangeInterface:
			if cv, ok := r.(ssa.Value); ok {
				if !st.verifyLoadedValueUses(fn, cv, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape, seen) {
					return false
				}
			}
		case *ssa.Store:
			if x.Addr == val {
				vOK, vCancel := st.isOwnedInteriorVal(x.Val, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, nil)
				if !vOK {
					return false
				}
				if !vCancel {
					*allCancelFuncs = false
				}
				if !isPrePub(x) && !requireLock(x, lockWrite) {
					return false
				}
				continue
			}
			if x.Val == val {
				// Storing into a local uncaptured stack cell (`entry := r.entries[id]`) is safe iff
				// every load/projection of that cell stays under the same lock and never escapes!
				a, ok := x.Addr.(*ssa.Alloc)
				if !ok || a.Heap || a.Parent() != fn || isResultSlot(a) || allocCaptured(a) {
					return false
				}
				if !st.verifySlotAddrUses(fn, a, isPrePub, requireLock, selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, allCancelFuncs, usedCancelEscape) {
					return false
				}
				continue
			}
			return false
		case *ssa.Call:
			b, isBuiltin := x.Call.Value.(*ssa.Builtin)
			if !isBuiltin {
				if callee := x.Call.StaticCallee(); callee != nil && !x.Call.IsInvoke() && x.Call.Value != val {
					if callee.Pkg != nil && callee.Pkg.Pkg != nil {
						switch callee.Pkg.Pkg.Path() {
						case "slices":
							if callee.Name() == "Clone" && len(x.Call.Args) == 1 && x.Call.Args[0] == val {
								if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem()) || (allowTypeParam && isTypeParamType(slice.Elem()))) {
									if isPrePub(x) || requireLock(x, lockRead) {
										continue
									}
								}
							}
							if (callee.Name() == "Contains" || callee.Name() == "Index") && len(x.Call.Args) == 2 && x.Call.Args[0] == val && x.Call.Args[1] != val {
								if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem()) || (allowTypeParam && isTypeParamType(slice.Elem()))) {
									if isPrePub(x) || requireLock(x, lockRead) {
										continue
									}
								}
							}
						case "maps":
							if callee.Name() == "Clone" && len(x.Call.Args) == 1 && x.Call.Args[0] == val {
								if mp, ok := val.Type().Underlying().(*types.Map); ok && st.cls.Shareable(mp.Key()) && (st.cls.Shareable(mp.Elem()) || st.isAutoSyncType(mp.Elem()) || (allowTypeParam && isTypeParamType(mp.Elem()))) {
									if isPrePub(x) || requireLock(x, lockRead) {
										continue
									}
								}
							}
							if callee.Name() == "Copy" && len(x.Call.Args) == 2 && x.Call.Args[1] == val && x.Call.Args[0] != val {
								if _, isMake := x.Call.Args[0].(*ssa.MakeMap); isMake {
									if mp, ok := val.Type().Underlying().(*types.Map); ok && st.cls.Shareable(mp.Key()) && (st.cls.Shareable(mp.Elem()) || st.isAutoSyncType(mp.Elem()) || (allowTypeParam && isTypeParamType(mp.Elem()))) {
										if isPrePub(x) || requireLock(x, lockRead) {
											continue
										}
									}
								}
							}
						}
					}
					if sum := st.summary(callee); !sum.pessimistic {
						safe := true
						need := lockRead
						for ai, arg := range x.Call.Args {
							if arg != val {
								continue
							}
							if sum.leaks(ai) || sum.spawns(ai) || sum.isIso(ai) {
								safe = false
								break
							}
							for _, l := range sum.links {
								if l.A == ai || l.B == ai {
									safe = false
									break
								}
							}
							if !safe {
								break
							}
							if sum.writes(ai) {
								need = lockWrite
							}
						}
						if safe && (isPrePub(x) || requireLock(x, need)) {
							continue
						}
					}
				}
				return false
			}
			switch b.Name() {
			case "len", "cap", "ssa:wrapnilchk":
				if !isPrePub(x) && !requireLock(x, lockRead) {
					return false
				}
			case "delete", "clear":
				if len(x.Call.Args) == 0 || x.Call.Args[0] != val {
					return false
				}
				if !isPrePub(x) && !requireLock(x, lockWrite) {
					return false
				}
			case "copy":
				if len(x.Call.Args) != 2 {
					return false
				}
				if x.Call.Args[0] == val {
					srcOK, srcCancel := st.isOwnedSliceSource(x.Call.Args[1], selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, nil)
					if !srcOK {
						return false
					}
					if !srcCancel {
						*allCancelFuncs = false
					}
					if !isPrePub(x) && !requireLock(x, lockWrite) {
						return false
					}
				}
				if x.Call.Args[1] == val {
					slice, ok := val.Type().Underlying().(*types.Slice)
					if !ok || (!st.cls.Shareable(slice.Elem()) && !st.isAutoSyncType(slice.Elem()) && !(allowTypeParam && isTypeParamType(slice.Elem())) && !(isSignatureType(slice.Elem()) && *allCancelFuncs)) {
						return false
					}
					if !isPrePub(x) && !requireLock(x, lockRead) {
						return false
					}
				}
			case "append":
				if len(x.Call.Args) != 2 {
					return false
				}
				if x.Call.Args[0] == val {
					srcOK, srcCancel := st.isOwnedSliceSource(x.Call.Args[1], selfField, selfGlobal, allowArbitraryFunc, allowTypeParam, nil)
					if !srcOK {
						return false
					}
					if !srcCancel {
						*allCancelFuncs = false
					}
					if !isPrePub(x) && !requireLock(x, lockWrite) {
						return false
					}
					if !st.verifyAppendedResult(x, selfField, selfGlobal) {
						return false
					}
				} else if x.Call.Args[1] == val {
					slice, ok := val.Type().Underlying().(*types.Slice)
					if !ok || (!st.cls.Shareable(slice.Elem()) && !st.isAutoSyncType(slice.Elem()) && !(allowTypeParam && isTypeParamType(slice.Elem())) && !(isSignatureType(slice.Elem()) && *allCancelFuncs)) {
						return false
					}
					if !isPrePub(x) && !requireLock(x, lockRead) {
						return false
					}
					if c, ok := x.Call.Args[0].(*ssa.Const); !ok || !c.IsNil() {
						if ms, ok := x.Call.Args[0].(*ssa.MakeSlice); !ok || ms == nil {
							return false
						}
					}
				}
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) verifyAppendedResult(app *ssa.Call, selfField *types.Var, selfGlobal *ssa.Global) bool {
	if app.Referrers() == nil {
		return true
	}
	for _, r := range *app.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if x.Val != app {
				return false
			}
			if selfField != nil {
				fa, ok := x.Addr.(*ssa.FieldAddr)
				if !ok {
					return false
				}
				_, s := derefNamedStruct(fa.X.Type())
				if s == nil || fa.Field >= s.NumFields() || s.Field(fa.Field).Origin() != selfField {
					return false
				}
				continue
			}
			if selfGlobal != nil && x.Addr == selfGlobal {
				continue
			}
			return false
		case *ssa.Slice:
			if x.X != app || !st.verifyAppendedResultSlice(x, selfField, selfGlobal) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) verifyAppendedResultSlice(sl *ssa.Slice, selfField *types.Var, selfGlobal *ssa.Global) bool {
	if sl.Referrers() == nil {
		return true
	}
	for _, r := range *sl.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if x.Val != sl {
				return false
			}
			if selfField != nil {
				fa, ok := x.Addr.(*ssa.FieldAddr)
				if !ok {
					return false
				}
				_, s := derefNamedStruct(fa.X.Type())
				if s == nil || fa.Field >= s.NumFields() || s.Field(fa.Field).Origin() != selfField {
					return false
				}
				continue
			}
			if selfGlobal != nil && x.Addr == selfGlobal {
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}

func isSyncPrimitiveValueType(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	pkgPath := named.Obj().Pkg().Path()
	name := named.Obj().Name()
	switch pkgPath {
	case "sync":
		switch name {
		case "Mutex", "RWMutex", "Once", "Map", "Pool", "WaitGroup", "Cond":
			return true
		}
	case "sync/atomic":
		switch name {
		case "Bool", "Int32", "Int64", "Uint32", "Uint64", "Uintptr", "Pointer", "Value":
			return true
		}
	}
	return false
}

// isAutoSyncType reports whether t is safe to share concurrently by reference without exclusive
// ownership: either st.cls.Shareable(t), a pointer to an AutoSync named struct (local or imported
// via FuncFact), or a value struct/array whose fields/elements are all AutoSync/Shareable.
func (st *state) isAutoSyncType(t types.Type) bool {
	return st.isAutoSyncTypeSeen(t, false, map[types.Type]bool{})
}

// isExportableAutoSyncSigType reports whether a parameter or return type on an exported function
// carries AutoSync (either concrete AutoSync or an uninstantiated generic AutoSync template G[T]).
func (st *state) isExportableAutoSyncSigType(t types.Type) bool {
	if st.isAutoSyncType(t) {
		return true
	}
	if t == nil {
		return false
	}
	u := types.Unalias(t)
	if p, ok := u.(*types.Pointer); ok {
		u = types.Unalias(p.Elem())
	}
	named, ok := u.(*types.Named)
	if !ok || named.Obj() == nil {
		return false
	}
	return st.genericAutoSyncTypes[named.Origin().Obj()]
}

func (st *state) isAutoSyncTypeSeen(t types.Type, allowTP bool, seen map[types.Type]bool) bool {
	if t == nil || isAtomicTarget(t) {
		return false
	}
	if st.cls != nil && st.cls.Shareable(t) {
		return true
	}
	if allowTP && isTypeParamType(t) {
		return true
	}
	t = types.Unalias(t)
	if seen[t] {
		return true
	}
	seen[t] = true
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		named, ok := types.Unalias(u.Elem()).(*types.Named)
		if !ok || named.Obj() == nil {
			return false
		}
		return st.isAutoSyncNamed(named, true, allowTP, seen)
	case *types.Struct:
		if named, ok := t.(*types.Named); ok && named.Obj() != nil {
			if st.isAutoSyncNamed(named, false, allowTP, seen) {
				return true
			}
		}
		if u.NumFields() == 0 {
			return true
		}
		for i := range u.NumFields() {
			ft := u.Field(i).Type()
			if !st.isAutoSyncTypeSeen(ft, allowTP, seen) {
				return false
			}
		}
		return true
	case *types.Array:
		return u.Len() == 0 || st.isAutoSyncTypeSeen(u.Elem(), allowTP, seen)
	}
	return false
}

func structHasSyncPrimitiveField(s *types.Struct, seen map[types.Type]bool) bool {
	if s == nil {
		return false
	}
	for i := range s.NumFields() {
		ft := types.Unalias(s.Field(i).Type())
		if isSyncPrimitiveValueType(ft) {
			return true
		}
		if sub, ok := ft.Underlying().(*types.Struct); ok && !seen[ft] {
			seen[ft] = true
			if structHasSyncPrimitiveField(sub, seen) {
				return true
			}
		}
	}
	return false
}

func (st *state) isAutoSyncNamed(named *types.Named, wantPtr bool, allowTP bool, seen map[types.Type]bool) bool {
	if named == nil || named.Obj() == nil {
		return false
	}
	orig := named.Origin()
	tn := orig.Obj()
	if tn == nil || tn.Pkg() == nil {
		return false
	}
	isGeneric := orig.TypeParams() != nil && orig.TypeParams().Len() > 0
	if isGeneric {
		if named.TypeArgs() == nil || named.TypeArgs().Len() != orig.TypeParams().Len() {
			if !allowTP {
				return false
			}
		} else {
			for p := range named.TypeArgs().Len() {
				if !st.isAutoSyncTypeSeen(named.TypeArgs().At(p), allowTP, seen) {
					return false
				}
			}
		}
	}
	if st.pass != nil && tn.Pkg() == st.pass.Pkg {
		if wantPtr {
			if isGeneric {
				return st.genericAutoSyncTypes[tn]
			}
			return st.autoSyncTypes[tn]
		}
		if st.valueAutoSyncTypes[tn] {
			return true
		}
		if s, ok := orig.Underlying().(*types.Struct); ok && !structHasSyncPrimitiveField(s, map[types.Type]bool{}) {
			if isGeneric {
				return st.genericAutoSyncTypes[tn]
			}
			return st.autoSyncTypes[tn]
		}
		return false
	}
	if st.pass == nil {
		return false
	}
	for i := range orig.NumMethods() {
		m := orig.Method(i)
		if m == nil || !m.Exported() || m.Pkg() != tn.Pkg() {
			continue
		}
		sig, ok := m.Type().(*types.Signature)
		if !ok || sig.Recv() == nil {
			continue
		}
		_, isPtrRecv := types.Unalias(sig.Recv().Type()).(*types.Pointer)
		if isPtrRecv != wantPtr {
			continue
		}
		var f FuncFact
		if st.pass.ImportObjectFact(m.Origin(), &f) && !f.Unknown {
			return len(f.ParamAutoSync) > 0 && f.ParamAutoSync[0]
		}
	}
	if scope := tn.Pkg().Scope(); scope != nil {
		for _, name := range scope.Names() {
			fnObj, ok := scope.Lookup(name).(*types.Func)
			if !ok || !fnObj.Exported() {
				continue
			}
			sig, ok := fnObj.Type().(*types.Signature)
			if !ok || sig.Recv() != nil || sig.Results() == nil {
				continue
			}
			for k := range sig.Results().Len() {
				rt := types.Unalias(sig.Results().At(k).Type())
				p, isPtr := rt.(*types.Pointer)
				if isPtr != wantPtr {
					continue
				}
				if isPtr {
					rt = types.Unalias(p.Elem())
				}
				rn, isNamed := rt.(*types.Named)
				if !isNamed || rn.Origin().Obj() != tn {
					continue
				}
				var f FuncFact
				if st.pass.ImportObjectFact(fnObj.Origin(), &f) && !f.Unknown {
					return k < len(f.ResAutoSync) && f.ResAutoSync[k]
				}
			}
		}
	}
	return false
}

func hasExportedOrPromotedField(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if seen[t] {
		return false
	}
	seen[t] = true
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	s, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := range s.NumFields() {
		sf := s.Field(i)
		if sf.Exported() {
			return true
		}
		if sf.Embedded() && hasExportedOrPromotedField(sf.Type(), seen) {
			return true
		}
	}
	return false
}

// inferAutoSyncTypes proves which struct types declared in this package are concurrent-safe
// service/container boundaries (analogous to Rust's structural `Sync` auto-trait combined with
// `Mutex<T>: Sync` and `Atomic*: Sync`). Runs after computeSummaries so method/closure write
// summaries are final.
func (st *state) inferAutoSyncTypes(fns []*ssa.Function) {
	st.autoSyncTypes = map[*types.TypeName]bool{}
	st.valueAutoSyncTypes = map[*types.TypeName]bool{}
	st.genericAutoSyncTypes = map[*types.TypeName]bool{}
	st.roLocalFields = map[*types.Var]bool{}
	if st.contracts == nil || st.ssa == nil || st.ssa.Pkg == nil {
		return
	}
	ci := st.contracts
	isMain := st.pass != nil && st.pass.Pkg != nil && st.pass.Pkg.Name() == "main"

	scanFns := fns
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil && !slices.Contains(scanFns, pkgInit) {
		scanFns = append(append([]*ssa.Function(nil), fns...), pkgInit)
	}

	overwrittenTypes := map[*types.TypeName]bool{}
	copiedPtrTypes := map[*types.TypeName]bool{}
	faByField := map[*types.Var][]faSite{}
	fvalByField := map[*types.Var][]fvalSite{}

	for _, fn := range scanFns {
		if len(fn.Blocks) == 0 {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			switch x := ins.(type) {
			case *ssa.UnOp:
				if x.Op == token.MUL {
					if named, ok := types.Unalias(x.Type()).(*types.Named); ok && named.Obj() != nil {
						if _, isStruct := named.Underlying().(*types.Struct); isStruct && !st.isFreshValueCtorLoad(fn, x) {
							copiedPtrTypes[named.Origin().Obj()] = true
						}
					}
				}
			case *ssa.Field:
				if named, ok := types.Unalias(x.X.Type()).(*types.Named); ok && named.Origin().Obj() != nil && named.Origin().Obj().Pkg() == st.pass.Pkg {
					if s, isStruct := named.Underlying().(*types.Struct); isStruct && x.Field < s.NumFields() {
						f := s.Field(x.Field).Origin()
						fvalByField[f] = append(fvalByField[f], fvalSite{fn: fn, fv: x})
					}
				}
			case *ssa.Store:
				if named, ok := types.Unalias(x.Val.Type()).(*types.Named); ok && named.Obj() != nil {
					if _, isStruct := named.Underlying().(*types.Struct); isStruct {
						if !st.autoPrePub(fn, x, x.Addr) {
							if a, isAlloc := x.Addr.(*ssa.Alloc); !isAlloc || a.Parent() != fn || !allocValueOnly(a) {
								overwrittenTypes[named.Origin().Obj()] = true
							}
						}
					}
				}
			case *ssa.FieldAddr:
				named, s := derefNamedStruct(x.X.Type())
				if named != nil && named.Origin().Obj().Pkg() == st.pass.Pkg && x.Field < s.NumFields() {
					f := s.Field(x.Field).Origin()
					faByField[f] = append(faByField[f], faSite{fn: fn, fa: x})
				}
			}
		})
	}

	for _, named := range ci.localAllNamedTypes {
		tn := named.Origin().Obj()
		s, ok := named.Underlying().(*types.Struct)
		if !ok || overwrittenTypes[tn] || copiedPtrTypes[tn] {
			continue
		}
		if len(ci.structConv[types.TypeString(named.Origin(), nil)]) > 0 {
			continue
		}
		for i := range s.NumFields() {
			sf := s.Field(i)
			if !isMain && tn.Exported() && (sf.Exported() || sf.Embedded()) {
				continue
			}
			f := sf.Origin()
			if st.fieldReadOnlyPostConstruction(faByField[f]) {
				st.roLocalFields[f] = true
			}
		}
	}

	var cands []*types.Named
	var valCands []*types.Named
	for _, named := range ci.localAllNamedTypes {
		tn := named.Origin().Obj()
		if overwrittenTypes[tn] {
			continue
		}
		s, ok := named.Underlying().(*types.Struct)
		if !ok || s.NumFields() == 0 {
			continue
		}
		typeKey := types.TypeString(named.Origin(), nil)
		if len(ci.structConv[typeKey]) > 0 {
			continue
		}
		isGeneric := named.Origin().TypeParams() != nil && named.Origin().TypeParams().Len() > 0
		unexposedType := !tn.Exported() && !st.typeExposedOutsidePkg(named)

		if !isGeneric && !structHasSyncPrimitiveField(s, map[types.Type]bool{}) {
			st.valueAutoSyncTypes[tn] = true
			valCands = append(valCands, named)
		}

		if copiedPtrTypes[tn] {
			continue
		}
		if !isMain && tn.Exported() && hasExportedOrPromotedField(named, map[types.Type]bool{}) {
			continue
		}
		hasServiceEvidence := named.NumMethods() > 0
		if !hasServiceEvidence {
			for i := range s.NumFields() {
				sf := s.Field(i)
				f := sf.Origin()
				if isSyncPrimitiveValueType(sf.Type()) || st.ix.Guarded[f] != nil || st.autoGuarded[f] != nil || (isGeneric && st.paramAutoGuarded[f] != nil) {
					hasServiceEvidence = true
					break
				}
				if sf.Embedded() {
					hasServiceEvidence = true
					break
				}
				if unexposedType {
					switch sf.Type().Underlying().(type) {
					case *types.Signature, *types.Interface:
						hasServiceEvidence = true
					}
					if hasServiceEvidence {
						break
					}
				}
			}
		}
		if !hasServiceEvidence {
			continue
		}
		if isGeneric {
			st.genericAutoSyncTypes[tn] = true
		} else {
			st.autoSyncTypes[tn] = true
		}
		cands = append(cands, named)
	}

	for changed := true; changed; {
		changed = false
		for _, named := range valCands {
			tn := named.Origin().Obj()
			if !st.valueAutoSyncTypes[tn] {
				continue
			}
			if !st.checkValueAutoSyncStructFields(named, faByField, fvalByField, isMain) {
				delete(st.valueAutoSyncTypes, tn)
				changed = true
			}
		}
		for _, named := range cands {
			tn := named.Origin().Obj()
			isGeneric := named.Origin().TypeParams() != nil && named.Origin().TypeParams().Len() > 0
			if isGeneric {
				if !st.genericAutoSyncTypes[tn] {
					continue
				}
				if !st.checkAutoSyncStructFields(named, faByField, fvalByField, isMain, true) {
					delete(st.genericAutoSyncTypes, tn)
					changed = true
				}
			} else {
				if !st.autoSyncTypes[tn] {
					continue
				}
				if !st.checkAutoSyncStructFields(named, faByField, fvalByField, isMain, false) {
					delete(st.autoSyncTypes, tn)
					changed = true
				}
			}
		}
	}
}

type fvalSite struct {
	fn *ssa.Function
	fv *ssa.Field
}

func (st *state) checkValueAutoSyncStructFields(named *types.Named, faByField map[*types.Var][]faSite, fvalByField map[*types.Var][]fvalSite, isMain bool) bool {
	s := named.Underlying().(*types.Struct)
	tn := named.Origin().Obj()
	unexposedType := !tn.Exported() && !st.typeExposedOutsidePkg(named)
	typeKey := types.TypeString(named.Origin(), nil)

	for i := range s.NumFields() {
		sf := s.Field(i)
		f := sf.Origin()
		ft := sf.Type()
		if st.cls.Shareable(ft) || st.isAutoSyncTypeSeen(ft, false, map[types.Type]bool{}) {
			continue
		}
		if !isMain && !unexposedType && (sf.Exported() || sf.Embedded()) {
			return false
		}
		sites := faByField[f]
		if !st.fieldReadOnlyPostConstruction(sites) {
			return false
		}
		switch u := ft.Underlying().(type) {
		case *types.Slice:
			if !st.isAutoSyncTypeSeen(u.Elem(), false, map[types.Type]bool{}) {
				return false
			}
			if !st.readOnlyContainerField(sites, fvalByField[f], f, false) {
				return false
			}
		case *types.Map:
			if !st.isAutoSyncTypeSeen(u.Key(), false, map[types.Type]bool{}) ||
				!st.isAutoSyncTypeSeen(u.Elem(), false, map[types.Type]bool{}) {
				return false
			}
			if !st.readOnlyContainerField(sites, fvalByField[f], f, false) {
				return false
			}
		case *types.Signature:
			if !st.closedReadOnlyCallableField(typeKey, i) {
				return false
			}
		case *types.Interface:
			if !st.closedReadOnlyInterfaceField(typeKey, i, u) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) checkAutoSyncStructFields(named *types.Named, faByField map[*types.Var][]faSite, fvalByField map[*types.Var][]fvalSite, isMain bool, allowTP bool) bool {
	s := named.Underlying().(*types.Struct)
	typeKey := types.TypeString(named.Origin(), nil)

	for i := range s.NumFields() {
		sf := s.Field(i)
		f := sf.Origin()
		if st.autoGuarded[f] != nil || (allowTP && st.paramAutoGuarded[f] != nil) {
			continue
		}
		sites := faByField[f]
		if isSyncPrimitiveValueType(sf.Type()) {
			if !st.syncFieldNeverOverwrittenOrCopied(sites) {
				return false
			}
			continue
		}
		ft := sf.Type()
		if _, isValStruct := ft.Underlying().(*types.Struct); isValStruct && st.isAutoSyncTypeSeen(ft, allowTP, map[types.Type]bool{}) {
			if !isMain && sf.Exported() {
				return false
			}
			if !st.embeddedAutoSyncStructNeverCopied(sites) {
				return false
			}
			continue
		}
		if !st.fieldReadOnlyPostConstruction(sites) {
			return false
		}
		if st.isAutoSyncTypeSeen(ft, allowTP, map[types.Type]bool{}) {
			continue
		}
		if !isMain && (sf.Exported() || sf.Embedded()) {
			return false
		}
		switch u := ft.Underlying().(type) {
		case *types.Slice:
			if !st.isAutoSyncTypeSeen(u.Elem(), allowTP, map[types.Type]bool{}) {
				return false
			}
			if !st.readOnlyContainerField(sites, fvalByField[f], f, allowTP) {
				return false
			}
		case *types.Map:
			if !st.isAutoSyncTypeSeen(u.Key(), allowTP, map[types.Type]bool{}) ||
				!st.isAutoSyncTypeSeen(u.Elem(), allowTP, map[types.Type]bool{}) {
				return false
			}
			if !st.readOnlyContainerField(sites, fvalByField[f], f, allowTP) {
				return false
			}
		case *types.Signature:
			if !st.closedReadOnlyCallableField(typeKey, i) {
				return false
			}
		case *types.Interface:
			if !st.closedReadOnlyInterfaceField(typeKey, i, u) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) embeddedAutoSyncStructNeverCopied(sites []faSite) bool {
	for _, site := range sites {
		if site.fa.Referrers() == nil {
			continue
		}
		for _, r := range *site.fa.Referrers() {
			switch x := r.(type) {
			case *ssa.DebugRef, *ssa.FieldAddr:
			case *ssa.Store:
				if x.Addr != site.fa || !st.autoPrePub(site.fn, x, site.fa.X) {
					return false
				}
			case *ssa.UnOp:
				if x.Op == token.MUL {
					return false
				}
			case *ssa.Call, *ssa.Defer:
				cc := r.(ssa.CallInstruction).Common()
				if cc.IsInvoke() || cc.StaticCallee() == nil || len(cc.Args) == 0 || cc.Args[0] != site.fa {
					return false
				}
				for k := 1; k < len(cc.Args); k++ {
					if cc.Args[k] == site.fa {
						return false
					}
				}
			default:
				return false
			}
		}
	}
	return true
}

type faSite struct {
	fn *ssa.Function
	fa *ssa.FieldAddr
}

func (st *state) syncFieldNeverOverwrittenOrCopied(sites []faSite) bool {
	for _, site := range sites {
		if site.fa.Referrers() == nil {
			continue
		}
		for _, r := range *site.fa.Referrers() {
			switch x := r.(type) {
			case *ssa.DebugRef:
			case *ssa.Store:
				if x.Addr != site.fa || !st.autoPrePub(site.fn, x, site.fa.X) {
					return false
				}
			case *ssa.UnOp:
				if x.Op == token.MUL {
					return false
				}
			case *ssa.Call, *ssa.Defer:
				// Method call on sync primitive receiver (&x.mu, &x.count, &x.once, &x.m).
				cc := r.(ssa.CallInstruction).Common()
				if cc.IsInvoke() || len(cc.Args) == 0 || cc.Args[0] != site.fa {
					return false
				}
				for k := 1; k < len(cc.Args); k++ {
					if cc.Args[k] == site.fa {
						return false
					}
				}
				callee := cc.StaticCallee()
				if callee == nil || callee.Pkg == nil || callee.Pkg.Pkg == nil {
					return false
				}
				pkgPath := callee.Pkg.Pkg.Path()
				if pkgPath != "sync" && pkgPath != "sync/atomic" {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func (st *state) fieldReadOnlyPostConstruction(sites []faSite) bool {
	for _, site := range sites {
		if !st.addrReadOnlyPostConstruction(site.fn, site.fa, site.fa.X) {
			return false
		}
	}
	return true
}

func (st *state) addrReadOnlyPostConstruction(fn *ssa.Function, addr, rootBase ssa.Value) bool {
	if addr.Referrers() == nil {
		return true
	}
	for _, r := range *addr.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if x.Addr != addr || !st.autoPrePub(fn, x, rootBase) {
				return false
			}
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != addr {
				return false
			}
		case *ssa.FieldAddr:
			if x.X != addr || !st.addrReadOnlyPostConstruction(fn, x, rootBase) {
				return false
			}
		case *ssa.IndexAddr:
			if x.X != addr || !st.addrReadOnlyPostConstruction(fn, x, rootBase) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) readOnlyContainerField(sites []faSite, fvalSites []fvalSite, selfField *types.Var, allowTP bool) bool {
	for _, site := range sites {
		if site.fa.Referrers() == nil {
			continue
		}
		for _, r := range *site.fa.Referrers() {
			switch x := r.(type) {
			case *ssa.DebugRef:
			case *ssa.Store:
				if x.Addr != site.fa || !st.autoPrePub(site.fn, x, site.fa.X) {
					return false
				}
				ok, _ := st.isOwnedInteriorVal(x.Val, selfField, nil, false, allowTP, nil)
				if !ok {
					return false
				}
			case *ssa.UnOp:
				if x.Op != token.MUL || x.X != site.fa {
					return false
				}
				if !st.containerValueStrictlyReadOnly(site.fn, x, map[ssa.Value]bool{}) {
					return false
				}
			default:
				return false
			}
		}
	}
	for _, fvSite := range fvalSites {
		if !st.containerValueStrictlyReadOnly(fvSite.fn, fvSite.fv, map[ssa.Value]bool{}) {
			return false
		}
	}
	return true
}

func (st *state) containerValueStrictlyReadOnly(fn *ssa.Function, val ssa.Value, seen map[ssa.Value]bool) bool {
	if val == nil || st.cls.Shareable(val.Type()) || st.isAutoSyncType(val.Type()) {
		return true
	}
	if seen[val] {
		return true
	}
	seen[val] = true
	if val.Referrers() == nil {
		return true
	}
	for _, r := range *val.Referrers() {
		if r.Parent() != fn {
			return false
		}
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return false
			}
		case *ssa.Lookup:
			if x.X != val || !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Extract:
			if !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Range:
			if x.X != val || !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Next:
			if !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Index:
			if x.X != val || !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.IndexAddr:
			if x.X != val || !st.addrStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.FieldAddr:
			if x.X != val || !st.addrStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Slice:
			if x.X != val || !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Phi:
			if !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.ChangeType:
			if !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.Store:
			if x.Val != val {
				return false
			}
			a, ok := x.Addr.(*ssa.Alloc)
			if !ok || a.Heap || a.Parent() != fn || isResultSlot(a) || allocCaptured(a) {
				return false
			}
			for _, ar := range *a.Referrers() {
				if load, ok := ar.(*ssa.UnOp); ok && load.Op == token.MUL && load.X == a {
					if !st.containerValueStrictlyReadOnly(fn, load, seen) {
						return false
					}
				}
			}
		case *ssa.Call:
			b, isBuiltin := x.Call.Value.(*ssa.Builtin)
			if !isBuiltin {
				if callee := x.Call.StaticCallee(); callee != nil && !x.Call.IsInvoke() && x.Call.Value != val && callee.Pkg != nil && callee.Pkg.Pkg != nil {
					switch callee.Pkg.Pkg.Path() {
					case "slices":
						if callee.Name() == "Clone" && len(x.Call.Args) == 1 && x.Call.Args[0] == val {
							if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem())) {
								continue
							}
						}
						if (callee.Name() == "Contains" || callee.Name() == "Index") && len(x.Call.Args) == 2 && x.Call.Args[0] == val && x.Call.Args[1] != val {
							if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem())) {
								continue
							}
						}
						if callee.Name() == "Equal" && len(x.Call.Args) == 2 && (x.Call.Args[0] == val || x.Call.Args[1] == val) {
							if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem())) {
								continue
							}
						}
					case "maps":
						if callee.Name() == "Clone" && len(x.Call.Args) == 1 && x.Call.Args[0] == val {
							if mp, ok := val.Type().Underlying().(*types.Map); ok && st.cls.Shareable(mp.Key()) && (st.cls.Shareable(mp.Elem()) || st.isAutoSyncType(mp.Elem())) {
								continue
							}
						}
						if callee.Name() == "Copy" && len(x.Call.Args) == 2 && x.Call.Args[1] == val && x.Call.Args[0] != val {
							if _, isMake := x.Call.Args[0].(*ssa.MakeMap); isMake {
								if mp, ok := val.Type().Underlying().(*types.Map); ok && st.cls.Shareable(mp.Key()) && (st.cls.Shareable(mp.Elem()) || st.isAutoSyncType(mp.Elem())) {
									continue
								}
							}
						}
					}
				}
				return false
			}
			switch b.Name() {
			case "len", "cap", "ssa:wrapnilchk":
				continue
			case "append":
				if len(x.Call.Args) == 2 && x.Call.Args[1] == val && x.Call.Args[0] != val {
					if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem())) {
						if c, isConst := x.Call.Args[0].(*ssa.Const); isConst && c.IsNil() {
							continue
						}
						if _, isMake := x.Call.Args[0].(*ssa.MakeSlice); isMake {
							continue
						}
					}
				}
				return false
			case "copy":
				if len(x.Call.Args) == 2 && x.Call.Args[1] == val && x.Call.Args[0] != val {
					if slice, ok := val.Type().Underlying().(*types.Slice); ok && (st.cls.Shareable(slice.Elem()) || st.isAutoSyncType(slice.Elem())) {
						continue
					}
				}
				return false
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) addrStrictlyReadOnly(fn *ssa.Function, addr ssa.Value, seen map[ssa.Value]bool) bool {
	if addr.Referrers() == nil {
		return true
	}
	for _, r := range *addr.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != addr || !st.containerValueStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.FieldAddr:
			if x.X != addr || !st.addrStrictlyReadOnly(fn, x, seen) {
				return false
			}
		case *ssa.IndexAddr:
			if x.X != addr || !st.addrStrictlyReadOnly(fn, x, seen) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// resolveClosedFieldCallables resolves all concrete function/closure targets that can ever be
// stored into unexported field fieldIdx of local struct typeKey. Returns ok=false if any store is
// unresolved, cut, or the struct has conversions/escapes that open the field.
func (st *state) resolveClosedFieldCallables(typeKey string, fieldIdx int) (targets []*ssa.Function, ok bool) {
	if st.contracts == nil || len(st.contracts.structConv[typeKey]) > 0 {
		return nil, false
	}
	stores := st.contracts.fieldStores(typeKey, fieldIdx)
	for _, iv := range stores {
		if c, isConst := iv.v.(*ssa.Const); isConst && c.IsNil() {
			continue
		}
		q := st.ask()
		leaves, incomplete := q.origins(iv.fn, iv.v)
		if incomplete || q.cut || len(leaves) == 0 {
			return nil, false
		}
		for _, lf := range leaves {
			switch x := lf.v.(type) {
			case *ssa.Const:
				if !x.IsNil() {
					return nil, false
				}
			case *ssa.Function:
				targets = append(targets, x)
			case *ssa.MakeClosure:
				fn, isFn := x.Fn.(*ssa.Function)
				if !isFn || fn == nil {
					return nil, false
				}
				if isBoundMethodWrapper(fn) {
					m, isMeth := fn.Object().(*types.Func)
					if !isMeth || types.IsInterface(m.Signature().Recv().Type()) || len(x.Bindings) != 1 {
						return nil, false
					}
					if !st.cls.Shareable(x.Bindings[0].Type()) && !st.isAutoSyncType(x.Bindings[0].Type()) {
						sum := st.summary(fn)
						if sum.writes(0) || sum.writesFree(0) {
							return nil, false
						}
					}
				} else {
					sum := st.summary(fn)
					for j, b := range x.Bindings {
						if !st.cls.Shareable(b.Type()) && !st.isAutoSyncType(b.Type()) && sum.writesFree(j) {
							return nil, false
						}
					}
				}
				targets = append(targets, fn)
			default:
				return nil, false
			}
		}
	}
	return targets, true
}

func (st *state) closedReadOnlyCallableField(typeKey string, fieldIdx int) bool {
	_, ok := st.resolveClosedFieldCallables(typeKey, fieldIdx)
	return ok
}

// resolveClosedFieldInterfaceTypes resolves all concrete types boxed into unexported interface
// field fieldIdx of local struct typeKey.
func (st *state) resolveClosedFieldInterfaceTypes(typeKey string, fieldIdx int) (concretes []types.Type, ok bool) {
	if st.contracts == nil || len(st.contracts.structConv[typeKey]) > 0 {
		return nil, false
	}
	stores := st.contracts.fieldStores(typeKey, fieldIdx)
	if len(stores) == 0 {
		return nil, false
	}
	for _, iv := range stores {
		if c, isConst := iv.v.(*ssa.Const); isConst && c.IsNil() {
			continue
		}
		q := st.ask()
		ts, incomplete := q.resolveInterfaceTypes(iv.fn, iv.v)
		if incomplete || q.cut {
			return nil, false
		}
		for _, t := range ts {
			exists := false
			for _, ex := range concretes {
				if types.Identical(ex, t) {
					exists = true
					break
				}
			}
			if !exists {
				concretes = append(concretes, t)
			}
		}
	}
	return concretes, len(concretes) > 0
}

func (st *state) closedReadOnlyInterfaceField(typeKey string, fieldIdx int, iface *types.Interface) bool {
	concretes, ok := st.resolveClosedFieldInterfaceTypes(typeKey, fieldIdx)
	if !ok {
		return false
	}
	for _, ct := range concretes {
		if st.cls.Shareable(ct) || st.isAutoSyncType(ct) {
			continue
		}
		// Even if concrete type is not Shareable/AutoSync, check if every method of iface on ct has writes(0) == false!
		mset := st.ssa.Pkg.Prog.MethodSets.MethodSet(ct)
		for m := range iface.NumMethods() {
			sel := mset.Lookup(iface.Method(m).Pkg(), iface.Method(m).Name())
			if sel == nil {
				return false
			}
			obj, isFunc := sel.Obj().(*types.Func)
			if !isFunc {
				return false
			}
			if st.summaryOfFuncObj(obj).writes(0) {
				return false
			}
		}
	}
	return true
}

// resolveLoadedFieldCallables resolves concrete functions stored into a read-only local struct
// callable field when callVal is a direct load (`*fa`) from that field.
func (st *state) resolveLoadedFieldCallables(callVal ssa.Value) ([]*ssa.Function, bool) {
	load, ok := callVal.(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return nil, false
	}
	fa, ok := load.X.(*ssa.FieldAddr)
	if !ok {
		return nil, false
	}
	named, s := derefNamedStruct(fa.X.Type())
	if named == nil || named.Origin().Obj() == nil || st.pass == nil || named.Origin().Obj().Pkg() != st.pass.Pkg || fa.Field >= s.NumFields() {
		return nil, false
	}
	sf := s.Field(fa.Field)
	if !st.roLocalFields[sf.Origin()] && (sf.Exported() || sf.Embedded()) {
		return nil, false
	}
	return st.resolveClosedFieldCallables(types.TypeString(named.Origin(), nil), fa.Field)
}

// resolveInvokeMethodSummaries resolves the method summaries of all concrete method
// implementations that cc (an interface method call in fn) can dispatch to when the receiver's
// concrete types are closed, including cross-package types whose methods live in FuncFact.
func (st *state) resolveInvokeMethodSummaries(fn *ssa.Function, cc *ssa.CallCommon) ([]*summary, bool) {
	if !cc.IsInvoke() || cc.Method == nil || st.ssa == nil || st.ssa.Pkg == nil || st.ssa.Pkg.Prog == nil {
		return nil, false
	}
	q := st.ask()
	concretes, incomplete := q.resolveInterfaceTypes(fn, cc.Value)
	if incomplete || q.cut || len(concretes) == 0 {
		concretes = nil
		if load, ok := cc.Value.(*ssa.UnOp); ok && load.Op == token.MUL {
			if fa, ok := load.X.(*ssa.FieldAddr); ok {
				named, s := derefNamedStruct(fa.X.Type())
				if named != nil && named.Origin().Obj() != nil && st.pass != nil && named.Origin().Obj().Pkg() == st.pass.Pkg && fa.Field < s.NumFields() {
					sf := s.Field(fa.Field)
					if st.roLocalFields[sf.Origin()] || (!sf.Exported() && !sf.Embedded()) {
						if closed, ok := st.resolveClosedFieldInterfaceTypes(types.TypeString(named.Origin(), nil), fa.Field); ok {
							concretes = closed
						}
					}
				}
			}
		}
		if len(concretes) == 0 {
			return nil, false
		}
	}
	prog := st.ssa.Pkg.Prog
	var sums []*summary
	for _, ct := range concretes {
		sel := prog.MethodSets.MethodSet(ct).Lookup(cc.Method.Pkg(), cc.Method.Name())
		if sel == nil {
			return nil, false
		}
		var ms *summary
		if mfn := prog.MethodValue(sel); mfn != nil {
			ms = st.summary(mfn)
		} else if obj, ok := sel.Obj().(*types.Func); ok {
			ms = st.summaryOfFuncObj(obj)
		}
		if ms == nil {
			return nil, false
		}
		sums = append(sums, ms)
	}
	return sums, len(sums) > 0
}

// resolveFreeVarCallables resolves concrete functions and closures that callVal (a closure
// FreeVar or load from a closed local function cell) can hold across all MakeClosure bindings of
// its enclosing anonymous function.
func (st *state) resolveFreeVarCallables(callVal ssa.Value) ([]callTarget, bool) {
	if st.contracts == nil || callVal == nil {
		return nil, false
	}
	switch x := callVal.(type) {
	case *ssa.FreeVar:
	case *ssa.UnOp:
		if x.Op != token.MUL {
			return nil, false
		}
	default:
		return nil, false
	}
	seen := map[ssa.Value]bool{}
	var out []callTarget
	var trace func(v ssa.Value) bool
	trace = func(v ssa.Value) bool {
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
		case *ssa.Function:
			out = append(out, callTarget{fn: x})
			return true
		case *ssa.MakeClosure:
			mcFn, ok := x.Fn.(*ssa.Function)
			if !ok || mcFn == nil {
				return false
			}
			out = append(out, callTarget{fn: mcFn, mc: x})
			return true
		case *ssa.ChangeType:
			return trace(x.X)
		case *ssa.Phi:
			if len(x.Edges) == 0 {
				return false
			}
			for _, e := range x.Edges {
				if !trace(e) {
					return false
				}
			}
			return true
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return false
			}
			return trace(x.X)
		case *ssa.Alloc:
			uses := usesOfCell(x)
			if uses.open || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 || len(uses.stores) == 0 {
				return false
			}
			for _, store := range uses.stores {
				if !trace(store.Val) {
					return false
				}
			}
			return true
		case *ssa.FreeVar:
			parent := x.Parent()
			if parent == nil {
				return false
			}
			idx := slices.Index(parent.FreeVars, x)
			if idx < 0 {
				return false
			}
			closures := st.contracts.closuresOf[canonicalBodyFn(parent)]
			if len(closures) == 0 {
				closures = st.contracts.closuresOf[parent]
			}
			if len(closures) == 0 {
				return false
			}
			for _, mc := range closures {
				if idx >= len(mc.Bindings) || !trace(mc.Bindings[idx]) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	if !trace(callVal) || len(out) == 0 {
		return nil, false
	}
	return out, true
}
