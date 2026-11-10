// Package bw_storage attacks BORROW001: a second-class borrow reaching memory that outlives the frame.
package bw_storage

type Tuple struct{ p *int }

type Node struct{ v *int }

var global *int // HIT(SAFE006)

var globalFn func() int // HIT(SAFE006)

var keepT Tuple // HIT(SAFE006)

// 1. control: storing a borrow parameter in a package variable.
//
//gosree:mub b
func storeGlobal(b *int) {
	global = b // HIT(BORROW001)
}

// 2. a borrow handed to an unannotated callee that stores it.
func keep(p *int) { global = p }

//gosree:mub b
func storeViaCallee(b *int) {
	keep(b) // HIT(BORROW001): keep stores b in a package variable
}

// 3. a closure capturing the borrow, stored in a package variable: the closure environment is heap
// memory that outlives the call.
//
//gosree:rob b
func storeClosureGlobal(b *int) {
	globalFn = func() int { return *b } // HIT(BORROW001): the closure's environment holds b and is stored in a package variable
}

// 4. a closure capturing the borrow, returned.
//
//gosree:mub b
func returnClosure(b *int) func() {
	return func() { *b = 1 } // HIT(BORROW001): the returned closure carries b out of the frame
}

// 5. control for 4: returning the borrow itself.
//
//gosree:mub b
func returnBorrow(b *int) *int {
	return b // HIT(BORROW001)
}

// 6. the transient stack struct exception, then the struct is stored through a helper.
func keepTuple(t Tuple) { keepT = t }

func tupleViaCallee() {
	x := 0
	b := &x //gosree:mut
	t := Tuple{p: b}
	keepTuple(t) // HIT(BORROW001): keepTuple stores t (holding b) in a package variable
}

// 7. control for 6: storing the tuple directly in the package variable.
func tupleDirect() {
	x := 0
	b := &x //gosree:mut
	t := Tuple{p: b}
	keepT = t // HIT(BORROW001)
}

// 8. the exception, then the struct escapes in a closure that is returned.
func tupleInReturnedClosure() func() int {
	x := 0
	b := &x //gosree:mut
	t := Tuple{p: b}
	return func() int { return *t.p } // HIT(BORROW001): the returned closure captures t, which holds b
}

// 9. a pointer to the stack tuple is returned through a second name.
func tupleAddrAlias() *Tuple {
	x := 0
	b := &x //gosree:mut
	t := Tuple{p: b}
	pt := &t
	return pt // HIT(BORROW001) FP(BORROW004): returning the address of a local is safe in Go (escape analysis moves t to the heap); see bw_return for the constructor shape
}

// 10. a named type whose underlying type is the borrow, stored in a global after conversion.
type IntPtr *int

//gosree:mub b
func namedConversion(b *int) {
	q := IntPtr(b)
	global = q // HIT(BORROW001)
}

// 11. stored into a slice element reached through a pointer to the slice.
//
//gosree:mub b
func sliceThroughPointer(ps *[]*int, b *int) {
	(*ps)[0] = b // HIT(BORROW001)
}

// 12. a func value sent on a channel: the receiver runs it on another goroutine with b inside.
//
//gosree:mub b
func sendClosure(ch chan func(), b *int) {
	ch <- func() { *b = 1 } // HIT(SAFE002) HIT(BORROW001) MISS(CAP003): the channel stores a closure holding b
}

// 13. FP probe: a function with an ordinary local named like a borrow elsewhere in the package.
func linkCounter(n *Node) {
	var b int
	n.v = &b
}

// 14. control for 13: the same code with the local renamed.
func linkCounterRenamed(n *Node) {
	var c int
	n.v = &c
}

// 15. FP probe: a borrow passed down to a helper that only reads it.
func readOnly(p *int) int { return *p }

//gosree:rob b
func passDown(b *int) int {
	return readOnly(b) + 1
}

// 16. FP probe: the documented transient tuple, passed by value to a reader.
func readTuple(t Tuple) int { return *t.p }

func tupleRead() int {
	x := 0
	b := &x //gosree:mut
	t := Tuple{p: b}
	return readTuple(t)
}
