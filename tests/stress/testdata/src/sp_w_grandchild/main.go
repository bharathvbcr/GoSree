// WITNESS: race
//
// Fork-join recovery restores j after wg.Wait because the callback's region shows no escape or
// transfer (forkjoin.go computeJoins). The callback has handed j to a grandchild goroutine inside an
// error value, which the checker treats as shareable, so the grandchild still writes j after Wait.
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
