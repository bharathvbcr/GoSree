// Package sp_publish shares memory with other goroutines without a spawn or a send: the value is
// stored where another goroutine can reach it, and the publisher keeps writing it.
package sp_publish

import (
	"sync"
	"weak"
)

type Job struct{ ID int }

// A registry behind a mutex. The map is guarded, the jobs in it are not: storing j under the lock
// publishes it, and the write after Unlock races with any reader that updates it under the lock.
var regMu sync.Mutex

var registry = map[string]*Job{} //safe:guarded_by regMu

func register() {
	j := &Job{}
	regMu.Lock()
	registry["a"] = j
	regMu.Unlock()
	j.ID = 1 // MISS(SAFE003): the map is guarded, the job in it is not; after Unlock the publisher writes j while bumpRegistered writes it under the lock
}

func bumpRegistered() {
	regMu.Lock()
	if j := registry["a"]; j != nil {
		j.ID++
	}
	regMu.Unlock()
}

// A hub already running on another goroutine: a guarded field is how it receives work, and the
// pointer stored into it is still written by the publisher.
type Hub struct {
	mu  sync.Mutex
	cur *Job //safe:guarded_by mu
}

func (h *Hub) loop() {
	for {
		h.mu.Lock()
		if h.cur != nil {
			h.cur.ID++
		}
		h.mu.Unlock()
	}
}

func hubPublish(h *Hub) {
	j := &Job{}
	h.mu.Lock()
	h.cur = j
	h.mu.Unlock()
	j.ID = 1 // MISS(SAFE003): j is stored into a guarded field of a Hub whose loop already runs on another goroutine; the write after Unlock races with loop
}

// control: a package-level channel is still a send.
var Work = make(chan *Job, 1)

func workSend() {
	j := &Job{}
	Work <- j
	j.ID = 1 // HIT(SAFE003)
}

// A closure stored in a package variable during init and called from several goroutines: the
// global is only written during initialisation (so SAFE006 is quiet) and the call loads it inside
// each goroutine, so no closure crosses a go statement.
var hook func()

func init() {
	n := 0
	hook = func() { n++ } // MISS(SAFE008): hook is called from two goroutines at once (callHooks) and n++ races with itself; neither SAFE006 (written only in init) nor a transfer (each goroutine loads hook itself) sees it
}

func callHooks() {
	go func() { hook() }()
	go func() { hook() }()
}

// control: starting the loaded closure itself is a transfer of a value that is not owned.
func callHookDirect() {
	go hook() // HIT(SAFE002) HIT(SAFE004)
}

// weak.Make: a weak pointer sent elsewhere still reaches j.
func weakSend(ch chan weak.Pointer[Job]) {
	j := &Job{}
	ch <- weak.Make(j) // HIT(SAFE002)
	j.ID = 1
}
