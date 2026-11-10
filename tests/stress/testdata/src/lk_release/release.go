// Package lk_release attacks how SAFE009 tracks a lock being released. The analysis must know
// every call that may unlock a held mutex; a release it does not see leaves the mutex "held" in
// its model while the program runs unlocked.
package lk_release

import "sync"

type S struct {
	mu   sync.Mutex
	n    int //safe:guarded_by mu
	cond *sync.Cond
}

// Control: an explicit unlock is seen.
func (s *S) unlockThenWrite() {
	s.mu.Lock()
	s.mu.Unlock()
	s.n = 1 // HIT(SAFE009)
}

// Control: a same-package helper that unlocks its parameter is summarised.
func unlockParam(s *S) { s.mu.Unlock() }

func (s *S) helperUnlocks() {
	s.mu.Lock()
	unlockParam(s)
	s.n = 1 // HIT(SAFE009)
}

// A closure called in place that unlocks a captured mutex: releaseSummaries records releases only
// on parameters, so the free variable's unlock is lost and the lock stays "held".
func (s *S) closureUnlocks() {
	s.mu.Lock()
	func() { s.mu.Unlock() }()
	s.n = 1 // MISS(SAFE009): the in-place closure released s.mu; releaseSummaries only records parameter paths, not free variables (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// Unlock through a method value.
func (s *S) methodValueUnlock() {
	s.mu.Lock()
	unlock := s.mu.Unlock
	unlock()
	s.n = 1 // HIT(SAFE009): s.mu was released through the method value s.mu.Unlock
}

// Unlock through the sync.Cond's Locker, held in a field: the interface receiver is a load of a
// field, which has no stable path, so killFor drops nothing.
func newS() *S {
	s := &S{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *S) condLockerUnlock() {
	s.mu.Lock()
	s.cond.L.Unlock()
	s.n = 1 // MISS(SAFE009): s.cond.L is &s.mu; the Unlock through it is not seen (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// Control: a Locker built from &s.mu in this function is killed.
func (s *S) lockerUnlock() {
	s.mu.Lock()
	var l sync.Locker = &s.mu
	l.Unlock()
	s.n = 1 // HIT(SAFE009)
}

// Control: a deferred Unlock keeps the lock to the end.
func (s *S) deferred() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n = 1
}

// Control: Unlock in a loop body that skips it on continue.
func (s *S) loopContinue(xs []int) {
	for _, x := range xs {
		s.mu.Lock()
		if x == 0 {
			continue
		}
		s.mu.Unlock()
	}
	s.n = 1 // HIT(SAFE009)
}

// A same-package helper that unlocks through a path not rooted at a parameter (a slice element):
// the release is never summarised, so the caller's lock survives the call.
func unlockAll(ss []*S) {
	for _, s := range ss {
		s.mu.Unlock()
	}
}

func (s *S) sliceHelperRelease() {
	s.mu.Lock()
	unlockAll([]*S{s})
	s.n = 1 // MISS(SAFE009): unlockAll released s.mu through the slice element (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// Lock state merges: locked on one branch only.
func (s *S) oneBranch(b bool) {
	if b {
		s.mu.Lock()
	}
	s.n = 1 // HIT(SAFE009)
}

// TryLock with the result ignored.
func (s *S) tryIgnored() {
	s.mu.TryLock()
	s.n = 1 // HIT(SAFE009)
	s.mu.Unlock()
}

// --- package variables ---

var (
	gmu   sync.Mutex
	total int //safe:guarded_by gmu
)

// A same-package helper that unlocks a package-level mutex.
func releaseGlobal() { gmu.Unlock() }

func globalHelperRelease() {
	gmu.Lock()
	releaseGlobal()
	total = 1 // HIT(SAFE009): releaseGlobal unlocked gmu
}

// The same through an interface method of this package.
type releaser interface{ release() }

type gRel struct{}

func (gRel) release() { gmu.Unlock() }

func globalIfaceRelease(r releaser) {
	gmu.Lock()
	r.release()
	total = 2 // MISS(SAFE009): an interface call may unlock a package mutex but killFor only drops guards reachable from its arguments (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// The same through a callback with no arguments.
func globalCallbackRelease(done func()) {
	gmu.Lock()
	done()
	total = 3 // MISS(SAFE009): a dynamic call with no arguments may unlock gmu; nothing is dropped (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
}

// Control: an explicit unlock of the package mutex.
func globalExplicit() {
	gmu.Lock()
	gmu.Unlock()
	total = 4 // HIT(SAFE009)
}

var _ releaser = gRel{}
