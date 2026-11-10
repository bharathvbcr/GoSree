// WITNESS: race
//
// A registry map guarded by a mutex: SAFE009 checks every access to the map, but not to the values
// stored in it. Storing j under the lock publishes it; the publisher's write after Unlock races with
// a reader that updates the job under the lock.
package main

import "sync"

type Job struct{ ID int }

var regMu sync.Mutex

var registry = map[string]*Job{} //gosree:guarded_by regMu

func bump(done chan struct{}) {
	regMu.Lock()
	if j := registry["a"]; j != nil {
		j.ID++
	}
	regMu.Unlock()
	close(done)
}

func main() {
	j := &Job{}
	regMu.Lock()
	registry["a"] = j
	regMu.Unlock()
	done := make(chan struct{})
	go bump(done)
	j.ID = 1 // MISS(SAFE003): j is reachable from the registry, and bump writes it under a lock this write does not hold
	<-done
}
