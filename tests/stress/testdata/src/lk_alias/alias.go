// Package lk_alias attacks "the same mutex": a guard path is a root value plus field and load
// steps, and a load counts as a step when the variable it reads is stored once. Locking one object
// and touching another with the same path must be reported.
package lk_alias

import "sync"

type S struct {
	mu sync.Mutex
	n  int //safe:guarded_by mu
}

//safe:holds mu
func (s *S) bumpLocked() { s.n++ }

// Control: two objects of one type.
func twoObjects(a, b *S) {
	a.mu.Lock()
	b.n = 1 // HIT(SAFE009)
	a.mu.Unlock()
}

// Control: lock item i, touch item j.
func loopIndex(xs []*S, i, j int) {
	xs[i].mu.Lock()
	xs[j].n = 1 // HIT(SAFE009)
	xs[i].mu.Unlock()
}

// Control: reassigning the variable between lock and use.
func reassign(a, b *S) {
	s := a
	s.mu.Lock()
	s = b
	s.n = 1 // HIT(SAFE009)
	s.mu.Unlock()
}

// Control: a swap through a closure that writes the variable.
func swapClosure(a, b *S) {
	p := a
	p.mu.Lock()
	func() { p = b }()
	p.n = 1 // HIT(SAFE009)
	a.mu.Unlock()
}

func redirect(pp **S, to *S) { *pp = to }

// The variable p is stored once in this function, so stableLoad treats every load of p as the same
// object; but its address goes to redirect, which points p at another S. The lock is on a, the
// write is on b.
func redirectThroughCall(a, b *S) {
	p := a
	p.mu.Lock()
	redirect(&p, b)
	p.n = 1 // MISS(SAFE009): p is b here; stableLoad ignores the call that received &p and wrote it (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
	a.mu.Unlock()
}

// The same with a holds helper.
func redirectHolds(a, b *S) {
	p := a
	p.mu.Lock()
	redirect(&p, b)
	p.bumpLocked() // MISS(SAFE009): b.mu is not held; a.mu is (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
	a.mu.Unlock()
}

// The same with the variable's address kept in a pointer and written in place.
func redirectInPlace(a, b *S) {
	p := a
	p.mu.Lock()
	pp := &p
	*pp = b
	p.n = 1 // HIT(SAFE009)
	a.mu.Unlock()
}
