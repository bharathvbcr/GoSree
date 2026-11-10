// Package bw_invalidate attacks BORROW003: the owner of a live borrow is written or moved by a route
// other than a plain `x = ...` statement.
package bw_invalidate

type Counter struct{ N int }

func (c *Counter) Set(n int) { c.N = n }

// 1. control: direct assignment while borrowed.
func directAssign() {
	x := 0
	b := &x //gocap:rob
	x = 1   // HIT(BORROW003)
	_ = *b
}

// 2. pointer-receiver method on the owner: `c.Set(1)` takes &c implicitly. Rust rejects
// `c.set(1)` while `&c.n` is live (E0502).
func methodReceiver() {
	var c Counter
	b := &c.N //gocap:rob
	c.Set(1)  // HIT(BORROW003): Set takes &c and writes c.N while b reads it
	_ = *b
}

// 3. write through an alias taken before the borrow.
func writeThroughAlias() {
	x := 0
	p := &x
	b := &x //gocap:rob
	*p = 1  // HIT(BORROW003): p == &x, so x changes under the read-only borrow b
	_ = *b
}

// 4. control: `*(&x) = ` is caught, through the address-take.
func derefAddr() {
	x := 0
	b := &x   //gocap:rob
	*(&x) = 1 // HIT(BORROW003)
	_ = *b
}

// 5. copy into the borrowed slice by name: caught.
func copyByName(src []int) {
	s := make([]int, 4)
	b := &s[0]   //gocap:rob
	copy(s, src) // HIT(BORROW003)
	_ = *b
}

// 6. copy into a reslice of the borrowed slice.
func copyBySlice(src []int) {
	s := make([]int, 4)
	b := &s[0]      //gocap:rob
	copy(s[:], src) // HIT(BORROW003): s[:] is s, so s[0] is overwritten under the read-only borrow b
	_ = *b
}

// 7. clear through a reslice.
func clearBySlice() {
	s := []int{1, 2, 3}
	b := &s[0]  //gocap:rob
	clear(s[:]) // HIT(BORROW003): clear zeroes s[0] while b is live
	_ = *b
}

// 8. append that writes into spare capacity without reassigning the owner: append(s[:1], 9) stores
// into s[1].
func appendIntoCapacity() {
	s := make([]int, 2, 4)
	b := &s[1]           //gocap:rob
	_ = append(s[:1], 9) // HIT(BORROW003): writes s[1] in place (cap 4 >= 2), so *b changes to 9 under a read-only borrow
	_ = *b
}

// 9. the iterator-invalidation twin: `p := &s[0]; s = append(s, v)` reallocates and leaves p
// pointing at the old array.
func appendRealloc() int {
	s := []int{1}
	p := &s[0]       //gocap:rob
	s = append(s, 2) // HIT(BORROW003)
	s[0] = 100       // HIT(BORROW003)
	return *p
}

// 10. range assigns to an existing owner variable.
func rangeReassign() {
	x := 0
	b := &x           //gocap:rob
	for x = range 3 { // HIT(BORROW003): each iteration assigns x while b is still read below
		_ = *b
	}
}

// 11. a closure that writes the owner, made BEFORE the borrow, then passed to a helper that calls it.
func runIt(f func()) { f() }

func closureViaHelper() {
	x := 0
	m := func() { x = 9 }
	b := &x  //gocap:rob
	runIt(m) // HIT(BORROW003): runIt calls m, which assigns x while b is live
	_ = *b
}

// 12. control for 11: direct call of the same closure.
func closureDirect() {
	x := 0
	m := func() { x = 9 }
	b := &x //gocap:rob
	m()     // HIT(BORROW003)
	_ = *b
}

// 12b. a closure that writes the owner, created while the borrow is live: the write inside the
// literal is reported at the literal, which matches Rust (the closure captures x by &mut).
func closureMadeWhileBorrowed() {
	x := 0
	b := &x               //gocap:rob
	m := func() { x = 9 } // HIT(BORROW003)
	m()                   // HIT(BORROW003)
	_ = *b
}

// 13. a closure held in a struct field (made before the borrow), called through the field.
type hook struct{ f func() }

func closureInField() {
	x := 0
	h := hook{f: func() { x = 9 }}
	b := &x //gocap:rob
	h.f()   // HIT(BORROW003): h.f assigns x while b is live
	_ = *b
}

// 14. a callee that writes the owner through a second pointer it was given.
func setVia(p *int) { *p = 7 }

func calleeWritesOwner() {
	x := 0
	b := &x    //gocap:rob
	setVia(&x) // HIT(BORROW003)
	_ = *b
}

// 15. the same callee, given the pointer through an alias taken earlier.
func calleeWritesAlias() {
	x := 0
	p := &x
	b := &x   //gocap:rob
	setVia(p) // HIT(BORROW003): p == &x and setVia writes through it while b is live
	_ = *b
}

// 16. whole-struct overwrite `s = Pair{}` while a field is borrowed.
type Pair struct{ A, B int }

func structCopy() {
	var s Pair
	b := &s.A  //gocap:rob
	s = Pair{} // HIT(BORROW003)
	_ = *b
}

// 17. swap through a multi-assign.
func swap() {
	x, y := 1, 2
	b := &x     //gocap:rob
	x, y = y, x // HIT(BORROW003)
	_ = *b
	_ = y
}

// 18. delete on a map while a value read from it is borrowed: Go map values are not addressable, so
// the borrow is of a copy and delete is harmless. FP probe.
func mapDelete(m map[string]Pair) {
	v := m["a"]
	b := &v.A //gocap:rob
	delete(m, "a")
	_ = *b
}

// 19. compound assignment through a field of the owner.
func compoundField() {
	var s Pair
	b := &s.A //gocap:rob
	s.A += 1  // HIT(BORROW003)
	_ = *b
}

// 20. moving the owner to a goroutine (as a pointer) while it is borrowed.
func moveToGoroutine(done chan bool) {
	x := 0
	b := &x                                      //gocap:rob
	go func(p *int) { *p = 1; done <- true }(&x) // HIT(BORROW003) HIT(BORROW001)
	_ = *b                                       // HIT(SAFE003)
}

// 21. the owner (an int) sent on a channel by value while a shared borrow of it is live: the send
// copies x, which is a read. Rust accepts reading a Copy value under a shared borrow
// (`let r = &x; tx.send(x); *r`), and in Go nothing aliases.
func sendOwnerValue(ch chan int) {
	x := 0
	b := &x //gocap:rob
	ch <- x // FP(BORROW003): sending an int copies it; a read under a read-only borrow is allowed by the documented matrix (only writes and moves invalidate, docs §5)
	_ = *b
}

// 22. FP: taking the owner's address after the borrow is dead. The storage pass marks the owner's
// alloc itself as a borrow value (storage.go, "Owner allocation in fn.Locals and fn.Blocks"),
// with no liveness, so publishing &x after b's last use is reported as storing a borrow.
type Node struct{ v *int }

func ownerAddrAfterBorrowDies(n *Node) {
	x := 0
	b := &x //gocap:rob
	_ = *b
	n.v = &x // FP(BORROW001): b is dead (last use on the line above) and &x is an ordinary pointer, not an annotated borrow
}

// 23. control for 22: the same code with no borrow ever taken.
func ownerAddrNoBorrow(n *Node) {
	x := 0
	n.v = &x
}
