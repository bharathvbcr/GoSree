// WITNESS: race
//
// Logging a struct that has guarded fields: fmt reads every field through reflection, but the
// call site only passes the pointer, which is not guarded memory, so the unlocked read of the
// guarded counter is never checked. Another goroutine writes it under the lock.
package main

import (
	"fmt"
	"io"
	"sync"
)

// Stats is imm apart from its guarded field, so goroutines may share it.
//
//gosree:imm
type Stats struct {
	mu sync.Mutex
	n  int //gosree:guarded_by mu
}

func (s *Stats) inc() {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}

func main() {
	s := &Stats{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			s.inc()
		}
	}()
	for range 200 {
		fmt.Fprintf(io.Discard, "%+v\n", s) // MISS(SAFE009): reads s.n without s.mu (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
	}
	wg.Wait()
}
