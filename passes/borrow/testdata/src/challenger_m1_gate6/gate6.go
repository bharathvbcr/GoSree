package gate6

type StackTuple struct {
	p *int
}

type Level1 struct {
	V any
}

type Level2 struct {
	L1 Level1
}

type Level3 struct {
	L2 Level2
}

type Level1Tuple struct {
	V any
}

type Level2Tuple struct {
	L1 Level1Tuple
}

type Level3Tuple struct {
	L2 Level2Tuple
}

type L1Clean struct {
	p *int
}

type L2Clean struct {
	inner L1Clean
}

type L3Clean struct {
	mid L2Clean
}

var GlobalAny any

// ============================================================================
// 1. Multi-Level Struct Nesting with Interface Fields
// ============================================================================

//gocap:mut b
func Adv_Nest_DirectLiteral(b *int) {
	_ = Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
}

//gocap:mut b
func Adv_Nest_FieldAssign(b *int) {
	var l3 Level3
	l3.L2.L1.V = b // want `BORROW001`
}

//gocap:mut b
func Adv_Nest_ExtractReturn(b *int) *int {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	return l3.L2.L1.V.(*int)                   // want `BORROW001`
}

//gocap:mut b
func Adv_Nest_ReturnWhole(b *int) Level3 {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	return l3                                  // want `BORROW001`
}

//gocap:mut b
func Adv_Nest_SendChan(ch chan any, b *int) {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	ch <- l3.L2.L1.V                           // want `BORROW001`
}

//gocap:mut b
func Adv_Nest_Goroutine(b *int) {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	go func(v any) {}(l3.L2.L1.V)
}

//gocap:mut b
func Adv_Nest_AssignGlobal(b *int) {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	GlobalAny = l3.L2.L1.V
}

//gocap:mut b
func Adv_Nest_TupleInInterface(b *int) {
	t := StackTuple{p: b}
	_ = Level3Tuple{L2: Level2Tuple{L1: Level1Tuple{V: t}}} // want `BORROW001`
}

//gocap:mut b
func Adv_Nest_TupleInInterface_ExtractReturn(b *int) *int {
	t := StackTuple{p: b}
	l3 := Level3Tuple{L2: Level2Tuple{L1: Level1Tuple{V: t}}} // want `BORROW001`
	extracted := l3.L2.L1.V.(StackTuple)
	return extracted.p // want `BORROW001` `BORROW004`
}

//gocap:mut b
func Adv_Nest_TypeSwitch(b *int) *int {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	switch v := l3.L2.L1.V.(type) {
	case *int:
		return v
	}
	return nil
}

//gocap:mut b
func Adv_Nest_TypeAssert_CommaOk(b *int) *int {
	l3 := Level3{L2: Level2{L1: Level1{V: b}}} // want `BORROW001`
	if v, ok := l3.L2.L1.V.(*int); ok {
		return v
	}
	return nil
}

//gocap:mut b
func Adv_ChainedTypeAssert(b *int) *int {
	h := AnyHolder{V: AnyHolder{V: b}} // want `BORROW001`
	return h.V.(AnyHolder).V.(*int)    // want `BORROW001`
}

// ============================================================================
// 2. Type Switches with Borrows & Interfaces
// ============================================================================

type AnyHolder struct {
	V any
}

//gocap:mut b
func Adv_TypeSwitch_ReturnExtracted(b *int) *int {
	h := AnyHolder{V: b} // want `BORROW001`
	switch v := h.V.(type) {
	case *int:
		return v
	}
	return nil
}

//gocap:mut b
func Adv_TypeSwitch_SendChan(ch chan *int, b *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	switch v := h.V.(type) {
	case *int:
		ch <- v
	}
}

//gocap:mut b
func Adv_TypeSwitch_Goroutine(b *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	switch v := h.V.(type) {
	case *int:
		go func(p *int) {}(v)
	}
}

//gocap:mut b
func Adv_TypeSwitch_AssignGlobal(b *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	switch v := h.V.(type) {
	case *int:
		GlobalAny = v
	}
}

//gocap:mut b
func Adv_TypeSwitch_DirectAny(b *int) *int {
	switch v := any(b).(type) { // want `BORROW001`
	case *int:
		return v
	}
	return nil
}

//gocap:mut b
func Adv_TypeSwitch_TupleAny(b *int) *int {
	t := StackTuple{p: b}
	switch v := any(t).(type) { // want `BORROW001`
	case StackTuple:
		return v.p // want `BORROW004`
	}
	return nil
}

//gocap:mut b
func Adv_TypeSwitch_FieldAssign(b *int) *int {
	var h AnyHolder
	h.V = b // want `BORROW001`
	switch v := h.V.(type) {
	case *int:
		return v
	}
	return nil
}

//gocap:mut b
func Adv_TypeSwitch_Default(b *int) *int {
	h := AnyHolder{V: b} // want `BORROW001`
	switch v := h.V.(type) {
	case string:
		return nil
	default:
		return v.(*int) // want `BORROW001`
	}
}

//gocap:mut b
func Adv_TypeAssert_CommaOk_Return(b *int) *int {
	h := AnyHolder{V: b} // want `BORROW001`
	if v, ok := h.V.(*int); ok {
		return v
	}
	return nil
}

//gocap:mut b
func Adv_TypeAssert_CommaOk_SendChan(ch chan *int, b *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	if v, ok := h.V.(*int); ok {
		ch <- v
	}
}

//gocap:mut b
func Adv_TypeAssert_CommaOk_Goroutine(b *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	if v, ok := h.V.(*int); ok {
		go func(p *int) {}(v)
	}
}

//gocap:mut b
func Adv_TypeAssert_CommaOk_AssignGlobal(b *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	if v, ok := h.V.(*int); ok {
		GlobalAny = v
	}
}

type Stringer interface {
	String() string
}

type CustomHolder struct {
	S Stringer
}

type BorrowStringer struct {
	p *int
}

func (b BorrowStringer) String() string { return "borrow" }

//gocap:mut b
func Adv_CustomInterface(b *int) {
	bs := BorrowStringer{p: b}
	_ = CustomHolder{S: bs} // want `BORROW001`
}

type EmbedInterface struct {
	any
}

//gocap:mut b
func Adv_EmbedInterface(b *int) {
	_ = EmbedInterface{b} // want `BORROW001`
}

// ============================================================================
// 3. Anonymous Struct Literals
// ============================================================================

//gocap:mut b
func Adv_AnonStruct_InterfaceField(b *int) {
	_ = struct{ V any }{V: b} // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_InterfaceField_Unkeyed(b *int) {
	_ = struct{ V any }{b} // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_ReturnDirect(b *int) struct{ p *int } {
	return struct{ p *int }{p: b} // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_ReturnVar(b *int) struct{ p *int } {
	s := struct{ p *int }{p: b}
	return s // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_ReturnAny(b *int) any {
	return struct{ V any }{V: b} // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_ExtractReturn(b *int) *int {
	s := struct{ V any }{V: b}
	return s.V.(*int) // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_ExtractSendChan(ch chan *int, b *int) {
	s := struct{ V any }{V: b}
	ch <- s.V.(*int) // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_ExtractGoroutine(b *int) {
	s := struct{ V any }{V: b}
	go func(p *int) {}(s.V.(*int)) // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_SliceOfBorrows(b *int) {
	_ = struct{ S []*int }{S: []*int{b}} // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_MapOfBorrows(b *int) {
	_ = struct{ M map[int]*int }{M: map[int]*int{0: b}} // want `BORROW001`
}

//gocap:mut b
func Adv_AnonStruct_Nested(b *int) {
	_ = struct {
		Inner struct{ V any }
	}{
		Inner: struct{ V any }{V: b}, // want `BORROW001`
	}
}

//gocap:mut b
func Adv_AnonStruct_TypeSwitch(b *int) *int {
	switch v := (struct{ V any }{V: b}).V.(type) { // want `BORROW001`
	case *int:
		return v
	}
	return nil
}

//gocap:mut b
func Adv_AnonStruct_CommaOk(b *int) *int {
	s := struct{ V any }{V: b} // want `BORROW001`
	if v, ok := s.V.(*int); ok {
		return v
	}
	return nil
}

//gocap:mut b
func Adv_AnonStruct_MixedFields(b *int) *int {
	s := struct {
		x int
		y *int
		z any
	}{1, b, "ok"}
	return s.y // want `BORROW001` `BORROW004`
}

//gocap:mut b
func Adv_MultiVarAssign(b *int) (*int, *int) {
	h := AnyHolder{V: b} // want `BORROW001`
	x, y := 1, h.V.(*int)
	return &x, y // want `BORROW001` `BORROW004`
}

// ============================================================================
// 4. Clean Positive Controls (Must NOT report BORROW001)
// ============================================================================

func Adv_Clean_AnonStructRead() int {
	x := 10
	//gocap:mut
	b := &x
	s := struct{ p *int }{p: b}
	return *s.p
}

func Adv_Clean_NestedStructRead() int {
	x := 10
	//gocap:mut
	b := &x
	l := L3Clean{mid: L2Clean{inner: L1Clean{p: b}}}
	return *l.mid.inner.p
}

func Adv_Clean_TypeSwitchSafe() int {
	var x any = 42
	switch v := x.(type) {
	case int:
		return v
	}
	return 0
}

type SafeStruct struct {
	x int
}

func Adv_Clean_TypeSwitchSafeStruct() int {
	var x any = SafeStruct{x: 10}
	switch v := x.(type) {
	case SafeStruct:
		return v.x
	}
	return 0
}
