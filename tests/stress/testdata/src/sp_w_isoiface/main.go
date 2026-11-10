// WITNESS: race
//
// A method with a //gocap:iso parameter owns its argument and may start a goroutine with it. Called
// statically, the caller's argument moves (SAFE003 on reuse). Called through an interface it
// satisfies, the call is an invoke with no summary (graph.call), the owned parameter is not seen,
// and the caller keeps writing what the method's goroutine writes.
package main

type Job struct{ ID int }

type Submitter interface{ Submit(j *Job) }

type worker struct{ done chan struct{} }

//gocap:iso j
func (w worker) Submit(j *Job) {
	go func() {
		j.ID = 2
		close(w.done)
	}()
}

func main() {
	done := make(chan struct{})
	var s Submitter = worker{done: done}
	j := &Job{}
	s.Submit(j)
	j.ID = 1 // MISS(SAFE003): Submit owns j, but the interface call does not move it
	<-done
}
