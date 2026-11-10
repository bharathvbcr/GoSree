package ownership

import (
	"cmp"
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gocap/internal/annot"
)

// SAFE009: memory annotated //safe:guarded_by is read or written only while its mutex is held, a
// //safe:holds helper is called only with its mutex held, and nothing non-shareable read from guarded
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
// place starts with its caller's locks; any other function starts with none, plus its //safe:holds.

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
func pathOf(v ssa.Value) (guardPath, bool) {
	var steps []string
	for range 32 {
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
			steps = append(steps, "*")
			v = x.X
		default:
			slices.Reverse(steps)
			return guardPath{v, strings.Join(steps, "")}, true
		}
	}
	return guardPath{}, false
}

// stableLoad reports whether a load reads a variable cell that holds one value for the whole time
// the load can see it: a captured variable the closure never assigns, or a local cell stored once,
// before the load.
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
// from or points into.
func (st *state) slotOf(v ssa.Value) (slot, bool) {
	for range 64 {
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
	return slot{}, false
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

// lockOp returns the mutex operation a call performs and the mutex it is called on.
func lockOp(cc *ssa.CallCommon) (lockOpKind, ssa.Value) {
	if cc.IsInvoke() || len(cc.Args) == 0 {
		return 0, nil
	}
	callee := cc.StaticCallee()
	if callee == nil {
		return 0, nil
	}
	obj, ok := callee.Object().(*types.Func)
	if !ok {
		return 0, nil
	}
	return lockOps[obj.FullName()], cc.Args[0]
}

// release is a mutex a function unlocks, as a path from one of its parameters.
type release struct {
	param int
	steps string
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
	gs := &guardState{
		releases: st.releaseSummaries(fns),
		before:   map[*ssa.Function]map[ssa.Instruction]lockset{},
		initOnly: st.initFuncs(fns),
		visiting: map[*ssa.Function]bool{},
	}
	st.acquireSummaries(gs, fns)
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

// releaseSummaries computes, for each function of the package, the parameter mutexes it may leave
// unlocked: Unlock or RUnlock (deferred or not) on a parameter path, directly or through calls.
func (st *state) releaseSummaries(fns []*ssa.Function) map[*ssa.Function][]release {
	out := map[*ssa.Function][]release{}
	paramIndex := func(fn *ssa.Function, v ssa.Value) int {
		for i, p := range fn.Params {
			if p == v {
				return i
			}
		}
		return -1
	}
	add := func(fn *ssa.Function, r release) bool {
		if slices.Contains(out[fn], r) {
			return false
		}
		out[fn] = append(out[fn], r)
		return true
	}
	for changed, rounds := true, 0; changed && rounds < 16; rounds++ {
		changed = false
		for _, fn := range fns {
			if st.holdsOf(fn) != nil {
				continue // a //safe:holds function must not release (SAFE007 checks it)
			}
			forEachInstr(fn, func(ins ssa.Instruction) {
				c, ok := ins.(ssa.CallInstruction)
				if !ok {
					return
				}
				cc := c.Common()
				if k, recv := lockOp(cc); k == opUnlock {
					if p, ok := pathOf(recv); ok {
						if i := paramIndex(fn, p.root); i >= 0 && add(fn, release{i, p.steps}) {
							changed = true
						}
					}
					return
				}
				callee := cc.StaticCallee()
				if callee == nil || callee.Pkg != st.ssa.Pkg {
					return
				}
				for _, r := range out[callee] {
					if r.param >= len(cc.Args) {
						continue
					}
					if p, ok := pathOf(cc.Args[r.param]); ok {
						if i := paramIndex(fn, p.root); i >= 0 && add(fn, release{i, p.steps + r.steps}) {
							changed = true
						}
					}
				}
			})
		}
	}
	return out
}

// holdsOf returns the //safe:holds mutexes of fn, or nil.
func (st *state) holdsOf(fn *ssa.Function) []*types.Var {
	obj, ok := fn.Object().(*types.Func)
	if !ok {
		return nil
	}
	return st.ix.Holds[obj.Origin()]
}

// holdsPaths maps fn's //safe:holds mutexes to guard paths in fn (callee) or, given the call's
// arguments, in the caller.
func (st *state) holdsPaths(fn *ssa.Function, recv ssa.Value) []guardPath {
	var out []guardPath
	for _, mu := range st.holdsOf(fn) {
		if mu.IsField() {
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

// holdsEntry is the lockset of fn's //safe:holds mutexes.
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

// entryLocks is what fn may assume held on entry: its //safe:holds mutexes, and for a closure called
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
// value is rooted at the matching free variable; package variables stay; the rest are dropped.
func translate(at lockset, mc *ssa.MakeClosure) lockset {
	out := lockset{}
	fn := mc.Fn.(*ssa.Function)
	for p, m := range at {
		if _, ok := p.root.(*ssa.Global); ok {
			out[p] = m
			continue
		}
		for j, b := range mc.Bindings {
			if b == p.root {
				out[guardPath{fn.FreeVars[j], p.steps}] = m
			}
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
func (st *state) flow(gs *guardState, fn *ssa.Function, entry lockset) map[ssa.Instruction]lockset {
	in := make([]lockset, len(fn.Blocks))
	in[0] = entry
	for changed, rounds := true, 0; changed && rounds < 64; rounds++ {
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

// acquire is a mutex a function returns holding, as a path from one of its parameters (param >= 0)
// or free variables (free >= 0).
type acquire struct {
	param, free int
	steps       string
	mode        lockMode
}

// acquireSummaries computes, for each function of the package, the parameter and captured mutexes it
// holds at every return, after its deferred calls have run: a helper that locks for its caller. It is
// a must property, so a function that locks on one path only acquires nothing. The summaries start
// empty and grow, so every intermediate answer under-approximates what a call acquires, which can
// only over-report.
func (st *state) acquireSummaries(gs *guardState, fns []*ssa.Function) map[*ssa.Function][]acquire {
	gs.acquires = map[*ssa.Function][]acquire{}
	for changed, rounds := true, 0; changed && rounds < 16; rounds++ {
		changed = false
		for _, fn := range fns {
			if len(fn.Blocks) == 0 {
				continue
			}
			next := st.acquiresOf(gs, fn)
			if !slices.Equal(next, gs.acquires[fn]) {
				gs.acquires[fn] = next
				changed = true
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
		a := acquire{param: -1, free: -1, steps: p.steps, mode: m}
		switch r := p.root.(type) {
		case *ssa.Parameter:
			a.param = slices.Index(fn.Params, r)
		case *ssa.FreeVar:
			a.free = slices.Index(fn.FreeVars, r)
		}
		if a.param >= 0 || a.free >= 0 {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y acquire) int {
		return cmp.Or(cmp.Compare(x.param, y.param), cmp.Compare(x.free, y.free), strings.Compare(x.steps, y.steps))
	})
	return out
}

// tryEdge adds the lock a TryLock took on the branch where it succeeded.
func tryEdge(out lockset, b *ssa.BasicBlock, succ int) lockset {
	iff, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
	if !ok {
		return out
	}
	cond, negated := iff.Cond, false
	if u, ok := cond.(*ssa.UnOp); ok && u.Op == token.NOT {
		cond, negated = u.X, true
	}
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
		// The goroutine may unlock what it is handed.
		for _, a := range cc.Args {
			killFor(s, a)
		}
		return
	}
	if callee := cc.StaticCallee(); callee != nil && !cc.IsInvoke() && callee.Pkg == st.ssa.Pkg {
		if st.holdsOf(callee) != nil {
			return
		}
		for _, r := range gs.releases[callee] {
			if r.param < len(cc.Args) {
				if p, ok := pathOf(cc.Args[r.param]); ok {
					delete(s, guardPath{p.root, p.steps + r.steps})
				}
			}
		}
		if !take {
			return
		}
		// What the callee returns holding, translated through the arguments, or for a closure built
		// in place, through its bindings (as translate does for entry locks).
		for _, a := range gs.acquires[callee] {
			var base ssa.Value
			switch mc, closure := cc.Value.(*ssa.MakeClosure); {
			case a.param >= 0 && a.param < len(cc.Args):
				base = cc.Args[a.param]
			case a.free >= 0 && closure && a.free < len(mc.Bindings):
				base = mc.Bindings[a.free]
			default:
				continue
			}
			if p, ok := pathOf(base); ok {
				k := guardPath{p.root, p.steps + a.steps}
				s[k] = max(s[k], a.mode)
			}
		}
		return
	}
	// Another package, an interface method or a dynamic call: anything it receives may be unlocked.
	if cc.IsInvoke() {
		killFor(s, cc.Value)
	}
	for _, a := range cc.Args {
		killFor(s, a)
	}
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
			if k.root == p.root && strings.HasPrefix(k.steps, p.steps) {
				delete(s, k)
			}
		}
	}
}

// argValues returns what an argument carries: v itself without interface conversions, or the values
// stored into a variadic argument slice.
func argValues(v ssa.Value) []ssa.Value {
	for range 8 {
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
	return []ssa.Value{v}
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
		// Releasing a //safe:holds mutex inside the helper breaks its contract.
		if c, ok := ins.(*ssa.Call); ok {
			if k, recv := lockOp(c.Common()); k == opUnlock {
				if p, ok := pathOf(recv); ok && slices.Contains(holdsOwn, p) {
					st.report(c.Pos(), annot.CategoryMalformed, fmt.Sprintf(
						"%s is //safe:holds %s but unlocks it here", fn.Name(), muName(st.holdsOf(fn), holdsOwn, p)))
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
		// //safe:holds helpers need their mutex held at the call.
		if c, ok := ins.(*ssa.Call); ok && !c.Call.IsInvoke() {
			if callee := c.Call.StaticCallee(); callee != nil && st.holdsOf(callee) != nil {
				var recv ssa.Value
				if len(c.Call.Args) > 0 {
					recv = c.Call.Args[0]
				}
				for i, p := range st.holdsPaths(callee, recv) {
					if p.root == nil || before[ins][p] < lockWrite {
						mu := st.holdsOf(callee)[min(i, len(st.holdsOf(callee))-1)]
						st.report(c.Pos(), GuardViolation, fmt.Sprintf(
							"%s requires %s to be held (//safe:holds %s), but it is not held here",
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
			"%s is guarded by %s but is %s here without holding it (lock %s, or mark %s //safe:holds %s)",
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
	for c := range g.region(n) {
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
