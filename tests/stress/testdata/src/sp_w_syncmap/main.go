// WITNESS: race
//
// *sync.Map is on the override table (it synchronises itself), but what it holds is not: Store
// publishes j to every goroutine that can reach the map, and the publisher keeps writing j.
package main

import "sync"

type Job struct{ ID int }

func main() {
	var m sync.Map
	j := &Job{}
	m.Store("k", j)
	done := make(chan struct{})
	go func() {
		if v, ok := m.Load("k"); ok {
			v.(*Job).ID = 2
		}
		close(done)
	}()
	j.ID = 1 // HIT(SAFE003): j was published by m.Store and is written by the goroutine that loads it
	<-done
}
