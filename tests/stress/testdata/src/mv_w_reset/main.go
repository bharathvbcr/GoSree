// WITNESS: race
//
// time.AfterFunc is a one-shot spawner (passes/ownership/spawn.go), so its closure is taken as a
// goroutine body that owns what it captured (cfg.go closureIsGoroutineBody). But the *Timer it
// returns can Reset, which runs the same closure again on a new goroutine. Nothing orders the two
// runs' writes to j, and the Reset is an ordinary method call on a value outside the moved region.
package main

import (
	"sync"
	"time"
)

type Job struct{ Runs int }

func main() {
	j := &Job{}
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	t := time.AfterFunc(0, func() {
		started <- struct{}{}
		j.Runs++
		wg.Done()
	})
	<-started
	t.Reset(0) // MISS(SAFE003): Reset starts the moved closure a second time; both runs write j
	wg.Wait()
}
