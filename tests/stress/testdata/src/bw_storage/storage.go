// Package bw_storage attacks BORROW001: a second-class borrow reaching memory that outlives the frame.
package bw_storage

type Tuple struct{ p *int }

type Node struct{ v *int }

var global *int // HIT(SAFE006)

var globalFn func() int // HIT(SAFE006)

var keepT Tuple // HIT(SAFE006)

// 1. control: storing a borrow parameter in a package variable.
//
//gocap:mub b
func storeGlobal(b *int) {
	global = b // HIT(BORROW001)
}

// 2. a borrow handed to an unannotated callee that stores it. The callee's parameter is a plain
// pointer, so nothing in either frame is reported, yet the borrow now lives in a global.
func keep(p *int) { global = p }

//gocap:mub b
func storeViaCallee(b *int) {
	keep(b) // MISS(BORROW001): keep stores b in a package variable; docs/BORROW_CHECKING_AND_NLL.md §1 "borrows never escape the stack frame"
}

// 3. a closure capturing the borrow, stored in a package variable: the closure environment is heap
// memory that outlives the call.
//
//gocap:rob b
func storeClosureGlobal(b *int) {
	globalFn = func() int { return *b } // MISS(BORROW001): the closure's environment holds b and is stored in a package variable
}

// 4. a closure capturing the borrow, returned: canHoldBorrow has no case for *types.Signature, so a
// func-typed result is never inspected.
//
//gocap:mub b
func returnClosure(b *int) func() {
	return func() { *b = 1 } // MISS(BORROW001): the returned closure carries b out of the frame
}

// 5. control for 4: returning the borrow itself.
//
//gocap:mub b
func returnBorrow(b *int) *int {
	return b // HIT(BORROW001)
}

// 6. the transient stack struct exception, then the struct is stored through a helper.
func keepTuple(t Tuple) { keepT = t }

func tupleViaCallee() {
	x := 0
	b := &x //gocap:mut
	t := Tuple{p: b}
	keepTuple(t) // MISS(BORROW001): keepTuple stores t (holding b) in a package variable; the exception requires that t never leave the frame (docs §2)
}

// 7. control for 6: storing the tuple directly in the package variable.
func tupleDirect() {
	x := 0
	b := &x //gocap:mut
	t := Tuple{p: b}
	keepT = t // HIT(BORROW001)
}

// 8. the exception, then the struct escapes in a closure that is returned.
func tupleInReturnedClosure() func() int {
	x := 0
	b := &x //gocap:mut
	t := Tuple{p: b}
	return func() int { return *t.p } // MISS(BORROW001): the returned closure captures t, which holds b; docs §2 "never leaves the current activation frame"
}

// 9. a pointer to the stack tuple is returned through a second name.
func tupleAddrAlias() *Tuple {
	x := 0
	b := &x //gocap:mut
	t := Tuple{p: b}
	pt := &t
	return pt // HIT(BORROW001) FP(BORROW004): returning the address of a local is safe in Go (escape analysis moves t to the heap); see bw_return for the constructor shape
}

// 10. a named type whose underlying type is the borrow, stored in a global after conversion.
type IntPtr *int

//gocap:mub b
func namedConversion(b *int) {
	q := IntPtr(b)
	global = q // HIT(BORROW001)
}

// 11. stored into a slice element reached through a pointer to the slice.
//
//gocap:mub b
func sliceThroughPointer(ps *[]*int, b *int) {
	(*ps)[0] = b // HIT(BORROW001)
}

// 12. a func value sent on a channel: the receiver runs it on another goroutine with b inside.
//
//gocap:mub b
func sendClosure(ch chan func(), b *int) {
	ch <- func() { *b = 1 } // HIT(SAFE002) MISS(BORROW001): the channel stores a closure holding b; MISS(CAP003): the receiving goroutine calls it and writes through b (the program is still rejected, by the ownership pass's SAFE002)
}

// 13. FP: a function with an ordinary local named like a borrow elsewhere in the package. The SSA
// mapping seeds isBorrowVar from every \mub/\rob variable in the package and then marks any local
// whose alloc Comment equals one of their names. `b` here is a plain int, unrelated to the borrow
// `b` in the functions above, yet storing its address in a heap node is reported.
func linkCounter(n *Node) {
	var b int
	n.v = &b // FP(BORROW001): this b is an ordinary local, not a borrow; renaming it (see linkCounterRenamed) makes the report go away
}

// 14. control for 13: the same code with the local renamed.
func linkCounterRenamed(n *Node) {
	var c int
	n.v = &c
}

// 15. FP probe: a borrow passed down to a helper that only reads it.
func readOnly(p *int) int { return *p }

//gocap:rob b
func passDown(b *int) int {
	return readOnly(b) + 1
}

// 16. FP probe: the documented transient tuple, passed by value to a reader.
func readTuple(t Tuple) int { return *t.p }

func tupleRead() int {
	x := 0
	b := &x //gocap:mut
	t := Tuple{p: b}
	return readTuple(t)
}
