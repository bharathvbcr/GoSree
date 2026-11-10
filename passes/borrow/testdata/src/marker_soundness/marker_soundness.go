package marker_soundness

import (
	"github.com/bharathvbcr/gocap/cap"
)

// Test 1: cap.BorrowMut creating simultaneous mutable borrows -> BORROW002
func TestMarkerSimultaneousMutBorrows() {
	x := 10
	b1 := cap.BorrowMut(&x)
	b2 := cap.BorrowMut(&x) // want `BORROW002: cannot borrow 'x' as mutable more than once at a time: already borrowed as mutable at line \d+`
	*b1 = 20
	*b2 = 30
}

// Test 2: cap.BorrowMut owner mutation -> BORROW003
func TestMarkerOwnerMutation() {
	x := 10
	b := cap.BorrowMut(&x)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	*b = 30
}

// Test 3: var declaration with cap.BorrowMut -> BORROW003
func TestVarDeclMarkerBorrow() {
	x := 10
	var b = cap.BorrowMut(&x)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	*b = 30
}

// Test 4: Typed var declaration with cap.Mut[int] -> BORROW003
func TestTypedVarDeclMutBorrow() {
	x := 10
	var b cap.Mut[int] = &x
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	*b = 30
}

// Test 5: Double pointer alias invalidation -> BORROW003
func TestDoublePointerAliasInvalidation() {
	x := 10
	b := cap.BorrowMut(&x)
	pp := &b
	x = 30 // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	**pp = 40
}

// Test 6: Single pointer alias of borrow -> BORROW003
func TestPointerAliasOfBorrowInvalidation() {
	x := 10
	b := cap.BorrowMut(&x)
	p := b
	x = 30 // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	*p = 40
}

// ============================================================================
// Round 2 Deep Stress Tests (Rust-grade parity)
// ============================================================================

// Test 7 (Hole 1A): Loop-carried borrow escape via cap.BorrowMut(&x) -> BORROW002
func TestLoopEscapeDirectCallMarker() {
	var outer *int
	for i := 0; i < 3; i++ {
		x := i
		outer = cap.BorrowMut(&x) // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	_ = outer
}

// Test 8 (Hole 1B): Loop-carried borrow escape via alias of borrow -> BORROW002
func TestLoopEscapeViaBorrowAlias() {
	var outer *int
	for i := 0; i < 3; i++ {
		x := i
		b := cap.BorrowMut(&x)
		alias := b
		outer = alias // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	_ = outer
}

// Test 9 (Hole 2A): Mutating borrowed owner via helper call mutate(&x) -> BORROW003
func mutatePtr(p *int) {
	*p = 99
}

func TestOwnerMutatedViaCallWhileBorrowed() {
	x := 10
	r := cap.Borrow(&x)
	mutatePtr(&x) // want `BORROW003: cannot borrow or take address of 'x' while it is borrowed at line \d+`
	_ = *r
}

// Test 10 (Hole 2A): Taking raw pointer alias p := &x while x is mutably borrowed -> BORROW003
func TestRawPointerTakenWhileMutBorrowed() {
	x := 10
	b := cap.BorrowMut(&x)
	p := &x // want `BORROW003: cannot borrow or take address of 'x' while it is borrowed at line \d+`
	*p = 20
	*b = 30
}

// Test 11 (Hole 2B): Mutating slice via clear(s) while element is borrowed -> BORROW003
func TestSliceClearWhileElementBorrowed() {
	s := []int{1, 2, 3}
	r := cap.Borrow(&s[0])
	clear(s) // want `BORROW003: cannot mutate 's' via clear\(\) because it is borrowed at line \d+`
	_ = *r
}

// Test 12 (Hole 2B): Mutating slice via copy(s, src) while element is borrowed -> BORROW003
func TestSliceCopyWhileElementBorrowed() {
	s := []int{1, 2, 3}
	src := []int{4, 5, 6}
	r := cap.Borrow(&s[0])
	copy(s, src) // want `BORROW003: cannot mutate 's' via copy\(\) because it is borrowed at line \d+`
	_ = *r
}

// Test 13 (Hole 2C): Calling pre-defined mutating closure while owner is borrowed -> BORROW003
func TestPredefinedClosureMutatesBorrowedOwner() {
	x := 10
	mutator := func() {
		x = 99
	}
	b := cap.BorrowMut(&x)
	mutator() // want `BORROW003: cannot assign to 'x' \(via closure 'mutator'\) because it is borrowed at line \d+`
	*b = 30
}

// Test 14 (Hole 2D): Moving owner via cap.Move(x) while borrowed -> BORROW003
func TestMoveOwnerWhileBorrowed() {
	x := 10
	r := cap.Borrow(&x)
	_ = cap.Move(x) // want `BORROW003: cannot move 'x' because it is borrowed at line \d+`
	_ = *r
}

// Test 15 (Hole 3): Dangling stack pointer return via branch-merged Phi node -> BORROW004
func TestDanglingReturnViaPhi(cond bool) *int {
	x := 10
	y := 20
	var p *int
	if cond {
		p = &x
	} else {
		p = &y
	}
	return p // want `BORROW004: cannot return reference to local stack variable '[xy]'`
}

// Test 16 (Hole 3): Dangling stack pointer return via heap box -> BORROW004
func TestDanglingReturnViaHeapBox() *int {
	x := 10
	box := new(*int)
	*box = &x
	return *box // want `BORROW004: cannot return reference to local stack variable 'x'`
}

// Test 17 (Hole 4A): Closure parameter with cap.Mut[int] escaping to global -> BORROW001
var globalSink *int

func TestClosureParamBorrowEscape() {
	fn := func(b cap.Mut[int]) {
		globalSink = b // want `BORROW001: package variable 'globalSink' cannot store second-class borrow`
	}
	x := 10
	fn(&x)
}

// Test 18 (Hole 4B): Borrow escaping closure into captured outer variable -> BORROW001
func TestClosureCapturedOuterVarEscape() {
	var escaped *int
	func() {
		x := 10
		b := cap.BorrowMut(&x)
		escaped = b // want `BORROW001: cannot store second-class borrow to heap or non-stack memory`
	}()
	_ = escaped
}

// ============================================================================
// Round 4 Adversarial Stress Tests (Breaking Borrow Checker)
// ============================================================================

func useTwoMut(a, b cap.Mut[int]) {
	*a = 1
	*b = 2
}

func useOneMutGeneric[T any](a cap.Mut[T]) {}
func useOneRobGeneric[T any](a cap.Ref[T]) {}

// Test 19: Inline simultaneous mutable borrows in a single function call -> BORROW002
func TestInlineSimultaneousMutBorrows() {
	x := 10
	useTwoMut(cap.BorrowMut(&x), cap.BorrowMut(&x)) // want `BORROW002: cannot borrow 'x' as mutable more than once at a time`
}

// Test 20: Inline mutable borrow call while local borrow is active, and generic cap.Ref / cap.Mut -> BORROW002
func TestInlineBorrowConflictAndGenericParams() {
	x := 10
	r := cap.Borrow(&x)
	useOneRobGeneric(&x) // clean: generic cap.Ref[T] parameter recognizesOrigin
	useOneMutGeneric(&x) // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	_ = *r
}

// Test 21: Pre-existing pointer alias mutation while owner is borrowed -> BORROW003
func TestPreExistingPointerAliasMutation() {
	x := 10
	p := &x
	r := cap.Borrow(&x)
	*p = 99 // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	_ = *r
}

// Test 22: Inline IIFE closure and goroutine mutating borrowed owner -> BORROW003
func TestIIFEAndGoroutineMutatingBorrowedOwner() {
	x := 10
	r := cap.Borrow(&x)
	func() { x = 20 }()    // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	go func() { x = 30 }() // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	_ = *r
}

type CounterVal struct{ n int }

func (c *CounterVal) Inc() { c.n++ }

// Test 23: Implicit address-take pointer-receiver method call on borrowed value owner -> BORROW003
func TestImplicitPointerReceiverMethodOnBorrowedValue() {
	var c CounterVal
	r := cap.Borrow(&c)
	c.Inc() // want `BORROW003: cannot call pointer-receiver method 'Inc' on 'c' while it is borrowed at line \d+`
	_ = r.n
}

// Test 24: Returning or storing a closure that captures a second-class borrow -> BORROW001
var globalCallback func()

func TestClosureCapturingBorrowEscapes(s []func()) func() int {
	x := 10
	b := cap.BorrowMut(&x)
	globalCallback = func() { *b = 20 } // want `BORROW001: package variable 'globalCallback' cannot store second-class borrow`
	s = append(s, func() { *b = 30 })   // want `BORROW001: slice append cannot store second-class borrow`
	_ = s
	return func() int { return *b } // want `BORROW001: second-class borrow cannot escape stack frame via return`
}

// Test 25: Returning borrow laundered through a conditional Phi node -> BORROW001
func TestPhiBorrowEscape(cond bool, safePtr *int) *int {
	x := 10
	b := cap.Borrow(&x)
	res := safePtr
	if cond {
		res = b
	}
	return res // want `BORROW001: borrow stored in struct field 'res' escapes stack frame`
}

// Test 26: Dangling stack pointer return laundered through composite literal struct or slice -> BORROW004
type PtrHolder struct{ p *int }

func TestDanglingReturnViaStructComplit() *int {
	x := 42
	h := &PtrHolder{p: &x}
	return h.p // want `BORROW004: cannot return reference to local stack variable 'x'`
}

func TestDanglingReturnViaSliceLit() *int {
	x := 42
	s := []*int{&x}
	return s[0] // want `BORROW004: cannot return reference to local stack variable 'x'`
}

// ============================================================================
// Round 5 Deep Adversarial Stress Tests
// ============================================================================

// Test 27: Cyclic pointer dereference in loop does not stack-overflow baseAllocSeen and still catches BORROW004
type LinkedPtr struct{ next *LinkedPtr }

func TestCyclicPointerDerefNoStackOverflow(cond bool) *LinkedPtr {
	var root LinkedPtr
	p := &root
	for cond {
		p = p.next
	}
	return &root // want `BORROW004: cannot return reference to local stack variable 'root'`
}

// Test 28: Multi-hop pointer alias, sub-slice, and interface type assertion borrow conflicts -> BORROW002
func TestMultiHopAndSubsliceAndIfaceBorrowConflicts(s []int, i any) {
	x := 10
	p := &x
	q := p
	b1 := cap.BorrowMut(p)
	b2 := cap.BorrowMut(q) // want `BORROW002: cannot borrow 'x' as mutable more than once at a time`
	*b2 = 1
	*b1 = 2

	t := s[1:]
	sb1 := cap.BorrowMut(&s[1])
	sb2 := cap.BorrowMut(&t[0]) // want `BORROW002: cannot borrow 's' as mutable more than once at a time`
	*sb2 = 3
	*sb1 = 4

	c1 := i.(*CounterVal)
	c2 := i.(*CounterVal)
	ib1 := cap.BorrowMut(&c1.n)
	ib2 := cap.BorrowMut(&c2.n) // want `BORROW002: cannot borrow 'i' as mutable more than once at a time`
	*ib2 = 5
	*ib1 = 6
}

// Test 29: Passing pre-existing pointer alias to cap.Mut[T] parameter while owner is borrowed -> BORROW002
func TestPreExistingAliasPassedToMutParam() {
	x := 10
	p := &x
	r := cap.Borrow(&x)
	useOneMutGeneric(p) // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	_ = *r
}

// Test 30: Owner invalidation via (*p)++, copy(s[:]), clear(s[:]), append(s[:1]), and range assign -> BORROW003
func TestSliceResliceAndIncDecAndRangeInvalidation(src []int) {
	x := 10
	p := &x
	r := cap.Borrow(&x)
	(*p)++ // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
	for x = range 3 { // want `BORROW003: cannot assign to 'x' because it is borrowed at line \d+`
		_ = *r
	}

	s := make([]int, 2, 4)
	sr := cap.Borrow(&s[0])
	copy(s[:], src)      // want `BORROW003: cannot mutate 's' via copy\(\) because it is borrowed at line \d+`
	clear(s[:])          // want `BORROW003: cannot mutate 's' via clear\(\) because it is borrowed at line \d+`
	_ = append(s[:1], 9) // want `BORROW003: cannot mutate 's' via append\(\) because it is borrowed at line \d+`
	_ = *sr
}

type closureBox struct{ f func() }

func callClosureHelper(fn func()) { fn() }

//gocap:iso v
func consumeIsoInt(v int) { _ = v }

// Test 31: Pre-existing closure in struct field / passed to helper / mutating *p, alias passed to callee, and Iso move -> BORROW003
func TestClosureAndAliasAndIsoInvalidation() {
	x := 10
	p := &x
	m := func() { *p = 77 }
	box := closureBox{f: func() { x = 88 }}
	r := cap.Borrow(&x)
	callClosureHelper(m) // want `BORROW003: cannot assign to 'x' \(via closure 'm'\) because it is borrowed at line \d+`
	box.f()              // want `BORROW003: cannot assign to 'x' \(via closure 'box'\) because it is borrowed at line \d+`
	mutatePtr(p)         // want `BORROW003: cannot borrow or take address of 'x' while it is borrowed at line \d+`
	consumeIsoInt(x)     // want `BORROW003: cannot move 'x' because it is borrowed at line \d+`
	_ = *r
}

var globalIntPtr *int

func storePtrInGlobal(p *int) { globalIntPtr = p }

// Test 32: Borrow escaping through unannotated helper into global or select send of phi borrow -> BORROW001
func TestHelperGlobalEscapeAndSelectSendPhi(ch chan *int, cond bool, safe *int) {
	x := 10
	b := cap.BorrowMut(&x)
	storePtrInGlobal(b) // want `BORROW001: package variable 'globalIntPtr' cannot store second-class borrow`
	res := safe
	if cond {
		res = b
	}
	select {
	case ch <- res: // want `BORROW001: channel send cannot store second-class borrow`
	default:
	}
}

// Test 33: Upgrading read-only borrow to cap.Mut[T] or passing alias to cap.Ref[T] while mutably borrowed -> BORROW002
func TestRobUpgradedToMutAndAliasRobConflict() {
	x := 10
	r := cap.Borrow(&x)
	useOneMutGeneric(r) // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	_ = *r

	y := 20
	py := &y
	bm := cap.BorrowMut(&y)
	useOneRobGeneric(py) // want `BORROW002: cannot borrow 'y' as read-only because it is also borrowed as mutable`
	*bm = 30
}

// Test 34: Direct mutation through read-only borrow (*r = 99, (*r)++, cr.n = 2, mutatePtr(r)) -> BORROW003
func TestDirectMutationThroughRob() {
	x := 10
	r := cap.Borrow(&x)
	*r = 99       // want `BORROW003: cannot assign through read-only borrow 'r' \(borrowed at line \d+\)`
	(*r)++        // want `BORROW003: cannot assign through read-only borrow 'r' \(borrowed at line \d+\)`
	mutatePtr(r)  // want `BORROW003: cannot assign through read-only borrow 'r' \(borrowed at line \d+\)`

	c := CounterVal{n: 1}
	cr := cap.Borrow(&c)
	cr.n = 2 // want `BORROW003: cannot assign through read-only borrow 'cr' \(borrowed at line \d+\)`
}

func idIntPtr(p *int) *int { return p }

// Test 35: Borrow or local stack address laundered through identity helper -> BORROW001 / BORROW004
func TestBorrowAndStackEscapeViaIdentityHelper() *int {
	x := 10
	b := cap.BorrowMut(&x)
	globalIntPtr = idIntPtr(b) // want `BORROW001: package variable 'globalIntPtr' cannot store second-class borrow`

	localVar := 42
	return idIntPtr(&localVar) // want `BORROW004: cannot return reference to local stack variable 'localVar'`
}




