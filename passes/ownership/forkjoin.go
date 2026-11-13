package ownership

import (
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// Fork-join: a goroutine can hand the memory it was given back to its parent.
//
// A move into a goroutine started by `go F(...)`, with F a function literal, ends at a join: a
// parent instruction after which F provably no longer touches what it was given. useAfter treats a
// join like a redefinition, so a use reached only through joins is not a use after the move.
//
// F hands the memory back through a synchronisation object O that it captured, when
//   - no instruction of F that touches the moved memory can run after a release of O in F (a send
//     or close on a channel, or WaitGroup.Done), counting deferred calls as running at exit;
//   - F neither moves that memory on nor lets it escape; and
//   - O is a channel made in the parent whose only sender is F, or a WaitGroup local to the parent
//     whose Add/Done calls are balanced by construction (see waitGroupOK).
//
// The join is then a plain receive from O, or O.Wait(), in the parent. By the Go memory model a send
// or close is synchronised before the receive that observes it, and Done before the Wait it
// unblocks; F's last touch of the memory comes before its release.

// A callback started by a spawner with a wait method (WaitGroup.Go, errgroup.Group.Go) has one more
// join: Wait on the same receiver returns only after every callback started on it has returned, so
// F's last touch of the memory comes before it. This holds in a loop too, since one Wait covers every
// callback started before it.

// joins returns the parent instructions that end the moves made at t, or nil when t is not a spawn
// site or the goroutine cannot hand anything back.
type joinCacheKey struct {
	ins   ssa.Instruction
	index int
}

func (st *state) joins(g *graph, t transfer) map[ssa.Instruction]bool {
	sp, ok := st.spawnSiteAt(t.instr, t.index)
	if !ok {
		return nil
	}
	key := joinCacheKey{ins: t.instr, index: sp.arg}
	if j, ok := st.joinCache[key]; ok {
		return j
	}
	j := st.computeJoins(g, sp)
	st.joinCache[key] = j
	return j
}

func (st *state) computeJoins(g *graph, sp spawnSite) map[ssa.Instruction]bool {
	if sp.repeated {
		// A handler runs again on the next request: it never hands anything back.
		return nil
	}
	f, mc := sp.closureWithState(st)
	if f == nil {
		return nil
	}
	// The moved memory as F sees it: every captured variable and argument that carries memory in the
	// parent. This is wider than any one transfer's region, which only makes the check stricter.
	var roots []ssa.Value
	var shareableCellRoots []*ssa.FreeVar
	hadMovedInput := false
	for j, b := range mc.Bindings {
		if g.val(b) != nil {
			hadMovedInput = true
			if st.isShareableCellBinding(b, f.FreeVars[j]) {
				shareableCellRoots = append(shareableCellRoots, f.FreeVars[j])
				continue
			}
			roots = append(roots, f.FreeVars[j])
		}
	}
	for i, a := range sp.args {
		if g.val(a) != nil {
			hadMovedInput = true
			roots = append(roots, f.Params[i])
		}
	}
	if !hadMovedInput {
		return nil
	}
	fg := st.graphOf(f)
	region := map[*node]bool{}
	for _, r := range roots {
		if n := fg.val(r); n != nil {
			for c := range fg.region(n) {
				region[c] = true
			}
		}
	}
	for c := range region {
		for _, e := range c.events {
			if e.kind == evEscape || e.kind == evTransfer {
				// Moved on or kept elsewhere: someone still holds it after F's release.
				return nil
			}
		}
	}
	shareableCellSet := map[ssa.Value]bool{}
	for _, fv := range shareableCellRoots {
		shareableCellSet[fv] = true
	}
	var accesses []ssa.Instruction
	var defers []*ssa.Defer
	forEachInstr(f, func(ins ssa.Instruction) {
		if _, dbg := ins.(*ssa.DebugRef); dbg {
			return
		}
		for _, op := range ins.Operands(nil) {
			if op == nil || *op == nil {
				continue
			}
			if shareableCellSet[*op] {
				accesses = append(accesses, ins)
				if d, ok := ins.(*ssa.Defer); ok {
					defers = append(defers, d)
				}
				return
			}
			if n := fg.nodes[*op]; n != nil && region[n.find()] {
				accesses = append(accesses, ins)
				if d, ok := ins.(*ssa.Defer); ok {
					defers = append(defers, d)
				}
				return
			}
		}
	})

	var out map[ssa.Instruction]bool
	join := func(ins ssa.Instruction) {
		if out == nil {
			out = map[ssa.Instruction]bool{}
		}
		out[ins] = true
	}
	if recv := sp.receiver(); recv != nil {
		// Every access of F happens before F returns, so no release check is needed.
		for _, w := range waitCalls(g.fn, recv, sp.wait) {
			join(w)
		}
	}
	if soleSpawnWithState(st, mc, true) == nil {
		// In a loop, a receive or Done does not say which instance of F it synchronised with.
		return out
	}
	if callIns, ok := sp.ins.(*ssa.Call); ok && (sp.via == "time.AfterFunc" || sp.via == "context.AfterFunc") {
		for _, iff := range stopIfJoins(g.fn, callIns, sp.via) {
			join(iff)
		}
	}
	for _, b := range mc.Bindings {
		so := syncObjectWithState(st, g.fn, b)
		if so == nil {
			continue
		}
		rels := so.releases[f]
		if len(rels) == 0 || !releasedClean(rels, accesses, defers) {
			continue
		}
		if so.isChan && len(so.releases) != 1 {
			if acq := sequentialChanAcquire(st, g, sp.ins, so); acq != nil {
				join(acq)
			}
			continue
		}
		for _, a := range so.acquires {
			join(a)
		}
	}
	for j, b := range mc.Bindings {
		for _, a := range st.helperClosureJoins(g, sp, f, mc, j, b, accesses, defers) {
			join(a)
		}
	}
	return out
}

func (st *state) isShareableCellBinding(b ssa.Value, fv *ssa.FreeVar) bool {
	if b == nil || fv == nil || fv.Referrers() == nil {
		return false
	}
	switch b.(type) {
	case *ssa.Alloc, *ssa.Phi:
	default:
		return false
	}
	ptr, ok := b.Type().Underlying().(*types.Pointer)
	if !ok || (!st.cls.Shareable(ptr.Elem()) && !st.isAutoSyncType(ptr.Elem())) {
		return false
	}
	for _, r := range *fv.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != fv {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (st *state) spawnerWaitJoins(g *graph, t transfer) map[ssa.Instruction]bool {
	sp, ok := st.spawnSiteAt(t.instr, t.index)
	if !ok || sp.repeated {
		return nil
	}
	recv := sp.receiver()
	if recv == nil {
		return nil
	}
	waits := waitCalls(g.fn, recv, sp.wait)
	if len(waits) == 0 {
		return nil
	}
	out := make(map[ssa.Instruction]bool, len(waits))
	for _, w := range waits {
		out[w] = true
	}
	return out
}

func (st *state) joinsForValue(g *graph, t transfer, w ssa.Value) map[ssa.Instruction]bool {
	if j := st.joins(g, t); len(j) > 0 {
		return j
	}
	sp, ok := st.spawnSiteAt(t.instr, t.index)
	if !ok || sp.repeated || sp.receiver() == nil || len(sp.args) > 0 {
		return nil
	}
	f, mc := sp.closureWithState(st)
	if f == nil || mc == nil {
		return nil
	}
	wn := g.val(w)
	if wn == nil {
		return nil
	}
	cs := st.summary(f)
	if cs.pessimistic {
		return nil
	}
	wReg := g.region(wn)
	fg := st.graphOf(f)
	for idx, b := range mc.Bindings {
		if idx >= len(f.FreeVars) {
			return nil
		}
		if st.cls.Shareable(b.Type()) || g.isAutoSyncCapture(t.instr, b) || st.isShareableCellBinding(b, f.FreeVars[idx]) {
			continue
		}
		if bn := g.val(b); bn != nil && wReg[bn.find()] {
			if st.childLeaksOrTransfersFreeVar(fg, f.FreeVars[idx]) || cs.freeLeaks(idx) {
				return nil
			}
		}
	}
	return st.spawnerWaitJoins(g, t)
}

// sequentialChanAcquire handles sequential single-goroutine channel handoffs (`go G1(); <-ch; go G2(); <-ch`)
// where G1 and G2 cannot overlap in time because each spawn dominates its matching receive, which in turn
// strictly dominates the next spawn with no loop back-edge.
func sequentialChanAcquire(st *state, g *graph, spawnIns ssa.Instruction, so *syncObj) ssa.Instruction {
	if !so.isChan || len(so.releases[g.fn]) > 0 || len(so.acquires) != len(so.releases) {
		return nil
	}
	var starts []ssa.Instruction
	for cf, rels := range so.releases {
		if cf == g.fn || len(rels) == 0 {
			return nil
		}
		cmc := so.closures[cf]
		if cmc == nil {
			return nil
		}
		start := soleSpawnWithState(st, cmc, true)
		if start == nil {
			return nil
		}
		for _, r := range rels {
			if _, isSend := r.(*ssa.Send); !isSend {
				return nil
			}
		}
		for _, r1 := range rels {
			after := reachableAfter(r1)
			for _, r2 := range rels {
				if after(r2) {
					return nil
				}
			}
		}
		starts = append(starts, start)
	}
	acqs := append([]ssa.Instruction(nil), so.acquires...)
	for _, a := range acqs {
		u, ok := a.(*ssa.UnOp)
		if !ok || u.Op != token.ARROW || a.Parent() != g.fn || reachableAfter(a)(a) {
			return nil
		}
	}
	slices.SortFunc(starts, func(a, b ssa.Instruction) int {
		if a == b {
			return 0
		}
		if dominates(a, b) {
			return -1
		}
		if dominates(b, a) {
			return 1
		}
		return 0
	})
	slices.SortFunc(acqs, func(a, b ssa.Instruction) int {
		if a == b {
			return 0
		}
		if dominates(a, b) {
			return -1
		}
		if dominates(b, a) {
			return 1
		}
		return 0
	})
	matchIdx := -1
	for k := range starts {
		s, a := starts[k], acqs[k]
		if !dominates(s, a) || reachableAfter(a)(s) {
			return nil
		}
		if k+1 < len(starts) {
			nextS := starts[k+1]
			if !dominates(a, nextS) || reachableAfter(nextS)(a) {
				return nil
			}
		}
		if s == spawnIns {
			matchIdx = k
		}
	}
	if matchIdx < 0 {
		return nil
	}
	return acqs[matchIdx]
}

// helperClosureJoins recognises a local helper closure `sub := func() { ...; c <- 0 }` captured
// solely by F and called once by F with no accesses in F after that call (e.g. `go func() { <-t.C; f() }()`).
func (st *state) helperClosureJoins(g *graph, sp spawnSite, f *ssa.Function, mc *ssa.MakeClosure, bindIdx int, b ssa.Value, fAccesses []ssa.Instruction, fDefers []*ssa.Defer) []ssa.Instruction {
	if len(fDefers) > 0 || bindIdx >= len(f.FreeVars) {
		return nil
	}
	subMC, ok := b.(*ssa.MakeClosure)
	if !ok || subMC.Referrers() == nil {
		return nil
	}
	for _, r := range *subMC.Referrers() {
		if _, dbg := r.(*ssa.DebugRef); dbg {
			continue
		}
		if r != mc {
			return nil
		}
	}
	bindCount := 0
	for _, mb := range mc.Bindings {
		if mb == b {
			bindCount++
		}
	}
	if bindCount != 1 {
		return nil
	}
	fv := f.FreeVars[bindIdx]
	if fv.Referrers() == nil {
		return nil
	}
	var subCall *ssa.Call
	for _, r := range *fv.Referrers() {
		if _, dbg := r.(*ssa.DebugRef); dbg {
			continue
		}
		c, isCall := r.(*ssa.Call)
		if !isCall || subCall != nil || c.Call.Value != fv || usedAsArg(c, fv, -1) {
			return nil
		}
		subCall = c
	}
	if subCall == nil || reachableAfter(subCall)(subCall) {
		return nil
	}
	afterCall := reachableAfter(subCall)
	for _, acc := range fAccesses {
		if acc != subCall && afterCall(acc) {
			return nil
		}
	}
	subFn, ok := subMC.Fn.(*ssa.Function)
	if !ok || len(subFn.Blocks) == 0 {
		return nil
	}
	subG := st.graphOf(subFn)
	subRegion := map[*node]bool{}
	for k, sb := range subMC.Bindings {
		if g.val(sb) != nil && k < len(subFn.FreeVars) {
			if n := subG.val(subFn.FreeVars[k]); n != nil {
				for c := range subG.region(n) {
					subRegion[c] = true
				}
			}
		}
	}
	for c := range subRegion {
		for _, e := range c.events {
			if e.kind == evEscape || e.kind == evTransfer {
				return nil
			}
		}
	}
	var subAccesses []ssa.Instruction
	var subDefers []*ssa.Defer
	forEachInstr(subFn, func(ins ssa.Instruction) {
		if _, dbg := ins.(*ssa.DebugRef); dbg {
			return
		}
		for _, op := range ins.Operands(nil) {
			if op == nil || *op == nil {
				continue
			}
			if n := subG.nodes[*op]; n != nil && subRegion[n.find()] {
				subAccesses = append(subAccesses, ins)
				if d, ok := ins.(*ssa.Defer); ok {
					subDefers = append(subDefers, d)
				}
				return
			}
		}
	})
	var out []ssa.Instruction
	for _, sb := range subMC.Bindings {
		so := syncObjectWithState(st, g.fn, sb)
		if so == nil {
			continue
		}
		rels := so.releases[subFn]
		if len(rels) == 0 || !releasedClean(rels, subAccesses, subDefers) {
			continue
		}
		if so.isChan && len(so.releases) != 1 {
			continue
		}
		out = append(out, so.acquires...)
	}
	return out
}

// waitCalls returns the non-deferred calls to the wait method on recv in fn. The receiver must be
// the very value the spawner was called on, so both calls reach the same object.
func waitCalls(fn *ssa.Function, recv ssa.Value, wait string) []ssa.Instruction {
	if recv == nil || recv.Referrers() == nil {
		return nil
	}
	var out []ssa.Instruction
	for _, r := range *recv.Referrers() {
		c, ok := r.(*ssa.Call)
		if !ok || c.Call.IsInvoke() || len(c.Call.Args) == 0 || c.Call.Args[0] != recv || c.Parent() != fn {
			continue
		}
		callee := c.Call.StaticCallee()
		if callee == nil {
			continue
		}
		if obj, ok := callee.Object().(*types.Func); ok && obj.FullName() == wait {
			out = append(out, c)
		}
	}
	return out
}

// stopIfJoins returns the *ssa.If instructions in fn whose branch condition checks the boolean
// result of (*time.Timer).Stop (for time.AfterFunc) or stop() (for context.AfterFunc) on spawnCall.
// When Stop() returns true, the callback was stopped before starting and never runs.
func stopIfJoins(fn *ssa.Function, spawnCall *ssa.Call, via string) []*ssa.If {
	var out []*ssa.If
	isSpawnHandle := func(v ssa.Value) bool {
		if v == spawnCall {
			return true
		}
		if u, ok := v.(*ssa.UnOp); ok && u.Op == token.MUL {
			if sv := uncapturedSingleStore(u); sv == spawnCall {
				return true
			}
		}
		return false
	}
	isStopCall := func(v ssa.Value) bool {
		c, ok := v.(*ssa.Call)
		if !ok || c.Call.IsInvoke() || c.Parent() != fn {
			return false
		}
		switch via {
		case "time.AfterFunc":
			if len(c.Call.Args) != 1 || !isSpawnHandle(c.Call.Args[0]) {
				return false
			}
			callee := c.Call.StaticCallee()
			if callee == nil {
				return false
			}
			obj, ok := callee.Object().(*types.Func)
			return ok && obj.FullName() == "(*time.Timer).Stop"
		case "context.AfterFunc":
			return len(c.Call.Args) == 0 && isSpawnHandle(c.Call.Value)
		}
		return false
	}
	for _, b := range fn.Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		iff, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		cond, _ := unwrapBoolCond(iff.Cond)
		if isStopCall(cond) {
			out = append(out, iff)
		}
	}
	return out
}

// releasedClean reports whether no access to the moved memory can run after any release. A plain
// release precedes every deferred call; a deferred release precedes the deferred calls registered
// after it (they run first) and follows those registered before it.
func releasedClean(rels, accesses []ssa.Instruction, defers []*ssa.Defer) bool {
	for _, r := range rels {
		if d, ok := r.(*ssa.Defer); ok {
			for _, d2 := range defers {
				if d2 == d || reachableAfter(d2)(d) {
					return false
				}
			}
			continue
		}
		if len(defers) > 0 {
			return false
		}
		after := reachableAfter(r)
		for _, a := range accesses {
			if after(a) {
				return false
			}
		}
	}
	return true
}

// syncObj records every operation on one channel or WaitGroup made in the parent.
type syncObj struct {
	st        *state
	isChan    bool
	acquires  []ssa.Instruction                   // parent receives / Wait calls
	releases  map[*ssa.Function][]ssa.Instruction // sends, closes, Done calls (incl. deferred)
	closures  map[*ssa.Function]*ssa.MakeClosure  // closures that capture the object
	adds      []*ssa.Call                         // WaitGroup.Add calls in the parent
	cellStore *ssa.Store                          // the one store into a captured channel variable
}

// syncObject returns o's operations when o is a channel or WaitGroup the parent creates and every
// use of it is one the join rule understands; otherwise nil.
func syncObject(parent *ssa.Function, o ssa.Value) *syncObj {
	return syncObjectWithState(nil, parent, o)
}

func syncObjectWithState(st *state, parent *ssa.Function, o ssa.Value) *syncObj {
	so := &syncObj{st: st, releases: map[*ssa.Function][]ssa.Instruction{}, closures: map[*ssa.Function]*ssa.MakeClosure{}}
	ok := false
	switch o := o.(type) {
	case *ssa.MakeChan:
		so.isChan = true
		ok = so.walk(parent, o, true)
	case *ssa.Alloc:
		switch {
		case isWaitGroupPtr(o.Type()):
			ok = so.walk(parent, o, true)
		case isChanCell(o.Type()):
			so.isChan = true
			ok = so.walkCell(parent, o, true)
		}
	}
	if !ok {
		return nil
	}
	if !so.isChan && !so.waitGroupOK() {
		return nil
	}
	return so
}

func isChanCell(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	_, ok = p.Elem().Underlying().(*types.Chan)
	return ok
}

func isWaitGroupPtr(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := p.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "sync" && obj.Name() == "WaitGroup"
}

// walkCell visits the uses of a captured channel variable's cell: SSA captures every variable by
// reference, so `c := make(chan T)` reaches its closures as a cell holding the channel. The cell is
// accepted when it is written exactly once, in the parent, with a channel the parent makes, and is
// otherwise only loaded or captured. Each load is then one of the channel's values.
func (so *syncObj) walkCell(fn *ssa.Function, cell ssa.Value, inParent bool) bool {
	for _, r := range *cell.Referrers() {
		switch r := r.(type) {
		case *ssa.DebugRef:
		case *ssa.MakeClosure:
			if !inParent {
				return false
			}
			cf := r.Fn.(*ssa.Function)
			so.closures[cf] = r
			for j, b := range r.Bindings {
				if b == cell && !so.walkCell(cf, cf.FreeVars[j], false) {
					return false
				}
			}
		case *ssa.Store:
			mk, ok := r.Val.(*ssa.MakeChan)
			if !inParent || r.Addr != cell || !ok || so.cellStore != nil {
				return false
			}
			so.cellStore = r
			if !so.walk(fn, mk, inParent) {
				return false
			}
		case *ssa.UnOp:
			if r.Op != token.MUL || !so.walk(fn, r, inParent) {
				return false
			}
		default:
			return false
		}
	}
	return so.cellStore != nil
}

// walk visits the uses of v in fn. It is a whitelist: any use it does not recognise (passing the
// object to a call, converting it, storing it, capturing it twice over) rejects the object.
func (so *syncObj) walk(fn *ssa.Function, v ssa.Value, inParent bool) bool {
	for _, r := range *v.Referrers() {
		switch r := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			// Only the single store that fills a captured channel variable's cell.
			if r != so.cellStore || r.Val != v {
				return false
			}
		case *ssa.MakeClosure:
			if !inParent {
				return false
			}
			cf := r.Fn.(*ssa.Function)
			so.closures[cf] = r
			for j, b := range r.Bindings {
				if b == v && !so.walk(cf, cf.FreeVars[j], false) {
					return false
				}
			}
		case *ssa.UnOp:
			if !so.isChan || r.Op != token.ARROW {
				return false
			}
			if inParent {
				so.acquires = append(so.acquires, r)
			}
		case *ssa.Send:
			if !so.isChan || r.X == v {
				return false
			}
			so.releases[fn] = append(so.releases[fn], r)
		case *ssa.Select:
			if !so.isChan {
				return false
			}
			for _, s := range r.States {
				if s.Send == v {
					return false
				}
				if s.Chan == v && s.Dir == types.SendOnly {
					so.releases[fn] = append(so.releases[fn], r)
				}
			}
		case *ssa.Call:
			if !so.call(fn, r, r.Common(), v, inParent, false) {
				return false
			}
		case *ssa.Defer:
			if !so.call(fn, r, r.Common(), v, inParent, true) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (so *syncObj) call(fn *ssa.Function, ins ssa.Instruction, cc *ssa.CallCommon, v ssa.Value, inParent, deferred bool) bool {
	if cc.IsInvoke() || cc.Value == v {
		return false
	}
	if b, ok := cc.Value.(*ssa.Builtin); ok {
		if !so.isChan {
			return false
		}
		switch b.Name() {
		case "len", "cap":
			return true
		case "close":
			so.releases[fn] = append(so.releases[fn], ins)
			return true
		}
		return false
	}
	callee := cc.StaticCallee()
	if so.isChan || callee == nil || len(cc.Args) == 0 || cc.Args[0] != v || slices.Contains(cc.Args[1:], v) {
		return false
	}
	obj, ok := callee.Object().(*types.Func)
	if !ok {
		return false
	}
	switch obj.FullName() {
	case "(*sync.WaitGroup).Add":
		c, isCall := ins.(*ssa.Call)
		if !inParent || !isCall {
			return false
		}
		so.adds = append(so.adds, c)
	case "(*sync.WaitGroup).Done":
		if inParent {
			return false
		}
		so.releases[fn] = append(so.releases[fn], ins)
	case "(*sync.WaitGroup).Wait":
		if inParent && !deferred {
			so.acquires = append(so.acquires, ins)
		}
	default:
		// Includes (*sync.WaitGroup).Go: a WaitGroup that also starts callbacks is not a captured
		// sync object here. Those callbacks join through their own Wait rule (computeJoins).
		return false
	}
	return true
}

// waitGroupOK checks that Wait returning implies every Done-calling goroutine has called Done:
// every Done runs in a goroutine started once per run of the parent and at most once per path
// through it; every Add is in the parent, adds a positive constant, runs at most once and before
// any of those goroutines starts; and the Adds sum to the number of those goroutines.
func (so *syncObj) waitGroupOK() bool {
	var starts []ssa.Instruction
	for cf, rels := range so.releases {
		mc := so.closures[cf]
		if mc == nil {
			return false
		}
		start := soleSpawnWithState(so.st, mc, true)
		if start == nil {
			return false
		}
		for _, a := range rels {
			after := reachableAfter(a)
			for _, b := range rels {
				if after(b) {
					return false
				}
			}
		}
		starts = append(starts, start)
	}
	var sum int64
	for _, a := range so.adds {
		c, ok := a.Call.Args[1].(*ssa.Const)
		if !ok || c.Value == nil {
			return false
		}
		k := c.Int64()
		if k <= 0 || reachableAfter(a)(a) {
			return false
		}
		for _, s := range starts {
			if !dominates(a, s) {
				return false
			}
		}
		sum += k
	}
	return sum == int64(len(starts))
}

// dominates reports whether a runs before b on every path to b.
func dominates(a, b ssa.Instruction) bool {
	if a.Block() == b.Block() {
		return indexIn(a) < indexIn(b)
	}
	return a.Block().Dominates(b.Block())
}
