// Package mv_alias probes the precision of the per-function alias graph (passes/ownership/graph.go):
// derivations, containers, closures, conversions and callee summaries that carry an alias of a
// moved value past the move. Every attack here is caught today; the file is a map of where the
// Steensgaard unification holds, kept as regression locks. Two clean controls pin the places where
// a copy really does break the alias.
package mv_alias

import "reflect"

type Job struct {
	ID   int
	Tags []string
	Next *Job
}

type Box struct{ P *Job }
type Box2 struct{ B Box }
type Box3 struct{ B *Box2 }

// three levels of struct nesting, the last through a pointer.
func deepField(ch chan *Job) {
	j := &Job{}
	b := &Box3{B: &Box2{B: Box{P: j}}}
	ch <- j
	b.B.B.P.ID = 1 // HIT(SAFE003)
}

// slice of slices: the inner slice is sent, the outer one still reaches its backing array.
func sliceOfSlices(ch chan []int) {
	inner := make([]int, 4)
	outer := [][]int{inner}
	ch <- inner
	outer[0][1] = 1 // HIT(SAFE003)
}

// map of pointers.
func mapOfPtrs(ch chan *Job) {
	j := &Job{}
	m := map[int]*Job{1: j}
	ch <- j
	m[1].ID = 1 // HIT(SAFE003)
}

// pointer to the variable holding the pointer.
func ptrPtr(ch chan *Job) {
	j := &Job{}
	pp := &j
	ch <- j
	(*pp).ID = 1 // HIT(SAFE003)
}

// a closure that returns the alias.
func closureReturns(ch chan *Job) {
	j := &Job{}
	get := func() *Job { return j }
	ch <- j
	get().ID = 1 // HIT(SAFE003)
}

func (j *Job) Set(v int) { j.ID = v }

// method expression called with the moved receiver.
func methodExpr(ch chan *Job) {
	j := &Job{}
	f := (*Job).Set
	ch <- j
	f(j, 1) // HIT(SAFE003)
}

// bound method value taken before the move, called after.
func methodValueLater(ch chan *Job) {
	j := &Job{}
	set := j.Set
	ch <- j
	set(1) // HIT(SAFE003)
}

type Setter interface{ Set(int) }

// interface holding the pointer, method called after the move.
func ifaceMethodAfter(ch chan *Job) {
	j := &Job{}
	var s Setter = j
	ch <- j
	s.Set(1) // HIT(SAFE003)
}

type Outer struct {
	Job
	N int
}

// interior pointer to an embedded field.
func embedded(ch chan *Outer) {
	o := &Outer{}
	inner := &o.Job
	ch <- o
	inner.ID = 1 // HIT(SAFE003)
}

// append into spare capacity: b shares a's backing array; write it through a re-slice of a.
func appendCap(ch chan []*Job) {
	a := make([]*Job, 1, 10)
	b := append(a, &Job{})
	ch <- b
	a[:2][1] = nil // HIT(SAFE003)
}

// a three-index slice still shares its first elements with the parent.
func threeIndex(ch chan []int) {
	a := make([]int, 4, 8)
	b := a[0:2:2]
	ch <- b
	a[0] = 1 // HIT(SAFE003)
}

// copy of a slice of pointers into a fresh slice: the elements are shared.
func copyPtrs(ch chan []*Job) {
	j := &Job{}
	src := []*Job{j}
	dst := make([]*Job, 1)
	copy(dst, src)
	ch <- dst
	j.ID = 1 // HIT(SAFE003)
}

// control: copy of a pointer-free slice shares nothing.
func copyInts(ch chan []int) {
	src := []int{1, 2}
	dst := make([]int, 2)
	copy(dst, src)
	ch <- dst
	src[0] = 3
}

// control: a []byte -> string -> []byte round trip copies.
func bytesString(ch chan []byte) {
	b := []byte("hi")
	c := []byte(string(b))
	ch <- c
	b[0] = 'x'
}

func pack(js ...*Job) []*Job { return js }

// variadic packing through a callee that returns the packed slice.
func variadic(ch chan []*Job) {
	j := &Job{}
	s := pack(j)
	ch <- s
	j.ID = 1 // HIT(SAFE003)
}

func two() (*Job, *Job) {
	j := &Job{}
	return j, j
}

// two results that are the same pointer.
func multiReturn(ch chan *Job) {
	a, b := two()
	ch <- a
	b.ID = 1 // HIT(SAFE003)
}

func named() (a *Job, b *Job) {
	a = &Job{}
	b = a
	return
}

// named results aliased inside the callee.
func namedResults(ch chan *Job) {
	a, b := named()
	ch <- a
	b.ID = 1 // HIT(SAFE003)
}

// type assertion recovers the pointer from an any made before the move.
func typeAssert(ch chan *Job) {
	j := &Job{}
	var x any = j
	ch <- j
	x.(*Job).ID = 1 // HIT(SAFE003)
}

// type switch on an any made before the move.
func typeSwitch(ch chan *Job) {
	j := &Job{}
	var x any = j
	ch <- j
	// The report is on the case line, where the type switch binds v (it was on the switch line
	// before graph.go changed on 2026-11-10).
	switch v := x.(type) {
	case *Job: // HIT(SAFE003)
		v.ID = 1
	}
}

type Stack[T any] struct{ items []T }

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }
func (s *Stack[T]) Top() T   { return s.items[len(s.items)-1] }

// generic container instantiated with a pointer.
func genericContainer(ch chan *Job) {
	j := &Job{}
	s := &Stack[*Job]{}
	s.Push(j)
	ch <- j
	s.Top().ID = 1 // HIT(SAFE003)
}

func id[T any](v T) T { return v }

// generic identity function.
func genericIdentity(ch chan *Job) {
	j := &Job{}
	k := id(j)
	ch <- j
	k.ID = 1 // HIT(SAFE003)
}

// reflection: a reflect.Value made before the move writes after it.
func reflectSet(ch chan *Job) {
	j := &Job{}
	v := reflect.ValueOf(j).Elem()
	ch <- j
	v.Field(0).SetInt(1) // HIT(SAFE001) HIT(SAFE003)
}

func toAny(j *Job) any { return j }

// any round trip through a callee.
func anyRoundTrip(ch chan *Job) {
	j := &Job{}
	x := toAny(j)
	ch <- j
	x.(*Job).ID = 1 // HIT(SAFE003)
}

type Hook struct{ F func() }

// func value stored in a struct and called after the move.
func funcInStruct(ch chan *Job) {
	j := &Job{}
	h := &Hook{F: func() { j.ID = 1 }}
	ch <- j
	h.F() // HIT(SAFE003)
}

func last(j *Job) *Job {
	if j.Next == nil {
		return j
	}
	return last(j.Next)
}

// recursion: a callee that walks a list and returns a node of it.
func recursion(ch chan *Job) {
	tail := &Job{}
	head := &Job{Next: tail}
	t := last(head)
	ch <- head
	t.ID = 1 // HIT(SAFE003)
}

func evenWalk(j *Job, n int) *Job {
	if n == 0 {
		return j
	}
	return oddWalk(j.Next, n-1)
}

func oddWalk(j *Job, n int) *Job {
	if n == 0 {
		return j
	}
	return evenWalk(j.Next, n-1)
}

// mutual recursion.
func mutualRecursion(ch chan *Job) {
	tail := &Job{}
	head := &Job{Next: tail}
	t := evenWalk(head, 1)
	ch <- head
	t.ID = 1 // HIT(SAFE003)
}

type Holder struct{ J *Job }

func wrap(j *Job) *Holder { return &Holder{J: j} }

// callee stashes its argument in a struct it returns.
func stashReturn(ch chan *Job) {
	j := &Job{}
	h := wrap(j)
	ch <- j
	h.J.ID = 1 // HIT(SAFE003)
}

type Registry struct{ last *Job }

func (r *Registry) Keep(j *Job) { r.last = j }

// callee stashes its argument in its receiver.
func stashReceiver(ch chan *Job) {
	j := &Job{}
	r := &Registry{}
	r.Keep(j)
	ch <- j
	r.last.ID = 1 // HIT(SAFE003)
}

// array value copy carrying a pointer.
func arrayCopy(ch chan *Job) {
	j := &Job{}
	a := [1]*Job{j}
	b := a
	ch <- j
	b[0].ID = 1 // HIT(SAFE003)
}

func copyBox(b Box) Box { return b }

// struct copy carrying a pointer, through a callee returning the copy.
func structCopyCall(ch chan *Job) {
	j := &Job{}
	b := copyBox(Box{P: j})
	ch <- j
	b.P.ID = 1 // HIT(SAFE003)
}

type E struct{ J *Job }

func (e *E) Error() string { return "" }

func launderErr(j *Job) *Job {
	var err error = &E{J: j}
	return err.(*E).J
}

// laundering a parameter out through an error box: anything read out of a shareable box is
// foreign, so the result is not taken as fresh.
func launderOut(ch chan *Job, j *Job) {
	ch <- launderErr(j) // HIT(SAFE002)
}
