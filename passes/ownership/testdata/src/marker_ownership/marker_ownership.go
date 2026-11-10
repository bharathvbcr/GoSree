package marker_ownership

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/bharathvbcr/gocap/cap"
)

// 1. Stack-scoped borrow escape via cap.BorrowMut to goroutine -> CAP003
func TestMarkerBorrowGoroutineEscape() { // want TestMarkerBorrowGoroutineEscape:"ownership summary"
	x := 10
	b := cap.BorrowMut(&x)
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	}()
}

// 2. Stack-scoped borrow escape via cap.Borrow to goroutine argument -> CAP003
func Worker(r cap.Ref[int]) {} // want Worker:"ownership summary"

func TestMarkerBorrowGoArgEscape() { // want TestMarkerBorrowGoArgEscape:"ownership summary"
	x := 10
	b := cap.Borrow(&x)
	go Worker(b) // want `CAP003: stack-scoped borrow 'b' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 3. Stack-scoped borrow sent on channel -> CAP003
func TestMarkerBorrowChanSend(ch chan cap.Mut[int]) { // want TestMarkerBorrowChanSend:"ownership summary"
	x := 10
	b := cap.BorrowMut(&x)
	ch <- b // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
}

// 4. Use-after-move via cap.Move -> SAFE003
type Node struct {
	Val int
}

func TestUseAfterMoveIntrinsic() int { // want TestUseAfterMoveIntrinsic:"ownership summary"
	n := &Node{Val: 42}
	y := cap.Move(n)
	_ = y
	return n.Val // want `SAFE003: composite literal is used after it was moved by cap.Move \(line \d+\)`
}

// 5. Channel element immutability -> SAFE005
//gocap:imm
type BadChanStruct struct { // want `SAFE005: //gocap:imm type BadChanStruct is not immutable: field ch has type chan \*int`
	ch chan *int
}

//gocap:imm
type GoodChanStruct struct { // want GoodChanStruct:"imm"
	ch chan int
}

// ============================================================================
// Round 2 Deep Stress Tests (Rust-grade parity)
// ============================================================================

// 6 (Hole 5A): //gocap:imm struct containing array of mutable channel [2]chan *int -> SAFE005
//gocap:imm
type BadArrayChanStruct struct { // want `SAFE005: //gocap:imm type BadArrayChanStruct is not immutable: field chs has type \[2\]chan \*int`
	chs [2]chan *int
}

// 7 (Hole 5A): //gocap:imm struct containing inline struct with exported field -> SAFE005
//gocap:imm
type BadInlineExportedStruct struct { // want `SAFE005: //gocap:imm type BadInlineExportedStruct is not immutable: field inner.Exported is exported, so other packages could write it`
	inner struct {
		Exported int
	}
}

// 8 (Hole 5B): Mutating //gocap:imm field via helper call mutateField(&c.rate) -> SAFE005
//gocap:imm
type ImmConfig struct { // want ImmConfig:"imm"
	rate int
}

func mutateField(p *int) {
	*p = 999
}

func TestImmMutatedViaFieldAddrCall(c *ImmConfig) { // want TestImmMutatedViaFieldAddrCall:"ownership summary"
	mutateField(&c.rate) // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
}

// 9 (Hole 5B): Mutating //gocap:imm field via branch-merged Phi pointer -> SAFE005
func TestImmMutatedViaPhiPtr(c *ImmConfig, cond bool) { // want TestImmMutatedViaPhiPtr:"ownership summary"
	var p *int
	if cond {
		p = &c.rate
	} else {
		p = &c.rate
	}
	*p = 999 // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
}

// 10 (Hole 6): Stack-scoped borrow captured by time.AfterFunc spawner -> CAP003
func TestBorrowEscapeViaAfterFunc() { // want TestBorrowEscapeViaAfterFunc:"ownership summary"
	x := 10
	b := cap.BorrowMut(&x)
	time.AfterFunc(time.Second, func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	})
}

// 11 (Hole 6): Stack-scoped borrow captured by http.HandleFunc handler -> CAP003
func TestBorrowEscapeViaHandleFunc() { // want TestBorrowEscapeViaHandleFunc:"ownership summary"
	x := 10
	b := cap.BorrowMut(&x)
	http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	})
}

// 12 (Hole 7): cap.Move inline in channel send and rebinding to new owner (must not false-positive SAFE003 on new owner)
func TestMoveRebindAndInlineSend(ch chan *Node) int { // want TestMoveRebindAndInlineSend:"ownership summary"
	n1 := &Node{Val: 10}
	k := cap.Move(n1)
	k.Val = 20

	n2 := &Node{Val: 30}
	ch <- cap.Move(n2)
	return k.Val
}

// ============================================================================
// Round 3 Deep Stress Tests (Closing Known Limitations)
// ============================================================================

// 13 (Item 12): Trailing directive on closing line of a multi-line statement attaches to that statement
func TestTrailingUnsafeOnMultilineGo() { // want TestTrailingUnsafeOnMultilineGo:"ownership summary"
	x := 0
	_ = x
	go func() {
		x = 1
	}() //gocap:unsafe intentional suppression on closing line of multiline go
	x = 2

	y := 0
	_ = y
	go func() { y = 1 }()
	y = 2 // want `SAFE003: variable y is used after it was captured by a goroutine`
}

// 14 (Item 5): SAFE008 catches interface method calls and dynamic function calls on shared handler state
type KVStore interface {
	Put(k string, v int)
}

type memKV struct{ n int }

func (m *memKV) Put(k string, v int) { m.n = v } // want Put:"ownership summary"

func TestHandlerInterfaceAndDynamicWrite() { // want TestHandlerInterfaceAndDynamicWrite:"ownership summary"
	var st KVStore = &memKV{}
	var fn func([]int) = func(xs []int) { xs[0] = 1 }
	s := []int{1, 2, 3}
	http.HandleFunc("/if", func(w http.ResponseWriter, r *http.Request) {
		st.Put("k", 1) // want `SAFE008: captured variable st is passed to interface method Put, which may write it`
		fn(s)          // want `SAFE008: captured variable s is passed to a dynamic function call, which may write it`
	})
}

// 15 (Item 10): SAFE006 catches package variable mutated outside init via helper call
var globalViaHelper int // want `SAFE006: package variable globalViaHelper is written outside init`

func setGlobalPtr(p *int) { *p = 42 }

func MutateGlobalViaHelper() { // want MutateGlobalViaHelper:"ownership summary"
	setGlobalPtr(&globalViaHelper)
}

// 16 (Item 3 & 7): SAFE009 in-place closure without capturing mu, mutex alias, and TryLock bool variable
func TestInPlaceClosureWithoutMuCapture() { // want TestInPlaceClosureWithoutMuCapture:"ownership summary"
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	_ = x
	go func() {
		mu.Lock()
		x = 1
		mu.Unlock()
	}()
	mu.Lock()
	func() {
		x = 2
	}()
	mu.Unlock()
}

func TestGuardedTryLockBoolVarAndAlias() { // want TestGuardedTryLockBoolVarAndAlias:"ownership summary"
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	_ = x
	go func() {
		m := &mu
		m.Lock()
		x = 1
		m.Unlock()
	}()
	ok := mu.TryLock()
	if ok == true {
		x = 2
		mu.Unlock()
	}
}

// 17 (Item 2): Selective //safe:guarded_by on multi-variable declaration
func TestSelectiveGuardedBy() { // want TestSelectiveGuardedBy:"ownership summary"
	var rw sync.RWMutex
	var x, y int = 0, 1 //safe:guarded_by rw x
	go func() {
		rw.Lock()
		x = 2
		rw.Unlock()
	}()
	rw.RLock()
	_ = x
	rw.RUnlock()
	y = 3
	_ = y
}

// 18 (Item 6): Tuple result per-index linking and freshness
func splitPair(shared, fresh *Node) (*Node, *Node) {
	return shared, &Node{Val: fresh.Val}
}

func TestTupleResultDisjointLink(ch chan *Node, shared *Node) { // want TestTupleResultDisjointLink:"ownership summary"
	local := &Node{Val: 1}
	r0, r1 := splitPair(shared, local)
	_ = r0
	ch <- r1
	ch <- r0 // want `SAFE002: .* is sent on a channel but is not owned here: it may alias parameter shared`
}

// 19 (Item 13): Range-over-func iterator obtained as a func value keeps ownership
func TestDynamicRangeOverFuncKeepsOwnership(ch chan *Node, iter func(func(int) bool)) { // want TestDynamicRangeOverFuncKeepsOwnership:"ownership summary"
	n := &Node{Val: 1}
	sum := 0
	for v := range iter {
		sum += v + n.Val
	}
	n.Val = sum
	ch <- n
}

// 20 (Item 9): Timer.Stop and context.AfterFunc stop() on true branch restores ownership
func TestTimerStopTrueBranchRestoresOwnership(ctx context.Context) { // want TestTimerStopTrueBranchRestoresOwnership:"ownership summary"
	n1 := &Node{Val: 1}
	t := time.AfterFunc(time.Hour, func() { n1.Val = 2 })
	if t.Stop() {
		n1.Val = 3
	} else {
		n1.Val = 4 // want `SAFE003: variable n1 is used after it was captured by a goroutine started by time\.AfterFunc`
	}

	n2 := &Node{Val: 1}
	stop := context.AfterFunc(ctx, func() { n2.Val = 2 })
	if !stop() {
		return
	}
	n2.Val = 3
}

// 21 (Item 4): Custom spawner function acts as a goroutine boundary
func SubmitTask(task func()) { // want SubmitTask:"ownership summary"
	go task()
}

func TestCustomSpawnerBoundary() { // want TestCustomSpawnerBoundary:"ownership summary"
	n := &Node{Val: 1}
	SubmitTask(func() { n.Val = 2 })
	n.Val = 3 // want `SAFE003: variable n is used after it was captured by a goroutine`
}

// ============================================================================
// ROUND 4 ADVERSARIAL STRESS TESTS
// ============================================================================

// 22. CAP003: Moving a borrow via cap.Move, passing a borrow to cap.Iso parameter, or inline cap.BorrowMut on channel
func ConsumeIsoInt(p cap.Iso[int]) { // want ConsumeIsoInt:"ownership summary"
	_ = p
}

func TestBorrowMoveAndIsoConversion(ch chan *int) { // want TestBorrowMoveAndIsoConversion:"ownership summary"
	x := 10
	b1 := cap.BorrowMut(&x)
	_ = cap.Move(b1) // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be moved by cap\.Move`

	y := 20
	b2 := cap.BorrowMut(&y)
	ConsumeIsoInt(b2) // want `CAP003: stack-scoped borrow 'b2' \(\\mub\) cannot cross goroutine boundary: cannot be passed to owned parameter 0 of marker_ownership\.ConsumeIsoInt`

	z := 30
	ch <- cap.BorrowMut(&z) // want `CAP003: stack-scoped borrow 'z' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
}

// 23. SAFE005: Mutating an //safe:imm struct's array field via slice, clear, or mutating helper
//safe:imm
type ImmArrayCfg struct { // want ImmArrayCfg:"imm"
	arr [4]int
}

func mutateIntPtr(p *int) {
	*p = 99
}

func TestImmArraySliceAndHelperMutation(c *ImmArrayCfg) { // want TestImmArraySliceAndHelperMutation:"ownership summary"
	c.arr[:][0] = 1         // want `SAFE005: write to a field of ImmArrayCfg, which is //safe:imm; imm values are only written by their composite literal`
	clear(c.arr[:])         // want `SAFE005: write to a field of ImmArrayCfg, which is //safe:imm; imm values are only written by their composite literal`
	mutateIntPtr(&c.arr[0]) // want `SAFE005: write to a field of ImmArrayCfg, which is //safe:imm; imm values are only written by their composite literal`
}

// 24. SAFE006: Mutating an unguarded package variable through a local struct pointer or getter helper
var unguardedGlobalStruct int = 1 // want `SAFE006: package variable unguardedGlobalStruct is written outside init`
var unguardedGlobalGetter int = 2 // want `SAFE006: package variable unguardedGlobalGetter is written outside init`

type GlobalPtrBox struct {
	p *int
}

func getUnguardedGlobalPtr() *int {
	return &unguardedGlobalGetter
}

func TestGlobalMutationViaStructAndGetter() { // want TestGlobalMutationViaStructAndGetter:"ownership summary"
	box := GlobalPtrBox{p: &unguardedGlobalStruct}
	*box.p = 42
	*getUnguardedGlobalPtr() = 99
}

// 25. SAFE008: Mutating shared handler state through a local struct wrapper or pointer helper
type NodeHolder struct {
	n *Node
}

func getNodeValPtr(n *Node) *int {
	return &n.Val
}

func TestHandlerWriteViaLocalStructAndHelper() { // want TestHandlerWriteViaLocalStructAndHelper:"ownership summary"
	s := &Node{Val: 0}
	http.HandleFunc("/round4", func(w http.ResponseWriter, r *http.Request) {
		h := NodeHolder{n: s}
		h.n.Val = 1           // want `SAFE008: captured variable s is written here by a handler registered with net/http\.HandleFunc`
		*getNodeValPtr(s) = 2 // want `SAFE008: captured variable s is written here by a handler registered with net/http\.HandleFunc`
	})
}

// 26. SAFE009: Unlocking a captured mutex inside an in-place closure or spawned goroutine
func TestClosureAndGoroutineUnlockKillsLock() { // want TestClosureAndGoroutineUnlockKillsLock:"ownership summary"
	var mu1 sync.Mutex
	var x1 int //safe:guarded_by mu1
	go func() {
		mu1.Lock()
		_ = x1
		mu1.Unlock()
	}()
	mu1.Lock()
	func() {
		mu1.Unlock()
	}()
	x1 = 1 // want `SAFE009: local variable x1 is guarded by mu1 but is written here without holding it`

	var mu2 sync.Mutex
	var x2 int //safe:guarded_by mu2
	mu2.Lock()
	go func() {
		_ = x2 // want `SAFE009: local variable x2 is guarded by mu2 but is read here without holding it`
		mu2.Unlock()
	}()
	x2 = 2 // want `SAFE009: local variable x2 is guarded by mu2 but is written here without holding it`
}

// ============================================================================
// Round 5 Deep Adversarial Stress Tests
// ============================================================================

// 27. Multi-return aliasing between returned values -> SAFE003
func newAliasedNodePair() (*Node, *Node) {
	n := &Node{Val: 1}
	return n, n
}

func TestMultiReturnAliasUseAfterMove(ch chan *Node) { // want TestMultiReturnAliasUseAfterMove:"ownership summary"
	a, b := newAliasedNodePair()
	ch <- a
	b.Val = 2 // want `SAFE003: this value is used after it was sent on a channel \(line \d+\)`
}

// 28. CAP003: Borrow laundered through conditional Phi or nested closure crossing goroutine boundary
func TestBorrowPhiAndNestedClosureEscape(ch chan *int, cond bool, safe *int) { // want TestBorrowPhiAndNestedClosureEscape:"ownership summary"
	x := 10
	b := cap.BorrowMut(&x)
	res := safe
	if cond {
		res = b
	}
	ch <- res // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`

	y := 20
	by := cap.BorrowMut(&y)
	inner := func() {
		*by = 30
	}
	go func() { // want `CAP003: stack-scoped borrow 'by' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		inner()
	}()
}

// 29. SAFE005: Mutating //gocap:imm via pointer type conversion, accessor method, slice slot, clear(immSlice), and array store
type MutableRateLayout struct {
	rate int
}

func (c *ImmConfig) ratePtr() *int {
	return &c.rate
}

func TestImmTypeConvAccessorSliceAndArrayStore(c *ImmConfig, cs []ImmConfig, arrPtr *[2]ImmConfig) { // want TestImmTypeConvAccessorSliceAndArrayStore:"ownership summary"
	(*MutableRateLayout)(c).rate = 9 // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
	*c.ratePtr() = 10                // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
	ps := []*int{&c.rate}
	mutateIntPtr(ps[0])      // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
	clear(cs)                // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
	*arrPtr = [2]ImmConfig{} // want `SAFE005: store through a \*\[2\]marker_ownership\.ImmConfig overwrites an immutable value`
}

// 30. SAFE006: init() spawning a goroutine that mutates an unguarded package variable
var initGoroutineGlobal int = 1 // want `SAFE006: package variable initGoroutineGlobal is written outside init`

func init() {
	go func() {
		initGoroutineGlobal = 2
	}()
}

// 31. SAFE008: HTTP handler mutating shared state via closure passed to helper, factory function, and http.StripPrefix
func runHandlerClosure(fn func()) {
	fn()
}

func makeCountingHandler() http.HandlerFunc {
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		n++ // want `SAFE008: captured variable n is written here by a handler registered with \(\*net/http\.ServeMux\)\.HandleFunc`
	}
}

type StripPrefixCounter struct {
	hits int
}

func (s *StripPrefixCounter) ServeHTTP(w http.ResponseWriter, r *http.Request) { // want ServeHTTP:"ownership summary"
	s.hits++ // want `SAFE008: receiver s is written here by a handler registered with \(\*net/http\.ServeMux\)\.Handle`
}

func TestHandlerHelperClosureFactoryAndStripPrefix(mux *http.ServeMux) { // want TestHandlerHelperClosureFactoryAndStripPrefix:"ownership summary"
	sharedCount := 0
	http.HandleFunc("/helper-closure", func(w http.ResponseWriter, r *http.Request) {
		runHandlerClosure(func() {
			sharedCount++ // want `SAFE008: captured variable sharedCount is written here by a handler registered with net/http\.HandleFunc`
		})
	})
	mux.HandleFunc("/factory", makeCountingHandler())
	mux.Handle("/strip", http.StripPrefix("/strip", &StripPrefixCounter{}))
}

// 32. SAFE007 & SAFE009: //safe:holds unlocking via defer/helper, calling //safe:holds via go/defer after unlock, and method-value unlock
type GuardedCounter struct {
	mu  sync.Mutex
	val int //safe:guarded_by mu
}

func unlockGuardedCounter(g *GuardedCounter) {
	g.mu.Unlock()
}

//safe:holds mu
func (g *GuardedCounter) badDeferUnlock() {
	defer g.mu.Unlock() // want `SAFE007: badDeferUnlock is //safe:holds mu but unlocks it here`
	g.val++
}

//safe:holds mu
func (g *GuardedCounter) badHelperUnlock() {
	unlockGuardedCounter(g) // want `SAFE007: badHelperUnlock is //safe:holds mu but unlocks it here`
}

//safe:holds mu
func (g *GuardedCounter) bumpHeld() {
	g.val++
}

func TestHoldsGoDeferAndMethodValUnlock(g2 *GuardedCounter) { // want TestHoldsGoDeferAndMethodValUnlock:"ownership summary"
	g1 := &GuardedCounter{}
	go g1.bumpHeld() // want `SAFE009: bumpHeld requires mu to be held \(//safe:holds mu\), but it is not held here`

	g2.mu.Lock()
	defer g2.bumpHeld() // want `SAFE009: bumpHeld requires mu to be held \(//safe:holds mu\), but it is not held here`
	unlock := g2.mu.Unlock
	unlock()
	g2.val = 10 // want `SAFE009: field val is guarded by mu but is written here without holding it`
}

func idIntPtrHelper(p *int) *int { return p }

var unguardedIdentityGlobal int = 1 // want `SAFE006: package variable unguardedIdentityGlobal is written outside init`

// Test 33: Identity helper pass-through for CAP003, SAFE005, SAFE006, and deferred helper unlock for SAFE009
func TestIdentityPassThroughAndDeferHelperUnlock(c *ImmConfig, g *GuardedCounter, ch chan *int) { // want TestIdentityPassThroughAndDeferHelperUnlock:"ownership summary"
	x := 10
	b := cap.BorrowMut(&x)
	ch <- idIntPtrHelper(b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`

	*idIntPtrHelper(&c.rate) = 77        // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`
	mutateIntPtr(idIntPtrHelper(&c.rate)) // want `SAFE005: write to a field of ImmConfig, which is //gocap:imm; imm values are only written by their composite literal`

	*idIntPtrHelper(&unguardedIdentityGlobal) = 88

	g.mu.Lock()
	defer g.bumpHeld()            // want `SAFE009: bumpHeld requires mu to be held \(//safe:holds mu\), but it is not held here`
	defer unlockGuardedCounter(g)
}






