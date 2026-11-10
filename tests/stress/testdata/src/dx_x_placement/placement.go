// Package dx_x_placement is the next round after rb_directives: placements that were accepted with no
// effect, or with an effect a reader would not expect. Each is now applied as documented below or
// reported as SAFE007; a reported directive has no effect at all.
package dx_x_placement

import (
	"sync"
	"unsafe"
)

type Job struct{ ID int }

// Duplicates are reported.
//
//gosree:imm
//gosree:imm // HIT(SAFE007)
type Frozen struct{ n int }

var gmu sync.Mutex

//gosree:holds gmu
//gosree:holds gmu // HIT(SAFE007)
func locked() {}

//gosree:iso j
//gosree:iso j // HIT(SAFE007)
func twice(j *Job) { _ = j }

func hatchTwice(p *int) uintptr {
	//gosree:unsafe reviewed
	//gosree:unsafe reviewed again // HIT(SAFE007)
	return uintptr(unsafe.Pointer(p))
}

// A list with one bad name has no effect at all, not even for the good names: q is not moved.
//
//gosree:iso p, nosuch // HIT(SAFE007)
func partial(p, q *Job) { _, _ = p, q }

func partialCaller() {
	p := &Job{}
	partial(p, nil)
	p.ID = 1
}

// A guard on a field with several names guards every name.
type Pair struct {
	mu   sync.Mutex
	x, y int //gosree:guarded_by mu
}

func (p *Pair) bump() {
	p.x++ // HIT(SAFE009)
	p.y++ // HIT(SAFE009)
}

// A borrow directive on a var statement with several names makes every name a borrow.
func multiVar() {
	a := 1
	//gosree:mut
	var b1, b2 = &a, &a // HIT(BORROW002)
	*b1, *b2 = 2, 3
}

// A binding is a mutable or a read-only borrow, not both: the same variable annotated both ways on
// two statements is reported at the second.
func rebind() {
	x, y := 1, 2
	b := &x //gosree:mut
	*b = 3
	b = &y //gosree:rob // HIT(SAFE007)
	_ = *b
}

// iso on a method of a generic type moves the caller's argument.
type Box[T any] struct{ v T }

//gosree:iso j
func (b *Box[T]) Put(j *Job) { _ = j }

func genericCaller(b *Box[int]) {
	j := &Job{}
	b.Put(j)
	j.ID = 1 // HIT(SAFE003)
}
