// Package rb_generics feeds the analyzers Go 1.18-1.27 generic constructs: generic functions and
// types, generic methods (Go 1.27), generic type aliases (Go 1.24), type sets, instantiation with
// pointer, func and chan types, recursive generics and generic closures.
package rb_generics

type Job struct {
	ID   int
	Next *Job
}

// List is a generic slice type.
type List[E any] []E

// Apply is a generic method (Go 1.27).
func (l List[E]) Apply[F any](f func(E) F) List[F] {
	r := make(List[F], len(l))
	for i, x := range l {
		r[i] = f(x)
	}
	return r
}

// Box holds one value.
type Box[T any] struct{ V T }

// SendTo is a generic method that sends its receiver's value.
func (b *Box[T]) SendTo[C ~chan T](ch C) { ch <- b.V } // HIT(SAFE002)

// Alias is a generic type alias (Go 1.24).
type Alias[T any] = []T

// PtrAlias is a generic alias of a pointer.
type PtrAlias[T any] = *T

// Number is a type set.
type Number interface {
	~int | ~int64 | ~float64
}

func sum[T Number](xs ...T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

// Tree is a recursive generic type.
type Tree[T any] struct {
	Left, Right *Tree[T]
	Val         T
}

func (t *Tree[T]) Walk(f func(T)) {
	if t == nil {
		return
	}
	t.Left.Walk(f)
	f(t.Val)
	t.Right.Walk(f)
}

// Ptr constrains a type parameter to pointers to T.
type Ptr[T any] interface{ *T }

func newOf[T any, P Ptr[T]]() P { return P(new(T)) }

func sendAny[T any](ch chan T, v T) { ch <- v } // HIT(SAFE002)

// sendViaGenericMethod: a generic method sends the pointer the box holds.
func sendViaGenericMethod(ch chan *Job) {
	j := &Job{}
	b := &Box[*Job]{V: j}
	b.SendTo(ch)
	j.ID = 1
}

// baselineGenericPtr: a plain send of a value built by a generic constructor.
func baselineGenericPtr(ch chan *Job) {
	j := newOf[Job]()
	ch <- j
	j.ID = 1 // HIT(SAFE003)
}

// aliasSend: a value of a generic alias type is sent, then written.
func aliasSend(ch chan Alias[int]) {
	a := Alias[int]{1, 2}
	ch <- a
	a[0] = 9 // HIT(SAFE003)
}

// ptrAliasSend: a generic pointer alias.
func ptrAliasSend(ch chan PtrAlias[Job]) {
	var p PtrAlias[Job] = &Job{}
	ch <- p
	p.ID = 2 // HIT(SAFE003)
}

// chanInst: instantiation with a chan type, sent through a generic helper.
func chanInst(ch chan chan *Job) {
	inner := make(chan *Job, 1)
	sendAny(ch, inner)
	_ = len(inner)
}

// funcInst: instantiation with a func type.
func funcInst(ch chan func()) {
	j := &Job{}
	f := func() { j.ID++ }
	ch <- f
	j.ID = 1 // HIT(SAFE003)
}

// genericClosure: a generic function returns a closure over its argument.
func capture[T any](v T) func() T { return func() T { return v } }

func genericClosure(ch chan func() *Job) {
	j := &Job{}
	ch <- capture(j)
	j.ID = 1 // HIT(SAFE003)
}

// applyThenSend: a generic method result is sent.
func applyThenSend(ch chan List[*Job]) {
	l := List[int]{1, 2}
	out := l.Apply(func(i int) *Job { return &Job{ID: i} })
	ch <- out     // HIT(SAFE002)
	out[0].ID = 1 // HIT(SAFE003)
}

// treeWalk: a recursive generic method, then a move of the tree.
func treeWalk(ch chan *Tree[int]) {
	t := &Tree[int]{Val: 1, Left: &Tree[int]{Val: 0}}
	t.Walk(func(int) {})
	ch <- t   // FP(SAFE004): the summary of the generic origin is used for Tree[int], where T is a type parameter and so not shareable; the non-generic twin ITree is clean
	t.Val = 3 // HIT(SAFE003)
}

// ITree is the non-generic twin of Tree.
type ITree struct {
	Left, Right *ITree
	Val         int
}

func (t *ITree) Walk(f func(int)) {
	if t == nil {
		return
	}
	t.Left.Walk(f)
	f(t.Val)
	t.Right.Walk(f)
}

func itreeWalk(ch chan *ITree) {
	t := &ITree{Val: 1, Left: &ITree{Val: 0}}
	t.Walk(func(int) {})
	ch <- t
	t.Val = 3 // HIT(SAFE003)
}

func useSum() int { return sum(1, 2, 3) + int(sum[float64](1.5)) }

// Pair with a Go 1.27 promoted-field composite-literal key.
type Inner struct{ P *Job }

type Outer struct {
	Inner
	Q int
}

func promotedKey(ch chan Outer) {
	j := &Job{}
	o := Outer{P: j, Q: 1}
	ch <- o
	j.ID = 1 // HIT(SAFE003)
}
