package ownership

import (
	"cmp"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// SAFE009: memory annotated //gosree:guarded_by is read or written only while its mutex is held, a
// //gosree:holds helper is called only with its mutex held, and nothing non-shareable read from guarded
// memory leaves the lock (returned, sent, stored elsewhere, handed to a goroutine or to a callee that
// may keep it).
//
// "Held" is a forward must-analysis per function over the set of locked guard paths. A guard path is
// a root SSA value plus field and load steps, so "the same mutex" means the same path from the same
// value: an alias reached another way is a different path, which over-reports, never under-reports.
// Lock and RLock add a path, Unlock and RUnlock remove it, a successful TryLock adds it on its branch,
// a deferred Unlock changes nothing until return. A call may release a lock: a function of this
// package that unlocks one of its parameters' mutexes (summarised), or any function of another
// package or dynamic call that receives the mutex or an object containing it. A closure called in
// place starts with its caller's locks; any other function starts with none, plus its //gosree:holds.

type guardPath struct {
	root  ssa.Value
	steps string
}

type lockMode uint8

const (
	lockRead lockMode = 1 + iota
	lockWrite
)

// lockset is the set of held guards; nil stands for "not reached yet" (the top of the lattice).
type lockset map[guardPath]lockMode

func (s lockset) clone() lockset {
	out := make(lockset, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// meet intersects two locksets, keeping the weaker mode.
func meet(a, b lockset) lockset {
	if a == nil {
		return b.clone()
	}
	out := lockset{}
	for k, m := range a {
		if n, ok := b[k]; ok {
			out[k] = min(m, n)
		}
	}
	return out
}

func sameLockset(a, b lockset) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// pathOf returns the guard path of v: a root value with the field and load steps that reach v. A
// load is a step only through a variable cell assigned once before it; any other load breaks the
// path, because what it reads may change.
//
// The walk has no step limit and needs none: each step goes to an operand of the value, or from a
// load to the value a store that dominates the load put there, and both are defined strictly
// earlier, so no value comes round twice. With a limit (it was 32) a lock reached through a longer
// path was not seen to be taken, and the guarded access under it was reported.
func pathOf(v ssa.Value) (guardPath, bool) {
	var steps []string
	for {
		switch x := v.(type) {
		case *ssa.FieldAddr:
			steps = append(steps, "."+strconv.Itoa(x.Field))
			v = x.X
		case *ssa.Field:
			steps = append(steps, "."+strconv.Itoa(x.Field))
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.UnOp:
			if x.Op != token.MUL || !stableLoad(x) {
				return guardPath{}, false
			}
			if sv := uncapturedSingleStore(x); sv != nil {
				v = sv
				continue
			}
			steps = append(steps, "*")
			v = x.X
		default:
			slices.Reverse(steps)
			return guardPath{v, strings.Join(steps, "")}, true
		}
	}
}

func uncapturedSingleStore(load *ssa.UnOp) ssa.Value {
	cell, ok := load.X.(*ssa.Alloc)
	if !ok {
		return nil
	}
	var store *ssa.Store
	for _, r := range *cell.Referrers() {
		switch r := r.(type) {
		case *ssa.MakeClosure:
			return nil
		case *ssa.Store:
			if r.Addr == cell {
				if store != nil {
					return nil
				}
				store = r
			}
		}
	}
	if store != nil && store.Block() != nil && dominates(store, load) {
		return store.Val
	}
	return nil
}

// stableLoad reports whether a load reads a variable cell that holds one value for the whole time
// the load can see it: a captured variable the closure never assigns, or a local cell stored once,
// before the load, or a struct pointer field not reassigned in this function.
func stableLoad(load *ssa.UnOp) bool {
	switch cell := load.X.(type) {
	case *ssa.FreeVar:
		for _, r := range *cell.Referrers() {
			if s, ok := r.(*ssa.Store); ok && s.Addr == cell {
				return false
			}
		}
		return true
	case *ssa.Alloc:
		var store *ssa.Store
		for _, r := range *cell.Referrers() {
			switch r := r.(type) {
			case *ssa.Store:
				if r.Addr == cell {
					if store != nil {
						return false
					}
					store = r
				}
			case *ssa.MakeClosure:
				// A closure that assigns the variable makes it change under us.
				for j, b := range r.Bindings {
					if b == cell && freeVarWritten(r.Fn.(*ssa.Function).FreeVars[j]) {
						return false
					}
				}
			}
		}
		return store == nil || (store.Block() != nil && dominates(store, load))
	case *ssa.FieldAddr:
		fn := load.Parent()
		if fn == nil {
			return false
		}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if s, ok := ins.(*ssa.Store); ok {
					if fa, ok := s.Addr.(*ssa.FieldAddr); ok && fa.Field == cell.Field && types.Identical(fa.X.Type(), cell.X.Type()) {
						return false
					}
				}
			}
		}
		return true
	}
	return false
}

// slot is guarded memory: the field or package variable an access reaches, and the guard path of
// its mutex at that access (unknown when the base has no path).
type slot struct {
	v      *types.Var
	mu     *types.Var
	muPath guardPath
	known  bool
	base   guardPath // the guarded object's path; root only for package variables
}

func (s slot) name() string {
	switch {
	case s.v.IsField():
		return "field " + s.v.Name()
	case s.v.Parent() == s.v.Pkg().Scope():
		return "package variable " + s.v.Name()
	}
	return "local variable " + s.v.Name()
}

// guardedAt reports whether v itself names guarded memory: a field address or field of a guarded
// field, a guarded package variable, or the cell of a guarded local.
func (st *state) guardedAt(v ssa.Value) (slot, bool) {
	switch x := v.(type) {
	case *ssa.FieldAddr:
		s, ok := derefStruct(x.X.Type())
		if !ok {
			return slot{}, false
		}
		return st.fieldSlot(s, x.Field, x.X)
	case *ssa.Field:
		s, ok := x.X.Type().Underlying().(*types.Struct)
		if !ok {
			return slot{}, false
		}
		return st.fieldSlot(s, x.Field, x.X)
	case *ssa.Global:
		gv, ok := x.Object().(*types.Var)
		if !ok {
			return slot{}, false
		}
		mu := st.ix.Guarded[gv]
		if mu == nil {
			return slot{}, false
		}
		out := slot{v: gv, mu: mu, base: guardPath{root: x}}
		if mg, ok := st.ssa.Pkg.Members[mu.Name()].(*ssa.Global); ok {
			out.muPath, out.known = guardPath{root: mg}, true
		}
		return out, true
	case *ssa.Alloc, *ssa.FreeVar:
		return st.localSlot(x)
	}
	return slot{}, false
}

func (st *state) fieldSlot(s *types.Struct, i int, base ssa.Value) (slot, bool) {
	f := s.Field(i).Origin()
	mu := st.ix.Guarded[f]
	if mu == nil {
		return slot{}, false
	}
	out := slot{v: f, mu: mu}
	j := -1
	for k := range s.NumFields() {
		if s.Field(k).Origin() == mu {
			j = k
		}
	}
	if bp, ok := pathOf(base); ok && j >= 0 {
		out.base = bp
		out.muPath = guardPath{bp.root, bp.steps + "." + strconv.Itoa(j)}
		out.known = true
	}
	return out, true
}

func derefStruct(t types.Type) (*types.Struct, bool) {
	p, ok := t.Underlying().(*types.Pointer)
	if !ok {
		return nil, false
	}
	s, ok := p.Elem().Underlying().(*types.Struct)
	return s, ok
}

// slotOf follows v back through derivations and loads to the nearest guarded memory it was read
// from or points into. Each step goes to an operand, which is defined strictly earlier, so the walk
// ends without a step limit; with one (it was 64) an access further than that from its guarded
// field read as unguarded memory and was not checked.
func (st *state) slotOf(v ssa.Value) (slot, bool) {
	for {
		if s, ok := st.guardedAt(v); ok {
			return s, true
		}
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
				return slot{}, false
			}
			v = x.X
		default:
			return slot{}, false
		}
	}
}

type lockOpKind uint8

const (
	opLock lockOpKind = 1 + iota
	opRLock
	opUnlock
	opTryLock
	opTryRLock
)

var lockOps = map[string]lockOpKind{
	"(*sync.Mutex).Lock":       opLock,
	"(*sync.Mutex).Unlock":     opUnlock,
	"(*sync.Mutex).TryLock":    opTryLock,
	"(*sync.RWMutex).Lock":     opLock,
	"(*sync.RWMutex).Unlock":   opUnlock,
	"(*sync.RWMutex).TryLock":  opTryLock,
	"(*sync.RWMutex).RLock":    opRLock,
	"(*sync.RWMutex).RUnlock":  opUnlock,
	"(*sync.RWMutex).TryRLock": opTryRLock,
}

// lockOp returns the mutex operation a call is and the mutex it is on: a call of a mutex method,
// or of a value that can be nothing but one bound to its mutex (f := mu.Unlock; f()). A call that
// may be one among other things is not one here: lockTargetOp answers for each thing it may be.
func lockOp(cc *ssa.CallCommon) (lockOpKind, ssa.Value) {
	if targets, all := lockCallTargetsAll(cc); all && len(targets) == 1 {
		return lockTargetOp(targets[0])
	}
	return 0, nil
}

// lockTarget is one function a call may run, with what it runs on: the call's arguments, and for
// a method value (s.m used as a value) the method as declared, with its receiver ahead of them.
type lockTarget struct {
	fn   *ssa.Function
	mc   *ssa.MakeClosure // the closure, for what it captures; nil for a function or a method value
	args []ssa.Value
	// entered: callTarget.entered, for a target that was read out of a variable.
	entered map[*ssa.Function][]*ssa.Call
}

// lockTargetOp returns the mutex operation a target is and the mutex it is on.
func lockTargetOp(t lockTarget) (lockOpKind, ssa.Value) {
	if len(t.args) == 0 {
		return 0, nil
	}
	obj, ok := t.fn.Object().(*types.Func)
	if !ok {
		return 0, nil
	}
	return lockOps[obj.FullName()], t.args[0]
}

// methodValue returns the method and the receiver a target is when it is a method value: the
// method as declared, which is what the summaries are kept for. An interface's method has none.
func methodValue(t callTarget) (*ssa.Function, ssa.Value) {
	if t.mc == nil || len(t.mc.Bindings) != 1 || !isBoundMethodWrapper(t.fn) {
		return nil, nil
	}
	method := methodBehind(t.fn)
	if method == nil {
		return nil, nil
	}
	// The receiver as the method takes it: a promoted method's receiver is a field of what the
	// value was made from, and no path from the one is a path from the other.
	if recv := method.Signature.Recv(); recv == nil || !types.Identical(recv.Type(), t.mc.Bindings[0].Type()) {
		return nil, nil
	}
	return method, t.mc.Bindings[0]
}

// release is a mutex a function unlocks, as a path from a parameter, free variable, or global.
// When prefix is true, any held lock whose path starts with steps on the same root may be unlocked.
type release struct {
	param  int
	free   int
	global *ssa.Global
	steps  string
	prefix bool
}

// guardState holds the per-package results the lock analysis shares between functions.
type guardState struct {
	releases map[*ssa.Function][]release
	acquires map[*ssa.Function][]acquire
	before   map[*ssa.Function]map[ssa.Instruction]lockset
	initOnly map[*ssa.Function]bool
	visiting map[*ssa.Function]bool
}

// checkGuarded runs SAFE009 over the package's functions.
func (st *state) checkGuarded(fns []*ssa.Function) {
	if len(st.ix.Guarded) == 0 && len(st.ix.Holds) == 0 {
		return
	}
	gs := st.ensureGuardSummaries(fns)
	for _, fn := range fns {
		if len(fn.Blocks) > 0 {
			st.locksOf(gs, fn)
		}
	}
	for _, fn := range fns {
		if len(fn.Blocks) > 0 {
			st.checkGuardedFunc(gs, fn)
		}
	}
}

func (st *state) hasLockOps(fns []*ssa.Function) bool {
	if len(st.ix.Holds) > 0 {
		return true
	}
	found := false
	for _, fn := range fns {
		forEachInstr(fn, func(ins ssa.Instruction) {
			if found {
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			// Any function the call may run that takes a lock.
			for _, t := range lockCallTargets(cc) {
				if k, _ := lockTargetOp(t); k == opLock || k == opRLock || k == opTryLock || k == opTryRLock {
					found = true
					return
				}
				if t.fn.Pkg != st.ssa.Pkg && len(st.summary(t.fn).acquires) > 0 {
					found = true
					return
				}
			}
		})
		if found {
			return true
		}
	}
	return false
}

func stepCount(steps string) int {
	n := 0
	for i := 0; i < len(steps); i++ {
		if steps[i] == '.' || steps[i] == '*' {
			n++
		}
	}
	return n
}

func truncateSteps(steps string, maxSteps int) string {
	n := 0
	for i := 0; i < len(steps); i++ {
		if steps[i] == '.' || steps[i] == '*' {
			if n == maxSteps {
				return steps[:i]
			}
			n++
		}
	}
	return steps
}

func singleStoredParam(fn *ssa.Function, cell *ssa.Alloc) int {
	if fn == nil || cell == nil || cell.Parent() != fn {
		return -1
	}
	refs := cell.Referrers()
	if refs == nil {
		return -1
	}
	var store *ssa.Store
	for _, r := range *refs {
		switch r := r.(type) {
		case *ssa.Store:
			if r.Addr == cell {
				if store != nil {
					return -1
				}
				store = r
			}
		case *ssa.MakeClosure:
			for j, b := range r.Bindings {
				if b == cell && freeVarWritten(r.Fn.(*ssa.Function).FreeVars[j]) {
					return -1
				}
			}
		}
	}
	if store == nil {
		return -1
	}
	for i, p := range fn.Params {
		if store.Val == p {
			return i
		}
	}
	return -1
}

func hasStepPrefix(steps, prefix string) bool {
	if prefix == "" || steps == prefix {
		return true
	}
	if !strings.HasPrefix(steps, prefix) {
		return false
	}
	next := steps[len(prefix)]
	return next == '.' || next == '*'
}

// releaseSummaries computes, for each function of the package, the mutexes it may leave
// unlocked: Unlock or RUnlock (deferred or not) on a parameter, free-var, or global path,
// directly or through calls. Recursive path growth is soundly widened to a prefix after a
// bounded step depth so recursive tree/graph walkers terminate in linear time.
func (st *state) releaseSummaries(fns []*ssa.Function) map[*ssa.Function][]release {
	const (
		maxReleaseSteps  = 8
		maxReleasesPerFn = 32
	)
	out := map[*ssa.Function][]release{}
	seen := map[*ssa.Function]map[release]bool{}
	inPkg := make(map[*ssa.Function]bool, len(fns))
	for _, fn := range fns {
		inPkg[fn] = true
	}

	paramIndex := func(fn *ssa.Function, v ssa.Value) int {
		for i, p := range fn.Params {
			if p == v {
				return i
			}
		}
		return -1
	}
	toRelease := func(fn *ssa.Function, p guardPath, prefix bool) (release, bool) {
		if i := paramIndex(fn, p.root); i >= 0 {
			return release{param: i, free: -1, steps: p.steps, prefix: prefix}, true
		}
		if alloc, ok := p.root.(*ssa.Alloc); ok && strings.HasPrefix(p.steps, "*") {
			if i := singleStoredParam(fn, alloc); i >= 0 {
				return release{param: i, free: -1, steps: p.steps[1:], prefix: prefix}, true
			}
		}
		if fv, ok := p.root.(*ssa.FreeVar); ok {
			if i := slices.Index(fn.FreeVars, fv); i >= 0 {
				return release{param: -1, free: i, steps: p.steps, prefix: prefix}, true
			}
		}
		if g, ok := p.root.(*ssa.Global); ok {
			return release{param: -1, free: -1, global: g, steps: p.steps, prefix: prefix}, true
		}
		return release{}, false
	}
	metrics := &releaseMetrics{}
	if observeRelease != nil {
		defer func() { observeRelease(st.pass.Pkg, *metrics) }()
	}
	add := func(fn *ssa.Function, r release) bool {
		metrics.work++
		if stepCount(r.steps) > maxReleaseSteps {
			r.steps = truncateSteps(r.steps, maxReleaseSteps)
			r.prefix = true
			metrics.stepCaps++
		}
		if len(out[fn]) >= maxReleasesPerFn {
			r.steps = ""
			r.prefix = true
			metrics.countCaps++
		}
		s := seen[fn]
		if s == nil {
			s = map[release]bool{}
			seen[fn] = s
		}
		if s[r] {
			return false
		}
		for _, cur := range out[fn] {
			if cur.prefix && cur.param == r.param && cur.free == r.free && cur.global == r.global && hasStepPrefix(r.steps, cur.steps) {
				return false
			}
		}
		s[r] = true
		out[fn] = append(out[fn], r)
		return true
	}

	type callEdge struct {
		callee *ssa.Function
		args   []ssa.Value
		mc     *ssa.MakeClosure
	}
	edgesOf := make(map[*ssa.Function][]callEdge, len(fns))
	callers := make(map[*ssa.Function][]*ssa.Function, len(fns))
	inQueue := make(map[*ssa.Function]bool, len(fns))
	var queue []*ssa.Function
	enqueue := func(fn *ssa.Function) {
		if !inQueue[fn] && st.holdsOf(fn) == nil {
			inQueue[fn] = true
			queue = append(queue, fn)
		}
	}

	for _, fn := range fns {
		if st.holdsOf(fn) != nil {
			continue // a //gosree:holds function must not release (SAFE007 checks it)
		}
		callerSeen := map[*ssa.Function]bool{}
		forEachInstr(fn, func(ins ssa.Instruction) {
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			// Every function the call may run: a release by any one of them is a release.
			for _, t := range lockCallTargets(cc) {
				if k, recv := lockTargetOp(t); k != 0 {
					if p, ok := pathOf(recv); ok && k == opUnlock {
						if rel, ok := toRelease(fn, p, false); ok {
							add(fn, rel)
						}
					}
					continue
				}
				callee := t.fn
				if !inPkg[callee] {
					continue
				}
				edgesOf[fn] = append(edgesOf[fn], callEdge{callee: callee, args: t.args, mc: t.mc})
				if !callerSeen[callee] {
					callerSeen[callee] = true
					callers[callee] = append(callers[callee], fn)
				}
			}
		})
	}

	propagate := func(fn *ssa.Function) bool {
		metrics.work++
		changed := false
		for _, e := range edgesOf[fn] {
			for _, r := range out[e.callee] {
				if r.global != nil {
					if add(fn, r) {
						changed = true
					}
					continue
				}
				var base ssa.Value
				switch {
				case r.param >= 0 && r.param < len(e.args):
					base = e.args[r.param]
				case r.free >= 0 && e.mc != nil && r.free < len(e.mc.Bindings):
					base = e.mc.Bindings[r.free]
				default:
					continue
				}
				if p, ok := pathOf(base); ok {
					if rel, ok := toRelease(fn, guardPath{p.root, p.steps + r.steps}, r.prefix); ok && add(fn, rel) {
						changed = true
					}
				}
			}
		}
		return changed
	}

	// Evaluate in bottom-up post-order first so arbitrarily deep non-recursive call DAGs
	// converge in a single pass, then run the monotone worklist to exact fixpoint on SCC cycles
	// (guaranteed finite because maxReleaseSteps and maxReleasesPerFn cap out[fn]).
	visited := make(map[*ssa.Function]bool, len(fns))
	postOrder := make([]*ssa.Function, 0, len(fns))
	var dfs func(fn *ssa.Function)
	dfs = func(fn *ssa.Function) {
		metrics.work++
		if visited[fn] {
			return
		}
		visited[fn] = true
		for _, e := range edgesOf[fn] {
			dfs(e.callee)
		}
		if st.holdsOf(fn) == nil {
			postOrder = append(postOrder, fn)
		}
	}
	for _, fn := range fns {
		dfs(fn)
	}
	for _, fn := range postOrder {
		propagate(fn)
		if len(out[fn]) > 0 {
			for _, caller := range callers[fn] {
				enqueue(caller)
			}
		}
	}

	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		delete(inQueue, fn)
		if propagate(fn) {
			for _, caller := range callers[fn] {
				enqueue(caller)
			}
		}
	}
	return out
}

// holdsOf returns the //gosree:holds mutexes of fn, or nil.
func (st *state) holdsOf(fn *ssa.Function) []*types.Var {
	obj, ok := fn.Object().(*types.Func)
	if !ok {
		return nil
	}
	return st.ix.Holds[obj.Origin()]
}

// holdsPaths maps fn's //gosree:holds mutexes to guard paths in fn (callee) or, given the call's
// arguments, in the caller.
func (st *state) holdsPaths(fn *ssa.Function, recv ssa.Value) []guardPath {
	var out []guardPath
	for _, mu := range st.holdsOf(fn) {
		if mu.IsField() {
			if recv == nil {
				out = append(out, guardPath{})
				continue
			}
			s, ok := derefStruct(recv.Type())
			if !ok {
				continue
			}
			bp, ok := pathOf(recv)
			if !ok {
				out = append(out, guardPath{}) // unknown: never held
				continue
			}
			for k := range s.NumFields() {
				if s.Field(k).Origin() == mu {
					out = append(out, guardPath{bp.root, bp.steps + "." + strconv.Itoa(k)})
				}
			}
			continue
		}
		if g, ok := st.ssa.Pkg.Members[mu.Name()].(*ssa.Global); ok {
			out = append(out, guardPath{root: g})
		}
	}
	return out
}

// holdsEntry is the lockset of fn's //gosree:holds mutexes.
func (st *state) holdsEntry(fn *ssa.Function) lockset {
	var recv ssa.Value
	if len(fn.Params) > 0 {
		recv = fn.Params[0]
	}
	entry := lockset{}
	for _, p := range st.holdsPaths(fn, recv) {
		entry[p] = lockWrite
	}
	return entry
}

// entryLocks is what fn may assume held on entry: its //gosree:holds mutexes, and for a closure called
// in place, what its caller holds at every such call.
func (st *state) entryLocks(gs *guardState, fn *ssa.Function) lockset {
	entry := st.holdsEntry(fn)
	parent := fn.Parent()
	if parent == nil || len(parent.Blocks) == 0 {
		return entry
	}
	var inherited lockset
	calls := 0
	for _, b := range parent.Blocks {
		for _, ins := range b.Instrs {
			mc, ok := ins.(*ssa.MakeClosure)
			if !ok || mc.Fn != fn {
				continue
			}
			for _, r := range *mc.Referrers() {
				if _, dbg := r.(*ssa.DebugRef); dbg {
					continue
				}
				c, ok := r.(*ssa.Call)
				if !ok || c.Call.Value != mc || usedAsArg(c, mc, -1) {
					return entry // not called in place
				}
				calls++
				at := st.locksOf(gs, parent)[c]
				inherited = meet(inherited, translate(at, mc))
			}
		}
	}
	if calls == 0 {
		return entry
	}
	for k, m := range inherited {
		entry[k] = max(entry[k], m)
	}
	return entry
}

// translate maps a caller's held guards into the closure mc builds: a guard rooted at a captured
// value is rooted at the matching free variable; package variables and uncaptured parent guards
// stay; the rest are mapped.
func translate(at lockset, mc *ssa.MakeClosure) lockset {
	out := lockset{}
	fn := mc.Fn.(*ssa.Function)
	for p, m := range at {
		if _, ok := p.root.(*ssa.Global); ok {
			out[p] = m
			continue
		}
		bound := false
		for j, b := range mc.Bindings {
			if b == p.root {
				out[guardPath{fn.FreeVars[j], p.steps}] = m
				bound = true
			}
		}
		if !bound {
			out[p] = m
		}
	}
	return out
}

// locksOf runs the must-hold analysis on fn and returns the held guards before each instruction.
func (st *state) locksOf(gs *guardState, fn *ssa.Function) map[ssa.Instruction]lockset {
	if r, ok := gs.before[fn]; ok {
		return r
	}
	if gs.visiting[fn] {
		return map[ssa.Instruction]lockset{}
	}
	gs.visiting[fn] = true
	defer delete(gs.visiting, fn)
	before := st.flow(gs, fn, st.entryLocks(gs, fn))
	gs.before[fn] = before
	return before
}

// flow runs the must-hold analysis on fn from the given entry locks and returns the held guards
// before each reachable instruction.
//
// The passes repeat until nothing changes, with no limit on their number. They end because a
// block's set, once it has one, is only ever replaced by its meet with another set, which can drop
// guards and weaken modes but never add: every change is one of finitely many. With a limit (it
// was 64) a block further than that from the entry kept no set, and what it accessed went unchecked.
func (st *state) flow(gs *guardState, fn *ssa.Function, entry lockset) map[ssa.Instruction]lockset {
	in := make([]lockset, len(fn.Blocks))
	in[0] = entry
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			if in[b.Index] == nil {
				continue
			}
			out := in[b.Index].clone()
			for _, ins := range b.Instrs {
				st.lockStep(gs, out, ins)
			}
			for si, succ := range b.Succs {
				edge := tryEdge(out, b, si)
				next := meet(in[succ.Index], edge)
				if in[succ.Index] == nil {
					next = edge.clone()
				}
				if !sameLockset(next, in[succ.Index]) {
					in[succ.Index] = next
					changed = true
				}
			}
		}
	}
	before := map[ssa.Instruction]lockset{}
	for _, b := range fn.Blocks {
		if in[b.Index] == nil {
			continue
		}
		cur := in[b.Index].clone()
		for _, ins := range b.Instrs {
			before[ins] = cur.clone()
			st.lockStep(gs, cur, ins)
		}
	}
	return before
}

// acquire is a mutex a function returns holding, as a path from one of its parameters (Param >= 0)
// or free variables (Free >= 0).
type acquire struct {
	Param, Free int
	Steps       string
	Mode        lockMode
}

// acquireSummaries computes, for each function of the package, the parameter and captured mutexes it
// holds at every return, after its deferred calls have run: a helper that locks for its caller. It is
// a must property, so a function that locks on one path only acquires nothing. The summaries start
// empty and grow, so every intermediate answer under-approximates what a call acquires, which can
// only over-report.
func (st *state) acquireSummaries(gs *guardState, fns []*ssa.Function) map[*ssa.Function][]acquire {
	const maxAcquireRounds = 16
	gs.acquires = map[*ssa.Function][]acquire{}
	inPkg := make(map[*ssa.Function]bool, len(fns))
	for _, fn := range fns {
		inPkg[fn] = true
	}

	calleesOf := make(map[*ssa.Function][]*ssa.Function, len(fns))
	callers := make(map[*ssa.Function][]*ssa.Function, len(fns))
	directSeed := make(map[*ssa.Function]bool, len(fns))
	inQueue := make(map[*ssa.Function]bool, len(fns))
	var queue []*ssa.Function
	enqueue := func(fn *ssa.Function) {
		if len(fn.Blocks) > 0 && !inQueue[fn] {
			inQueue[fn] = true
			queue = append(queue, fn)
		}
	}

	for _, fn := range fns {
		if len(fn.Blocks) == 0 {
			continue
		}
		if st.holdsOf(fn) != nil {
			directSeed[fn] = true
		}
		callerSeen := map[*ssa.Function]bool{}
		forEachInstr(fn, func(ins ssa.Instruction) {
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			// Every function the call may run. Whether what one of them takes is held after the
			// call is callStep's to say; here it only makes fn worth evaluating.
			for _, t := range lockCallTargets(cc) {
				if k, _ := lockTargetOp(t); k != 0 {
					if k != opUnlock {
						directSeed[fn] = true
					}
					continue
				}
				callee := t.fn
				if inPkg[callee] {
					if !callerSeen[callee] {
						callerSeen[callee] = true
						calleesOf[fn] = append(calleesOf[fn], callee)
						callers[callee] = append(callers[callee], fn)
					}
				} else if len(st.summary(callee).acquires) > 0 {
					directSeed[fn] = true
				}
			}
		})
	}

	// Evaluate in bottom-up post-order first so arbitrarily deep non-recursive lock-helper DAGs
	// converge in 1 pass, then run bounded worklist rounds for recursive SCC cycles.
	visited := make(map[*ssa.Function]bool, len(fns))
	needsEval := make(map[*ssa.Function]bool, len(fns))
	var postOrder []*ssa.Function
	var dfs func(fn *ssa.Function)
	dfs = func(fn *ssa.Function) {
		if visited[fn] {
			return
		}
		visited[fn] = true
		for _, c := range calleesOf[fn] {
			dfs(c)
			if needsEval[c] {
				needsEval[fn] = true
			}
		}
		if directSeed[fn] {
			needsEval[fn] = true
		}
		if len(fn.Blocks) > 0 {
			postOrder = append(postOrder, fn)
		}
	}
	for _, fn := range fns {
		dfs(fn)
	}
	for _, fn := range postOrder {
		if !needsEval[fn] && len(gs.acquires[fn]) == 0 {
			continue
		}
		next := st.acquiresOf(gs, fn)
		if !slices.Equal(next, gs.acquires[fn]) {
			gs.acquires[fn] = next
			for _, caller := range callers[fn] {
				needsEval[caller] = true
				enqueue(caller)
			}
		}
	}

	visits := map[*ssa.Function]int{}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		delete(inQueue, fn)
		if visits[fn] >= maxAcquireRounds {
			continue
		}
		visits[fn]++
		next := st.acquiresOf(gs, fn)
		if !slices.Equal(next, gs.acquires[fn]) {
			gs.acquires[fn] = next
			for _, caller := range callers[fn] {
				enqueue(caller)
			}
		}
	}
	return gs.acquires
}

// acquiresOf computes fn's acquires from the current summaries of its callees.
func (st *state) acquiresOf(gs *guardState, fn *ssa.Function) []acquire {
	entry := st.holdsEntry(fn)
	before := st.flow(gs, fn, entry.clone())
	var defers []*ssa.Defer
	forEachInstr(fn, func(ins ssa.Instruction) {
		if d, ok := ins.(*ssa.Defer); ok {
			defers = append(defers, d)
		}
	})
	var held lockset
	returns := 0
	forEachInstr(fn, func(ins ssa.Instruction) {
		ret, ok := ins.(*ssa.Return)
		if !ok {
			return
		}
		at, reached := before[ret]
		if !reached {
			return
		}
		returns++
		at = at.clone()
		// Deferred calls run at return, last first; one that may not have been registered on this
		// path can only release, so running it anyway under-approximates.
		for i := len(defers) - 1; i >= 0; i-- {
			st.callStep(gs, at, defers[i], defers[i].Common(), false)
		}
		held = meet(held, at)
	})
	if returns == 0 {
		return nil
	}
	var out []acquire
	for p, m := range held {
		if _, ok := entry[p]; ok {
			continue
		}
		a := acquire{Param: -1, Free: -1, Steps: p.steps, Mode: m}
		switch r := p.root.(type) {
		case *ssa.Parameter:
			a.Param = slices.Index(fn.Params, r)
		case *ssa.FreeVar:
			a.Free = slices.Index(fn.FreeVars, r)
		}
		if a.Param >= 0 || a.Free >= 0 {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y acquire) int {
		return cmp.Or(cmp.Compare(x.Param, y.Param), cmp.Compare(x.Free, y.Free), strings.Compare(x.Steps, y.Steps))
	})
	return out
}

// unwrapBoolCond strips boolean negations, single-store local loads, and == / != bool constant
// comparisons from a branch condition, returning the underlying SSA value and whether it is negated.
// Every step goes to a value defined strictly earlier (an operand, or what a dominating store put
// in the cell), so it runs to the end; stopping after 16 lost the TryLock behind a longer chain.
func unwrapBoolCond(cond ssa.Value) (ssa.Value, bool) {
	negated := false
	for {
		switch x := cond.(type) {
		case *ssa.UnOp:
			if x.Op == token.NOT {
				negated = !negated
				cond = x.X
				continue
			}
			if x.Op == token.MUL {
				if sv := uncapturedSingleStore(x); sv != nil {
					cond = sv
					continue
				}
			}
		case *ssa.BinOp:
			if x.Op == token.EQL || x.Op == token.NEQ {
				if c, ok := x.Y.(*ssa.Const); ok && c.Value != nil && c.Type().Underlying() == types.Typ[types.Bool] {
					val := constant.BoolVal(c.Value)
					if (x.Op == token.EQL && !val) || (x.Op == token.NEQ && val) {
						negated = !negated
					}
					cond = x.X
					continue
				}
				if c, ok := x.X.(*ssa.Const); ok && c.Value != nil && c.Type().Underlying() == types.Typ[types.Bool] {
					val := constant.BoolVal(c.Value)
					if (x.Op == token.EQL && !val) || (x.Op == token.NEQ && val) {
						negated = !negated
					}
					cond = x.Y
					continue
				}
			}
		}
		break
	}
	return cond, negated
}

// tryEdge adds the lock a TryLock took on the branch where it succeeded.
func tryEdge(out lockset, b *ssa.BasicBlock, succ int) lockset {
	iff, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
	if !ok {
		return out
	}
	cond, negated := unwrapBoolCond(iff.Cond)
	c, ok := cond.(*ssa.Call)
	if !ok {
		return out
	}
	k, recv := lockOp(c.Common())
	if k != opTryLock && k != opTryRLock {
		return out
	}
	if (succ == 0) == negated {
		return out
	}
	p, ok := pathOf(recv)
	if !ok {
		return out
	}
	edge := out.clone()
	if k == opTryLock {
		edge[p] = lockWrite
	} else {
		edge[p] = max(edge[p], lockRead)
	}
	return edge
}

// lockStep applies one instruction to the held set.
func (st *state) lockStep(gs *guardState, s lockset, ins ssa.Instruction) {
	c, ok := ins.(ssa.CallInstruction)
	if !ok {
		return
	}
	if _, deferred := ins.(*ssa.Defer); deferred {
		return // a deferred call runs at return
	}
	st.callStep(gs, s, ins, c.Common(), true)
}

// lockCallTargetsAll lists the functions a call may run, each as declared (not an instance), and
// whether that is all of them. A call of an interface method has none here.
func lockCallTargetsAll(cc *ssa.CallCommon) ([]lockTarget, bool) {
	if cc.IsInvoke() {
		return nil, false
	}
	var found []callTarget
	all := true
	if callee := cc.StaticCallee(); callee != nil {
		mc, _ := cc.Value.(*ssa.MakeClosure)
		found = []callTarget{{fn: callee, mc: mc}}
	} else {
		var rest []ssa.Value
		found, rest = callTargets(cc.Value)
		all = len(rest) == 0
	}
	targets := make([]lockTarget, 0, len(found))
	for _, f := range found {
		t := lockTarget{fn: f.fn, mc: f.mc, args: cc.Args, entered: f.entered}
		if method, recv := methodValue(f); method != nil {
			t = lockTarget{fn: method, args: append([]ssa.Value{recv}, cc.Args...), entered: f.entered}
		}
		if o := t.fn.Origin(); o != nil {
			t.fn = o
		}
		targets = append(targets, t)
	}
	return targets, all
}

// lockCallTargets is lockCallTargetsAll for a caller that asks what any one target may do.
func lockCallTargets(cc *ssa.CallCommon) []lockTarget {
	targets, _ := lockCallTargetsAll(cc)
	return targets
}

// callStep applies a call to the held set. With take unset it only releases: a deferred call run at
// return is applied that way, since it may not have been registered on every path.
func (st *state) callStep(gs *guardState, s lockset, ins ssa.Instruction, cc *ssa.CallCommon, take bool) {
	if k, recv := lockOp(cc); k != 0 {
		p, ok := pathOf(recv)
		if !ok {
			return
		}
		switch {
		case k == opLock && take:
			s[p] = lockWrite
		case k == opRLock && take:
			s[p] = max(s[p], lockRead)
		case k == opUnlock:
			delete(s, p)
		}
		return
	}
	if _, isGo := ins.(*ssa.Go); isGo {
		// The goroutine may unlock what it is handed or captures.
		for _, a := range cc.Args {
			killFor(s, a)
		}
		for _, mc := range closureCandidates(cc.Value) {
			for _, b := range mc.Bindings {
				killFor(s, b)
			}
		}
		return
	}
	// What the call may run. Each target gives its own releases, whatever the others are: a lock
	// one of them releases is released. A lock is held afterwards only if every one of them
	// returns holding it and the list is known to be all of them.
	targets, all := lockCallTargetsAll(cc)
	if !all && !cc.IsInvoke() && cc.StaticCallee() == nil {
		// A variable whose walk was cut short (restCut) may hold a closure made here over any
		// of this function's values, and that closure may unlock any of them: nothing is known
		// to be held after the call.
		if _, rest := callTargets(cc.Value); restCut(rest) {
			clear(s)
			return
		}
	}
	if len(targets) == 0 {
		// An interface method, or a called value nothing is known about: anything it receives
		// may be unlocked.
		if cc.IsInvoke() {
			killFor(s, cc.Value)
		}
		for _, a := range cc.Args {
			killFor(s, a)
		}
		return
	}
	dropRelease := func(target guardPath, prefix bool) {
		if !prefix {
			delete(s, target)
			return
		}
		for k := range s {
			if k.root == target.root && hasStepPrefix(k.steps, target.steps) {
				delete(s, k)
			}
		}
	}
	// What a target unlocks is named by the values it runs on: its arguments, its receiver, what
	// it captured. A target read out of a variable may have been made in another function, and
	// then those are that function's values. valuesHere brings them here. lost is set when one
	// could not be brought: the target then unlocks something that cannot be named, and nothing
	// is known to be held after the call.
	here := ins.Parent()
	lost := false
	releaseAt := func(t lockTarget, base ssa.Value, steps string, prefix bool) {
		paths, gone := valuesHere(here, base, steps, t.entered)
		lost = lost || gone
		for _, p := range paths {
			dropRelease(p, prefix)
		}
	}
	// handedOver is killFor for a value a target runs on.
	handedOver := func(t lockTarget, v ssa.Value) {
		for _, u := range argValues(v) {
			releaseAt(t, u, "", true)
		}
	}
	// held is what every target returns holding, translated through what the target runs on, or
	// for a closure through its bindings (as translate does for entry locks).
	var held lockset
	for _, t := range targets {
		got := lockset{}
		k, recv := lockTargetOp(t)
		switch {
		case k != 0:
			// A mutex method bound to its mutex.
			if k == opUnlock {
				releaseAt(t, recv, "", false)
			} else if p, ok := pathOf(recv); ok {
				switch k {
				case opLock:
					got[p] = lockWrite
				case opRLock:
					got[p] = lockRead
				}
			}
		case t.fn.Pkg != st.ssa.Pkg:
			// A function of another package: anything it receives or captures may be unlocked,
			// and it takes what its summary says.
			for _, a := range t.args {
				handedOver(t, a)
			}
			if t.mc != nil {
				for _, b := range t.mc.Bindings {
					handedOver(t, b)
				}
			}
			for _, a := range st.summary(t.fn).acquires {
				if a.Param >= 0 && a.Param < len(t.args) {
					if p, ok := pathOf(t.args[a.Param]); ok {
						k := guardPath{p.root, p.steps + a.Steps}
						got[k] = max(got[k], a.Mode)
					}
				}
			}
		case st.holdsOf(t.fn) == nil:
			// A function of this package, by its summaries. A //gosree:holds function neither
			// releases nor takes: it leaves got empty.
			for _, r := range gs.releases[t.fn] {
				if r.global != nil {
					dropRelease(guardPath{r.global, r.steps}, r.prefix)
					continue
				}
				var base ssa.Value
				switch {
				case r.param >= 0 && r.param < len(t.args):
					base = t.args[r.param]
				case r.free >= 0 && t.mc != nil && r.free < len(t.mc.Bindings):
					base = t.mc.Bindings[r.free]
				default:
					continue
				}
				releaseAt(t, base, r.steps, r.prefix)
			}
			for _, a := range gs.acquires[t.fn] {
				var base ssa.Value
				switch {
				case a.Param >= 0 && a.Param < len(t.args):
					base = t.args[a.Param]
				case a.Free >= 0 && t.mc != nil && a.Free < len(t.mc.Bindings):
					base = t.mc.Bindings[a.Free]
				default:
					continue
				}
				if p, ok := pathOf(base); ok {
					k := guardPath{p.root, p.steps + a.Steps}
					got[k] = max(got[k], a.Mode)
				}
			}
		}
		if held == nil {
			held = got
			continue
		}
		for k, m := range held {
			if g, ok := got[k]; ok {
				held[k] = min(m, g)
			} else {
				delete(held, k)
			}
		}
	}
	if lost {
		clear(s)
		return
	}
	if !all {
		// Something this did not find may run as well: it may unlock what it is handed, as a
		// call that is not known may, and nothing the others take can be counted on.
		for _, a := range cc.Args {
			killFor(s, a)
		}
		return
	}
	if !take {
		return
	}
	for k, m := range held {
		s[k] = max(s[k], m)
	}
}

// valuesHere gives the guard paths, in the function here, of what is steps on from a value v that
// a call's target runs on, and whether v is lost: a value of another function that could not be
// brought here.
//
// A target made in here runs on here's values and v's own path is the answer. A target read out
// of a variable may have been stored by another function, and v is then a value of that
// function. It is brought here by its root:
//
//   - a parameter of a function the variable's address was passed to is the argument at each
//     call the address went in by (entered), which is a value of the function that makes the
//     call, brought here in turn;
//   - a variable of that function that holds one of its parameters and nothing else (the cell a
//     closure made there captured) is that parameter;
//   - a captured variable of a closure is what the closure was made with, in the function that
//     made it.
//
// Anything else is lost. A value of here with no path is not: that is as it was, and such a
// value is left out by whoever asks.
//
// Each path is brought once (seen). A helper that calls itself with the variable's address is
// entered by its own call too, where its parameter is its own parameter again: that adds nothing
// and is where the walk ends.
func valuesHere(here *ssa.Function, v ssa.Value, steps string, entered map[*ssa.Function][]*ssa.Call) (paths []guardPath, lost bool) {
	p, ok := pathOf(v)
	if !ok {
		from := functionOf(v)
		return nil, from != nil && from != here
	}
	return rootHere(here, guardPath{p.root, p.steps + steps}, entered, &rootSeen{paths: map[guardPath]bool{}, perRoot: map[ssa.Value]int{}})
}

// rootSeen is what one valuesHere walk has brought: each path once, and how many distinct paths
// it met for each root. A helper that calls itself on a field of its parameter is entered with a
// longer path every time, so no path repeats; past rootPathLimit paths for one root the value is
// lost (reported as unknown) instead of followed without end.
type rootSeen struct {
	paths   map[guardPath]bool
	perRoot map[ssa.Value]int
}

const rootPathLimit = 8

func rootHere(here *ssa.Function, p guardPath, entered map[*ssa.Function][]*ssa.Call, seen *rootSeen) (paths []guardPath, lost bool) {
	from := functionOf(p.root)
	if from == nil || from == here {
		return []guardPath{p}, false
	}
	if seen.paths[p] {
		return nil, false
	}
	if seen.perRoot[p.root] >= rootPathLimit {
		return nil, true
	}
	seen.paths[p] = true
	seen.perRoot[p.root]++
	bring := func(v ssa.Value, steps string) {
		vp, ok := pathOf(v)
		if !ok {
			lost = true
			return
		}
		sub, gone := rootHere(here, guardPath{vp.root, vp.steps + steps}, entered, seen)
		paths = append(paths, sub...)
		lost = lost || gone
	}
	switch root := p.root.(type) {
	case *ssa.Parameter:
		i := slices.Index(from.Params, root)
		calls := entered[canonicalBodyFn(from)]
		if i < 0 || len(calls) == 0 {
			return nil, true
		}
		for _, call := range calls {
			if i >= len(call.Call.Args) {
				return nil, true
			}
			bring(call.Call.Args[i], p.steps)
		}
		return paths, lost
	case *ssa.Alloc:
		if i := singleStoredParam(from, root); i >= 0 && strings.HasPrefix(p.steps, "*") {
			return rootHere(here, guardPath{from.Params[i], p.steps[1:]}, entered, seen)
		}
	case *ssa.FreeVar:
		j := slices.Index(from.FreeVars, root)
		made := false
		if parent := from.Parent(); parent != nil && j >= 0 {
			forEachInstr(parent, func(ins ssa.Instruction) {
				if mc, ok := ins.(*ssa.MakeClosure); ok && mc.Fn == ssa.Value(from) && j < len(mc.Bindings) {
					made = true
					bring(mc.Bindings[j], p.steps)
				}
			})
		}
		return paths, lost || !made
	}
	return nil, true
}

// functionOf is the function a value belongs to, or nil for one that belongs to none (a package
// variable, a function, a constant).
func functionOf(v ssa.Value) *ssa.Function {
	switch x := v.(type) {
	case *ssa.Parameter:
		return x.Parent()
	case *ssa.FreeVar:
		return x.Parent()
	case ssa.Instruction:
		return x.Parent()
	}
	return nil
}

// killFor drops every held guard inside the object v points to (or v itself, for &mu). It looks
// through interface boxing and into the slice a variadic call builds, where fmt.Println(c) puts c.
func killFor(s lockset, v ssa.Value) {
	for _, u := range argValues(v) {
		p, ok := pathOf(u)
		if !ok {
			continue
		}
		for k := range s {
			if k.root == p.root && hasStepPrefix(k.steps, p.steps) {
				delete(s, k)
			}
		}
	}
}

// argValues returns what an argument carries: v itself without interface conversions, or the values
// stored into a variadic argument slice. Conversions are followed to the end: each step goes to an
// operand, defined strictly earlier. Stopping after 8 left a lock handed over through more
// conversions than that looking as if it were still held.
func argValues(v ssa.Value) []ssa.Value {
	for {
		switch x := v.(type) {
		case *ssa.MakeInterface:
			v = x.X
			continue
		case *ssa.ChangeInterface:
			v = x.X
			continue
		case *ssa.Slice:
			a, ok := x.X.(*ssa.Alloc)
			if !ok || a.Comment != "varargs" {
				return []ssa.Value{v}
			}
			var out []ssa.Value
			for _, r := range *a.Referrers() {
				ia, ok := r.(*ssa.IndexAddr)
				if !ok {
					continue
				}
				for _, rr := range *ia.Referrers() {
					if st, ok := rr.(*ssa.Store); ok && st.Addr == ia {
						out = append(out, argValues(st.Val)...)
					}
				}
			}
			return out
		}
		return []ssa.Value{v}
	}
}

// checkGuardedFunc reports the SAFE009 findings of fn, one per line and guarded variable.
func (st *state) checkGuardedFunc(gs *guardState, fn *ssa.Function) {
	before := st.locksOf(gs, fn)
	type key struct {
		line int
		file string
		v    *types.Var
	}
	type finding struct {
		pos   token.Pos
		s     slot
		write bool
	}
	found := map[key]*finding{}
	var order []key
	note := func(ins ssa.Instruction, s slot, write bool) {
		pos := posOf(ins, ins)
		p := st.pass.Fset.Position(pos)
		k := key{p.Line, p.Filename, s.v}
		if f, ok := found[k]; ok {
			f.write = f.write || write
			return
		}
		found[k] = &finding{pos, s, write}
		order = append(order, k)
	}
	held := func(ins ssa.Instruction, s slot, need lockMode) bool {
		if !s.known {
			return false
		}
		return before[ins][s.muPath] >= need
	}
	holdsOwn := st.holdsPaths(fn, firstParam(fn))
	forEachInstr(fn, func(ins ssa.Instruction) {
		if _, ok := before[ins]; !ok {
			return // unreachable
		}
		// Releasing a //gosree:holds mutex inside the helper breaks its contract.
		if ci, ok := ins.(ssa.CallInstruction); ok {
			cc := ci.Common()
			// Any function the call may run that unlocks it. One report for the call.
			unlocks := func(t lockTarget) (guardPath, bool) {
				if k, recv := lockTargetOp(t); k != 0 {
					if p, ok := pathOf(recv); ok && k == opUnlock && slices.Contains(holdsOwn, p) {
						return p, true
					}
					return guardPath{}, false
				}
				for _, rel := range gs.releases[t.fn] {
					var p guardPath
					var ok bool
					if rel.param >= 0 && rel.param < len(t.args) {
						p, ok = pathOf(t.args[rel.param])
						p.steps += rel.steps
					} else if rel.free >= 0 && t.mc != nil && rel.free < len(t.mc.Bindings) {
						p, ok = pathOf(t.mc.Bindings[rel.free])
						p.steps += rel.steps
					} else if rel.global != nil {
						p, ok = guardPath{root: rel.global, steps: rel.steps}, true
					}
					if !ok {
						continue
					}
					if !rel.prefix {
						if slices.Contains(holdsOwn, p) {
							return p, true
						}
						continue
					}
					for _, hp := range holdsOwn {
						if hp.root == p.root && hasStepPrefix(hp.steps, p.steps) {
							return hp, true
						}
					}
				}
				return guardPath{}, false
			}
			if len(holdsOwn) > 0 {
				for _, t := range lockCallTargets(cc) {
					if matched, hit := unlocks(t); hit {
						st.report(ci.Pos(), annot.CategoryMalformed, fmt.Sprintf(
							"%s is //gosree:holds %s but unlocks it here", fn.Name(), muName(st.holdsOf(fn), holdsOwn, matched)))
						break
					}
				}
			}
		}
		// Accesses.
		if w, ok := writeOf(ins); ok {
			if s, ok := st.slotOf(w.addr); ok && !st.guardExempt(gs, fn, ins, s) && !held(ins, s, lockWrite) {
				note(ins, s, true)
			}
		}
		if u, ok := ins.(*ssa.UnOp); ok && u.Op == token.MUL {
			if s, ok := st.slotOf(u.X); ok && !st.guardExempt(gs, fn, ins, s) && !held(ins, s, lockRead) {
				note(ins, s, false)
			}
		}
		// //gosree:holds helpers need their mutex held at the call.
		if ci, ok := ins.(ssa.CallInstruction); ok && !ci.Common().IsInvoke() {
			cc := ci.Common()
			if callee := cc.StaticCallee(); callee != nil && st.holdsOf(callee) != nil {
				var recv ssa.Value
				if len(cc.Args) > 0 {
					recv = cc.Args[0]
				}
				for i, p := range st.holdsPaths(callee, recv) {
					isHeld := false
					switch ins.(type) {
					case *ssa.Call:
						isHeld = p.root != nil && before[ins][p] >= lockWrite
					case *ssa.Go:
						isHeld = false
					case *ssa.Defer:
						if p.root != nil {
							isHeld = true
							hasRet := false
							seenThisDefer := false
							simSet := lockset{p: lockWrite}
							for _, b := range fn.Blocks {
								for _, bi := range b.Instrs {
									if bi == ins {
										seenThisDefer = true
										continue
									}
									if seenThisDefer {
										if d2, ok := bi.(*ssa.Defer); ok {
											st.callStep(gs, simSet, d2, d2.Common(), false)
											if simSet[p] < lockWrite {
												isHeld = false
											}
										}
									}
									if _, isRet := bi.(*ssa.Return); isRet {
										if _, reach := before[bi]; reach {
											hasRet = true
											if before[bi][p] < lockWrite {
												isHeld = false
											}
										}
									}
								}
							}
							if !hasRet {
								isHeld = false
							}
						}
					}
					if !isHeld {
						mu := st.holdsOf(callee)[min(i, len(st.holdsOf(callee))-1)]
						st.report(ci.Pos(), GuardViolation, fmt.Sprintf(
							"%s requires %s to be held (//gosree:holds %s), but it is not held here",
							callee.Name(), mu.Name(), mu.Name()))
					}
				}
			}
		}
		st.checkLeaves(ins)
	})
	for _, k := range order {
		f := found[k]
		verb := "read"
		if f.write {
			verb = "written"
		}
		where := "the function"
		if fn.Parent() == nil {
			where = fn.Name()
		}
		st.report(f.pos, GuardViolation, fmt.Sprintf(
			"%s is guarded by %s but is %s here without holding it (lock %s, or mark %s //gosree:holds %s)",
			f.s.name(), f.s.mu.Name(), verb, f.s.mu.Name(), where, f.s.mu.Name()))
	}
}

func firstParam(fn *ssa.Function) ssa.Value {
	if len(fn.Params) > 0 {
		return fn.Params[0]
	}
	return nil
}

func muName(mus []*types.Var, paths []guardPath, p guardPath) string {
	for i, q := range paths {
		if q == p && i < len(mus) {
			return mus[i].Name()
		}
	}
	return "its mutex"
}

// guardExempt reports accesses that need no lock: a guarded package variable during
// initialisation, a guarded local before anything can share it, and an object this function
// allocated that nothing else can see yet (a constructor filling it in before it is returned, stored
// or moved).
func (st *state) guardExempt(gs *guardState, fn *ssa.Function, ins ssa.Instruction, s slot) bool {
	if _, ok := s.base.root.(*ssa.Global); ok && !s.v.IsField() {
		return gs.initOnly[fn]
	}
	if lg, ok := st.localGuards[s.base.root]; ok && lg.v == s.v {
		// A guarded local needs no lock in its own function until a closure or pointer can share it.
		a, ok := s.base.root.(*ssa.Alloc)
		return ok && a.Parent() == fn && !sharedBefore(a, ins)
	}
	if !s.known {
		return false
	}
	root, ok := s.base.root.(*ssa.Alloc)
	if !ok || root.Parent() != fn {
		return false
	}
	// A store inside the object's own composite literal runs before anything can see the object.
	if root.Comment == "complit" {
		pos := ins.Pos()
		if store, ok := ins.(*ssa.Store); ok && !pos.IsValid() {
			pos = store.Addr.Pos()
		}
		if pos.IsValid() && st.inComplit(root, pos) {
			return true
		}
	}
	g := st.graphOf(fn)
	n := g.val(root)
	if n == nil {
		return false
	}
	cur := n
	for i := 0; i < len(s.base.steps); i++ {
		if s.base.steps[i] != '*' {
			continue
		}
		if cur.pointee == nil {
			return false
		}
		cur = cur.pointee.find()
		for _, e := range cur.events {
			if e.kind == evForeign {
				return false
			}
		}
	}
	for c := range g.region(cur) {
		for _, e := range c.events {
			switch e.kind {
			case evEscape, evTransfer, evReturn:
				if e.instr == nil || e.instr == ins || reachableAfter(e.instr)(ins) {
					return false
				}
			}
		}
	}
	return true
}

// checkLeaves reports non-shareable values read from guarded memory that leave the lock.
func (st *state) checkLeaves(ins ssa.Instruction) {
	leaves := func(v ssa.Value, how string) {
		if v == nil || st.cls.Shareable(v.Type()) {
			return
		}
		s, ok := st.slotOf(v)
		if !ok {
			return
		}
		st.report(posOf(ins, ins), GuardViolation, fmt.Sprintf(
			"%s is guarded by %s, but a value read from it leaves its lock here: %s",
			s.name(), s.mu.Name(), how))
	}
	switch x := ins.(type) {
	case *ssa.Return:
		for _, r := range x.Results {
			leaves(r, "returned")
		}
	case *ssa.Send:
		leaves(x.X, "sent on a channel")
	case *ssa.Store:
		src, ok := st.slotOf(x.Val)
		if !ok || st.cls.Shareable(x.Val.Type()) {
			return
		}
		if a, ok := x.Addr.(*ssa.Alloc); ok && !a.Heap {
			// With a defer, `return v` stores v into the result slot on that return's path and the
			// function returns a load of it: the store is the return. Any other local slot keeps the
			// value in this function.
			if isResultSlot(a) {
				leaves(x.Val, "returned")
			}
			return
		}
		if dst, ok := st.slotOf(x.Addr); ok && dst.known && src.known && dst.muPath == src.muPath {
			return // moved within memory the same mutex guards
		}
		leaves(x.Val, "stored outside the guarded object")
	case *ssa.Go:
		for _, a := range x.Call.Args {
			leaves(a, "passed to a goroutine")
		}
	case *ssa.Call:
		if sp, ok := spawnOf(x); ok && sp.via != "" {
			leaves(sp.fn, "passed to "+sp.via+", which runs it on another goroutine")
		}
		callee := x.Call.StaticCallee()
		if callee == nil || x.Call.IsInvoke() {
			return
		}
		if _, ok := lockOps[funcName(callee)]; ok {
			return
		}
		sum := st.summary(callee)
		for i, a := range x.Call.Args {
			if sum.leaks(i) {
				leaves(a, "passed to "+calleeName(callee)+", which may retain it")
			}
		}
	}
}

// isResultSlot reports whether a local slot only carries a function result: it is stored to, and
// every load of it is returned.
func isResultSlot(a *ssa.Alloc) bool {
	loads := 0
	for _, r := range *a.Referrers() {
		switch r := r.(type) {
		case *ssa.Store:
			if r.Addr != a {
				return false
			}
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return false
			}
			for _, rr := range *r.Referrers() {
				switch rr.(type) {
				case *ssa.Return, *ssa.DebugRef:
				default:
					return false
				}
			}
			loads++
		case *ssa.DebugRef:
		default:
			return false
		}
	}
	return loads > 0
}

func funcName(fn *ssa.Function) string {
	if obj, ok := fn.Object().(*types.Func); ok {
		return obj.FullName()
	}
	return ""
}
