package borrows

// TC-CONFLICT-01: Multiple simultaneous mutable borrows
func testSimultaneousMutBorrow() {
	x := 1
	b1 := &x /*gocap:mut*/
	b2 := &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable more than once at a time: already borrowed as mutable at line 6`
	*b1 = 2
	*b2 = 3
}

// TC-CONFLICT-02: Mutable borrow aliasing active read-only borrow
func testMutAliasingReadOnly() {
	x := 1
	r := &x /*gocap:rob*/
	m := &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only at line 15`
	println(*r)
	*m = 2
}

// TC-CONFLICT-03: Read-only borrow aliasing active mutable borrow
func testReadOnlyAliasingMut() {
	x := 1
	m := &x /*gocap:mut*/
	r := &x /*gocap:rob*/ // want `BORROW002: cannot borrow 'x' as read-only because it is also borrowed as mutable at line 24`
	*m = 2
	println(*r)
}

// TC-CONFLICT-04: Mutation of owner allocation while borrowed
func testOwnerMutationWhileBorrowed() {
	x := 1
	r := &x /*gocap:rob*/
	x = 2 // want `BORROW003: cannot assign to 'x' because it is borrowed at line 33`
	println(*r)
}

// TC-CONFLICT-05: Dangling reference returning local stack address
func testDanglingLocalReturn() *int {
	x := 1
	return &x /*gocap:rob*/ // want `BORROW004: cannot return reference to local stack variable 'x'`
}

// Multi-value assignment simultaneous borrow
func testMultiAssignConflict() {
	x := 10
	b1, b2 := &x /*gocap:mut*/, &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable more than once at a time: already borrowed as mutable`
	*b1 = 20
	*b2 = 30
}

// Pointer alias collision detected via Steensgaard unification
func testAliasCollision() {
	x := 1
	p := &x
	b1 := p /*gocap:mut*/
	b2 := &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable more than once at a time: already borrowed as mutable`
	*b1 = 2
	*b2 = 3
}

// Move of owner allocation while borrowed
func testMoveWhileBorrowed(ch chan int) {
	x := 42
	r := &x /*gocap:rob*/
	ch <- x // want `BORROW003: cannot move 'x' because it is borrowed at line 65`
	println(*r)
}

// Defer extends borrow lifetime across function remainder
func testDeferExtendsBorrow() {
	x := 1
	b := &x /*gocap:mut*/
	defer println(*b)
	x = 2 // want `BORROW003: cannot assign to 'x' because it is borrowed at line 73`
}

// Unsafe escape hatch suppresses diagnostic
func testUnsafeSuppressed() {
	x := 1
	//gocap:unsafe deliberate alias for performance
	b1 := &x /*gocap:mut*/
	b2 := &x /*gocap:mut*/
	*b1 = 2
	*b2 = 3
}
