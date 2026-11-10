// WITNESS: race
//
// A same-package helper unlocks a package-level mutex. Release summaries only record unlocks of
// parameter paths, so the caller still believes gmu is held after the call, and the write to the
// guarded package variable is accepted with no lock.
package main

import "sync"

var (
	gmu   sync.Mutex
	total int //safe:guarded_by gmu
)

func release() { gmu.Unlock() }

func add() {
	gmu.Lock()
	release()
	total++ // HIT(SAFE009): release() unlocked gmu
}

func main() {
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				add()
			}
		}()
	}
	wg.Wait()
}
