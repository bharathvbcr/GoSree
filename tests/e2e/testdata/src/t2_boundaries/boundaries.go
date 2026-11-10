package t2_boundaries

import "sync"

// =========================================================================
// Category 1: Zero/Empty Inputs & Nil Pointers
// =========================================================================

// TC-T2-1.1: Zero-sized empty struct allocation
type EmptyStruct struct{}

func EmptyStructBorrow() {
	e := EmptyStruct{}
	//gosree:mut
	b := &e
	_ = b
}

// TC-T2-1.2: Zero-length array borrow
func ZeroLengthArrayBorrow() {
	var arr [0]int
	//gosree:rob
	r := &arr
	_ = len(*r)
}

// TC-T2-1.3: Nil pointer assigned to borrow binding
func NilPointerBorrow() {
	var p *int = nil
	//gosree:rob
	r := p
	_ = r
}

// TC-T2-1.4: Channel of size 0 (unbuffered) sending \iso
func UnbufferedChanIsoSend(ch chan *EmptyStruct) {
	e := &EmptyStruct{}
	ch <- e
	_ = *e // want `SAFE003: composite literal is used after it was sent on a channel`
}

// TC-T2-1.5: Empty sync.WaitGroup with Add(0) and Wait()
func EmptyWaitGroup() {
	var wg sync.WaitGroup
	wg.Add(0)
	wg.Wait()
}

// =========================================================================
// Category 2: Loops, Iterations & Loop-Carried Borrows
// =========================================================================

// TC-T2-2.1: Mutable borrow strictly scoped within loop iteration (Valid)
func LoopLocalBorrow() {
	for i := 0; i < 5; i++ {
		x := i
		//gosree:mut
		b := &x
		*b = x + 1
	}
}

// TC-T2-2.2: Loop attempting to leak borrow into outer scope rejected (BORROW002)
func LoopCarriedBorrowEscape() {
	var outer *int
	for i := 0; i < 5; i++ {
		x := i
		//gosree:mut
		b := &x
		outer = b // want `BORROW002: cannot borrow 'x' as mutable`
	}
	_ = outer
}

// TC-T2-2.3: Nested for loops with inner borrow and outer mutation (BORROW003)
func NestedLoopConflict() {
	x := 10
	for i := 0; i < 3; i++ {
		//gosree:rob
		r := &x
		for j := 0; j < 3; j++ {
			x = i + j // want `BORROW003: cannot assign to 'x' because it is borrowed`
			println(*r)
		}
	}
}

// TC-T2-2.4: Loop sending \iso items across iterations (New item per iteration is valid)
func LoopSendingFreshIso(ch chan *EmptyStruct) {
	for i := 0; i < 3; i++ {
		item := &EmptyStruct{}
		ch <- item // Valid: fresh allocation every iteration
	}
}

// TC-T2-2.5: Break and continue paths across borrow lifetimes
func LoopBreakContinueBorrow() int {
	sum := 0
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			continue
		}
		x := i * 2
		//gosree:mut
		b := &x
		*b += 1
		sum += *b
		if sum > 20 {
			break
		}
	}
	return sum
}

// =========================================================================
// Category 3: Defers, Stack Unwinding & Mutex Releases
// =========================================================================

// TC-T2-3.1: defer extending borrow lifetime across entire function
//gosree:mut p
func DeferExtendsBorrow(p *int) {
	*p = 100
}

func DeferBorrowCall() {
	x := 10
	//gosree:mut
	b := &x
	defer DeferExtendsBorrow(b)
	// Between defer and return, b is active
}

// TC-T2-3.2: Mutation of owner between defer borrow and return rejected (BORROW003)
func DeferOwnerConflict() {
	x := 10
	//gosree:mut
	b := &x
	defer DeferExtendsBorrow(b)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
}

// TC-T2-3.3: Multiple defers with LIFO evaluation
func MultipleDefers() int {
	x := 0
	defer func() { x += 1 }()
	defer func() { x += 2 }()
	return x
}

// TC-T2-3.4: Defer unlocking WaitGroup in goroutine fork-join
func DeferWaitGroupDone() int {
	res := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		res = 42
	}()
	wg.Wait()
	return res
}

// TC-T2-3.5: Panic recover handling without leaking borrow state
func PanicRecoverBoundary() (recovered bool) {
	defer func() {
		if r := recover(); r != nil {
			recovered = true
		}
	}()
	x := 10
	//gosree:mut
	b := &x
	if *b == 10 {
		panic("expected")
	}
	return false
}

// =========================================================================
// Category 4: Multiple Returns, Control Flow Branching & Early Exits
// =========================================================================

// TC-T2-4.1: If / Else branching: borrow used only in true branch; false branch allows new borrow
func BranchingBorrowLifetimes(cond bool) int {
	x := 10
	if cond {
		//gosree:mut
		b := &x
		*b = 20
		return *b
	} else {
		// Valid: b is dead on false branch!
		//gosree:mut
		b2 := &x
		*b2 = 30
		return *b2
	}
}

// TC-T2-4.2: Early return terminating borrow lifetime cleanly
func EarlyReturnBorrow(val int) int {
	x := val
	if x < 0 {
		return 0
	}
	//gosree:mut
	b := &x
	*b *= 2
	return *b
}

// TC-T2-4.3: Switch statement with isolated borrow per case branch
func SwitchCaseBorrows(mode int) int {
	x := 100
	switch mode {
	case 1:
		//gosree:mut
		b := &x
		*b += 10
		return *b
	case 2:
		//gosree:rob
		r := &x
		return *r
	default:
		return 0
	}
}

// TC-T2-4.4: Select statement channel send of \iso
func SelectIsoSend(ch chan *EmptyStruct) {
	e := &EmptyStruct{}
	select {
	case ch <- e:
		_ = *e // want `SAFE003: composite literal is used after it was sent on a channel`
	default:
	}
}

// TC-T2-4.5: Multiple return values (borrow and error)
func MultiReturnBorrow(input int) (int, error) {
	x := input
	//gosree:rob
	r := &x
	return *r, nil
}

// =========================================================================
// Category 5: Interface Conversions, Method Receivers & Slice Indexing
// =========================================================================

// TC-T2-5.1: Boxing second-class borrow into interface rejected (BORROW001)
func InterfaceConversionReject() {
	x := 10
	//gosree:mut
	b := &x
	var i any = b // want `BORROW001: struct field or interface cannot box second-class borrow`
	_ = i
}

// TC-T2-5.2: Method receiver with //gosree:mut receiver directive
type Counter struct {
	Count int
}

//gosree:mut c
func (c *Counter) Increment() {
	c.Count++
}

func CallMethodReceiver() int {
	c := Counter{Count: 0}
	c.Increment()
	return c.Count
}

// TC-T2-5.3: Slice element borrowing conservatively borrows entire slice
func SliceElementBorrow() {
	s := []int{1, 2, 3}
	//gosree:rob
	r := &s[0]
	_ = r
}

// TC-T2-5.4: Multi-variable simultaneous assignment
func MultiVarSimultaneousAssign() {
	x := 10
	//gosree:mut
	m1 := &x
	//gosree:mut
	m2 := &x // want `BORROW002: cannot borrow 'x' as mutable more than once at a time`
	*m1 = 20
	*m2 = 30
}

// TC-T2-5.5: Struct field access through borrow pointer
func StructFieldThroughBorrow() int {
	c := Counter{Count: 42}
	//gosree:rob
	r := &c
	return r.Count
}

// TC-T2-5.6: Loop-carried borrow escape via alias rejected (BORROW002)
func LoopCarriedBorrowAliasEscape() {
	var outer *int
	for i := 0; i < 3; i++ {
		x := i
		//gosree:mut
		b := &x
		alias := b
		outer = alias // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	_ = outer
}
