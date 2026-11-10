// Package lk_local attacks guarded local variables shared with goroutines. A guarded local's cell,
// and the free variables bound to it, are guarded memory; a pointer to the cell held anywhere else
// is not.
package lk_local

import (
	"sync"
	"sync/atomic"
)

func set(p *int) { *p = 1 }

// Control: the goroutine writes x directly without the lock.
func direct() {
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		x++ // HIT(SAFE009)
	}()
	mu.Lock()
	x++
	mu.Unlock()
	wg.Wait()
}

// The goroutine writes x through a pointer variable it captured: the store's address is a load of
// p's cell, which is not x's cell.
func throughPointer() {
	var mu sync.Mutex
	x := 0  //safe:guarded_by mu
	p := &x // HIT(SAFE009)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		*p = 2
	}()
	mu.Lock()
	x++
	mu.Unlock()
	wg.Wait()
}

// The goroutine hands x's address to a helper: no access at the call, a write in set.
func throughHelper() {
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		set(&x) // MISS(SAFE009): set writes x on this goroutine without mu while main writes it under mu (RULES.md SAFE009: guarded memory may only be accessed while mu is held)
	}()
	mu.Lock()
	x++
	mu.Unlock()
	wg.Wait()
}

// Control: the declaring function after the goroutine starts needs the lock.
func declaringAfterShare() {
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mu.Lock()
		x++
		mu.Unlock()
	}()
	x++ // HIT(SAFE009)
	wg.Wait()
}

// A guarded int64 incremented atomically in one goroutine and under the lock in another.
func atomicMix() {
	var mu sync.Mutex
	var x int64 //safe:guarded_by mu
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		atomic.AddInt64(&x, 1) // HIT(SAFE009)
	}()
	mu.Lock()
	x++
	mu.Unlock()
	wg.Wait()
}
