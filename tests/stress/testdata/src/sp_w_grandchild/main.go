// WITNESS: race
//
// Fork-join recovery restored j after wg.Wait because the callback's region showed no escape or
// transfer (forkjoin.go computeJoins): the callback hands j to a grandchild goroutine inside an
// error value, which was treated as shareable. Reported since 2026-11-11 (boxed values are tracked).
package main

import "sync"

type Job struct{ ID int }

type jobErr struct{ j *Job }

func (e *jobErr) Error() string { return "job" }

func main() {
	var wg sync.WaitGroup
	j := &Job{}
	done := make(chan struct{})
	wg.Go(func() {
		go func(e error) {
			e.(*jobErr).j.ID = 2
			close(done)
		}(&jobErr{j: j})
	})
	wg.Wait()
	j.ID = 1 // HIT(SAFE003)
	<-done
}
