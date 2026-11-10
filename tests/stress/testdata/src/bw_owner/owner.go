// Package bw_owner attacks what the borrow checker treats as "the same allocation" when it decides
// whether two borrows conflict (BORROW002).
package bw_owner

type Pair struct {
	A int
	B int
}

type Outer struct {
	In Pair
}

// 1. control: two mutable borrows of the same field.
func sameField() {
	var s Pair
	b1 := &s.A //gosree:mut
	b2 := &s.A //gosree:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 2. disjoint fields of one struct. Rust accepts `let a = &mut s.a; let b = &mut s.b;` (the borrow
// checker tracks places, not variables); the two pointers can never alias.
func disjointFields() {
	var s Pair
	a := &s.A //gosree:mut
	b := &s.B //gosree:mut // FP(BORROW002): s.A and s.B are disjoint places; ownership is keyed on the root variable s, so any two field borrows of s conflict
	*a = 1
	*b = 2
	*a = 3
}

// 3. control: the whole struct against one of its fields must conflict.
func wholeVsField() {
	var s Pair
	w := &s   //gosree:mut
	f := &s.A //gosree:mut // HIT(BORROW002)
	*f = 1
	w.B = 2
}

// 4. two different fresh allocations: distinct composite literals have distinct synthetic owners.
func freshAllocations() {
	a := &Pair{} //gosree:mut
	b := &Pair{} //gosree:mut
	a.A = 1
	b.A = 2
	a.B = 3
}

// 5. two borrows through two different pointer parameters, written as `&(*p).A` / `&(*q).A`.
func derefDifferentPointers(p, q *Pair) {
	a := &(*p).A //gosree:mut
	b := &(*q).A //gosree:mut
	*a = 1
	*b = 2
	*a = 3
}

// 5b. control for 5: the same borrows written without the explicit deref are told apart.
func derefControl(p, q *Pair) {
	a := &p.A //gosree:mut
	b := &q.A //gosree:mut
	*a = 1
	*b = 2
	*a = 3
}

// 5c. a fresh allocation does not collide with an unrelated local named x.
func defaultNameCollides() {
	x := 0
	a := &x      //gosree:mut
	b := &Pair{} //gosree:mut
	b.A = 1
	*a = 2
}

// 5d. control for 5c: the same code with the local renamed.
func defaultNameRenamed() {
	y := 0
	a := &y      //gosree:mut
	b := &Pair{} //gosree:mut
	b.A = 1
	*a = 2
}

// 6. an alias made before the borrow: p and q are two names for x, reached through a pointer copy.
func aliasBeforeBorrow() {
	x := 0
	p := &x
	q := p
	b1 := p //gosree:mut
	b2 := q //gosree:mut // HIT(BORROW002): q == p == &x, so b1 and b2 are two live mutable borrows of x
	*b2 = 1
	*b1 = 2
}

// 7. control for 6: the direct alias is followed.
func aliasDirect() {
	x := 0
	p := &x
	b1 := p  //gosree:mut
	b2 := &x //gosree:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 8. two parameters that alias at the call site: the callee cannot know, so the caller must not
// pass the same object twice as \mub. The callee holds two "exclusive" borrows of one int.
//
//gosree:mub a, b
func addTo(a, b *int) {
	*a += 1
	*b += 1
}

func callerAliases() {
	x := 0
	addTo(&x, &x) // HIT(BORROW002): two \mub arguments are the same allocation, two simultaneous mutable borrows of x
}

// 9. a field reached through two paths: o.In.A via the struct and via a pointer to o.In.
func nestedPath() {
	var o Outer
	in := &o.In
	b1 := &in.A   //gosree:mut
	b2 := &o.In.A //gosree:mut // HIT(BORROW002): in == &o.In, so b1 and b2 both point at o.In.A
	*b2 = 1
	*b1 = 2
}

// 10. array element against the whole array.
func arrayElemVsWhole() {
	var arr [4]int
	e := &arr[0] //gosree:mut
	w := &arr    //gosree:mut // HIT(BORROW002)
	w[1] = 1
	*e = 2
}

// 11. distinct array elements: in Rust, `split_at_mut` is needed, so a conflict here is the Rust
// answer too. Recorded as a lock.
func arrayDistinctElems() {
	var arr [4]int
	e0 := &arr[0] //gosree:mut
	e1 := &arr[1] //gosree:mut // HIT(BORROW002)
	*e1 = 1
	*e0 = 2
}

// 12. a sub-slice sharing the backing array: t is s[1:], and &t[0] is &s[1].
func subSliceShares(s []int) {
	t := s[1:]
	b1 := &s[1] //gosree:mut
	b2 := &t[0] //gosree:mut // HIT(BORROW002): t[0] and s[1] are the same element of one backing array
	*b2 = 1
	*b1 = 2
}

// 13. converting between named pointer types does not change the allocation.
type IntPtr *int

func namedPointerConversion() {
	x := 0
	b1 := &x         //gosree:mut
	b2 := IntPtr(&x) //gosree:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 14. a borrow of a value behind an interface: both come from the same *Pair inside i.
func behindInterface(i any) {
	p := i.(*Pair)
	q := i.(*Pair)
	b1 := &p.A //gosree:mut
	b2 := &q.A //gosree:mut // HIT(BORROW002): p and q are the same *Pair, unboxed twice from i
	*b2 = 1
	*b1 = 2
}
