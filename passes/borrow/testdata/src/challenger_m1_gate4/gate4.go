package gate4

type Point struct {
	X int
	Y int
}

type Nested struct {
	Inner Point
}

type Container struct {
	Data [3]int
}

// ----------------------------------------------------------------------------
// 1. Variable Shadowing: Outer Composite Shadowed by Inner Scalar
// ----------------------------------------------------------------------------

func TestOuterStructShadowedByInnerScalar(chInt chan int, chPt chan Point) {
	pt := Point{X: 10, Y: 20}
	b := &pt.X /*gocap:rob*/
	{
		pt := 42
		chInt <- pt // Inner send: MUST produce 0 diagnostics!
		pt = 99     // Inner assign: MUST produce 0 diagnostics!
		_ = pt
	}
	chPt <- pt // want `BORROW003: cannot move 'pt' because it is borrowed`
	println(*b)
}

func TestOuterStructShadowedByInnerScalarAssign(chPt chan Point) {
	pt := Point{X: 10, Y: 20}
	b := &pt.X /*gocap:rob*/
	{
		pt := 42
		pt = 99 // Inner assign: MUST produce 0 diagnostics!
		_ = pt
	}
	pt.X = 100 // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	println(*b)
}

func TestOuterArrayShadowedByInnerStruct(chArr chan [3]int, chPt chan Point) {
	arr := [3]int{1, 2, 3}
	b := &arr[0] /*gocap:rob*/
	{
		arr := Point{X: 1, Y: 2}
		chPt <- arr // Inner send: MUST produce 0 diagnostics!
		arr.X = 5   // Inner assign: MUST produce 0 diagnostics!
		_ = arr
	}
	chArr <- arr // want `BORROW003: cannot move 'arr' because it is borrowed`
	println(*b)
}

func TestOuterArrayShadowedByInnerStructAssign() {
	arr := [3]int{1, 2, 3}
	b := &arr[0] /*gocap:rob*/
	{
		arr := Point{X: 1, Y: 2}
		arr.X = 5 // Inner assign: MUST produce 0 diagnostics!
		_ = arr
	}
	arr[0] = 50 // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	println(*b)
}

// ----------------------------------------------------------------------------
// 2. Multi-Level Variable Shadowing (Scalar -> Array -> Struct)
// ----------------------------------------------------------------------------

func TestMultiLevelShadowChannelSend(chInt chan int, chArr chan [2]int, chPt chan Point) {
	x := 10
	b := &x /*gocap:rob*/
	{
		x := [2]int{1, 2}
		{
			x := Point{X: 3, Y: 4}
			chPt <- x // Inner-most send: MUST produce 0 diagnostics!
		}
		chArr <- x // Middle send: MUST produce 0 diagnostics!
	}
	chInt <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}

// ----------------------------------------------------------------------------
// 3. Sibling Blocks (No Cross-Scope Pollution)
// ----------------------------------------------------------------------------

func TestSiblingBlocksScopeIsolation() {
	{
		x := Point{X: 1, Y: 2}
		b := &x.X /*gocap:rob*/
		x.X = 10 // want `BORROW003: cannot assign to 'x' because it is borrowed`
		println(*b)
	}
	{
		x := Point{X: 3, Y: 4}
		b := &x.Y /*gocap:rob*/
		x.Y = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
		println(*b)
	}
	{
		x := [2]int{1, 2}
		b := &x[0] /*gocap:rob*/
		x[0] = 30 // want `BORROW003: cannot assign to 'x' because it is borrowed`
		println(*b)
	}
}

// ----------------------------------------------------------------------------
// 4. Nested Composite Types (Deep Selectors and Indices)
// ----------------------------------------------------------------------------

func TestDeepNestedStructField(ch chan Nested) {
	n := Nested{Inner: Point{X: 1, Y: 2}}
	b := &n.Inner.X /*gocap:rob*/
	n.Inner.X = 10 // want `BORROW003: cannot assign to 'n' because it is borrowed`
	ch <- n        // want `BORROW003: cannot move 'n' because it is borrowed`
	println(*b)
}

func TestArrayOfStructsField(ch chan [2]Point) {
	arr := [2]Point{{X: 1, Y: 2}, {X: 3, Y: 4}}
	b := &arr[0].X /*gocap:rob*/
	arr[0].X = 10  // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	ch <- arr      // want `BORROW003: cannot move 'arr' because it is borrowed`
	println(*b)
}

func TestStructWithArrayField(ch chan Container) {
	c := Container{Data: [3]int{1, 2, 3}}
	b := &c.Data[1] /*gocap:rob*/
	c.Data[1] = 99  // want `BORROW003: cannot assign to 'c' because it is borrowed`
	ch <- c         // want `BORROW003: cannot move 'c' because it is borrowed`
	println(*b)
}

func TestMultiDimArrayElement(ch chan [2][2]int) {
	grid := [2][2]int{{1, 2}, {3, 4}}
	b := &grid[0][1] /*gocap:rob*/
	grid[0][1] = 99  // want `BORROW003: cannot assign to 'grid' because it is borrowed`
	ch <- grid       // want `BORROW003: cannot move 'grid' because it is borrowed`
	println(*b)
}

// ----------------------------------------------------------------------------
// 5. Parameter Moves and Shadowing
// ----------------------------------------------------------------------------

func TestParamScalarMove(x int, ch chan int) {
	b := &x /*gocap:rob*/
	ch <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}

func TestParamStructMove(pt Point, ch chan Point) {
	b := &pt.X /*gocap:rob*/
	pt.X = 10  // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	ch <- pt   // want `BORROW003: cannot move 'pt' because it is borrowed`
	println(*b)
}

func TestParamArrayMove(arr [2]int, ch chan [2]int) {
	b := &arr[0] /*gocap:rob*/
	arr[0] = 10  // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	ch <- arr    // want `BORROW003: cannot move 'arr' because it is borrowed`
	println(*b)
}

func TestParamStructShadowedByScalar(pt Point, chInt chan int, chPt chan Point) {
	b := &pt.X /*gocap:rob*/
	{
		pt := 123
		chInt <- pt // Inner send: MUST produce 0 diagnostics!
	}
	chPt <- pt // want `BORROW003: cannot move 'pt' because it is borrowed`
	println(*b)
}

func TestParamArrayShadowedByStruct(arr [2]int, chPt chan Point, chArr chan [2]int) {
	b := &arr[0] /*gocap:rob*/
	{
		arr := Point{X: 1, Y: 2}
		chPt <- arr // Inner send: MUST produce 0 diagnostics!
	}
	chArr <- arr // want `BORROW003: cannot move 'arr' because it is borrowed`
	println(*b)
}

// ----------------------------------------------------------------------------
// 6. Address-Taken Inner Variable When Outer is Borrowed
// ----------------------------------------------------------------------------

func TestShadowInnerAddressTaken(ch chan Point) {
	x := Point{X: 10, Y: 20}
	b := &x.X /*gocap:rob*/
	{
		x := Point{X: 99, Y: 100}
		ptr := &x
		_ = ptr
		ch <- x // Inner x send: MUST produce 0 diagnostics!
	}
	ch <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}

// ----------------------------------------------------------------------------
// 7. Borrow of Inner Shadowed Variable
// ----------------------------------------------------------------------------

func TestBorrowInnerShadowedVariable(ch chan Point) {
	x := 10
	{
		x := Point{X: 1, Y: 2}
		b := &x.X /*gocap:rob*/
		ch <- x   // want `BORROW003: cannot move 'x' because it is borrowed`
		println(*b)
	}
	x = 20 // Outer x: valid because inner borrow ended!
	_ = x
}


// ----------------------------------------------------------------------------
// 8. Pointer Alias with Variable Shadowing & Parameters
// ----------------------------------------------------------------------------

func TestPointerAliasShadowing(ch chan int) {
	x := 10
	p := &x
	b := p /*gocap:rob*/
	{
		x := 99
		ch <- x // Inner send: MUST produce 0 diagnostics!
	}
	ch <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}

func TestParamPointerAlias(x int, ch1 chan int, ch2 chan int) {
	p := &x
	b := p /*gocap:rob*/
	{
		x := 99
		ch1 <- x // Inner send: MUST produce 0 diagnostics!
	}
	ch2 <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*b)
}

// ----------------------------------------------------------------------------
// 9. Nested Active Borrows with Same Variable Name
// ----------------------------------------------------------------------------

func TestNestedBorrowsSameName() {
	x := 10
	b1 := &x /*gocap:rob*/
	{
		x := 20
		b2 := &x /*gocap:rob*/
		println(*b2)
	}
	println(*b1)
}

func TestNestedBorrowsSameNameMutated() {
	x := 10
	b1 := &x /*gocap:rob*/
	{
		x := 20
		b2 := &x /*gocap:rob*/
		x = 30 // want `BORROW003: cannot assign to 'x' because it is borrowed at line 275`
		println(*b2)
	}
	println(*b1)
}

// ----------------------------------------------------------------------------
// 10. Anonymous Struct and Slice Element Borrows
// ----------------------------------------------------------------------------

type AnonymousWrapper struct {
	Anon struct {
		Val int
	}
}

func TestAnonStructField(ch chan AnonymousWrapper) {
	w := AnonymousWrapper{}
	b := &w.Anon.Val /*gocap:rob*/
	w.Anon.Val = 10 // want `BORROW003: cannot assign to 'w' because it is borrowed`
	ch <- w         // want `BORROW003: cannot move 'w' because it is borrowed`
	println(*b)
}

func TestSliceElementBorrow(s []int, ch chan []int) {
	b := &s[0] /*gocap:rob*/
	s[0] = 10  // want `BORROW003: cannot assign to 's' because it is borrowed`
	ch <- s    // want `BORROW003: cannot move 's' because it is borrowed`
	println(*b)
}
