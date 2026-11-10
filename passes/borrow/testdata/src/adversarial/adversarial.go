package adversarial

type Point struct {
	X int
	Y int
}

// ============================================================================
// 1. Lexical Variable Shadowing Tests (Zero False Positives)
// ============================================================================

// 1.1 Basic shadowing: inner x mutated while outer x is borrowed
func TestShadowBasic() {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := 99
		x = 100 // Inner x assignment: MUST produce 0 diagnostics!
		_ = x
	}
	println(*b)
}

// 1.2 Multi-level nested shadowing
func TestShadowMultiLevel() {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := 20
		{
			x := 30
			x = 40 // Inner x assignment: MUST produce 0 diagnostics!
			_ = x
		}
		x = 25 // Middle x assignment: MUST produce 0 diagnostics!
		_ = x
	}
	println(*b)
}

// 1.3 Inner shadowing with IncDec
func TestShadowIncDec() {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := 99
		x++ // Inner x increment: MUST produce 0 diagnostics!
		x-- // Inner x decrement: MUST produce 0 diagnostics!
		_ = x
	}
	println(*b)
}

// 1.4 Shadowing in for loop body
func TestShadowInForLoop() {
	x := 10
	b := &x /*gosree:rob*/
	for x := 0; x < 2; x++ {
		x = x + 1 // Inner loop x: MUST produce 0 diagnostics!
		_ = x
	}
	println(*b)
}

// 1.5 Shadowing in range loop
func TestShadowInRangeLoop() {
	x := 10
	b := &x /*gosree:rob*/
	for x := range []int{1, 2} {
		_ = x // Range loop x: MUST produce 0 diagnostics!
	}
	println(*b)
}

// 1.5b Shadowing channel send
func TestShadowChannelSend(ch chan int) {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := 99
		ch <- x
	}
	println(*b)
}

// 1.5c Shadowing channel send with address taken
func TestShadowAddressTakenChannelSend(ch chan int) {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := 99
		p := &x
		_ = p
		ch <- x
	}
	println(*b)
}

// 1.6 Inner variable borrowed and mutated: flags inner mutation, not outer
func TestShadowInnerBorrow() {
	x := 10
	{
		x := 99
		b := &x /*gosree:rob*/
		x = 100 // want `BORROW003: cannot assign to 'x' because it is borrowed`
		println(*b)
	}
	x = 20 // Outer x: valid because inner borrow ended!
	_ = x
}

// 1.7 Outer variable mutation while borrowed: control test to ensure true mutation is caught
func TestShadowOuterMutated() {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := 99
		_ = x
	}
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	println(*b)
}

// 1.8 Distinct variable borrows: inner mut borrow does NOT conflict with outer mut borrow
func TestShadowConcurrentBorrow() {
	x := 10
	b1 := &x /*gosree:mut*/
	{
		x := 99
		b2 := &x /*gosree:mut*/
		*b2 = 100
	}
	*b1 = 20
}

// 1.9 Shadowed pointer alias owner resolution
func TestShadowPointerAlias() {
	x := 10
	y := 20
	b := &x /*gosree:mut*/
	*b = 11
	{
		b := &y
		b2 := b /*gosree:mut*/
		x = 12
		y = 22 // want `BORROW003: cannot assign to 'y' because it is borrowed`
		*b2 = 23
	}
}

// ============================================================================
// 2. Parenthesized RHS and LHS Expressions
// ============================================================================

// 2.1 Parenthesized unary RHS: b := (&y)
func TestParenUnaryRHS() {
	y := 10
	b := (&y) /*gosree:mut*/
	y = 20 // want `BORROW003: cannot assign to 'y' because it is borrowed`
	*b = 30
}

// 2.2 Deeply nested parenthesized RHS: b := (((&((y)))))
func TestDeepParenRHS() {
	y := 10
	b := (((&((y))))) /*gosree:mut*/
	y = 20 // want `BORROW003: cannot assign to 'y' because it is borrowed`
	*b = 30
}

// 2.3 Parenthesized RHS with struct field: b := (&(pt.X))
func TestParenStructFieldRHS() {
	pt := Point{X: 1, Y: 2}
	b := (&(pt.X)) /*gosree:mut*/
	pt.X = 20 // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	*b = 30
}

// 2.4 Parenthesized LHS assignment: (y) = 20
func TestParenLHS() {
	y := 10
	b := &y /*gosree:mut*/
	(y) = 20 // want `BORROW003: cannot assign to 'y' because it is borrowed`
	*b = 30
}

// 2.5 Parenthesized IncDec: (y)++
func TestParenIncDec() {
	y := 10
	b := &y /*gosree:mut*/
	(y)++ // want `BORROW003: cannot assign to 'y' because it is borrowed`
	*b = 30
}

// ============================================================================
// 3. Loop-Carried Escapes (BORROW002)
// ============================================================================

// 3.1 Direct assignment in standard for loop
func TestLoopDirectEscape() {
	x := 10
	var outer *int
	for i := 0; i < 3; i++ {
		outer = &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	*outer = 20
}

// 3.2 Direct assignment in for range loop
func TestRangeDirectEscape() {
	x := 10
	var outer *int
	for range [3]int{} {
		outer = &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	*outer = 20
}

// 3.3 Variable assignment in for range loop
func TestRangeVariableEscape() {
	x := 10
	var outer *int
	for range [3]int{} {
		b := &x /*gosree:mut*/
		outer = b // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	*outer = 20
}

// 3.4 Direct assignment in for range slice
func TestRangeSliceEscape() {
	x := 10
	var outer *int
	s := []int{1, 2, 3}
	for _, _ = range s {
		outer = &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	*outer = 20
}

// 3.4b Shadowed identifier in loop assigning to outer variable
func TestLoopShadowedIdentEscape() {
	x := 10
	b := &x /*gosree:mut*/
	defer println(*b)

	var outerStr string
	for i := 0; i < 2; i++ {
		b := "shadowed string"
		outerStr = b
	}
	_ = outerStr
}

// 3.5 Clean loop-local borrow (control test)
func TestCleanLoopBorrow() {
	x := 10
	for i := 0; i < 3; i++ {
		b := &x /*gosree:mut*/
		*b = i
	}
}

// ============================================================================
// 4. Struct Field and Array Element Mutations (BORROW003)
// ============================================================================

// 4.1 Struct field assignment: pt.X = 20
func TestStructFieldAssign() {
	pt := Point{X: 1, Y: 2}
	b := &pt.X /*gosree:mut*/
	pt.X = 20 // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	*b = 30
}

// 4.2 Struct field IncDec: pt.X++
func TestStructFieldIncDec() {
	pt := Point{X: 1, Y: 2}
	b := &pt.X /*gosree:mut*/
	pt.X++ // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	*b = 30
}

// 4.3 Array element assignment: arr[0] = 20
func TestArrayElementAssign() {
	arr := [3]int{1, 2, 3}
	b := &arr[0] /*gosree:mut*/
	arr[0] = 20 // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	*b = 30
}

// 4.4 Array element IncDec: arr[0]++
func TestArrayElementIncDec() {
	arr := [3]int{1, 2, 3}
	b := &arr[0] /*gosree:mut*/
	arr[0]++ // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	*b = 30
}

// 4.5 Whole struct overwrite while field borrowed
func TestWholeStructOverwrite() {
	pt := Point{X: 1, Y: 2}
	b := &pt.X /*gosree:mut*/
	pt = Point{X: 10, Y: 20} // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	*b = 30
}

// 4.6 Whole array overwrite while element borrowed
func TestWholeArrayOverwrite() {
	arr := [3]int{1, 2, 3}
	b := &arr[0] /*gosree:mut*/
	arr = [3]int{4, 5, 6} // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	*b = 30
}

// 4.7 Mutation through borrow itself (valid control check)
func TestMutationThroughBorrow() {
	pt := Point{X: 1, Y: 2}
	b := &pt.X /*gosree:mut*/
	*b = 30 // Valid mutation through borrow: MUST produce 0 diagnostics!
	println(*b)
}

// ============================================================================
// 5. Control Flow, Defers, and Dangling Returns
// ============================================================================

// 5.1 Branch drop with clean join
func TestBranchDropCleanJoin(cond bool) {
	x := 10
	if cond {
		b := &x /*gosree:mut*/
		*b = 20
	} else {
		// x unused
	}
	b2 := &x /*gosree:mut*/
	*b2 = 30
}

// 5.2 Defer extending borrow lifetime
func TestDeferExtendsBorrowLifetime() {
	x := 10
	b := &x /*gosree:mut*/
	defer println(*b)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
}

// 5.3 Dangling returns
func TestDanglingScalar() *int {
	x := 10
	return &x /*gosree:rob*/ // want `BORROW004: cannot return reference to local stack variable 'x'`
}

func TestDanglingField() *int {
	pt := Point{X: 1, Y: 2}
	return &pt.X /*gosree:rob*/ // want `BORROW004: cannot return reference to local stack variable`
}

func TestDanglingArray() *int {
	arr := [3]int{1, 2, 3}
	return &arr[0] /*gosree:rob*/ // want `BORROW004: cannot return reference to local stack variable 'arr'`
}

// ============================================================================
// 6. Deep Adversarial Stress Scenarios (Shadowing across control flow & channels)
// ============================================================================

// 6.1 Shadowing in if-else statements
func TestShadowIfElse() {
	x := 10
	b := &x /*gosree:rob*/
	if x := 20; x > 10 {
		x = 30
		_ = x
	} else {
		x = 40
		_ = x
	}
	println(*b)
}

// 6.2 Shadowing in switch statements
func TestShadowSwitch() {
	x := 10
	b := &x /*gosree:rob*/
	switch x := 20; x {
	case 20:
		x = 30
		_ = x
	default:
		x = 40
		_ = x
	}
	println(*b)
}

// 6.3 Shadowing in select statements
func TestShadowSelect(ch chan int) {
	x := 10
	b := &x /*gosree:rob*/
	select {
	case x := <-ch:
		x = x + 1
		_ = x
	default:
	}
	println(*b)
}

// 6.4 Shadowing in nested loops with IncDec and channel send
func TestShadowNestedLoopChannel(ch chan int) {
	x := 10
	b := &x /*gosree:rob*/
	for i := 0; i < 2; i++ {
		for x := 0; x < 2; x++ {
			x++
			ch <- x
		}
	}
	println(*b)
}

// 6.5a Shadowing struct channel send (inner struct in fn.Locals)
func TestShadowStructChannelSend(ch1 chan int) {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := Point{X: 99, Y: 100}
		ch1 <- x.X
	}
	println(*b)
}

// 6.5b Shadowing array channel send (inner array in fn.Locals)
func TestShadowArrayChannelSend(ch2 chan int) {
	x := 10
	b := &x /*gosree:rob*/
	{
		x := [2]int{99, 100}
		ch2 <- x[0]
	}
	println(*b)
}


// 6.6 True owner channel send MUST be flagged as BORROW003
func TestTrueOwnerChannelSend(ch chan int) {
	x := 10
	b := &x /*gosree:rob*/
	ch <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}

// 6.7 Direct channel send of borrow variable: BORROW001, NOT BORROW003
func TestBorrowChannelSend(ch chan *int) {
	x := 10
	b := &x /*gosree:rob*/
	ch <- b // want `BORROW001: channel send cannot store second-class borrow`
	println(*b)
}

// 6.8 Direct channel send of mutable borrow: BORROW001, NOT BORROW003
func TestMutBorrowChannelSend(ch chan *int) {
	x := 10
	b := &x /*gosree:mut*/
	ch <- b // want `BORROW001: channel send cannot store second-class borrow`
	*b = 20
}

// 6.9 Loop-carried escape with unrelated pointer shadowing
func TestLoopUnrelatedPointerShadowing() {
	x := 10
	b := &x /*gosree:mut*/
	defer println(*b)

	unrelated := 100
	var outerPtr *int
	for i := 0; i < 2; i++ {
		b := &unrelated
		outerPtr = b
	}
	_ = outerPtr
}

// 6.10 True loop-carried escape with outer borrow alias
func TestLoopTrueBorrowEscape() {
	x := 10
	b := &x /*gosree:mut*/
	var outer *int
	for i := 0; i < 2; i++ {
		outer = b // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	_ = outer
}

// 6.11 Parameter shadowed by inner variable with borrow
func TestShadowParamWithInnerBorrow(x int) {
	{
		x := 10
		b := &x /*gosree:rob*/
		x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
		println(*b)
	}
	x = 30 // Outer param x: MUST produce 0 diagnostics!
}

// 6.12 Parameter borrow shadowed by inner struct
func TestParamShadowHole(x int, ch1 chan int, ch2 chan int) {
	b := &x /*gosree:rob*/
	{
		x := Point{X: 99}
		ch1 <- x.X
	}
	ch2 <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}



