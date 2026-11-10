// WITNESS: race
//
// sync.Pool.New runs on whichever goroutine's Get finds the pool empty. A New that writes a captured
// local is a callback that races with itself; the pool is shared by override, and New is a struct
// field, not a call in the spawner table.
package main

import "sync"

func main() {
	n := 0
	p := &sync.Pool{New: func() any {
		n++ // MISS(SAFE008): New runs concurrently on both goroutines below
		return new(int)
	}}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range 100 {
				_ = p.Get()
			}
		})
	}
	wg.Wait()
}
