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
func (st *state) joins(g *graph, t transfer) map[ssa.Instruction]bool {
	sp, ok := spawnOf(t.instr)
	if !ok {
		return nil
	}
	if j, ok := st.joinCache[t.instr]; ok {
		return j
	}
	j := st.computeJoins(g, sp)
	st.joinCache[t.instr] = j
	return j
}

func (st *state) computeJoins(g *graph, sp spawnSite) map[ssa.Instruction]bool {
	if sp.repeated {
		// A handler runs again on the next request: it never hands anything back.
		return nil
	}
	f, mc := sp.closure()
	if f == nil {
		return nil
	}
	// The moved memory as F sees it: every captured variable and argument that carries memory in the
	// parent. This is wider than any one transfer's region, which only makes the check stricter.
	var roots []ssa.Value
	for j, b := range mc.Bindings {
		if g.val(b) != nil {
			roots = append(roots, f.FreeVars[j])
		}
	}
	for i, a := range sp.args {
		if g.val(a) != nil {
			roots = append(roots, f.Params[i])
		}
	}
	if len(roots) == 0 {
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
	if soleSpawn(mc, true) == nil {
		// In a loop, a receive or Done does not say which instance of F it synchronised with.
		return out
	}
	for _, b := range mc.Bindings {
		so := syncObject(g.fn, b)
		if so == nil {
			continue
		}
		rels := so.releases[f]
		if len(rels) == 0 || !releasedClean(rels, accesses, defers) {
			continue
		}
		if so.isChan && len(so.releases) != 1 {
			// Another goroutine (or the parent) also sends on or closes the channel, so a receive
			// does not say which sender it synchronised with.
			continue
		}
		for _, a := range so.acquires {
			join(a)
		}
	}
	return out
}

// waitCalls returns the non-deferred calls to the wait method on recv in fn. The receiver must be
// the very value the spawner was called on, so both calls reach the same object.
func waitCalls(fn *ssa.Function, recv ssa.Value, wait string) []ssa.Instruction {
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
	so := &syncObj{releases: map[*ssa.Function][]ssa.Instruction{}, closures: map[*ssa.Function]*ssa.MakeClosure{}}
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
		start := soleSpawn(mc, true)
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
