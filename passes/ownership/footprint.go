package ownership

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Access footprints: whether a use after a move to a goroutine can race with that goroutine.
//
// A use after a move (SAFE003) is reported because the sender may touch memory the goroutine now
// works on. It races only when the two touch the same location, at least one of them writes, neither
// is ordered before the other, and no mutex they both hold excludes them (the Go memory model). The
// alias graph cannot tell that: it is field-insensitive and flow-insensitive, and has no notion of a
// read. This file decides it for the moves it can follow completely, and gives up — leaving the use
// reported — everywhere else.
//
// A footprint is the set of accesses a piece of code makes to memory reachable from a root: a fresh
// allocation of the parent (an Alloc, make of a slice or a map). Each access names a place — the root
// and a path of field, constant-index, any-index and dereference steps, the way rustc's MIR names a
// place by a local and its projections — and whether it reads, writes or operates atomically, and the
// mutexes held at it. Two accesses conflict when their places overlap, one writes, they are not both
// atomic, and no mutex is held by both with at least one holding it exclusively (RLock and RLock do
// not exclude a write).
//
// The goroutine's footprint is collected from its body, from the captured variables or the arguments
// that carry the moved memory. The sender's is collected from the roots forward, over every
// instruction reachable after the move that does not pass a join (forkjoin.go). A move is benign when
// no access of the one conflicts with an access of the other, and when every value of the moved region
// with a use after the move is one the forward walk followed: a use of memory reached another way
// stays reported.
//
// The walk fails closed. It stops and the move is left as it was when the memory leaves what it can
// follow: a pointer stored into memory, sent, returned, handed to an interface method, a dynamic call
// or a callee that may keep it, converted to unsafe.Pointer, used by an instruction it does not model,
// or when the walk passes its budget.

type stepKind uint8

const (
	stField stepKind = iota
	stIndex
	stSlicePrefix
	stLoopIndex
	stAnyIndex
	stDeref
	// stAll is every location at or behind the place: what a callee summarised as writing or reading
	// memory reachable from an argument may touch.
	stAll
)

type step struct {
	k stepKind
	n int64
}

// desig is what a walked value designates: for a pointer, slice or map, the place it points to; for
// a struct or array value, the place it was copied from (copy). A slice taken at an offset (shifted)
// has element indices that do not match the original's; a zero-based slice s[:hi] tracks prefixHi.
type desig struct {
	root      ssa.Value
	path      []step
	shifted   bool
	copy      bool
	hasPrefix bool
	prefixHi  int64
}

func (d desig) with(s step) desig {
	p := make([]step, len(d.path), len(d.path)+1)
	copy(p, d.path)
	d.path = append(p, s)
	d.hasPrefix, d.prefixHi = false, 0
	return d
}

func (d desig) key() string {
	var b strings.Builder
	for _, s := range d.path {
		b.WriteByte(byte('a' + s.k))
		b.WriteString(strconv.FormatInt(s.n, 10))
		b.WriteByte('/')
	}
	if d.shifted {
		b.WriteByte('s')
	}
	if d.copy {
		b.WriteByte('c')
	}
	if d.hasPrefix {
		b.WriteByte('p')
		b.WriteString(strconv.FormatInt(d.prefixHi, 10))
	}
	return b.String()
}

type access struct {
	root   ssa.Value
	path   []step
	write  bool
	atomic bool
	locks  lockset
	// site is the instruction of the sender's function at which the access happens, for accesses of
	// the sender; exit marks one made by a deferred call, which runs when the function returns.
	site ssa.Instruction
	exit bool
}

// fpFrame is a function the walk is in: the outermost is the sender's function; an inner one is a
// closure or callee entered from the frame above it, by mc or call.
type fpFrame struct {
	fn     *ssa.Function
	parent *fpFrame
	mc     *ssa.MakeClosure
	call   ssa.CallInstruction
	// site is the instruction of the outermost frame through which this frame was entered, and exit
	// whether that instruction is a defer.
	site ssa.Instruction
	exit bool
}

func (f *fpFrame) inChain(fn *ssa.Function) bool {
	for p := f; p != nil; p = p.parent {
		if p.fn == fn {
			return true
		}
	}
	return false
}

const fpBudget = 20000

type fpWalker struct {
	st          *state
	gs          *guardState
	budget      int
	top         bool
	why         string
	allowReturn bool
	spawnSite   ssa.Instruction
	joins       map[ssa.Instruction]bool
	loopHdrs    map[int64]*ssa.BasicBlock
	acc         []access
	// covered holds the values of the outermost frame the walk followed.
	covered map[ssa.Value]bool
	seen    map[fpSeenKey]bool
	live    map[*ssa.Function][]bool
	// rootStores are stores of the walked value into a moved root's cell: the memory behind the
	// cell is walked from the root itself, so the store hands nothing to memory the walk loses.
	rootStores map[*ssa.Store]bool
}

type fpSeenKey struct {
	v     ssa.Value
	path  string
	frame *fpFrame
}

func (w *fpWalker) giveUp(why string) {
	if !w.top {
		w.top, w.why = true, why
	}
}

// locksAt is the canonical lockset held before ins in frame f: guard paths rooted at values of the
// outermost frame. A guard that cannot be expressed there is dropped, which can only add conflicts.
func (w *fpWalker) locksAt(f *fpFrame, ins ssa.Instruction) lockset {
	if w.gs == nil {
		return nil
	}
	held := w.st.locksOf(w.gs, f.fn)[ins]
	if len(held) == 0 {
		return nil
	}
	out := lockset{}
	for p, m := range held {
		if c, ok := canonGuard(f, p); ok {
			out[c] = max(out[c], m)
		}
	}
	return out
}

func canonGuard(f *fpFrame, p guardPath) (guardPath, bool) {
	for f.parent != nil {
		switch r := p.root.(type) {
		case *ssa.Global:
			return p, true
		case *ssa.FreeVar:
			if f.mc == nil {
				return guardPath{}, false
			}
			j := indexOfFreeVar(f.fn, r)
			if j < 0 || j >= len(f.mc.Bindings) {
				return guardPath{}, false
			}
			bp, ok := pathOf(f.mc.Bindings[j])
			if !ok {
				return guardPath{}, false
			}
			p = guardPath{bp.root, bp.steps + p.steps}
		case *ssa.Parameter:
			if f.call == nil {
				return guardPath{}, false
			}
			i := indexOfParam(f.fn, r)
			args := f.call.Common().Args
			if i < 0 || i >= len(args) {
				return guardPath{}, false
			}
			ap, ok := pathOf(args[i])
			if !ok {
				return guardPath{}, false
			}
			p = guardPath{ap.root, ap.steps + p.steps}
		default:
			return guardPath{}, false
		}
		f = f.parent
	}
	return p, true
}

func indexOfFreeVar(fn *ssa.Function, fv *ssa.FreeVar) int {
	for j, x := range fn.FreeVars {
		if x == fv {
			return j
		}
	}
	return -1
}

func indexOfParam(fn *ssa.Function, p *ssa.Parameter) int {
	for i, x := range fn.Params {
		if x == p {
			return i
		}
	}
	return -1
}

func (w *fpWalker) record(f *fpFrame, at ssa.Instruction, d desig, write, atomic bool) {
	if !w.isLive(at) {
		return
	}
	a := access{root: d.root, path: d.path, write: write, atomic: atomic}
	_, deferred := at.(*ssa.Defer)
	if f.parent == nil {
		a.site, a.exit = at, deferred
	} else {
		a.site, a.exit = f.site, f.exit
	}
	if !deferred && !a.exit {
		a.locks = w.locksAt(f, at)
	}
	w.acc = append(w.acc, a)
}

// isPointerLike reports whether a value of type t designates memory it does not hold inline.
func isPointerLike(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Signature, *types.Interface:
		return true
	}
	return false
}

func isAggregate(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

// loaded is what a value read from the place d designates designates, and whether there is
// anything to follow.
func loaded(d desig, t types.Type) (desig, bool) {
	switch {
	case isPointerLike(t):
		d = d.with(step{k: stDeref})
		d.shifted, d.copy = false, false
		return d, true
	case isAggregate(t):
		d.copy = true
		d.hasPrefix, d.prefixHi = false, 0
		return d, true
	}
	return d, false
}

func constIndex(v ssa.Value) (int64, bool) {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.Int {
		return 0, false
	}
	return constant.Int64Val(c.Value)
}

func (w *fpWalker) index(f *fpFrame, d desig, idx ssa.Value) desig {
	if n, ok := constIndex(idx); ok && !d.shifted {
		return d.with(step{k: stIndex, n: n})
	}
	if !d.shifted && w.spawnSite != nil {
		if outerIdx, ok := w.unwrapOuterLoopValue(f, idx); ok {
			if loopID, hdr, ok := matchMonotonicLoopCounter(w.spawnSite.Parent(), outerIdx, w.spawnSite, w.joins); ok {
				if w.loopHdrs != nil {
					w.loopHdrs[loopID] = hdr
				}
				return d.with(step{k: stLoopIndex, n: loopID})
			}
		}
	}
	return d.with(step{k: stAnyIndex})
}

// walk follows every use of v, which designates d, in frame f.
func (w *fpWalker) walk(f *fpFrame, v ssa.Value, d desig) {
	if w.top {
		return
	}
	if w.untracked(v) {
		// Pointer-free values and shared-by-design or imm memory carry nothing the move checks,
		// as in the alias graph (graph.val). A synchronisation object is still followed, so its
		// operations count as atomic accesses and a plain write over it as a conflict.
		return
	}
	k := fpSeenKey{v, d.key(), f}
	if w.seen[k] {
		return
	}
	w.seen[k] = true
	if f.parent == nil {
		w.covered[v] = true
	}
	refs := v.Referrers()
	if refs == nil {
		w.giveUp("a value with unknown uses")
		return
	}
	for _, r := range *refs {
		if !w.isLive(r) {
			continue
		}
		if w.budget--; w.budget < 0 {
			w.giveUp("the walk passed its budget")
			return
		}
		w.use(f, v, d, r)
		if w.top {
			return
		}
	}
}

// isMutableValueSubobjectAddr reports whether v is an interior address (`&s.field` or `&arr[i]`)
// whose pointer type is Shareable or AutoSync (other than sync/atomic primitives handled by
// isAtomicTarget and //gosree:imm types enforced by SAFE005), so whole-field overwrites or
// external pointer writes conflict with concurrent encapsulated method calls on the subobject.
func isMutableValueSubobjectAddr(st *state, v ssa.Value) bool {
	if st == nil || v == nil {
		return false
	}
	switch v.(type) {
	case *ssa.FieldAddr, *ssa.IndexAddr:
	default:
		return false
	}
	if isAtomicTarget(v.Type()) {
		return false
	}
	if !st.cls.Shareable(v.Type()) && !st.isAutoSyncType(v.Type()) {
		return false
	}
	p, ok := v.Type().Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	return !st.isImmNamed(p.Elem())
}

// untracked mirrors graph.val: a value whose type cannot carry unchecked shared memory.
func (w *fpWalker) untracked(v ssa.Value) bool {
	if isAtomicTarget(v.Type()) || isMutableValueSubobjectAddr(w.st, v) {
		return false
	}
	if mi, ok := v.(*ssa.MakeInterface); ok {
		return (w.st.cls.Shareable(mi.Type()) || w.st.isAutoSyncType(mi.Type())) &&
			(w.st.cls.Shareable(mi.X.Type()) || w.st.isAutoSyncType(mi.X.Type()))
	}
	return w.st.cls.Shareable(v.Type()) || w.st.isAutoSyncType(v.Type())
}

// isAtomicTarget reports whether t is a pointer to a synchronisation object or atomic: something
// whose methods operate on the memory atomically.
func isAtomicTarget(t types.Type) bool {
	p, ok := t.Underlying().(*types.Pointer)
	if !ok {
		return false
	}
	name := qualified(p.Elem())
	return strings.HasPrefix(name, "sync.") || strings.HasPrefix(name, atomicPkg+".")
}

// funcPkgPath is the path of the package that declares fn (of its origin, for an instance), or "".
func funcPkgPath(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}
	if o := fn.Origin(); o != nil {
		fn = o
	}
	if obj, ok := fn.Object().(*types.Func); ok && obj.Pkg() != nil {
		return obj.Pkg().Path()
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	return ""
}

func qualified(t types.Type) string {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ""
	}
	return n.Obj().Pkg().Path() + "." + n.Obj().Name()
}

func (w *fpWalker) use(f *fpFrame, v ssa.Value, d desig, r ssa.Instruction) {
	if isAtomicTarget(v.Type()) {
		// Methods of sync and sync/atomic types: atomic operations on the object. Anything else
		// done with the pointer is followed as usual.
		if c, ok := r.(ssa.CallInstruction); ok && !c.Common().IsInvoke() {
			if path := funcPkgPath(c.Common().StaticCallee()); path == "sync" || path == atomicPkg {
				if _, isGo := r.(*ssa.Go); !isGo {
					w.record(f, r, d, true, true)
					return
				}
			}
		}
	}
	if isMutableValueSubobjectAddr(w.st, v) {
		if c, ok := r.(ssa.CallInstruction); ok && !c.Common().IsInvoke() {
			if _, isGo := r.(*ssa.Go); !isGo {
				cc := c.Common()
				callee := cc.StaticCallee()
				if isEncapsulatedMethodRecvCall(w.st, callee, 0, v) && len(cc.Args) > 0 && cc.Args[0] == v && !slices.Contains(cc.Args[1:], v) {
					mayWrite := len(callee.Blocks) == 0 || w.st.summary(callee).writes(0)
					if p, ok := v.Type().Underlying().(*types.Pointer); ok && w.st.cls.Shareable(p.Elem()) {
						mayWrite = false
					}
					w.record(f, r, d, mayWrite, true)
					return
				}
			}
		}
	}
	switch r := r.(type) {
	case *ssa.DebugRef:
	case *ssa.UnOp:
		switch r.Op {
		case token.MUL:
			w.record(f, r, d, false, false)
			if nd, ok := loaded(d, r.Type()); ok {
				w.walk(f, r, nd)
			}
		case token.ARROW:
			// A receive from a channel the walk reached: channels are shareable, never walked.
			w.giveUp("receive through walked memory")
		default:
			// Arithmetic on a copied value reads nothing.
		}
	case *ssa.Store:
		selfRet := w.isNamedReturnSelfStore(f, r)
		if r.Addr == v && !selfRet {
			w.record(f, r, d, true, false)
		}
		if r.Val == v && !selfRet {
			if !w.rootStores[r] && !w.walkSingleStoreCell(f, r, d) {
				w.giveUp("stored into memory")
			}
		}
	case *ssa.FieldAddr:
		w.walk(f, r, d.with(step{k: stField, n: int64(r.Field)}))
	case *ssa.Field:
		nd := d.with(step{k: stField, n: int64(r.Field)})
		if isPointerLike(r.Type()) {
			nd = nd.with(step{k: stDeref})
			nd.copy, nd.shifted = false, false
		} else if !isAggregate(r.Type()) {
			return
		}
		w.walk(f, r, nd)
	case *ssa.IndexAddr:
		if r.X != v {
			return // v is the index
		}
		nd := w.index(f, d, r.Index)
		nd.shifted, nd.copy = false, false
		w.walk(f, r, nd)
	case *ssa.Index:
		if r.X != v {
			return
		}
		nd := w.index(f, d, r.Index)
		nd.shifted = false
		if isPointerLike(r.Type()) {
			nd = nd.with(step{k: stDeref})
			nd.copy = false
		} else if !isAggregate(r.Type()) {
			return
		}
		w.walk(f, r, nd)
	case *ssa.Slice:
		if r.X != v {
			return
		}
		nd := d
		nd.copy = false
		nd.hasPrefix, nd.prefixHi = false, 0
		lowZero := true
		if r.Low != nil {
			if n, ok := constIndex(r.Low); !ok || n != 0 {
				nd.shifted = true
				lowZero = false
			}
		}
		if !nd.shifted && lowZero {
			if r.High != nil {
				if hi, ok := constIndex(r.High); ok && hi >= 0 {
					nd.hasPrefix, nd.prefixHi = true, hi
				}
			} else if d.hasPrefix {
				nd.hasPrefix, nd.prefixHi = true, d.prefixHi
			}
		}
		if _, isPtr := v.Type().Underlying().(*types.Pointer); !isPtr && isAggregate(v.Type()) {
			// Slicing an addressable array value is impossible in SSA; be safe.
			w.giveUp("slice of an array value")
			return
		}
		w.walk(f, r, nd)
	case *ssa.Convert, *ssa.MultiConvert:
		rv := r.(ssa.Value)
		if b, ok := rv.Type().Underlying().(*types.Basic); ok {
			if b.Kind() == types.UnsafePointer {
				w.giveUp("converted to unsafe.Pointer")
				return
			}
			// string(b) and the like copy every element out.
			w.record(f, r, d.with(step{k: stAnyIndex}), false, false)
			return
		}
		nd := d
		nd.hasPrefix, nd.prefixHi = false, 0
		w.walk(f, rv, nd)
	case *ssa.Phi:
		if b, ok := r.Type().Underlying().(*types.Basic); ok && b.Kind() == types.UnsafePointer {
			w.giveUp("converted to unsafe.Pointer")
			return
		}
		w.walk(f, r, d)
	case *ssa.ChangeType, *ssa.ChangeInterface, *ssa.MakeInterface, *ssa.SliceToArrayPointer:
		rv := r.(ssa.Value)
		if b, ok := rv.Type().Underlying().(*types.Basic); ok && b.Kind() == types.UnsafePointer {
			w.giveUp("converted to unsafe.Pointer")
			return
		}
		nd := d
		nd.hasPrefix, nd.prefixHi = false, 0
		w.walk(f, rv, nd)
	case *ssa.TypeAssert:
		if r.CommaOk {
			w.walkExtract(f, r, 0, d)
			return
		}
		w.walk(f, r, d)
	case *ssa.Lookup:
		if r.X != v {
			return // v is the key: a copy of it is compared
		}
		if _, isMap := v.Type().Underlying().(*types.Map); !isMap {
			// Indexing a string reads a byte.
			return
		}
		w.record(f, r, d, false, false)
		ed := d.with(step{k: stAnyIndex})
		if r.CommaOk {
			if nd, ok := loaded(ed, r.Type().(*types.Tuple).At(0).Type()); ok {
				w.walkExtract(f, r, 0, nd)
			}
			return
		}
		if nd, ok := loaded(ed, r.Type()); ok {
			w.walk(f, r, nd)
		}
	case *ssa.MapUpdate:
		if r.Map == v {
			w.record(f, r, d, true, false)
		}
		if r.Key == v || r.Value == v {
			w.giveUp("stored in a map")
		}
	case *ssa.Range:
		if _, isMap := v.Type().Underlying().(*types.Map); isMap {
			w.record(f, r, d, false, false)
			w.walkNext(f, r, d)
		}
	case *ssa.BinOp:
		// A comparison of pointers or arithmetic on copies reads no memory.
	case *ssa.If, *ssa.Jump:
	case *ssa.Return:
		if w.allowReturn && f.parent == nil {
			if w.isLive(r) {
				w.acc = append(w.acc, access{
					root:  d.root,
					path:  d.with(step{k: stAll}).path,
					write: true,
					site:  r,
				})
			}
			return
		}
		w.giveUp("returned")
	case *ssa.Panic:
		w.giveUp("passed to panic")
	case *ssa.Send:
		w.giveUp("sent on a channel")
	case *ssa.Select:
		w.giveUp("sent in a select")
	case *ssa.MakeClosure:
		w.closure(f, v, d, r)
	case *ssa.Call:
		w.call(f, v, d, r, r.Common())
	case *ssa.Defer:
		w.call(f, v, d, r, r.Common())
	case *ssa.Go:
		w.call(f, v, d, r, r.Common())
	default:
		w.giveUp("used by an instruction the walk does not model")
	}
}

func (w *fpWalker) walkExtract(f *fpFrame, tuple ssa.Value, idx int, d desig) {
	for _, r := range *tuple.Referrers() {
		if e, ok := r.(*ssa.Extract); ok && e.Index == idx {
			w.walk(f, e, d)
		}
	}
}

func (w *fpWalker) walkNext(f *fpFrame, rng *ssa.Range, d desig) {
	for _, r := range *rng.Referrers() {
		next, ok := r.(*ssa.Next)
		if !ok {
			continue
		}
		tup := next.Type().(*types.Tuple)
		for _, nr := range *next.Referrers() {
			e, ok := nr.(*ssa.Extract)
			if !ok || e.Index == 0 {
				continue
			}
			if nd, ok := loaded(d.with(step{k: stAnyIndex}), tup.At(e.Index).Type()); ok {
				w.walk(f, e, nd)
			}
		}
	}
}

type closureRunSite struct {
	site ssa.Instruction
	exit bool
}

func freeVarOnlyCalledDirectly(v ssa.Value) bool {
	if v == nil || v.Referrers() == nil {
		return false
	}
	for _, r := range *v.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Call:
			if x.Call.IsInvoke() || x.Call.Value != v || usedAsArg(x, v, -1) {
				return false
			}
		case *ssa.Defer:
			if x.Call.IsInvoke() || x.Call.Value != v || usedAsArg(&ssa.Call{Call: x.Call}, v, -1) {
				return false
			}
		case *ssa.MakeClosure:
			subFn, ok := x.Fn.(*ssa.Function)
			if !ok || len(subFn.Blocks) == 0 || x.Referrers() == nil {
				return false
			}
			for _, sr := range *x.Referrers() {
				switch sx := sr.(type) {
				case *ssa.DebugRef:
				case *ssa.Call:
					if sx.Call.IsInvoke() || sx.Call.Value != x || usedAsArg(sx, x, -1) {
						return false
					}
				case *ssa.Defer:
					if sx.Call.IsInvoke() || sx.Call.Value != x || usedAsArg(&ssa.Call{Call: sx.Call}, x, -1) {
						return false
					}
				default:
					return false
				}
			}
			for k, bind := range x.Bindings {
				if bind == v && (k >= len(subFn.FreeVars) || !freeVarOnlyCalledDirectly(subFn.FreeVars[k])) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func (w *fpWalker) collectClosureRunSites(mc *ssa.MakeClosure, allowCapture bool, seen map[*ssa.MakeClosure]bool) ([]closureRunSite, bool) {
	if mc == nil || mc.Referrers() == nil {
		return nil, false
	}
	if seen[mc] {
		return nil, false
	}
	seen[mc] = true
	var out []closureRunSite
	for _, r := range *mc.Referrers() {
		switch r := r.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.Call:
			if r.Call.Value == mc && !usedAsArg(r, mc, -1) {
				out = append(out, closureRunSite{site: r})
				continue
			}
			if sp, ok := spawnOf(r); ok && sp.fn == mc && !usedAsArg(r, mc, sp.arg) {
				out = append(out, closureRunSite{site: r})
				continue
			}
			if w.startsCallback(r, mc) {
				out = append(out, closureRunSite{site: r})
				continue
			}
		case *ssa.Defer:
			if r.Call.Value == mc && !usedAsArg(&ssa.Call{Call: r.Call}, mc, -1) {
				out = append(out, closureRunSite{site: r, exit: true})
				continue
			}
		case *ssa.Go:
			if r.Call.Value == mc && !usedAsArg(&ssa.Call{Call: r.Call}, mc, -1) {
				out = append(out, closureRunSite{site: r})
				continue
			}
		case *ssa.MakeClosure:
			if allowCapture {
				outerFn, ok := r.Fn.(*ssa.Function)
				if ok && len(outerFn.Blocks) > 0 {
					allDirect := true
					for k, bind := range r.Bindings {
						if bind == mc && (k >= len(outerFn.FreeVars) || !freeVarOnlyCalledDirectly(outerFn.FreeVars[k])) {
							allDirect = false
							break
						}
					}
					if allDirect {
						if subSites, ok := w.collectClosureRunSites(r, true, seen); ok {
							w.covered[r] = true
							out = append(out, subSites...)
							continue
						}
					}
				}
			}
		}
		return nil, false
	}
	return out, true
}

// closure follows a closure that captures v: its body's accesses through the matching free
// variable, at every place the closure runs. A closure that is kept or passed on is not followed.
func (w *fpWalker) closure(f *fpFrame, v ssa.Value, d desig, mc *ssa.MakeClosure) {
	fn, ok := mc.Fn.(*ssa.Function)
	if !ok || len(fn.Blocks) == 0 {
		w.giveUp("captured by a closure without a body")
		return
	}
	sites, ok := w.collectClosureRunSites(mc, f.parent == nil, map[*ssa.MakeClosure]bool{})
	if !ok {
		w.giveUp("a closure capturing it is kept or passed on")
		return
	}
	if f.inChain(fn) {
		w.giveUp("recursive closure cycle")
		return
	}
	if f.parent == nil {
		w.covered[mc] = true
	}
	for j, b := range mc.Bindings {
		if b != v {
			continue
		}
		for _, rs := range sites {
			inner := &fpFrame{fn: fn, parent: f, mc: mc, site: f.site, exit: f.exit}
			if f.parent == nil {
				inner.exit = rs.exit
				inner.site = rs.site
			}
			w.walk(inner, fn.FreeVars[j], d)
		}
	}
}

// startsCallback reports whether call passes mc, once, to a parameter its callee of this package
// starts on another goroutine (summary.spawns), as graph.call treats it.
func (w *fpWalker) startsCallback(call *ssa.Call, mc *ssa.MakeClosure) bool {
	if call.Call.IsInvoke() || call.Call.Value == mc {
		return false
	}
	callee := call.Call.StaticCallee()
	if callee == nil {
		return false
	}
	sum := w.st.summary(callee)
	n := 0
	ok := false
	for i, a := range call.Call.Args {
		if a == mc {
			n++
			ok = sum.spawns(i) && !sum.isIso(i)
		}
	}
	return n == 1 && ok
}

// call follows v passed to a call: into the body of a function literal or a function of this
// package started by a go statement, through the callee's summary otherwise.
func (w *fpWalker) call(f *fpFrame, v ssa.Value, d desig, ins ssa.CallInstruction, cc *ssa.CallCommon) {
	if cc.IsInvoke() {
		w.giveUp("handed to an interface method")
		return
	}
	if cc.Value == v {
		// Calling a function value read from walked memory runs code that captured something else.
		for _, a := range cc.Args {
			if a == v {
				w.giveUp("a function value passed to itself")
				return
			}
		}
		return
	}
	if b, ok := cc.Value.(*ssa.Builtin); ok {
		w.builtin(f, v, d, ins, b, cc.Args)
		return
	}
	fn := cc.StaticCallee()
	if fn == nil {
		w.giveUp("passed to a dynamic call")
		return
	}
	if funcPkgPath(fn) == atomicPkg {
		// A sync/atomic function operates atomically on what its first argument points to.
		for i, a := range cc.Args {
			if a == v && i != 0 {
				w.giveUp("published by a sync/atomic function")
				return
			}
		}
		w.record(f, ins, d, true, true)
		return
	}
	if _, isGo := ins.(*ssa.Go); isGo && len(fn.Blocks) > 0 && fn.Pkg == f.fn.Pkg && fn.Synthetic == "" {
		if f.inChain(fn) {
			w.giveUp("recursive goroutine call cycle")
			return
		}
		for i, a := range cc.Args {
			if a != v || i >= len(fn.Params) {
				continue
			}
			inner := &fpFrame{fn: fn, parent: f, call: ins, site: f.site, exit: f.exit}
			if f.parent == nil {
				inner.site = ins
			}
			w.walk(inner, fn.Params[i], d)
		}
		return
	}
	sum := w.st.summary(fn)
	if sum.pessimistic {
		w.giveUp("passed to a callee with no summary")
		return
	}
	for i, a := range cc.Args {
		if a != v {
			continue
		}
		if sum.leaks(i) || sum.spawns(i) || sum.isIso(i) {
			w.giveUp("passed to a callee that may keep it")
			return
		}
		for _, l := range sum.links {
			if l.A == i || l.B == i {
				w.giveUp("passed to a callee that links it to another value")
				return
			}
		}
		if !sum.writes(i) && i == 0 && len(fn.Blocks) > 0 && fn.Synthetic == "" &&
			fn.Signature != nil && fn.Signature.Recv() != nil &&
			fn.Signature.Results() != nil && fn.Signature.Results().Len() > 0 &&
			i < len(fn.Params) && !hasNonDebugReferrer(fn.Params[0]) {
			continue
		}
		w.record(f, ins, d.with(step{k: stAll}), sum.writes(i), false)
	}
}

func (w *fpWalker) builtin(f *fpFrame, v ssa.Value, d desig, ins ssa.CallInstruction, b *ssa.Builtin, args []ssa.Value) {
	all := d.with(step{k: stAnyIndex})
	switch b.Name() {
	case "len", "cap":
		if _, isMap := v.Type().Underlying().(*types.Map); isMap {
			w.record(f, ins, d, false, false)
		}
	case "append":
		if len(args) > 0 && args[0] == v {
			w.record(f, ins, all, true, false)
			for _, a := range args[1:] {
				if a == v {
					w.giveUp("appended to itself")
					return
				}
			}
			if c, ok := ins.(*ssa.Call); ok {
				nd := d
				nd.shifted = true
				nd.hasPrefix, nd.prefixHi = false, 0
				w.walk(f, c, nd)
			}
			return
		}
		// Appended as the elements' source: append copies them.
		if len(args) == 2 {
			if _, isSlice := v.Type().Underlying().(*types.Slice); isSlice {
				if et, ok := v.Type().Underlying().(*types.Slice); ok && !isPointerLike(et.Elem()) && !isAggregate(et.Elem()) {
					w.record(f, ins, all, false, false)
					return
				}
			}
		}
		w.giveUp("appended into another slice")
	case "copy":
		cpStep := all
		if d.hasPrefix {
			cpStep = d.with(step{k: stSlicePrefix, n: d.prefixHi})
		}
		if len(args) == 2 && args[0] == v {
			w.record(f, ins, cpStep, true, false)
			return
		}
		if len(args) == 2 && args[1] == v {
			if et, ok := v.Type().Underlying().(*types.Slice); ok {
				if !isPointerLike(et.Elem()) && !isAggregate(et.Elem()) {
					w.record(f, ins, cpStep, false, false)
					return
				}
				if w.copyToLocalSlice(f, ins, args[0], d, cpStep) {
					return
				}
			}
		}
		w.giveUp("copied into another slice")
	case "delete", "clear":
		w.record(f, ins, all, true, false)
	case "print", "println":
		w.record(f, ins, d, false, false)
	case "min", "max":
	default:
		w.giveUp("passed to builtin " + b.Name())
	}
}

// copyToLocalSlice models copy(dst, src) when src carries pointer or aggregate elements and dst
// comes from a non-escaping local MakeSlice whose only element uses are direct loads that w
// follows as loads from src.
func (w *fpWalker) copyToLocalSlice(f *fpFrame, copyIns ssa.CallInstruction, dst ssa.Value, srcDesig, cpStep desig) bool {
	base := dst
	for {
		sl, ok := base.(*ssa.Slice)
		if !ok {
			break
		}
		base = sl.X
	}
	ms, ok := base.(*ssa.MakeSlice)
	if !ok || ms.Parent() != f.fn || ms == srcDesig.root {
		return false
	}
	var elemLoads []*ssa.UnOp
	seen := map[ssa.Value]bool{}
	var checkSliceUses func(cur ssa.Value) bool
	checkSliceUses = func(cur ssa.Value) bool {
		if seen[cur] {
			return true
		}
		seen[cur] = true
		if cur.Referrers() == nil {
			return false
		}
		for _, mr := range *cur.Referrers() {
			if !w.isLive(mr) {
				continue
			}
			switch mr := mr.(type) {
			case *ssa.DebugRef:
			case *ssa.Slice:
				if mr.X != cur || !checkSliceUses(mr) {
					return false
				}
			case *ssa.IndexAddr:
				if mr.X != cur || mr.Referrers() == nil {
					return false
				}
				for _, ir := range *mr.Referrers() {
					if !w.isLive(ir) {
						continue
					}
					switch ir := ir.(type) {
					case *ssa.DebugRef:
					case *ssa.UnOp:
						if ir.Op != token.MUL {
							return false
						}
						elemLoads = append(elemLoads, ir)
					default:
						return false
					}
				}
			case ssa.CallInstruction:
				if mr == copyIns {
					args := mr.Common().Args
					if len(args) != 2 || args[0] != cur || args[1] == cur {
						return false
					}
					continue
				}
				b, isB := mr.Common().Value.(*ssa.Builtin)
				if !isB || (b.Name() != "len" && b.Name() != "cap") {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !checkSliceUses(ms) {
		return false
	}
	w.record(f, copyIns, cpStep, false, false)
	anyElem := srcDesig.with(step{k: stAnyIndex})
	for _, ld := range elemLoads {
		if nd, ok := loaded(anyElem, ld.Type()); ok {
			w.walk(f, ld, nd)
			if w.top {
				return true
			}
		}
	}
	return true
}

// walkSingleStoreCell follows v stored once into a non-escaping local variable cell (such as Go SSA's
// heap box for a captured `slot := &results[i]` local), walking every load of that cell at d.
func (w *fpWalker) walkSingleStoreCell(f *fpFrame, store *ssa.Store, d desig) bool {
	alloc, ok := store.Addr.(*ssa.Alloc)
	if !ok || alloc.Parent() != f.fn || alloc == d.root || alloc.Referrers() == nil {
		return false
	}
	uses := usesOfCell(alloc)
	if uses.open || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 || len(uses.stores) != 1 || uses.stores[0] != store {
		return false
	}
	type mcTarget struct {
		mc    *ssa.MakeClosure
		fn    *ssa.Function
		sites []closureRunSite
	}
	var loads []*ssa.UnOp
	var mcs []mcTarget
	for _, r := range *alloc.Referrers() {
		if !w.isLive(r) {
			continue
		}
		switch r := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if r != store {
				return false
			}
		case *ssa.UnOp:
			if r.Op != token.MUL || r.X != alloc {
				return false
			}
			loads = append(loads, r)
		case *ssa.MakeClosure:
			fn, ok := r.Fn.(*ssa.Function)
			if !ok || len(fn.Blocks) == 0 || f.inChain(fn) {
				return false
			}
			sites, ok := w.collectClosureRunSites(r, f.parent == nil, map[*ssa.MakeClosure]bool{})
			if !ok {
				return false
			}
			// Only closures run on this goroutine, by a direct call or defer of the closure
			// itself, are followed. A closure a go statement or a spawner starts runs elsewhere:
			// following it would credit another goroutine's accesses to this walk (and let a
			// walk meant to prove the goroutine cannot reach the memory follow it into the
			// goroutine). The one exception is the move being refined itself (w.spawnSite): the
			// goroutine it starts is walked separately as the child, so it is skipped here.
			var runHere []closureRunSite
			for _, rs := range sites {
				var cc *ssa.CallCommon
				switch s := rs.site.(type) {
				case *ssa.Call:
					cc = &s.Call
				case *ssa.Defer:
					cc = &s.Call
				}
				if cc != nil {
					if _, direct := cc.Value.(*ssa.MakeClosure); direct {
						runHere = append(runHere, rs)
						continue
					}
				}
				if w.spawnSite != nil && rs.site == w.spawnSite && f.parent == nil {
					continue
				}
				return false
			}
			sites = runHere
			for j, b := range r.Bindings {
				if b != alloc {
					continue
				}
				if j >= len(fn.FreeVars) || fn.FreeVars[j].Referrers() == nil {
					return false
				}
				for _, fr := range *fn.FreeVars[j].Referrers() {
					switch fx := fr.(type) {
					case *ssa.DebugRef:
					case *ssa.UnOp:
						if fx.Op != token.MUL || fx.X != fn.FreeVars[j] {
							return false
						}
					default:
						return false
					}
				}
			}
			mcs = append(mcs, mcTarget{mc: r, fn: fn, sites: sites})
		default:
			return false
		}
	}
	if f.parent == nil {
		w.covered[alloc] = true
	}
	for _, ld := range loads {
		if isPointerLike(ld.Type()) || isAggregate(ld.Type()) {
			w.walk(f, ld, d)
			if w.top {
				return true
			}
		}
	}
	for _, m := range mcs {
		if f.parent == nil {
			w.covered[m.mc] = true
		}
		for j, b := range m.mc.Bindings {
			if b != alloc {
				continue
			}
			fv := m.fn.FreeVars[j]
			for _, rs := range m.sites {
				inner := &fpFrame{fn: m.fn, parent: f, mc: m.mc, site: f.site, exit: f.exit}
				if f.parent == nil {
					inner.exit = rs.exit
					inner.site = rs.site
				}
				for _, fr := range *fv.Referrers() {
					if ld, isUn := fr.(*ssa.UnOp); isUn && ld.Op == token.MUL && (isPointerLike(ld.Type()) || isAggregate(ld.Type())) {
						w.walk(inner, ld, d)
						if w.top {
							return true
						}
					}
				}
			}
		}
	}
	return true
}

func isDerivedPlaceVal(v ssa.Value) bool {
	switch v.(type) {
	case *ssa.IndexAddr, *ssa.FieldAddr, *ssa.Slice, *ssa.ChangeType:
		return true
	}
	return false
}

func unwrapDerivedPlaceCell(local, outer ssa.Value, spawnSite ssa.Instruction, joins map[ssa.Instruction]bool, loopHdrs map[int64]*ssa.BasicBlock) (desig, []*ssa.UnOp, bool) {
	alloc, ok := outer.(*ssa.Alloc)
	if !ok || spawnSite == nil || alloc.Parent() != spawnSite.Parent() || local == nil || local.Referrers() == nil {
		return desig{}, nil, false
	}
	uses := usesOfCell(alloc)
	if uses.open || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 || len(uses.stores) != 1 {
		return desig{}, nil, false
	}
	store := uses.stores[0]
	if store.Addr != alloc || store.Parent() != alloc.Parent() || !isDerivedPlaceVal(store.Val) {
		return desig{}, nil, false
	}
	if !dominatesInstr(alloc, store) || !dominatesInstr(store, spawnSite) || reachableWithoutKill(spawnSite, store, alloc) {
		return desig{}, nil, false
	}
	var loads []*ssa.UnOp
	for _, r := range *local.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != local {
				return desig{}, nil, false
			}
			loads = append(loads, x)
		default:
			return desig{}, nil, false
		}
	}
	d, ok := moveRootAt(store.Val, spawnSite, joins, loopHdrs)
	if !ok || d.root == alloc || d.root.Parent() != alloc.Parent() {
		return desig{}, nil, false
	}
	return d, loads, true
}

// moveRoot is where a value moved to a goroutine comes from: a fresh allocation of the sender and the
// path to the place the value designates.
func moveRoot(v ssa.Value) (desig, bool) {
	return moveRootAt(v, nil, nil, nil)
}

func moveRootAt(v ssa.Value, spawnSite ssa.Instruction, joins map[ssa.Instruction]bool, loopHdrs map[int64]*ssa.BasicBlock) (desig, bool) {
	var rev []step
	shifted := false
	for {
		switch x := v.(type) {
		case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeMap:
			path := make([]step, len(rev))
			for i := range rev {
				path[i] = rev[len(rev)-1-i]
			}
			return desig{root: x, path: path, shifted: shifted}, true
		case *ssa.FieldAddr:
			rev = append(rev, step{k: stField, n: int64(x.Field)})
			v = x.X
		case *ssa.IndexAddr:
			if n, ok := constIndex(x.Index); ok {
				rev = append(rev, step{k: stIndex, n: n})
			} else if spawnSite != nil && x.Parent() == spawnSite.Parent() && dominatesInstr(x, spawnSite) {
				outerIdx := x.Index
				if u, isUn := outerIdx.(*ssa.UnOp); isUn && u.Op == token.MUL {
					if phi, isPhi := u.X.(*ssa.Phi); isPhi && phi.Parent() == spawnSite.Parent() {
						outerIdx = phi
					} else if initVal, ok := singleIterCellValue(spawnSite.Parent(), u.X, spawnSite); ok {
						outerIdx = initVal
					}
				}
				if loopID, hdr, ok := matchMonotonicLoopCounter(spawnSite.Parent(), outerIdx, spawnSite, joins); ok {
					if loopHdrs != nil {
						loopHdrs[loopID] = hdr
					}
					rev = append(rev, step{k: stLoopIndex, n: loopID})
				} else {
					rev = append(rev, step{k: stAnyIndex})
				}
			} else {
				rev = append(rev, step{k: stAnyIndex})
			}
			v = x.X
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return desig{}, false
			}
			rev = append(rev, step{k: stDeref})
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.Slice:
			if x.Low != nil {
				if n, ok := constIndex(x.Low); !ok || n != 0 {
					// Indices of what is reached through it no longer match: widen them.
					for i := range rev {
						if rev[i].k == stIndex || rev[i].k == stLoopIndex {
							rev[i] = step{k: stAnyIndex}
						}
					}
					shifted = true
				}
			}
			v = x.X
		default:
			return desig{}, false
		}
	}
}

func isWordIntType(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	switch b.Kind() {
	case types.Int, types.Int64, types.Uint, types.Uint64, types.Uintptr:
		return true
	}
	return false
}

func dominatesInstr(a, b ssa.Instruction) bool {
	if a == nil || b == nil || a.Block() == nil || b.Block() == nil || a.Parent() != b.Parent() {
		return false
	}
	if a.Block() == b.Block() {
		return indexIn(a) < indexIn(b)
	}
	return a.Block().Dominates(b.Block())
}

func singleIterCellValue(outerFn *ssa.Function, cell ssa.Value, spawnSite ssa.Instruction) (ssa.Value, bool) {
	alloc, ok := cell.(*ssa.Alloc)
	if !ok || !alloc.Heap || alloc.Parent() != outerFn {
		return nil, false
	}
	uses := usesOfCell(alloc)
	if uses.open || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 || len(uses.stores) != 1 {
		return nil, false
	}
	store := uses.stores[0]
	if store.Addr != alloc || store.Parent() != outerFn || !isWordIntType(store.Val.Type()) {
		return nil, false
	}
	if !dominatesInstr(alloc, store) || !dominatesInstr(store, spawnSite) || reachableWithoutKill(spawnSite, store, alloc) {
		return nil, false
	}
	return store.Val, true
}

func (w *fpWalker) unwrapOuterLoopValue(f *fpFrame, v ssa.Value) (ssa.Value, bool) {
	if f == nil || v == nil || w.spawnSite == nil || w.spawnSite.Parent() == nil {
		return nil, false
	}
	outerFn := w.spawnSite.Parent()
	for cur := f; cur.parent != nil; cur = cur.parent {
		if cur.exit {
			return nil, false
		}
		if cur.parent.parent == nil {
			if cur.site != w.spawnSite {
				return nil, false
			}
			continue
		}
		if cur.call != nil {
			if _, isGo := cur.call.(*ssa.Go); isGo {
				return nil, false
			}
		}
		if cur.mc != nil && cur.mc.Referrers() != nil {
			for _, r := range *cur.mc.Referrers() {
				switch rx := r.(type) {
				case *ssa.DebugRef:
				case *ssa.Call:
					if rx.Call.IsInvoke() || rx.Call.Value != cur.mc || usedAsArg(rx, cur.mc, -1) {
						return nil, false
					}
				default:
					return nil, false
				}
			}
		}
	}
	cur := f
	for steps := 0; steps < 8; steps++ {
		if !isWordIntType(v.Type()) {
			return nil, false
		}
		if cur.parent == nil {
			if cur.fn != outerFn {
				return nil, false
			}
			if u, isUn := v.(*ssa.UnOp); isUn && u.Op == token.MUL {
				if phi, isPhi := u.X.(*ssa.Phi); isPhi && phi.Parent() == outerFn {
					return phi, true
				}
				if initVal, ok := singleIterCellValue(outerFn, u.X, w.spawnSite); ok {
					v = initVal
					continue
				}
			}
			return v, true
		}
		switch x := v.(type) {
		case *ssa.Parameter:
			if cur.call == nil {
				return nil, false
			}
			idx := indexOfParam(cur.fn, x)
			args := cur.call.Common().Args
			if idx < 0 || idx >= len(args) {
				return nil, false
			}
			v = args[idx]
			cur = cur.parent
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			fv, isFV := x.X.(*ssa.FreeVar)
			if !isFV || cur.mc == nil || freeVarWritten(fv) {
				return nil, false
			}
			j := indexOfFreeVar(cur.fn, fv)
			if j < 0 || j >= len(cur.mc.Bindings) {
				return nil, false
			}
			b := cur.mc.Bindings[j]
			cur = cur.parent
			if cur.parent == nil && cur.fn == outerFn {
				if phi, isPhi := b.(*ssa.Phi); isPhi && phi.Parent() == outerFn {
					return phi, true
				}
				if initVal, ok := singleIterCellValue(outerFn, b, w.spawnSite); ok {
					v = initVal
					continue
				}
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}

func unitStepDelta(bin *ssa.BinOp, base ssa.Value) (int64, bool) {
	if bin == nil || base == nil {
		return 0, false
	}
	switch bin.Op {
	case token.ADD:
		if bin.X == base {
			if n, ok := constIndex(bin.Y); ok && (n == 1 || n == -1) {
				return n, true
			}
		}
		if bin.Y == base {
			if n, ok := constIndex(bin.X); ok && (n == 1 || n == -1) {
				return n, true
			}
		}
	case token.SUB:
		if bin.X == base {
			if n, ok := constIndex(bin.Y); ok && (n == 1 || n == -1) {
				return -n, true
			}
		}
	}
	return 0, false
}

func matchMonotonicLoopCounter(outerFn *ssa.Function, outerVal ssa.Value, spawnSite ssa.Instruction, joins map[ssa.Instruction]bool) (int64, *ssa.BasicBlock, bool) {
	if outerFn == nil || outerVal == nil || spawnSite == nil || spawnSite.Parent() != outerFn {
		return 0, nil, false
	}
	checkHeader := func(hdr, preBlock *ssa.BasicBlock) bool {
		if hdr == nil || preBlock == nil || hdr.Dominates(preBlock) {
			return false
		}
		if spawnSite.Block() != hdr && !hdr.Dominates(spawnSite.Block()) {
			return false
		}
		if canReachBlockWithoutJoins(outerFn, spawnSite, preBlock, joins) {
			return false
		}
		if canReachInstrWithoutBlock(outerFn, spawnSite, spawnSite, hdr) {
			return false
		}
		return true
	}

	switch x := outerVal.(type) {
	case *ssa.Phi:
		if x.Parent() != outerFn || len(x.Edges) != 2 || len(x.Block().Preds) != 2 {
			return 0, nil, false
		}
		hdr := x.Block()
		if isWordIntType(x.Type()) {
			for initIdx := range x.Edges {
				stepIdx := 1 - initIdx
				stepBin, ok := x.Edges[stepIdx].(*ssa.BinOp)
				if !ok || stepBin.Parent() != outerFn || (stepBin.Block() != hdr && !hdr.Dominates(stepBin.Block())) {
					continue
				}
				if _, ok := unitStepDelta(stepBin, x); !ok {
					continue
				}
				if checkHeader(hdr, hdr.Preds[initIdx]) {
					loopID := (int64(hdr.Index) << 20) | (int64(indexIn(x)) << 1)
					return loopID, hdr, true
				}
			}
			return 0, nil, false
		}
		ptr, ok := x.Type().Underlying().(*types.Pointer)
		if !ok || !isWordIntType(ptr.Elem()) || x.Referrers() == nil {
			return 0, nil, false
		}
		for _, r := range *x.Referrers() {
			switch rx := r.(type) {
			case *ssa.DebugRef:
			case *ssa.UnOp:
				if rx.Op != token.MUL || rx.X != x {
					return 0, nil, false
				}
			case *ssa.MakeClosure:
				fn, ok := rx.Fn.(*ssa.Function)
				if !ok {
					return 0, nil, false
				}
				for j, bnd := range rx.Bindings {
					if bnd == x && (j >= len(fn.FreeVars) || freeVarWritten(fn.FreeVars[j])) {
						return 0, nil, false
					}
				}
			default:
				return 0, nil, false
			}
		}
		for initIdx := range x.Edges {
			stepIdx := 1 - initIdx
			initAlloc, ok1 := x.Edges[initIdx].(*ssa.Alloc)
			nextAlloc, ok2 := x.Edges[stepIdx].(*ssa.Alloc)
			if !ok1 || !ok2 || !initAlloc.Heap || !nextAlloc.Heap || initAlloc.Parent() != outerFn || nextAlloc.Parent() != outerFn {
				continue
			}
			if hdr.Dominates(initAlloc.Block()) || (nextAlloc.Block() != hdr && !hdr.Dominates(nextAlloc.Block())) {
				continue
			}
			uInit := usesOfCell(initAlloc)
			if uInit.open || len(uInit.fields) > 0 || len(uInit.elems) > 0 || len(uInit.slices) > 0 || len(uInit.stores) == 0 {
				continue
			}
			initClean := true
			for _, s := range uInit.stores {
				if s.Addr != initAlloc || s.Parent() != outerFn || s.Block() == hdr || hdr.Dominates(s.Block()) {
					initClean = false
					break
				}
			}
			if !initClean || !isLoopNextCellStepOfPhi(nextAlloc, x) {
				continue
			}
			if checkHeader(hdr, hdr.Preds[initIdx]) {
				loopID := (int64(hdr.Index) << 20) | (int64(indexIn(x)) << 1)
				return loopID, hdr, true
			}
		}

	case *ssa.BinOp:
		if x.Parent() != outerFn || !isWordIntType(x.Type()) || !dominatesInstr(x, spawnSite) {
			return 0, nil, false
		}
		hdr := x.Block()
		for _, cand := range []ssa.Value{x.X, x.Y} {
			phi, ok := cand.(*ssa.Phi)
			if !ok || phi.Block() != hdr || len(phi.Edges) != 2 || len(hdr.Preds) != 2 {
				continue
			}
			if _, ok := unitStepDelta(x, phi); !ok {
				continue
			}
			for initIdx := range phi.Edges {
				if phi.Edges[1-initIdx] == x && checkHeader(hdr, hdr.Preds[initIdx]) {
					loopID := (int64(hdr.Index) << 20) | (int64(indexIn(phi)) << 1) | 1
					return loopID, hdr, true
				}
			}
		}
	}
	return 0, nil, false
}

func isLoopNextCellStepOfPhi(nextAlloc *ssa.Alloc, phi *ssa.Phi) bool {
	if nextAlloc.Referrers() == nil {
		return false
	}
	var stores []*ssa.Store
	b := nextAlloc.Block()
	for _, r := range *nextAlloc.Referrers() {
		switch rx := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Phi:
			if rx != phi {
				return false
			}
		case *ssa.UnOp:
			if rx.Op != token.MUL || rx.X != nextAlloc || rx.Block() != b {
				return false
			}
		case *ssa.Store:
			if rx.Addr != nextAlloc || rx.Block() != b || indexIn(rx) <= indexIn(nextAlloc) {
				return false
			}
			stores = append(stores, rx)
		default:
			return false
		}
	}
	isLoadOf := func(v, ptr ssa.Value) bool {
		u, ok := v.(*ssa.UnOp)
		return ok && u.Op == token.MUL && u.X == ptr
	}
	if len(stores) == 1 {
		bin, ok := stores[0].Val.(*ssa.BinOp)
		if !ok {
			return false
		}
		if _, ok := unitStepDelta(bin, bin.X); ok && isLoadOf(bin.X, phi) {
			return true
		}
		if _, ok := unitStepDelta(bin, bin.Y); ok && isLoadOf(bin.Y, phi) {
			return true
		}
		return false
	}
	if len(stores) == 2 {
		s0, s1 := stores[0], stores[1]
		if indexIn(s0) > indexIn(s1) {
			s0, s1 = s1, s0
		}
		if !isLoadOf(s0.Val, phi) {
			return false
		}
		bin, ok := s1.Val.(*ssa.BinOp)
		if !ok {
			return false
		}
		checkBase := func(base ssa.Value) bool {
			if _, ok := unitStepDelta(bin, base); !ok {
				return false
			}
			if isLoadOf(base, phi) {
				return true
			}
			if u, ok := base.(*ssa.UnOp); ok && u.Op == token.MUL && u.X == nextAlloc && u.Block() == b {
				idx := indexIn(u)
				return idx > indexIn(s0) && idx < indexIn(s1)
			}
			return false
		}
		return checkBase(bin.X) || checkBase(bin.Y)
	}
	return false
}

func canReachBlockWithoutJoins(fn *ssa.Function, start ssa.Instruction, targetBlock *ssa.BasicBlock, joins map[ssa.Instruction]bool) bool {
	if fn == nil || start == nil || targetBlock == nil {
		return true
	}
	sb := start.Block()
	for i := indexIn(start) + 1; i < len(sb.Instrs); i++ {
		if joins[sb.Instrs[i]] {
			if _, isIf := sb.Instrs[i].(*ssa.If); !isIf {
				return false
			}
		}
	}
	visited := make([]bool, len(fn.Blocks))
	var queue []*ssa.BasicBlock
	enqueueSuccs := func(b *ssa.BasicBlock) {
		live := liveSuccs(b)
		for si, succ := range b.Succs {
			if iff, ok := lastInstr(b).(*ssa.If); ok && joins[iff] {
				_, negated := unwrapBoolCond(iff.Cond)
				if (si == 0) != negated {
					continue
				}
			}
			if len(live) == 1 && live[0] != succ {
				continue
			}
			if succ.Index >= 0 && succ.Index < len(visited) && !visited[succ.Index] {
				visited[succ.Index] = true
				queue = append(queue, succ)
			}
		}
	}
	enqueueSuccs(sb)
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		if b == targetBlock {
			return true
		}
		joined := false
		for _, ins := range b.Instrs {
			if joins[ins] {
				if _, isIf := ins.(*ssa.If); !isIf {
					joined = true
					break
				}
			}
		}
		if !joined {
			enqueueSuccs(b)
		}
	}
	return false
}

func canReachInstrWithoutBlock(fn *ssa.Function, start, target ssa.Instruction, forbidden *ssa.BasicBlock) bool {
	return canReachInstrWithoutBlockOrJoins(fn, start, target, forbidden, nil)
}

func canReachInstrWithoutBlockOrJoins(fn *ssa.Function, start, target ssa.Instruction, forbidden *ssa.BasicBlock, joins map[ssa.Instruction]bool) bool {
	if fn == nil || start == nil || target == nil || forbidden == nil {
		return true
	}
	sb := start.Block()
	for i := indexIn(start) + 1; i < len(sb.Instrs); i++ {
		ins := sb.Instrs[i]
		if ins == target {
			return true
		}
		if joins[ins] {
			if _, isIf := ins.(*ssa.If); !isIf {
				return false
			}
		}
	}
	visited := make([]bool, len(fn.Blocks))
	var queue []*ssa.BasicBlock
	enqueueSuccs := func(b *ssa.BasicBlock) {
		live := liveSuccs(b)
		for si, succ := range b.Succs {
			if succ == forbidden {
				continue
			}
			if iff, ok := lastInstr(b).(*ssa.If); ok && joins[iff] {
				_, negated := unwrapBoolCond(iff.Cond)
				if (si == 0) != negated {
					continue
				}
			}
			if len(live) == 1 && live[0] != succ {
				continue
			}
			if succ.Index >= 0 && succ.Index < len(visited) && !visited[succ.Index] {
				visited[succ.Index] = true
				queue = append(queue, succ)
			}
		}
	}
	enqueueSuccs(sb)
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		joined := false
		for _, ins := range b.Instrs {
			if ins == target {
				return true
			}
			if joins[ins] {
				if _, isIf := ins.(*ssa.If); !isIf {
					joined = true
					break
				}
			}
		}
		if !joined {
			enqueueSuccs(b)
		}
	}
	return false
}

func isIndexStep(k stepKind) bool {
	return k == stIndex || k == stSlicePrefix || k == stLoopIndex || k == stAnyIndex
}

func indexStepOverlap(x, y step, loopDisjoint func(int64) bool) bool {
	if (x.k == stSlicePrefix && x.n <= 0) || (y.k == stSlicePrefix && y.n <= 0) {
		return false
	}
	switch {
	case x.k == stIndex && y.k == stIndex:
		return x.n == y.n
	case x.k == stIndex && y.k == stSlicePrefix:
		return x.n >= 0 && x.n < y.n
	case x.k == stSlicePrefix && y.k == stIndex:
		return y.n >= 0 && y.n < x.n
	case x.k == stLoopIndex && y.k == stLoopIndex:
		if x.n == y.n && loopDisjoint != nil && loopDisjoint(x.n) {
			return false
		}
		return true
	default:
		return true
	}
}

// overlap reports whether two places of one root may share a location. A place covers itself and
// what it holds inline (fields, array elements), not what it points to.
func overlap(a, b []step) bool {
	return overlapWithLoop(a, b, nil)
}

func overlapWithLoop(a, b []step, loopDisjoint func(int64) bool) bool {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		x, y := a[i], b[i]
		if x.k == stAll || y.k == stAll {
			return true
		}
		switch {
		case x.k == stField && y.k == stField:
			if x.n != y.n {
				return false
			}
		case isIndexStep(x.k) && isIndexStep(y.k):
			if !indexStepOverlap(x, y, loopDisjoint) {
				return false
			}
		case x.k == stDeref && y.k == stDeref:
		default:
			// Paths that disagree on the shape of the memory: assume they meet.
			return true
		}
	}
	rest := a[n:]
	if len(b) > n {
		rest = b[n:]
	}
	for _, s := range rest {
		switch s.k {
		case stAll:
			return true
		case stDeref:
			return false
		case stSlicePrefix:
			if s.n <= 0 {
				return false
			}
		}
	}
	return true
}

// excluded reports whether a mutex held at both accesses keeps them apart: both hold it, and at
// least one holds it exclusively.
func excluded(a, b lockset) bool {
	for p, m := range a {
		if n, ok := b[p]; ok && (m == lockWrite || n == lockWrite) {
			return true
		}
	}
	return false
}

func conflicts(a, b access) bool {
	return conflictsWithLoop(a, b, nil)
}

func conflictsWithLoop(a, b access, loopDisjoint func(int64) bool) bool {
	if a.root != b.root || (!a.write && !b.write) || (a.atomic && b.atomic) {
		return false
	}
	if !overlapWithLoop(a.path, b.path, loopDisjoint) {
		return false
	}
	return !excluded(a.locks, b.locks)
}

// moveVerdict is the outcome of refining one move.
type moveVerdict struct {
	// benign: no access the goroutine makes conflicts with one the sender makes after the move.
	benign bool
	// covered are the sender's values the walk followed; a use of any other value stays reported.
	covered map[ssa.Value]bool
}

// childOf returns the goroutine body a move starts and how its parameters and free variables are
// bound, when the move is one the refinement understands: a go statement, a spawner's callback, or a
// callback a callee of this package starts. selfConcurrent says whether the body may run concurrently
// with itself (a handler, or a callee that may start its callback more than once).
func childOf(st *state, t transfer) (fn *ssa.Function, mc *ssa.MakeClosure, call ssa.CallInstruction, argIdx int, selfConcurrent bool, ok bool) {
	switch ins := t.instr.(type) {
	case *ssa.Go:
		if t.index < 0 {
			m, isMC := t.v.(*ssa.MakeClosure)
			if !isMC || ins.Call.Value != m {
				return nil, nil, nil, 0, false, false
			}
			f, _ := m.Fn.(*ssa.Function)
			return f, m, ins, -1, false, f != nil
		}
		callee := ins.Call.StaticCallee()
		if callee == nil || ins.Call.IsInvoke() {
			return nil, nil, nil, 0, false, false
		}
		return callee, nil, ins, t.index, false, true
	case *ssa.Call:
		m, isMC := t.v.(*ssa.MakeClosure)
		if !isMC {
			return nil, nil, nil, 0, false, false
		}
		f, _ := m.Fn.(*ssa.Function)
		if f == nil {
			return nil, nil, nil, 0, false, false
		}
		if sp, ok := spawnOf(ins); ok && sp.via != "" && sp.fn == m {
			return f, m, ins, -1, sp.repeated, true
		}
		if callee := ins.Call.StaticCallee(); callee != nil {
			sum := st.summary(callee)
			for i, a := range ins.Call.Args {
				if a == m && sum.spawns(i) {
					return f, m, ins, -1, !sum.singleSpawn(), true
				}
			}
		}
	}
	return nil, nil, nil, 0, false, false
}

// refineMove decides whether the uses after move t can race with the goroutine t starts.
//
// extra are the other values of the moved region; a fresh allocation among them that the goroutine
// provably cannot reach is covered too.
func (st *state) refineMove(g *graph, t transfer, joins map[ssa.Instruction]bool, extra []ssa.Value) moveVerdict {
	fn, mc, call, argIdx, selfConcurrent, ok := childOf(st, t)
	if !ok {
		return moveVerdict{}
	}
	parent := &fpFrame{fn: g.fn}
	var gs *guardState
	if st.hasLockOps(st.allFns) {
		gs = st.ensureGuardSummaries(st.allFns)
	}
	loopHdrs := map[int64]*ssa.BasicBlock{}
	var walkSpawnSite ssa.Instruction
	if !selfConcurrent {
		walkSpawnSite = t.instr
	}
	child := &fpWalker{st: st, gs: gs, budget: fpBudget, spawnSite: walkSpawnSite, joins: joins, loopHdrs: loopHdrs, covered: map[ssa.Value]bool{}, seen: map[fpSeenKey]bool{}}
	roots := map[ssa.Value]bool{}
	helperClosures := map[ssa.Value]bool{}
	var bind func(inner *fpFrame, local ssa.Value, outer ssa.Value) bool
	bind = func(inner *fpFrame, local ssa.Value, outer ssa.Value) bool {
		if g.val(outer) == nil || g.isAutoSyncCapture(t.instr, outer) {
			return true // carries no memory the move checks
		}
		if helperMC, isMC := outer.(*ssa.MakeClosure); isMC && helperMC.Parent() == g.fn && freeVarOnlyCalledDirectly(local) {
			helperFn, ok := helperMC.Fn.(*ssa.Function)
			if !ok || len(helperFn.Blocks) == 0 || inner.inChain(helperFn) {
				return false
			}
			helperClosures[helperMC] = true
			helperFrame := &fpFrame{fn: helperFn, parent: parent, mc: helperMC, site: t.instr}
			for k, hb := range helperMC.Bindings {
				if k >= len(helperFn.FreeVars) || !bind(helperFrame, helperFn.FreeVars[k], hb) {
					return false
				}
			}
			return true
		}
		if d, loads, ok := unwrapDerivedPlaceCell(local, outer, walkSpawnSite, joins, loopHdrs); ok {
			roots[d.root] = true
			if inner.fn == nil || len(inner.fn.Blocks) == 0 {
				return false
			}
			for _, ld := range loads {
				child.walk(inner, ld, d)
				if child.top {
					return false
				}
			}
			return true
		}
		d, ok := moveRootAt(outer, walkSpawnSite, joins, loopHdrs)
		if !ok {
			return false
		}
		roots[d.root] = true
		if inner.fn == nil || len(inner.fn.Blocks) == 0 {
			return false
		}
		child.walk(inner, local, d)
		return !child.top
	}
	switch {
	case mc != nil:
		if len(fn.Blocks) == 0 {
			return moveVerdict{}
		}
		inner := &fpFrame{fn: fn, parent: parent, mc: mc, call: call, site: t.instr}
		for j, b := range mc.Bindings {
			if !bind(inner, fn.FreeVars[j], b) {
				return moveVerdict{}
			}
		}
		// A go statement's arguments are the literal's parameters: memory they carry is touched
		// by the same goroutine.
		if goIns, isGo := call.(*ssa.Go); isGo {
			for i, a := range goIns.Call.Args {
				if i >= len(fn.Params) || !bind(inner, fn.Params[i], a) {
					return moveVerdict{}
				}
			}
		}
	default:
		args := call.Common().Args
		if argIdx < 0 || argIdx >= len(args) {
			return moveVerdict{}
		}
		if len(fn.Blocks) == 0 || fn.Pkg != g.fn.Pkg || fn.Synthetic != "" || argIdx >= len(fn.Params) {
			return moveVerdict{}
		}
		// An empty/unused parameter in a goroutine sink (`func sink(p *T) {}`) stands in for an
		// opaque consumer in move-semantics contracts; keep the move check conservative on it.
		if !hasNonDebugReferrer(fn.Params[argIdx]) {
			return moveVerdict{}
		}
		inner := &fpFrame{fn: fn, parent: parent, call: call, site: t.instr}
		if subMC, isMC := call.Common().Value.(*ssa.MakeClosure); isMC {
			inner.mc = subMC
			for j, b := range subMC.Bindings {
				if j >= len(fn.FreeVars) || !bind(inner, fn.FreeVars[j], b) {
					return moveVerdict{}
				}
			}
		}
		// Every argument the go statement passes is part of what the goroutine touches.
		for i, a := range args {
			if i >= len(fn.Params) || !bind(inner, fn.Params[i], a) {
				return moveVerdict{}
			}
		}
	}
	if child.top {
		return moveVerdict{}
	}
	if len(roots) == 0 {
		return moveVerdict{}
	}

	sender := &fpWalker{st: st, gs: gs, budget: fpBudget, allowReturn: true, spawnSite: walkSpawnSite, joins: joins, loopHdrs: loopHdrs, covered: map[ssa.Value]bool{}, seen: map[fpSeenKey]bool{}}
	for hk := range helperClosures {
		sender.covered[hk] = true
	}
	for r := range roots {
		if r.Parent() != g.fn {
			return moveVerdict{}
		}
		sender.walk(parent, r, desig{root: r})
		if sender.top {
			return moveVerdict{}
		}
	}

	// The sender's accesses that can run after the move without a join in between.
	afterByRoot := map[ssa.Value]map[ssa.Instruction]bool{}
	for r := range roots {
		def, _ := r.(ssa.Instruction)
		afterByRoot[r] = instrsAfter(g, t, joins, def)
	}
	if !st.aliasAccessesBenign(g, t, sender.acc, child.acc, afterByRoot, selfConcurrent, loopHdrs, joins) {
		return moveVerdict{}
	}
	if selfConcurrent {
		for i, a := range child.acc {
			for _, c := range child.acc[i:] {
				if conflicts(a, c) {
					return moveVerdict{}
				}
			}
		}
	}
	// A fresh allocation of the sender that only shares the moved region's alias class (two
	// closures capture it beside the moved memory, say) is memory the goroutine cannot reach when
	// its whole use can be followed without its address leaving what the walk models: then the
	// goroutine has no access to it, and the sender's uses of it are not uses of moved memory.
	for _, v := range extra {
		if roots[v] || sender.covered[v] {
			continue
		}
		switch v.(type) {
		case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeMap:
		default:
			continue
		}
		if v.Parent() != g.fn {
			continue
		}
		// The value held in a moved variable's cell (`a := make([]int, 2)` captured by the
		// goroutine is `c = new []int; *c = makeslice`): its direct uses designate what the root
		// cell's contents designate, so they are walked as that place and checked against the
		// goroutine's accesses like any other access of the sender.
		if cell, store, ok := rootCellOf(v, roots); ok {
			alias := &fpWalker{st: st, gs: gs, budget: fpBudget, allowReturn: true, spawnSite: walkSpawnSite, joins: joins, loopHdrs: loopHdrs, rootStores: map[*ssa.Store]bool{store: true}, covered: map[ssa.Value]bool{}, seen: map[fpSeenKey]bool{}}
			alias.walk(parent, v, desig{root: cell, path: []step{{k: stDeref}}})
			if alias.top || !st.aliasAccessesBenign(g, t, alias.acc, child.acc, afterByRoot, selfConcurrent, loopHdrs, joins) {
				continue
			}
			for c := range alias.covered {
				sender.covered[c] = true
			}
			continue
		}
		other := &fpWalker{st: st, gs: gs, budget: fpBudget, covered: map[ssa.Value]bool{}, seen: map[fpSeenKey]bool{}}
		other.walk(parent, v, desig{root: v})
		if other.top {
			continue
		}
		for c := range other.covered {
			sender.covered[c] = true
		}
	}
	return moveVerdict{benign: true, covered: sender.covered}
}

// aliasAccessesBenign reports whether none of the sender's accesses that can run after the move t
// (afterByRoot, keyed by each access's root; an access at exit always counts) conflicts with an
// access of the goroutine. A loop-index place is disjoint from the goroutine's only when the
// access cannot reach the spawn again without passing the loop header or a join.
func (st *state) aliasAccessesBenign(g *graph, t transfer, senderAcc, childAcc []access, afterByRoot map[ssa.Value]map[ssa.Instruction]bool, selfConcurrent bool, loopHdrs map[int64]*ssa.BasicBlock, joins map[ssa.Instruction]bool) bool {
	for _, a := range senderAcc {
		if !a.exit && !afterByRoot[a.root][a.site] {
			continue
		}
		var loopDisjoint func(int64) bool
		if !selfConcurrent && !a.exit && a.site != nil && len(loopHdrs) > 0 &&
			!(a.site != t.instr && st.isUnjoinedConcurrentSpawnBefore(g, a.site, t.instr)) {
			site := a.site
			loopDisjoint = func(loopID int64) bool {
				hdr := loopHdrs[loopID]
				return hdr != nil && !canReachInstrWithoutBlockOrJoins(g.fn, t.instr, site, hdr, joins)
			}
		}
		for _, c := range childAcc {
			if conflictsWithLoop(a, c, loopDisjoint) {
				return false
			}
		}
	}
	return true
}

// rootCellOf returns the moved root cell v is stored into, and the store, when that store is the
// only one into the cell (a local variable's heap cell, captured by the goroutine) and the cell's
// address is used only by loads, that store and closures: then v and the cell's contents are the
// same memory, and the root's walk follows every use of the contents.
func rootCellOf(v ssa.Value, roots map[ssa.Value]bool) (*ssa.Alloc, *ssa.Store, bool) {
	refs := v.Referrers()
	if refs == nil {
		return nil, nil, false
	}
	var found *ssa.Store
	for _, r := range *refs {
		s, ok := r.(*ssa.Store)
		if !ok || s.Val != v {
			continue
		}
		cell, ok := s.Addr.(*ssa.Alloc)
		if !ok || !roots[cell] {
			continue
		}
		if found != nil {
			return nil, nil, false
		}
		found = s
	}
	if found == nil {
		return nil, nil, false
	}
	cell := found.Addr.(*ssa.Alloc)
	uses := usesOfCell(cell)
	if uses.open || len(uses.fields) > 0 || len(uses.elems) > 0 || len(uses.slices) > 0 || len(uses.stores) != 1 || uses.stores[0] != found {
		return nil, nil, false
	}
	return cell, found, true
}

// instrsAfter returns the instructions of g.fn that can run after the move t on some path that
// passes neither a join of the move nor kill (the root's allocation, which makes fresh memory).
func instrsAfter(g *graph, t transfer, joins map[ssa.Instruction]bool, kill ssa.Instruction) map[ssa.Instruction]bool {
	out := map[ssa.Instruction]bool{}
	fn := g.fn
	visited := make([]bool, len(fn.Blocks))
	type item struct {
		b    *ssa.BasicBlock
		from int
	}
	var queue []item
	if t.caseBlock != nil {
		queue = append(queue, item{t.caseBlock, 0})
	} else {
		queue = append(queue, item{t.instr.Block(), g.idxOf(t.instr) + 1})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		killed := false
		for i := it.from; i < len(it.b.Instrs); i++ {
			ins := it.b.Instrs[i]
			if ins == kill {
				killed = true
				break
			}
			out[ins] = true
			if joins[ins] {
				if _, isIf := ins.(*ssa.If); !isIf {
					killed = true
					break
				}
			}
		}
		if killed {
			continue
		}
		live := liveSuccs(it.b)
		for si, succ := range it.b.Succs {
			if iff, ok := lastInstr(it.b).(*ssa.If); ok && joins[iff] {
				_, negated := unwrapBoolCond(iff.Cond)
				if (si == 0) != negated {
					continue
				}
			}
			if len(live) == 1 && live[0] != succ {
				continue
			}
			if succ.Index >= 0 && succ.Index < len(visited) {
				if visited[succ.Index] {
					continue
				}
				visited[succ.Index] = true
			}
			queue = append(queue, item{succ, 0})
		}
	}
	return out
}

// foldedCond is the value of a branch condition the compiler would fold: a boolean constant, a
// comparison of two constants, or the negation of either. Code behind the branch not taken never
// runs, so it cannot race.
func foldedCond(v ssa.Value) (bool, bool) {
	if cv, ok := foldedConstValue(v); ok && cv != nil && cv.Kind() == constant.Bool {
		return constant.BoolVal(cv), true
	}
	switch x := v.(type) {
	case *ssa.UnOp:
		if x.Op == token.NOT {
			if b, ok := foldedCond(x.X); ok {
				return !b, true
			}
		}
	case *ssa.BinOp:
		a, aok := foldedConstValue(x.X)
		b, bok := foldedConstValue(x.Y)
		if !aok || !bok || a == nil || b == nil {
			return false, false
		}
		switch x.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			if a.Kind() == constant.Unknown || b.Kind() == constant.Unknown {
				return false, false
			}
			return constant.Compare(a, x.Op, b), true
		}
	}
	return false, false
}

// foldedConstValue resolves compile-time constants and loads from provably constant local
// allocations: either a fresh allocation read within the same basic block before any escape or
// non-constant store, or a non-escaping local allocation initialized at most once with a constant
// dominating every read/closure site.
func foldedConstValue(v ssa.Value) (constant.Value, bool) {
	switch x := v.(type) {
	case *ssa.Const:
		if x.Value == nil {
			return nil, false
		}
		return x.Value, true
	case *ssa.UnOp:
		if x.Op != token.MUL {
			return nil, false
		}
		if alloc, ok := x.X.(*ssa.Alloc); ok {
			if cv, ok := intraBlockFreshConst(alloc, x); ok {
				return cv, true
			}
			if cv, storeIns, ok := immutableAllocConst(alloc); ok {
				if storeIns == alloc || strictlyDominatesInstr(storeIns, x) {
					return cv, true
				}
			}
			return nil, false
		}
		if fv, ok := x.X.(*ssa.FreeVar); ok {
			return freeVarConst(fv)
		}
	}
	return nil, false
}

func zeroConstOf(t types.Type) (constant.Value, bool) {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return nil, false
	}
	info := b.Info()
	switch {
	case info&types.IsBoolean != 0:
		return constant.MakeBool(false), true
	case info&types.IsInteger != 0:
		return constant.MakeInt64(0), true
	case info&types.IsFloat != 0:
		return constant.MakeFloat64(0), true
	case info&types.IsString != 0:
		return constant.MakeString(""), true
	}
	return nil, false
}

func intraBlockFreshConst(alloc *ssa.Alloc, ld *ssa.UnOp) (constant.Value, bool) {
	b := alloc.Block()
	if b == nil || ld.Block() != b {
		return nil, false
	}
	aIdx, lIdx := -1, -1
	for i, ins := range b.Instrs {
		if ins == alloc {
			aIdx = i
		} else if ins == ld {
			lIdx = i
			break
		}
	}
	if aIdx < 0 || lIdx <= aIdx {
		return nil, false
	}
	cur, ok := zeroConstOf(ld.Type())
	if !ok {
		return nil, false
	}
	var ops []*ssa.Value
	for i := aIdx + 1; i < lIdx; i++ {
		ins := b.Instrs[i]
		switch s := ins.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.UnOp:
			if s.Op == token.MUL && s.X == alloc {
				continue
			}
		case *ssa.Store:
			if s.Addr == alloc && s.Val != alloc {
				c, isConst := s.Val.(*ssa.Const)
				if !isConst || c.Value == nil {
					return nil, false
				}
				cur = c.Value
				continue
			}
		}
		ops = ins.Operands(ops[:0])
		for _, op := range ops {
			if op != nil && *op == alloc {
				return nil, false
			}
		}
	}
	return cur, true
}

func immutableAllocConst(alloc *ssa.Alloc) (constant.Value, ssa.Instruction, bool) {
	if alloc == nil || alloc.Referrers() == nil {
		return nil, nil, false
	}
	ptr, isPtr := alloc.Type().Underlying().(*types.Pointer)
	if !isPtr {
		return nil, nil, false
	}
	zero, ok := zeroConstOf(ptr.Elem())
	if !ok {
		return nil, nil, false
	}
	val := zero
	var storeIns ssa.Instruction = alloc
	stores := 0
	for _, r := range *alloc.Referrers() {
		switch r := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if r.Op != token.MUL || r.X != alloc {
				return nil, nil, false
			}
		case *ssa.Store:
			if r.Addr != alloc || r.Val == alloc {
				return nil, nil, false
			}
			stores++
			if stores > 1 {
				return nil, nil, false
			}
			c, isConst := r.Val.(*ssa.Const)
			if !isConst || c.Value == nil {
				return nil, nil, false
			}
			val = c.Value
			storeIns = r
		case *ssa.MakeClosure:
			mcFn, ok := r.Fn.(*ssa.Function)
			if !ok || len(mcFn.Blocks) == 0 {
				return nil, nil, false
			}
			for j, b := range r.Bindings {
				if b != alloc {
					continue
				}
				if j >= len(mcFn.FreeVars) || !freeVarReadOnlyLoads(mcFn.FreeVars[j]) {
					return nil, nil, false
				}
			}
		default:
			return nil, nil, false
		}
	}
	return val, storeIns, true
}

func freeVarReadOnlyLoads(fv *ssa.FreeVar) bool {
	if fv == nil || fv.Referrers() == nil {
		return false
	}
	for _, r := range *fv.Referrers() {
		switch r := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if r.Op != token.MUL || r.X != fv {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func freeVarConst(fv *ssa.FreeVar) (constant.Value, bool) {
	fn := fv.Parent()
	if fn == nil || fn.Parent() == nil {
		return nil, false
	}
	j := indexOfFreeVar(fn, fv)
	if j < 0 {
		return nil, false
	}
	outer := fn.Parent()
	var alloc *ssa.Alloc
	var mcs []*ssa.MakeClosure
	for _, b := range outer.Blocks {
		for _, ins := range b.Instrs {
			mc, ok := ins.(*ssa.MakeClosure)
			if !ok || mc.Fn != fn {
				continue
			}
			if j >= len(mc.Bindings) {
				return nil, false
			}
			a, ok := mc.Bindings[j].(*ssa.Alloc)
			if !ok || a.Parent() != outer || (alloc != nil && a != alloc) {
				return nil, false
			}
			alloc = a
			mcs = append(mcs, mc)
		}
	}
	if alloc == nil || len(mcs) == 0 {
		return nil, false
	}
	cv, storeIns, ok := immutableAllocConst(alloc)
	if !ok {
		return nil, false
	}
	if storeIns != alloc {
		for _, mc := range mcs {
			if !strictlyDominatesInstr(storeIns, mc) {
				return nil, false
			}
		}
	}
	return cv, true
}

func strictlyDominatesInstr(dom, use ssa.Instruction) bool {
	if dom == nil || use == nil {
		return false
	}
	db, ub := dom.Block(), use.Block()
	if db == nil || ub == nil || db.Parent() != ub.Parent() {
		return false
	}
	if db == ub {
		di, ui := -1, -1
		for i, ins := range db.Instrs {
			if ins == dom {
				di = i
			} else if ins == use {
				ui = i
				break
			}
		}
		return di >= 0 && ui > di
	}
	return db.Dominates(ub)
}

func hasDeferInstr(fn *ssa.Function) bool {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if _, ok := ins.(*ssa.Defer); ok {
				return true
			}
		}
	}
	return false
}

func (w *fpWalker) isNamedReturnSelfStore(f *fpFrame, r *ssa.Store) bool {
	if f.parent != nil || f.fn == nil || hasDeferInstr(f.fn) || w.st == nil || w.st.pass == nil {
		return false
	}
	alloc, ok := r.Addr.(*ssa.Alloc)
	if !ok || alloc.Parent() != f.fn {
		return false
	}
	ld, ok := r.Val.(*ssa.UnOp)
	if !ok || ld.Op != token.MUL || ld.X != alloc {
		return false
	}
	b := r.Block()
	if b == nil || ld.Block() != b {
		return false
	}
	if _, isRet := lastInstr(b).(*ssa.Return); !isRet {
		return false
	}
	lIdx, rIdx := -1, -1
	for i, ins := range b.Instrs {
		if ins == ld {
			lIdx = i
		} else if ins == r {
			rIdx = i
			break
		}
	}
	if lIdx < 0 || rIdx <= lIdx {
		return false
	}
	for i := lIdx + 1; i < rIdx; i++ {
		switch s := b.Instrs[i].(type) {
		case *ssa.DebugRef, *ssa.MakeInterface, *ssa.ChangeType, *ssa.ChangeInterface, *ssa.Convert:
		case *ssa.UnOp:
			if s.Op != token.MUL {
				return false
			}
		case *ssa.Store:
			if s.Addr == alloc {
				return false
			}
		default:
			return false
		}
	}
	return posInReturnStmt(w.st.pass.Files, r.Pos())
}

func posInReturnStmt(files []*ast.File, pos token.Pos) bool {
	if pos == token.NoPos {
		return false
	}
	for _, f := range files {
		if pos < f.Pos() || pos > f.End() {
			continue
		}
		inRet := false
		inAssign := false
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			if pos < n.Pos() || pos > n.End() {
				return false
			}
			switch n.(type) {
			case *ast.ReturnStmt:
				inRet = true
			case *ast.AssignStmt:
				if inRet {
					inAssign = true
				}
			case *ast.FuncLit:
				inRet = false
				inAssign = false
			}
			return true
		})
		return inRet && !inAssign
	}
	return false
}

// liveSuccs returns the successors of b that a run can reach: both, or the one a folded If takes.
func liveSuccs(b *ssa.BasicBlock) []*ssa.BasicBlock {
	if len(b.Instrs) > 0 {
		if iff, ok := lastInstr(b).(*ssa.If); ok && len(b.Succs) == 2 {
			if v, ok := foldedCond(iff.Cond); ok {
				if v {
					return b.Succs[:1]
				}
				return b.Succs[1:]
			}
		}
	}
	return b.Succs
}

// liveBlocks marks the blocks of fn a run can reach, following only taken branches of folded
// conditions. The recover block is reachable by a panic.
func liveBlocks(fn *ssa.Function) []bool {
	live := make([]bool, len(fn.Blocks))
	var stack []*ssa.BasicBlock
	push := func(b *ssa.BasicBlock) {
		if b != nil && b.Index >= 0 && b.Index < len(live) && !live[b.Index] {
			live[b.Index] = true
			stack = append(stack, b)
		}
	}
	if len(fn.Blocks) > 0 {
		push(fn.Blocks[0])
	}
	push(fn.Recover)
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, succ := range liveSuccs(b) {
			push(succ)
		}
	}
	return live
}

// isLive reports whether ins can run at all in its function.
func (w *fpWalker) isLive(ins ssa.Instruction) bool {
	b := ins.Block()
	if b == nil || b.Parent() == nil {
		return true
	}
	fn := b.Parent()
	if w.live == nil {
		w.live = map[*ssa.Function][]bool{}
	}
	lv, ok := w.live[fn]
	if !ok {
		lv = liveBlocks(fn)
		w.live[fn] = lv
	}
	return b.Index < 0 || b.Index >= len(lv) || lv[b.Index]
}

func hasNonDebugReferrer(v ssa.Value) bool {
	if v == nil || v.Referrers() == nil {
		return false
	}
	for _, r := range *v.Referrers() {
		if _, dbg := r.(*ssa.DebugRef); !dbg {
			return true
		}
	}
	return false
}
