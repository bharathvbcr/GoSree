package storage_adversarial

// ============================================================================
// Objective 1: Nested Struct Types Containing Borrow Fields
// ============================================================================

type InnerWithBorrow struct {
	//gosree:mut
	p *int // want `BORROW001: struct field 'p' cannot have second-class borrow type`
}

type OuterHoldingInner struct {
	inner InnerWithBorrow
}

type StackInner struct {
	p *int
}

type StackOuter struct {
	inner StackInner
}

// 1.1: Clean local nested stack tuple (no escape) -> Expected: CLEAN PASS
func NestedStackTupleClean() {
	x := 10
	//gosree:mut
	b := &x
	out := StackOuter{inner: StackInner{p: b}}
	_ = *out.inner.p
}

// 1.2: Nested stack tuple escaping via return -> Expected: BORROW001
func NestedStackTupleEscapeReturn() StackOuter {
	x := 10
	//gosree:mut
	b := &x
	out := StackOuter{inner: StackInner{p: b}}
	return out // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 1.3: Nested stack tuple escaping via global store -> Expected: BORROW001
var globalOuter StackOuter

func NestedStackTupleEscapeGlobal() {
	x := 10
	//gosree:mut
	b := &x
	out := StackOuter{inner: StackInner{p: b}}
	globalOuter = out // want `BORROW001: package variable 'globalOuter' cannot store second-class borrow`
}

// ============================================================================
// Objective 2: Embedding Struct Types with Borrows
// ============================================================================

// 2.1: Anonymous embedded struct field with borrow annotation -> Expected: BORROW001
type StructWithEmbeddedBorrow struct {
	//gosree:mut
	*int // want `BORROW001: embedded struct field cannot have second-class borrow type`
}

type EmbeddedStackTuple struct {
	p *int
}

type OuterWithEmbed struct {
	EmbeddedStackTuple
}

// 2.2: Clean local embedded stack tuple -> Expected: CLEAN PASS
func EmbeddedStackTupleClean() {
	x := 10
	//gosree:mut
	b := &x
	s := OuterWithEmbed{EmbeddedStackTuple: EmbeddedStackTuple{p: b}}
	_ = *s.p
}

// 2.3: Embedded stack tuple escaping via return -> Expected: BORROW001
func EmbeddedStackTupleEscapeReturn() OuterWithEmbed {
	x := 10
	//gosree:mut
	b := &x
	s := OuterWithEmbed{EmbeddedStackTuple: EmbeddedStackTuple{p: b}}
	return s // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 2.4: Embedded stack tuple escaping via global store -> Expected: BORROW001
var globalEmbed OuterWithEmbed

func EmbeddedStackTupleEscapeGlobal() {
	x := 10
	//gosree:mut
	b := &x
	s := OuterWithEmbed{EmbeddedStackTuple: EmbeddedStackTuple{p: b}}
	globalEmbed = s // want `BORROW001: package variable 'globalEmbed' cannot store second-class borrow`
}

// ============================================================================
// Objective 3: Storing Stack Tuples Holding Borrows into Maps, Slices, Channels
// ============================================================================

type StackTuple struct {
	p *int
}

// 3.1: Storing stack tuple holding borrow into a heap map -> Expected: BORROW001
func EscapeToMap() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	m := make(map[int]StackTuple)
	m[0] = t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 3.2: Storing borrow directly into a map -> Expected: BORROW001
func BorrowDirectToMap() {
	x := 10
	//gosree:mut
	b := &x
	m := make(map[int]*int)
	m[0] = b // want `BORROW001`
}

// 3.3: Storing stack tuple holding borrow into a slice element -> Expected: BORROW001
func EscapeToSlice() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	s := make([]StackTuple, 1)
	s[0] = t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 3.4: Appending stack tuple holding borrow to a slice -> Expected: BORROW001
func EscapeViaAppend() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	var s []StackTuple
	s = append(s, t) // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
	_ = s
}

// 3.5: Storing borrow directly into a slice element -> Expected: BORROW001
func BorrowDirectToSlice() {
	x := 10
	//gosree:mut
	b := &x
	s := make([]*int, 1)
	s[0] = b // want `BORROW001`
}

// 3.6: Storing stack tuple holding borrow into a channel -> Expected: BORROW001
func EscapeToChannel(ch chan StackTuple) {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	ch <- t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 3.7: Storing borrow directly into a channel -> Expected: BORROW001
func BorrowDirectToChannel(ch chan *int) {
	x := 10
	//gosree:mut
	b := &x
	ch <- b // want `BORROW001`
}

// ============================================================================
// Objective 4: Boxing Stack Tuples and Borrows into Interfaces
// ============================================================================

// 4.1: Boxing stack tuple holding borrow into any -> Expected: BORROW001
func BoxStackTupleToInterface() any {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	var i any = t // want `BORROW001: struct field or interface cannot box second-class borrow`
	return i
}

// 4.2: Boxing borrow directly into any -> Expected: BORROW001
func BoxBorrowToInterface() any {
	x := 10
	//gosree:mut
	b := &x
	var i any = b // want `BORROW001: struct field or interface cannot box second-class borrow`
	return i
}

// 4.3: Local interface boxing without return -> Expected: BORROW001
func BoxStackTupleLocal() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	var i any = t // want `BORROW001: struct field or interface cannot box second-class borrow`
	_ = i
}

// ============================================================================
// Objective 5: Assigning Borrows to Global / Heap via Indirection & Helpers
// ============================================================================

var globalTarget *int
var globalTarget2 *int

// 5.1: Assigning borrow to global via pointer indirection -> Expected: BORROW001
func AssignGlobalViaIndirection() {
	x := 42
	//gosree:mut
	b := &x
	p := &globalTarget
	*p = b // want `BORROW001: package variable 'globalTarget' cannot store second-class borrow`
}

func assignHelper(target **int, val *int) {
	*target = val
}

// 5.2: Assigning borrow to global via multi-hop helper -> Expected: BORROW001
func AssignGlobalViaMultiHop() {
	x := 42
	//gosree:mut
	b := &x
	assignHelper(&globalTarget2, b) // want `BORROW001`
}

// 5.3: Assigning borrow directly to a heap-allocated pointer -> Expected: BORROW001
func AssignBorrowToHeapPointer() {
	x := 42
	//gosree:mut
	b := &x
	h := new(*int)
	*h = b // want `BORROW001`
}

// ============================================================================
// Objective 6: Transient Local Stack Tuples Without Escape (Clean Pass)
// ============================================================================

// 6.1: Clean local stack tuple with dereference return (returns primitive int) -> Expected: CLEAN PASS
func CleanStackTupleSimple() int {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	*t.p = 20
	return *t.p // clean primitive return: must NOT be flagged as BORROW001
}

// 6.2: Clean local stack tuple with field update -> Expected: CLEAN PASS
func CleanStackTupleFieldUpdate() int {
	x := 10
	y := 20
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	//gosree:mut
	b2 := &y
	t.p = b2
	return *t.p // clean primitive return: must NOT be flagged as BORROW001
}

// 6.3: Clean separate scopes with transient stack tuples -> Expected: CLEAN PASS
func CleanStackTupleScopes() {
	{
		x := 10
		//gosree:mut
		b := &x
		t1 := StackTuple{p: b}
		_ = *t1.p
	}
	{
		y := 20
		//gosree:rob
		b2 := &y
		t2 := StackTuple{p: b2}
		_ = *t2.p
	}
}

// ============================================================================
// Objective 7: Parameter Borrows Escaping Directly
// ============================================================================

// 7.1: Parameter borrow boxed into interface -> Expected: BORROW001
//gosree:mut b
func ParamBorrowToInterface(b *int) any {
	var i any = b // want `BORROW001: struct field or interface cannot box second-class borrow`
	return i
}

// 7.2: Parameter borrow stored in map -> Expected: BORROW001
//gosree:mut b
func ParamBorrowToMap(b *int) {
	m := make(map[int]*int)
	m[0] = b // want `BORROW001`
}

// 7.3: Parameter borrow stored in slice -> Expected: BORROW001
//gosree:mut b
func ParamBorrowToSlice(b *int) {
	s := make([]*int, 1)
	s[0] = b // want `BORROW001`
}

// 7.4: Parameter borrow sent over channel -> Expected: BORROW001
//gosree:mut b
func ParamBorrowToChannel(b *int, ch chan *int) {
	ch <- b // want `BORROW001`
}

// 7.5: Parameter borrow stored in global -> Expected: BORROW001
//gosree:mut b
func ParamBorrowToGlobal(b *int) {
	globalTarget = b // want `BORROW001: package variable 'globalTarget' cannot store second-class borrow`
}

// 7.6: Parameter borrow stored in heap pointer -> Expected: BORROW001
//gosree:mut b
func ParamBorrowToHeap(b *int) {
	h := new(*int)
	*h = b // want `BORROW001`
}

// ============================================================================
// Objective 8: Remediated Storage Escape Holes (Challenger 1 Verifications)
// ============================================================================

// 8.1: Stack tuple holding borrow as map key -> Expected: BORROW001
//gosree:mut b
func EscapeMapKeyTuple(b *int) {
	t := StackTuple{p: b}
	m := make(map[StackTuple]int)
	m[t] = 1 // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 8.2: Direct borrow as map key -> Expected: BORROW001
//gosree:mut b
func EscapeMapKeyBorrow(b *int) {
	m := make(map[*int]int)
	m[b] = 1 // want `BORROW001: map cannot store second-class borrow`
}

// 8.3: Map composite literal holding borrow in value -> Expected: BORROW001
//gosree:mut b
func EscapeMapLitValue(b *int) {
	_ = map[int]*int{0: b} // want `BORROW001: map cannot store second-class borrow`
}

// 8.4: Map composite literal holding borrow in key -> Expected: BORROW001
//gosree:mut b
func EscapeMapLitKey(b *int) {
	_ = map[*int]int{b: 0} // want `BORROW001: map cannot store second-class borrow`
}

// 8.5: Slice composite literal holding borrow -> Expected: BORROW001
//gosree:mut b
func EscapeSliceLitBorrow(b *int) {
	_ = []*int{b} // want `BORROW001: slice cannot store second-class borrow`
}

// 8.6: Slice composite literal holding stack tuple -> Expected: BORROW001
//gosree:mut b
func EscapeSliceLitTuple(b *int) {
	t := StackTuple{p: b}
	_ = []StackTuple{t} // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 8.7: Passing borrow to function taking interface -> Expected: BORROW001
func interfaceSink(val any) {}

//gosree:mut b
func EscapeInterfaceCallArgBorrow(b *int) {
	interfaceSink(b) // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 8.8: Passing stack tuple to function taking interface -> Expected: BORROW001
//gosree:mut b
func EscapeInterfaceCallArgTuple(b *int) {
	t := StackTuple{p: b}
	interfaceSink(t) // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 8.9: Returning borrow directly as interface -> Expected: BORROW001
//gosree:mut b
func EscapeInterfaceReturnBorrow(b *int) any {
	return b // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 8.10: Returning stack tuple directly as interface -> Expected: BORROW001
//gosree:mut b
func EscapeInterfaceReturnTuple(b *int) any {
	t := StackTuple{p: b}
	return t // want `BORROW001: struct field or interface cannot box second-class borrow`
}

// 8.11: Storing borrow into heap struct field via new() -> Expected: BORROW001
//gosree:mut b
func EscapeHeapFieldNew(b *int) {
	h := new(StackTuple)
	h.p = b // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 8.12: Storing borrow into pointer parameter field -> Expected: BORROW001
//gosree:mut b
func EscapeHeapFieldParam(h *StackTuple, b *int) {
	h.p = b // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// 8.13: Address-taken borrow variable stored into heap struct field -> Expected: BORROW001
func EscapeAddressTakenAllocHeapField() {
	x := 42
	//gosree:mut
	b := &x
	h := new(StackTuple)
	h.p = b // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}


