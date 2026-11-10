// WITNESS: race
//
// Fork-join through WaitGroup.Go trusts any Wait on the same receiver (forkjoin.go computeJoins,
// sp.receiver), without checking the counter. A stray Done in the parent brings the counter to zero,
// Wait returns at once, and the parent writes j while the callback still writes it. (The callback's
// own Done later drives the counter negative; the race is reported before that.)
package main

import (
	"sync"
	"time"
)

type Job struct{ ID int }

func main() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() {
		time.Sleep(10 * time.Millisecond)
		j.ID = 2
	})
	wg.Done()
	wg.Wait()
	j.ID = 1 // MISS(SAFE003): Wait returned because of the stray Done, not because the callback finished
	time.Sleep(200 * time.Millisecond)
}
