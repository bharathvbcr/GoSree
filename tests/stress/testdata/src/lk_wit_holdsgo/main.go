// WITNESS: race
//
// A //gosree:holds helper started with a go statement runs on a goroutine that does not hold the
// caller's lock. The call-site check of //gosree:holds looks only at *ssa.Call, so the go statement
// is accepted, and the helper body is checked as if mu were held.
package main

import (
	"sync"
	"time"
)

// S is imm apart from its guarded field, so goroutines may share it.
//
//gosree:imm
type S struct {
	mu sync.Mutex
	n  int //gosree:guarded_by mu
}

//gosree:holds mu
func (s *S) bumpLocked() { s.n++ }

func main() {
	s := &S{}
	s.mu.Lock()
	go s.bumpLocked() // HIT(SAFE009): the new goroutine does not hold s.mu
	s.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	// Nothing orders the helper's write before this one: the helper never touched s.mu.
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}
