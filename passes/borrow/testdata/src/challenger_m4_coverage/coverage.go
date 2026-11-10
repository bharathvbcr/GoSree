package challenger_m4_coverage

type Mut[T any] *T
type Rob[T any] *T

var (
	GlobalInt    int
	GlobalTarget *int
	GlobalAny    any
)

type LocalPoint struct {
	X int
	Y int
}

type LocalStruct struct {
	Val int
}

type InnerNode struct {
	p *int
}

type MidNode struct {
	in InnerNode
}

type OuterNode struct {
	m MidNode
}

type AnySink interface {
	Consume(val any)
}

type Reader interface {
	Read(p *int)
}

type PtrAlias *int

var GlobalAlias PtrAlias

// ============================================================================
// 1. Embedded Struct Fields with Borrow Types (BORROW001)
// ============================================================================

type StructWithMarkers struct {
	f1 Mut[int] // want `BORROW001: struct field 'f1' cannot have second-class borrow type`
	f2 Rob[int] // want `BORROW001: struct field 'f2' cannot have second-class borrow type`
}

type EmbeddedBlockComment struct {
	*int /*gosree:mut*/ // want `BORROW001: embedded struct field cannot have second-class borrow type`
}

type EmbeddedLegacyBlockComment struct {
	*int /*safe:rob*/ // want `BORROW001: embedded struct field cannot have second-class borrow type`
}

type EmbeddedDocComment struct {
	//gosree:mut
	*int // want `BORROW001: embedded struct field cannot have second-class borrow type`
}

type BlockCommentStruct struct {
	f1 *int /*safe:mut*/ // want `BORROW001: struct field 'f1' cannot have second-class borrow type`
	f2 *int /*safe:rob*/ // want `BORROW001: struct field 'f2' cannot have second-class borrow type`
}

// ============================================================================
// 2. Defer Statements with Active Borrows
// ============================================================================

func dummyInspect(p *int) {
	_ = p
}

func dummyCleanup(v int) {
	_ = v
}

// 2.1 Owner mutated after defer: borrow lifetime extended to return by defer!
func TestDeferBorrowExtendsLifetime() {
	x := 10
	b := &x /*gosree:rob*/
	defer dummyInspect(b)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	_ = x
}

// 2.2 Conflicting mutable borrow after defer: borrow lifetime extended to return!
func TestDeferBorrowConflict() {
	x := 10
	b1 := &x /*gosree:rob*/
	defer dummyInspect(b1)
	b2 := &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	*b2 = 20
}

// 2.3 Conflicting simultaneous mutable borrow across defer
func TestDeferMutBorrowConflict() {
	x := 10
	b1 := &x /*gosree:mut*/
	defer dummyInspect(b1)
	b2 := &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable more than once at a time`
	*b2 = 30
}

// 2.4 Clean defer: independent variable used in defer, borrow already expired
func TestDeferIndependentVarClean() {
	x := 10
	b := &x /*gosree:rob*/
	println(*b)
	y := 20
	defer dummyCleanup(y)
	x = 30 // clean! b expired at println(*b)
	_ = x
}

// ============================================================================
// 3. Deeply Nested Loops & Loop-Carried Escapes (BORROW002)
// ============================================================================

// 3.1 Double-nested loop with mutable borrow escape to outer scope
func TestNestedLoopEscape() {
	var out *int
	x := 10
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			out = &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
		}
	}
	_ = out
}

// 3.2 Middle scope escape in nested loop
func TestMiddleLoopEscape() {
	x := 10
	for i := 0; i < 2; i++ {
		var mid *int
		for j := 0; j < 2; j++ {
			mid = &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
		}
		_ = mid
	}
}

// 3.3 Range loop with loop-carried mutable borrow escape
func TestRangeLoopEscape() {
	var out *int
	x := 10
	items := []int{1, 2}
	for range items {
		out = &x /*gosree:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	_ = out
}

// 3.4 Loop-carried borrow via variable identifier
func TestLoopCarriedVarEscape() {
	var out *int
	x := 10
	for i := 0; i < 2; i++ {
		b := &x /*gosree:mut*/
		out = b // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
	}
	_ = out
}

// 3.5 Loop local borrow: created and consumed strictly within body (clean)
func TestLoopLocalBorrowClean() {
	x := 10
	for i := 0; i < 3; i++ {
		b := &x /*gosree:mut*/
		*b += i
	}
	x = 99 // clean! borrow expired
	_ = x
}

// 3.6 Loop with control flow break and continue (NLL resolution)
func TestLoopControlFlowNLL() {
	x := 10
	for i := 0; i < 5; i++ {
		if i == 1 {
			continue
		}
		b := &x /*gosree:mut*/
		*b += i
		if i == 3 {
			break
		}
	}
	x = 100 // clean!
	_ = x
}

// ============================================================================
// 4. Type Conversions & Multi-Hop Indirections
// ============================================================================

// 4.1 Named pointer type conversion escaping via return
//gosree:mut b
func TestChangeTypeEscapeReturn(b *int) PtrAlias {
	pa := PtrAlias(b)
	return pa // want `BORROW001: second-class borrow cannot escape stack frame via return`
}

// 4.2 Named pointer type conversion stored to package global
//gosree:mut b
func TestChangeTypeStoreGlobal(b *int) {
	GlobalAlias = PtrAlias(b) // want `BORROW001: package variable 'GlobalAlias' cannot store second-class borrow`
}

// 4.3 Multi-hop pointer to global store
//gosree:mut b
func TestMultiHopGlobalStore(b *int) {
	pp := &GlobalTarget
	*pp = b // want `BORROW001: package variable 'GlobalTarget' cannot store second-class borrow`
}

// 4.4 Multi-hop helper escape passing borrow alongside global address
//gosree:mut b
func TestMultiHopHelperEscape(b *int) {
	assignHelper(&GlobalTarget, b) // want `BORROW001: package variable 'GlobalTarget' cannot store second-class borrow`
}

func assignHelper(dst **int, src *int) {
	*dst = src
}

// ============================================================================
// 5. Interface Boxing, Method Calls & Variadic Calls
// ============================================================================

// 5.1 Interface parameter boxing in call expression
//gosree:mut b
func TestInterfaceParamBoxing(s AnySink, b *int) {
	s.Consume(b) // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 5.2 Variadic function with direct interface boxing
func variadicSink(args ...any) {
	_ = args
}

//gosree:mut b
func TestVariadicDirect(b *int) {
	variadicSink(b) // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 5.3 Interface method call without interface parameter boxing (clean)
//gosree:rob b
func TestInterfaceMethodCallClean(r Reader, b *int) {
	r.Read(b) // clean! p *int is a direct pointer parameter, not an interface
}

// ============================================================================
// 6. Recursive Functions Passing Borrows
// ============================================================================

// 6.1 Clean recursive borrow passing (e.g. countdown mutation)
//gosree:mut b
func TestRecursiveCountdownClean(n int, b *int) {
	if n <= 0 {
		return
	}
	*b += n
	TestRecursiveCountdownClean(n-1, b) // clean!
}

// 6.2 Clean recursive read-only borrow passing
//gosree:rob b
func TestRecursiveReadClean(n int, b *int) int {
	if n <= 0 {
		return *b
	}
	return TestRecursiveReadClean(n-1, b) // clean!
}

// 6.3 Recursive function escaping borrow to package global
//gosree:mut b
func TestRecursiveEscapeGlobal(n int, b *int) {
	if n <= 0 {
		GlobalTarget = b // want `BORROW001: package variable 'GlobalTarget' cannot store second-class borrow`
		return
	}
	TestRecursiveEscapeGlobal(n-1, b)
}

// 6.4 Recursive function escaping borrow via return
//gosree:mut b
func TestRecursiveEscapeReturn(n int, b *int) *int {
	if n <= 0 {
		return b // want `BORROW001: borrow stored in struct field 'b' escapes stack frame`
	}
	return nil
}

// ============================================================================
// 7. DeclStmt with Var Specs (Borrow Propagation)
// ============================================================================

// 7.1 Var declaration propagating borrow, then escaping via return
//gosree:mut b
func TestVarDeclPropagation(b *int) *int {
	var b2 = b
	return b2 // want `BORROW001: borrow stored in struct field 'b2' escapes stack frame`
}

// 7.2 Var declaration group storing to global
//gosree:mut b
func TestVarDeclGroup(b *int) {
	var (
		x  = 10
		b2 = b
	)
	_ = x
	GlobalTarget = b2 // want `BORROW001: package variable 'GlobalTarget' cannot store second-class borrow`
}

// ============================================================================
// 8. Array Storage and Array Interface Boxing
// ============================================================================

// 8.1 Fixed-size array storing borrow (index assign and composite lit)
//gosree:mut b
func TestArrayStorage(b *int) {
	var arr [2]*int
	arr[0] = b          // want `BORROW001: array cannot store second-class borrow`
	_ = [2]*int{b, nil} // want `BORROW001: array cannot store second-class borrow`
}

// 8.2 Array of interface elements boxing borrow
//gosree:mut b
func TestArrayInterfaceBoxing(b *int) {
	var arr [2]any
	arr[0] = b         // want `BORROW001: struct field or interface cannot box second-class borrow`
	_ = [2]any{b, nil} // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// ============================================================================
// 9. Nested FieldAddr & Deep Struct Storage
// ============================================================================

// 9.1 Multi-level nested fieldAddr escaping via return
//gosree:mut b
func TestNestedStackTupleEscape(b *int) OuterNode {
	var o OuterNode
	o.m.in.p = b // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
	return o    // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// ============================================================================
// 10. Dangling Stack Reference Returns (BORROW004)
// ============================================================================

// 10.1 Dangling reference to local struct field
func TestDanglingStructField() *int {
	var s LocalStruct
	return &s.Val // want `BORROW004: cannot return reference to local stack variable`
}

// 10.2 Dangling reference to local scalar variable
func TestDanglingLocalScalar() *int {
	var a int
	return &a // want `BORROW004: cannot return reference to local stack variable 'a'`
}

// ============================================================================
// 11. IncDecStmt & Compound Assignment Invalidation (BORROW003)
// ============================================================================

// 11.1 Owner increment while borrowed
func TestOwnerIncInvalidation() {
	x := 10
	b := &x /*gosree:rob*/
	x++     // want `BORROW003: cannot assign to 'x' because it is borrowed`
	println(*b)
}

// 11.2 Owner decrement while borrowed
func TestOwnerDecInvalidation() {
	x := 10
	b := &x /*gosree:rob*/
	x--     // want `BORROW003: cannot assign to 'x' because it is borrowed`
	println(*b)
}

// 11.3 Owner compound assignment while borrowed
func TestOwnerCompoundAssign() {
	x := 10
	b := &x /*gosree:rob*/
	x += 5  // want `BORROW003: cannot assign to 'x' because it is borrowed`
	println(*b)
}

// ============================================================================
// 12. Struct Field & Array Element Owner Invalidation (BORROW003)
// ============================================================================

// 12.1 Borrow struct field, then mutate sibling field of owner
func TestOwnerStructFieldMutation() {
	var pt LocalPoint
	b := &pt.X /*gosree:rob*/
	pt.Y = 20  // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	println(*b)
}

// 12.2 Borrow struct field, then send owner struct to channel
func TestOwnerStructFieldChannelMove(ch chan LocalPoint) {
	var pt LocalPoint
	b := &pt.X /*gosree:rob*/
	ch <- pt   // want `BORROW003: cannot move 'pt' because it is borrowed`
	println(*b)
}

// 12.3 Borrow array element, then mutate array owner
func TestOwnerArrayElementMutation() {
	var arr [4]int
	b := &arr[0] /*gosree:rob*/
	arr[1] = 99  // want `BORROW003: cannot assign to 'arr' because it is borrowed`
	println(*b)
}

// 12.4 Borrow array element, then move array owner to channel
func TestOwnerArrayElementChannelMove(ch chan [4]int) {
	var arr [4]int
	b := &arr[0] /*gosree:rob*/
	ch <- arr    // want `BORROW003: cannot move 'arr' because it is borrowed`
	println(*b)
}

// ============================================================================
// 13. Heap Pointer Borrow Storage (BORROW001)
// ============================================================================

type StackTuple struct {
	p *int
}

// 13.1 Storing borrow into heap-allocated pointer via new(*int)
//gosree:mut b
func TestHeapPointerStore(b *int) {
	hp := new(*int)
	*hp = b // want `BORROW001: cannot store second-class borrow to heap or non-stack memory`
}

// 13.2 Storing borrow into field of heap-allocated struct via new(StackTuple)
//gosree:mut b
func TestHeapStructFieldStore(b *int) {
	h := new(StackTuple)
	h.p = b // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// ============================================================================
// 14. Interface Return Boxing & Concurrency Escapes (BORROW001)
// ============================================================================

// 14.1 Direct borrow returned boxed as any
//gosree:mut b
func TestReturnDirectInterfaceBoxing(b *int) any {
	return b // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 14.2 Local stack tuple holding borrow returned boxed as any
//gosree:mut b
func TestReturnTupleInterfaceBoxing(b *int) any {
	t := StackTuple{p: b}
	return t // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 14.3 Channel send of borrow
//gosree:mut b
func TestChannelSendBorrow(ch chan *int, b *int) {
	ch <- b // want `BORROW001: channel send cannot store second-class borrow`
}

// 14.4 Channel send of tuple holding borrow
//gosree:mut b
func TestChannelSendTupleHoldingBorrow(ch chan StackTuple, b *int) {
	t := StackTuple{p: b}
	ch <- t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 14.5 Goroutine capturing borrow via call argument
//gosree:mut b
func TestGoroutineArgBorrow(b *int) {
	go dummyInspect(b) // want `BORROW001: goroutine cannot capture second-class borrow`
}

func dummyTupleSink(t StackTuple) {
	_ = t
}

// 14.6 Goroutine capturing tuple holding borrow via call argument
//gosree:mut b
func TestGoroutineArgTuple(b *int) {
	t := StackTuple{p: b}
	go dummyTupleSink(t) // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// ============================================================================
// 15. Package Global Declarations with Marker Types (BORROW001)
// ============================================================================

var (
	GlobalMarkerMut Mut[int] // want `BORROW001: package variable 'GlobalMarkerMut' cannot store second-class borrow`
	GlobalMarkerRob Rob[int] // want `BORROW001: package variable 'GlobalMarkerRob' cannot store second-class borrow`
)
