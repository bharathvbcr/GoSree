// WITNESS: race
//
// Wrong instance through a variable's address: p is assigned once in this function, so every load
// of p counts as the same object, but redirect receives &p and points it at another S. The lock is
// on a.mu, the write is to b.n, and another goroutine writes b.n under b.mu.
package main

import "sync"

// S is imm apart from its guarded field, so goroutines may share it.
//
//safe:imm
type S struct {
	mu sync.Mutex
	n  int //safe:guarded_by mu
}

func redirect(pp **S, to *S) { *pp = to }

func bump(a, b *S) {
	p := a
	p.mu.Lock()
	redirect(&p, b)
	p.n++ // MISS(SAFE009): p is b; b.mu is not held
	a.mu.Unlock()
}

func main() {
	a, b := &S{}, &S{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 500 {
			b.mu.Lock()
			b.n++
			b.mu.Unlock()
		}
	}()
	for range 500 {
		bump(a, b)
	}
	wg.Wait()
}
