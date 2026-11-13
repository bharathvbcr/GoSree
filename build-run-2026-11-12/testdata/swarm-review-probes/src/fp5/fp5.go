// Package fp5: channel hand-off to a receiver goroutine the function can see.
package fp5

type Job struct{ ID int }

// H1: writer goroutine receives j from a local channel; a reader goroutine captured j first.
func H1() {
	j := &Job{}
	ch := make(chan *Job, 1)
	go func() { x := <-ch; x.ID = 5 }()
	go func() { _ = j.ID }()
	ch <- j // race: reader vs writer
}

// H4: H1 plus a done channel the parent receives on after the send.
func H4() {
	j := &Job{}
	ch := make(chan *Job, 1)
	go func() { x := <-ch; x.ID = 5 }()
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	ch <- j // race: reader vs writer
	<-done
}

// H5: H4 without the writer goroutine (no one receives from ch here).
func H5() chan *Job {
	j := &Job{}
	ch := make(chan *Job, 1)
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	ch <- j
	<-done
	return ch
}

// H2: same, but the reader goroutine WRITES and the receiver only reads.
func H2() {
	j := &Job{}
	ch := make(chan *Job, 1)
	go func() { x := <-ch; _ = x.ID }()
	go func() { j.ID = 1 }()
	ch <- j // race
}

// H3: both write.
func H3() {
	j := &Job{}
	ch := make(chan *Job, 1)
	go func() { x := <-ch; x.ID = 2 }()
	go func() { j.ID = 1 }()
	ch <- j // race
}
