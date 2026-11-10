// Package lk_holds attacks the //safe:holds contract: a helper marked holds mu assumes mu is held
// on entry and accesses guarded memory freely, so every way of running it without mu held is a hole.
// The call-site check looks only at static *ssa.Call instructions.
package lk_holds

import "sync"

// S is imm apart from its guarded field, so goroutines may share it (as in the ownership
// testdata, shareServer): the only thing wrong below is the lock.
//
//safe:imm
type S struct {
	mu sync.Mutex
	n  int //safe:guarded_by mu
}

//safe:holds mu
func (s *S) bumpLocked() { s.n++ }

// Control: a plain call without the lock is reported.
func (s *S) plainCall() {
	s.bumpLocked() // HIT(SAFE009)
}

// Control: a plain call with the lock is accepted.
func (s *S) lockedCall() {
	s.mu.Lock()
	s.bumpLocked()
	s.mu.Unlock()
}

// A go statement runs the helper on a new goroutine, which never holds the caller's lock.
func (s *S) goHelper() {
	s.mu.Lock()
	go s.bumpLocked() // HIT(SAFE009): the new goroutine does not hold s.mu
	s.mu.Unlock()
}

// A deferred helper registered before the deferred Unlock runs after it (LIFO).
func (s *S) deferAfterUnlock() {
	s.mu.Lock()
	defer s.bumpLocked() // HIT(SAFE009): runs after the deferred Unlock (LIFO), without s.mu
	defer s.mu.Unlock()
}

// A deferred helper with no lock anywhere.
func (s *S) deferNoLock() {
	defer s.bumpLocked() // HIT(SAFE009): no lock at all
}

// Control: correct order, the helper runs before the Unlock.
func (s *S) deferBeforeUnlock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.bumpLocked()
}

// Through an interface.
type bumper interface{ bumpLocked() }

func viaInterface(b bumper) {
	b.bumpLocked() // MISS(SAFE009): an interface call reaches the holds helper with nothing held (RULES.md SAFE009: holds functions must be invoked with mu held)
}

// Calling a holds helper on a different object than the one locked.
func wrongObject(a, b *S) {
	a.mu.Lock()
	b.bumpLocked() // HIT(SAFE009)
	a.mu.Unlock()
}

var _ bumper = (*S)(nil)
