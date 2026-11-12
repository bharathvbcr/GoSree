// WITNESS: race
//
// The spawner table is matched against a static callee's FullName (spawn.go spawnOf). A method value
// of a listed spawner calls a bound-method wrapper instead, so the callback it starts is no transfer
// and the parent keeps writing what the callback writes.
package main

import "sync"

type Job struct{ ID int }

func main() {
	var wg sync.WaitGroup
	j := &Job{}
	start := wg.Go
	start(func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
	wg.Wait()
}
