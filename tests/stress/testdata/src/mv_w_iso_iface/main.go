// WITNESS: race
//
// An //gocap:iso parameter reached through an interface method. The iso callee owns its parameter
// and starts a goroutine on it without complaint; the caller's call is dynamic, so graph.call
// records only an escape (no transfer), and the caller's later write is never checked. The same
// program with s.Take(j) replaced by worker{}.Take(j) is rejected (mv_iso direct).
package main

import "sync"

type Job struct{ ID int }

type Sink interface{ Take(j *Job) }

type worker struct{ wg *sync.WaitGroup }

//gocap:iso j
func (w worker) Take(j *Job) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		j.ID++
	}()
}

func main() {
	var wg sync.WaitGroup
	var s Sink = worker{wg: &wg}
	j := &Job{}
	s.Take(j)
	j.ID = 1 // MISS(SAFE003): j moved into Take's iso parameter, whose goroutine writes it
	wg.Wait()
}
