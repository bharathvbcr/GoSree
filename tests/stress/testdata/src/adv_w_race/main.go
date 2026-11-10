// WITNESS: race
//
// An unannotated helper starts a callback on a background goroutine. FuncFact.Spawns propagates
// the goroutine boundary to the caller, statically convicting the caller's unsynchronised write,
// while `-race` witnesses the real data race at runtime.
package main

import (
	"sync"
)

func asyncRun(fn func()) {
	go fn()
}

func main() {
	var wg sync.WaitGroup
	wg.Add(1)
	shared := 0
	asyncRun(func() {
		defer wg.Done()
		for range 1000 {
			shared++
		}
	})
	for range 1000 {
		shared++ // HIT(SAFE003)
	}
	wg.Wait()
}
