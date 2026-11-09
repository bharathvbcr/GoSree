package adversarial

type Point struct {
	X int
	Y int
}

// ============================================================================
// 1. Loop-Carried Borrows
// ============================================================================

// TC-ADV-LOOP-01: Borrow assigned to outer variable inside for loop (escapes loop)
func TestLoopCarriedBorrowEscape() {
	x := 10
	var outer *int
	for i := 0; i < 3; i++ {
		b := &x /*gocap:mut*/
		outer = b
	}
	*outer = 20
}

// TC-ADV-LOOP-02: Direct borrow assigned to outer variable inside for loop
func TestLoopDirectBorrowEscape() {
	x := 10
	var outer *int
	for i := 0; i < 3; i++ {
		outer = &x /*gocap:mut*/
	}
	*outer = 20
}

// TC-ADV-LOOP-03: Outer borrow used across loop iterations
func TestLoopBorrowUsedAcrossIterations() {
	x := 10
	var outer *int
	for i := 0; i < 3; i++ {
		if outer != nil {
			*outer = 15
		}
		b := &x /*gocap:mut*/
		outer = b
	}
}

// TC-ADV-LOOP-04: Range loop borrow escape
func TestRangeLoopBorrowEscape() {
	x := 10
	var outer *int
	for range [3]int{} {
		b := &x /*gocap:mut*/
		outer = b
	}
	*outer = 20
}

// TC-ADV-LOOP-05: Clean per-iteration borrow (0 diagnostics expected)
func TestCleanLoopBorrow() {
	x := 10
	for i := 0; i < 5; i++ {
		b := &x /*gocap:mut*/
		*b += i
	}
}

// ============================================================================
// 2. Multiple Branch Merging & Control Flow
// ============================================================================

// TC-ADV-BRANCH-01: Drop in branch A, no use in branch B, sequential borrow in join block
// Expected: Clean pass (b is dead at join point on all paths)
func TestBranchDropCleanJoin(cond bool) {
	x := 10
	b := &x /*gocap:mut*/
	if cond {
		*b = 20 // last use of b in branch A
	} else {
		// branch B never uses b
	}
	// b is dead here
	b2 := &x /*gocap:mut*/
	*b2 = 30
}

// TC-ADV-BRANCH-02: Borrow used in join block after if-else; branch B attempts concurrent borrow
// Expected: Conflict detection (b remains active into join block)
func TestBranchActiveIntoJoin(cond bool) {
	x := 10
	b := &x /*gocap:mut*/
	if cond {
		// branch A does not borrow
	} else {
		b2 := &x /*gocap:mut*/
		*b2 = 25
	}
	// b is used after the join
	*b = 30
}

// TC-ADV-BRANCH-03: Borrows in both branches merged via phi node, owner mutated before use
func TestBranchPhiMergeOwnerMutation(cond bool) {
	x := 10
	var p *int
	if cond {
		b1 := &x /*gocap:mut*/
		*b1 = 20
		p = b1
	} else {
		b2 := &x /*gocap:mut*/
		*b2 = 30
		p = b2
	}
	x = 50 // owner mutation
	println(*p)
}

// ============================================================================
// 3. Defer Semantics
// ============================================================================

// TC-ADV-DEFER-01: Defer holding borrow blocks subsequent mutable borrow
func TestDeferBlocksSubsequentBorrow() {
	x := 10
	b1 := &x /*gocap:mut*/
	defer println(*b1)
	b2 := &x /*gocap:mut*/
	*b2 = 20
}

// TC-ADV-DEFER-02: Defer holding borrow blocks owner mutation
func TestDeferBlocksOwnerMutation() {
	x := 10
	b := &x /*gocap:mut*/
	defer println(*b)
	x = 20
}

// TC-ADV-DEFER-03: Defer closure capturing borrow blocks owner mutation
func TestDeferClosureBlocksOwnerMutation() {
	x := 10
	b := &x /*gocap:mut*/
	defer func() {
		println(*b)
	}()
	x = 20
}

// ============================================================================
// 4. Owner Mutation & Aliasing
// ============================================================================

// TC-ADV-MUT-01: Direct owner assignment x = 20 while borrowed -> BORROW003
func TestOwnerDirectAssign() {
	x := 10
	b := &x /*gocap:mut*/
	x = 20
	*b = 30
}

// TC-ADV-MUT-02: Owner increment x++ while borrowed -> BORROW003
func TestOwnerIncrement() {
	x := 10
	b := &x /*gocap:mut*/
	x++
	*b = 30
}

// TC-ADV-MUT-03: Mutation through borrow *b = 30 -> Clean Pass
func TestMutationThroughBorrow() {
	x := 10
	b := &x /*gocap:mut*/
	*b = 30
}

// TC-ADV-MUT-04: Struct field mutation pt.X = 20 while pt.X is borrowed
func TestStructFieldMutation() {
	pt := Point{X: 1, Y: 2}
	b := &pt.X /*gocap:mut*/
	pt.X = 20
	*b = 30
}

// TC-ADV-MUT-05: Array element mutation arr[0] = 20 while arr[0] is borrowed
func TestArrayElementMutation() {
	arr := [3]int{1, 2, 3}
	b := &arr[0] /*gocap:mut*/
	arr[0] = 20
	*b = 30
}

// TC-ADV-MUT-06: Shadowed variable assignment (must NOT trigger false positive on outer x)
func TestShadowedVariableAssignment() {
	x := 10
	b := &x /*gocap:rob*/
	{
		x := 99
		x = 100 // inner x assignment
		_ = x
	}
	println(*b)
}

// ============================================================================
// 5. Dangling Returns
// ============================================================================

// TC-ADV-DANGLE-01: Returning address of local scalar &x -> BORROW004
func TestDanglingScalarReturn() *int {
	x := 42
	return &x
}

// TC-ADV-DANGLE-02: Returning address of struct field &pt.X -> BORROW004
func TestDanglingFieldReturn() *int {
	pt := Point{X: 1, Y: 2}
	return &pt.X
}

// TC-ADV-DANGLE-03: Returning address of local array element &arr[0] -> BORROW004
func TestDanglingArrayElemReturn() *int {
	arr := [3]int{1, 2, 3}
	return &arr[0]
}

// TC-ADV-DANGLE-04: Returning heap-allocated pointer new(int) -> Clean Pass
func TestValidHeapReturn() *int {
	return new(int)
}

// TC-ADV-DANGLE-05: Returning composite literal &Point{X: 1} -> Clean Pass
func TestValidComplitReturn() *Point {
	return &Point{X: 1, Y: 2}
}

// TC-ADV-DANGLE-06: Returning borrow parameter -> Clean Pass
//gocap:mut p
func TestValidParamReturn(p *int) *int {
	return p
}

// ============================================================================
// 6. Clean Sequential & Multi-Borrow Cases (AC3)
// ============================================================================

// TC-ADV-CLEAN-01: Sequential mutable borrows
func TestCleanSequentialBorrows() int {
	x := 10
	b1 := &x /*gocap:mut*/
	*b1 = 20
	b2 := &x /*gocap:mut*/
	*b2 = 30
	return x
}

// TC-ADV-CLEAN-02: Concurrent read-only borrows
func TestCleanMultiRob() int {
	x := 10
	r1 := &x /*gocap:rob*/
	r2 := &x /*gocap:rob*/
	return *r1 + *r2
}
