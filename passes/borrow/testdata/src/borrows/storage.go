package borrows

// ============================================================================
// 1. Named Struct Field Borrow Declarations (BORROW001)
// ============================================================================

// Field with mutable borrow directive in named struct -> BORROW001
type StructWithBorrowField struct {
	//gosree:mut
	ptr *int // want `BORROW001: struct field 'ptr' cannot have second-class borrow type`
}

// Field with read-only borrow trailing comment in named struct -> BORROW001
type StructWithRobField struct {
	val *int //gosree:rob // want `BORROW001: struct field 'val' cannot have second-class borrow type`
}

// Field with legacy safe prefix -> BORROW001
type StructWithLegacyField struct {
	//gosree:mub
	data *string // want `BORROW001: struct field 'data' cannot have second-class borrow type`
}

// ============================================================================
// 2. Global Variable Borrow Declarations & Stores (BORROW001)
// ============================================================================

// Package-level global with doc comment borrow directive -> BORROW001
//gosree:mut
var globalMut *int // want `BORROW001: package variable 'globalMut' cannot store second-class borrow`

// Package-level global with trailing comment borrow directive -> BORROW001
var globalRob *int //gosree:rob // want `BORROW001: package variable 'globalRob' cannot store second-class borrow`

var plainGlobal *int

// Dynamic SSA store of a borrow into a package-level global -> BORROW001
func storeGlobalBorrow() {
	x := 42
	//gosree:mut
	b := &x
	plainGlobal = b // want `BORROW001: package variable 'plainGlobal' cannot store second-class borrow`
}

// ============================================================================
// 3. Transient Stack-Scoped Struct Exception (PASS - Clean 0 diagnostics)
// ============================================================================

type StackTuple struct {
	p *int
}

// Transient stack tuple allocated and used strictly within function activation -> CLEAN PASS
func localStackStructClean() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	_ = *t.p
	*t.p = 20
}

// Clean transient stack tuple with primitive return value -> CLEAN PASS
func localStackStructReturnPrimitive() int {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	*t.p = 20
	return *t.p
}

func localStackStructFieldUpdateReturnPrimitive() int {
	x := 10
	y := 20
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	//gosree:mut
	b2 := &y
	t.p = b2
	return *t.p
}

// ============================================================================
// 4. Escaping Stack Struct Holding Borrows (BORROW001)
// ============================================================================

// Stack struct holding borrow escaping via value return -> BORROW001
func escapeViaReturn() StackTuple {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	return t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// Stack struct holding borrow escaping via pointer return -> BORROW001
func escapeViaPointerReturn() *StackTuple {
	x := 10
	//gosree:mut
	b := &x
	t := &StackTuple{p: b}
	return t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// Stack struct holding borrow escaping via heap store -> BORROW001
func escapeViaHeapStore(heapPtr *StackTuple) {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	*heapPtr = t // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

// Stack struct holding borrow escaping via global store -> BORROW001
var globalTuple StackTuple

func escapeViaGlobalStore() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	globalTuple = t // want `BORROW001: package variable 'globalTuple' cannot store second-class borrow`
}

// ============================================================================
// 5. Unsafe Escape Hatch (Suppressed 0 diagnostics)
// ============================================================================

//gosree:unsafe testing escape hatch
type UnsafeStruct struct {
	//gosree:mut
	p *int
}

// ============================================================================
// 6. Adversarial Storage Escapes (Map Keys, Composite Literals, Interface Boxing, Heap Fields)
// ============================================================================

func escapeMapKey() {
	x := 10
	//gosree:mut
	b := &x
	t := StackTuple{p: b}
	m := make(map[StackTuple]int)
	m[t] = 1 // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

func escapeMapLit() {
	x := 10
	//gosree:mut
	b := &x
	_ = map[int]*int{0: b} // want `BORROW001: map cannot store second-class borrow`
}

func escapeSliceLit() {
	x := 10
	//gosree:mut
	b := &x
	_ = []*int{b} // want `BORROW001: slice cannot store second-class borrow`
}

func boxArgHelper(v any) {}

func escapeInterfaceArg() {
	x := 10
	//gosree:mut
	b := &x
	boxArgHelper(b) // want `BORROW001: struct field or interface cannot box second-class borrow`
}

func escapeInterfaceRet() any {
	x := 10
	//gosree:mut
	b := &x
	return b // want `BORROW001: struct field or interface cannot box second-class borrow`
}

func escapeHeapField() {
	x := 10
	//gosree:mut
	b := &x
	h := new(StackTuple)
	h.p = b // want `BORROW001: borrow stored in struct field 'p' escapes stack frame`
}

