// WITNESS: race
//
// A closure called in place unlocks the captured mutex. Release summaries record unlocks of
// parameters only, so after the closure the analysis still believes s.mu is held and the write
// to the guarded field is accepted. Two goroutines then write s.n with no lock.
package main

import "sync"

// S is imm apart from its guarded field, so goroutines may share it.
//
//gosree:imm
type S struct {
	mu sync.Mutex
	n  int //gosree:guarded_by mu
}

func (s *S) bump() {
	s.mu.Lock()
	func() { s.mu.Unlock() }()
	s.n++ // HIT(SAFE009): s.mu was released by the in-place closure
}

func main() {
	s := &S{}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				s.bump()
			}
		}()
	}
	wg.Wait()
}
