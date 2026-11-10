// Package lk_access attacks what SAFE009 counts as an access to guarded memory. The check sees a
// load (UnOp *) and a write (store, map update, delete/clear/copy/append) whose address leads back to
// a guarded field. Anything else that touches the memory is invisible.
package lk_access

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
)

type S struct {
	mu   sync.Mutex
	n    int64          //safe:guarded_by mu
	m    map[string]int //safe:guarded_by mu
	data []int          //safe:guarded_by mu
	buf  bytes.Buffer   //safe:guarded_by mu
	free int
}

func set(p *int64) { *p = 1 }

// Control: the same write, inline.
func (s *S) inlineWrite() {
	*(&s.n) = 1 // HIT(SAFE009)
}

// The address of a guarded field handed to a helper that writes through it: no load or store
// happens at the call site, and inside set the write is through a parameter.
func (s *S) addrToHelper() {
	set(&s.n) // MISS(SAFE009): set writes s.n with no lock held; taking the address is not an access and the write is through a parameter (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// The same with sync/atomic: an atomic write racing the locked plain accesses of n.
func (s *S) addrToAtomic() {
	atomic.AddInt64(&s.n, 1) // HIT(SAFE009)
}

// A method with a pointer receiver on a guarded struct-valued field: the receiver is &s.buf.
func (s *S) methodOnGuardedField() {
	s.buf.WriteString("x") // MISS(SAFE009): WriteString writes the guarded buffer through its receiver &s.buf with no lock held (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// Control: reading the guarded buffer's bytes by value is a load.
func (s *S) bufCopy() bytes.Buffer {
	return s.buf // HIT(SAFE009)
}

// A guarded map copied under the lock, read after it: Lookup is neither a load nor a write.
func (s *S) mapLookupAfterUnlock() int {
	s.mu.Lock()
	m := s.m
	s.mu.Unlock()
	return m["k"] // MISS(SAFE009): a map lookup on the guarded map after Unlock (ssa.Lookup is not a load) (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// The same with range.
func (s *S) mapRangeAfterUnlock() int {
	s.mu.Lock()
	m := s.m
	s.mu.Unlock()
	t := 0
	for _, v := range m { // MISS(SAFE009): ranging over the guarded map after Unlock (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
		t += v
	}
	return t
}

// Control: a map update after unlock is seen.
func (s *S) mapWriteAfterUnlock() {
	s.mu.Lock()
	m := s.m
	s.mu.Unlock()
	m["k"] = 1 // HIT(SAFE009)
}

// Control: a slice element read after unlock is seen (IndexAddr then load).
func (s *S) sliceReadAfterUnlock() int {
	s.mu.Lock()
	d := s.data
	s.mu.Unlock()
	return d[0] // HIT(SAFE009)
}

// copy reads its second argument; only the first counts as written.
func (s *S) copyFromAfterUnlock(dst []int) {
	s.mu.Lock()
	d := s.data
	s.mu.Unlock()
	copy(dst, d) // MISS(SAFE009): copy reads the guarded backing array after Unlock; only its destination counts (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// A phi breaks the trace from a value back to the guarded field.
func (s *S) phiLaunders(b bool, other []int) {
	s.mu.Lock()
	d := other
	if b {
		d = s.data
	}
	s.mu.Unlock()
	d[0] = 1 // MISS(SAFE009): d may be s.data; slotOf does not follow a phi (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

func id(x []int) []int { return x }

// So does a call that returns its argument.
func (s *S) callLaunders() {
	s.mu.Lock()
	d := id(s.data)
	s.mu.Unlock()
	d[0] = 1 // MISS(SAFE009): d is s.data returned through id; slotOf stops at a call result (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// Whole-object reads: fmt reads every field of *s through reflection.
func (s *S) printAll() {
	fmt.Println(s.free, s) // MISS(SAFE009): fmt reads s.n, s.m, s.data and s.buf through reflection with no lock held (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// A struct copy reads every field, guarded ones included, before any of them is named.
func (s *S) copyGuarded() int64 {
	cp := *s // MISS(SAFE009): the copy loads s.n (and every guarded field) with no lock held (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
	return cp.n
}

// Struct comparison reads every field.
type P struct {
	mu sync.Mutex
	n  int //safe:guarded_by mu
}

func equal(a, b *P) bool {
	return *a == *b // MISS(SAFE009): comparing the structs reads both guarded n fields with no lock held (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}
