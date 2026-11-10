package gate5

type StackTuple struct {
	p *int
}

type NestedKey struct {
	Inner StackTuple
}

type SliceHolder struct {
	S []*int
}

type MapHolder struct {
	M map[int]*int
}

type AnyHolder struct {
	V any
}

type MethodSink struct{}

func (MethodSink) Take(v any) {}

type AnySink interface {
	Take(v any)
}

type HeapOuter struct {
	Inner *StackTuple
}

type HeapEmbed struct {
	StackTuple
}

func getHeap() *StackTuple {
	return new(StackTuple)
}

func VariadicSink(args ...any) {}

func Apply(fn func(any), val any) {
	fn(val)
}

// ============================================================================
// 1. Map Keys & Updates Adversarial Attacks
// ============================================================================

//gocap:mut b
func Adv_MapKey_IncDec(b *int) {
	t := StackTuple{p: b}
	m := make(map[StackTuple]int)
	m[t]++ // want `BORROW001`
}

//gocap:mut b
func Adv_MapKey_AddAssign(b *int) {
	t := StackTuple{p: b}
	m := make(map[StackTuple]int)
	m[t] += 1 // want `BORROW001`
}

//gocap:mut b
func Adv_MapKey_DirectBorrow_IncDec(b *int) {
	m := make(map[*int]int)
	m[b]++ // want `BORROW001`
}

//gocap:mut b
func Adv_MapKey_NestedStruct(b *int) {
	t := StackTuple{p: b}
	m := make(map[NestedKey]int)
	m[NestedKey{Inner: t}] = 1 // want `BORROW001`
}

//gocap:mut b
func Adv_MapKey_AnyInterface(b *int) {
	m := make(map[any]int)
	m[b] = 1 // want `BORROW001`
}

//gocap:mut b
func Adv_MapKey_AnyInterfaceTuple(b *int) {
	t := StackTuple{p: b}
	m := make(map[any]int)
	m[t] = 1 // want `BORROW001`
}

//gocap:mut b
func Adv_MapKey_NestedMap(b *int) {
	t := StackTuple{p: b}
	m := make(map[string]map[StackTuple]int)
	m["k"][t] = 1 // want `BORROW001`
}

// ============================================================================
// 2. Composite Literals Adversarial Attacks
// ============================================================================

//gocap:mut b
func Adv_CompLit_MapKeyTuple(b *int) {
	t := StackTuple{p: b}
	_ = map[StackTuple]int{t: 1} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_NestedSlice(b *int) {
	_ = [][]*int{{b}} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_NestedMap(b *int) {
	_ = map[string]map[int]*int{"k": {0: b}} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_SliceInStruct(b *int) {
	_ = SliceHolder{S: []*int{b}} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_MapInStruct(b *int) {
	_ = MapHolder{M: map[int]*int{0: b}} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_InterfaceStruct(b *int) {
	_ = AnyHolder{V: b} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_InterfaceStructTuple(b *int) {
	t := StackTuple{p: b}
	_ = AnyHolder{V: t} // want `BORROW001`
}

//gocap:mut b
func Adv_CompLit_ReturnInterfaceStruct(b *int) AnyHolder {
	return AnyHolder{V: b} // want `BORROW001`
}

//gocap:mut b
func Adv_Field_Assign_Interface(b *int) {
	h := AnyHolder{}
	h.V = b // want `BORROW001`
}

//gocap:mut b
func Adv_EscapeExtractFromInterfaceField(b *int) *int {
	h := AnyHolder{}
	h.V = b
	return h.V.(*int) // want `BORROW001`
}

//gocap:mut b
func Adv_EscapeExtractFromLiteral(b *int) *int {
	h := AnyHolder{V: b}
	return h.V.(*int) // want `BORROW001`
}

var GlobalAny any

//gocap:mut b
func Adv_EscapeToGlobalViaInterfaceField(b *int) {
	h := AnyHolder{V: b}
	GlobalAny = h.V // want `BORROW001`
}

//gocap:mut b
func Adv_EscapeToChannelViaTypeAssert(ch chan *int, b *int) {
	h := AnyHolder{V: b}
	ch <- h.V.(*int) // want `BORROW001`
}

//gocap:mut b
func Adv_EscapeToGoroutineViaTypeAssert(b *int) {
	h := AnyHolder{V: b}
	go func(p *int) {}(h.V.(*int)) // want `BORROW001`
}

//gocap:mut b
func Adv_Array_Interface(b *int) {
	var arr [1]any
	arr[0] = b // want `BORROW001`
}

//gocap:mut b
func Adv_Array_CompLit(b *int) {
	_ = [1]any{b} // want `BORROW001`
}

//gocap:mut b
func Adv_Array_Borrow_CompLit(b *int) {
	_ = [1]*int{b} // want `BORROW001`
}

// ============================================================================
// 3. Interface Call Boxing Adversarial Attacks
// ============================================================================

//gocap:mut b
func Adv_InterfaceCall_MethodConcrete(b *int) {
	MethodSink{}.Take(b) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_MethodConcreteTuple(b *int) {
	t := StackTuple{p: b}
	MethodSink{}.Take(t) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_MethodInterface(s AnySink, b *int) {
	s.Take(b) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_MethodInterfaceTuple(s AnySink, b *int) {
	t := StackTuple{p: b}
	s.Take(t) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_Variadic(b *int) {
	VariadicSink(1, b, 2) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_VariadicTuple(b *int) {
	t := StackTuple{p: b}
	VariadicSink(1, t, 2) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_AnonFunc(b *int) {
	func(x any) {}(b) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_AnonFuncTuple(b *int) {
	t := StackTuple{p: b}
	func(x any) {}(t) // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceCall_HigherOrder(b *int) {
	Apply(func(any) {}, b) // want `BORROW001`
}

// ============================================================================
// 4. Interface Return Boxing Adversarial Attacks
// ============================================================================

//gocap:mut b
func Adv_InterfaceRet_MultiDirect(b *int) (int, any) {
	return 1, b // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceRet_MultiTuple(b *int) (int, any) {
	t := StackTuple{p: b}
	return 1, t // want `BORROW001`
}

//gocap:mut b
func Adv_InterfaceRet_NamedDirect(b *int) (res any) {
	res = b // want `BORROW001`
	return
}

//gocap:mut b
func Adv_InterfaceRet_NamedTuple(b *int) (res any) {
	t := StackTuple{p: b}
	res = t // want `BORROW001`
	return
}

// ============================================================================
// 5. Heap Struct Field Stores Adversarial Attacks
// ============================================================================

//gocap:mut b
func (h *StackTuple) Set(b *int) {
	h.p = b // want `BORROW001`
}

//gocap:mut b
func Adv_HeapStore_ChainedSelector(b *int) {
	ho := new(HeapOuter)
	ho.Inner = new(StackTuple)
	ho.Inner.p = b // want `BORROW001`
}

//gocap:mut b
func Adv_HeapStore_FuncReturnHeap(b *int) {
	getHeap().p = b // want `BORROW001`
}

//gocap:mut b
func Adv_HeapStore_EmbeddedHeap(b *int) {
	he := new(HeapEmbed)
	he.p = b // want `BORROW001`
}

// ============================================================================
// 6. SSA Alloc Tracking & Address-Taken Variables
// ============================================================================

func Adv_Alloc_ConditionalBlock() {
	var b *int
	if true {
		x := 10
		//gocap:mut
		b = &x
	}
	h := new(StackTuple)
	h.p = b // want `BORROW001`
}

func Adv_Alloc_LoopBlock() {
	for i := 0; i < 1; i++ {
		x := 10
		//gocap:mut
		b := &x
		h := new(StackTuple)
		h.p = b // want `BORROW001`
	}
}

func Adv_Alloc_MultiAlias() {
	x := 10
	//gocap:mut
	b1 := &x
	b2 := b1
	b3 := b2
	h := new(StackTuple)
	h.p = b3 // want `BORROW001`
}

func Adv_Alloc_TupleCopy() {
	x := 10
	//gocap:mut
	b := &x
	t1 := StackTuple{p: b}
	t2 := t1
	h := new(StackTuple)
	h.p = t2.p // want `BORROW001`
}

// ============================================================================
// 7. Clean Valid Positive Controls (Must NOT report BORROW001)
// ============================================================================

func Adv_Clean_StackTupleFieldRead() int {
	x := 10
	//gocap:mut
	b := &x
	t := StackTuple{p: b}
	return *t.p
}

func Adv_Clean_StackTupleFieldUpdate() int {
	x := 10
	y := 20
	//gocap:mut
	b1 := &x
	//gocap:mut
	b2 := &y
	t := StackTuple{p: b1}
	t.p = b2
	return *t.p
}

func Adv_Clean_StackTupleInSeparateBlocks() {
	{
		x := 1
		//gocap:mut
		b := &x
		t := StackTuple{p: b}
		_ = *t.p
	}
	{
		y := 2
		//gocap:mut
		b := &y
		t := StackTuple{p: b}
		_ = *t.p
	}
}

func Adv_Clean_MapKeyInt() {
	m := make(map[int]int)
	m[1] = 2
}

func Adv_Clean_NonBorrowCall() {
	f := func(v any) {}
	f(42)
}
