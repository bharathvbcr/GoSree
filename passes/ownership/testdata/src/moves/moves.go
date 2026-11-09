package moves

import (
	"sync"
	"testing"
)

type Job struct {
	ID   int
	Data []byte
}

// Captured variable written by a goroutine and by its parent afterwards.
func capturedRace() {
	x := 0
	_ = x
	go func() {
		x = 1
	}()
	x = 2 // want `SAFE003: variable x is used after it was captured by a goroutine`
}

// Two goroutines capture the same mutable variable.
func twoGoroutines() {
	x := 0
	go func() { x++ }()
	go func() { x++ }() // want `SAFE003: variable x is used after it was captured by a goroutine`
}

// The parent never touches x again: the goroutine owns it.
func capturedMoved() {
	x := 0
	_ = x
	go func() {
		x = 1
	}()
}

// Read-only captures of shareable values are not moves.
func readOnlyCaptures(t *testing.T) {
	n := 5
	ch := make(chan int)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch <- n
		t.Log(n)
	}()
	<-ch
	wg.Wait()
	t.Log(n)
}

// A synchronous closure call is not a goroutine boundary.
func syncClosure() int {
	x := 0
	func() { x++ }()
	x++
	return x
}

// Sending a fresh value moves it; using it afterwards is a use after move.
func sendThenUse(ch chan *Job) {
	j := &Job{ID: 1}
	ch <- j
	j.ID = 2 // want `SAFE003: composite literal is used after it was sent on a channel`
}

// Sending a fresh value and never touching it again is fine.
func sendFresh(ch chan *Job) {
	j := &Job{ID: 1, Data: make([]byte, 8)}
	j.Data[0] = 1
	ch <- j
}

// A parameter is not owned by the callee, so it cannot be moved.
func sendParam(ch chan *Job, j *Job) {
	ch <- j // want `SAFE002: parameter j is sent on a channel but is not owned here: the caller still holds it`
}

// An iso parameter is owned, so it can be moved on.
//
//safe:iso j
func forward(ch chan *Job, j *Job) {
	ch <- j
}

// Passing to an iso parameter is itself a move.
func callForward(ch chan *Job) {
	j := &Job{}
	forward(ch, j)
	_ = j.ID // want `SAFE003: composite literal is used after it was passed to owned parameter 1`
}

var registry []*Job // want `SAFE006: package variable registry is written outside init`

// Moving a value that is also stored in a package variable.
func sendAndKeep(ch chan *Job) {
	j := &Job{}
	registry = append(registry, j)
	ch <- j // want `SAFE002: composite literal is sent on a channel but is not owned here: it may alias package variable registry` `SAFE004: composite literal is sent on a channel but also escapes: stored in package variable registry`
}

// A received value is owned by the receiver and can be sent on.
func relay(in <-chan *Job, out chan<- *Job) {
	for j := range in {
		j.ID++
		out <- j
	}
}

// Moving inside a loop a value defined outside the loop moves it twice.
func loopMove(ch chan *Job) {
	j := &Job{}
	for range 3 {
		ch <- j // want `SAFE003: composite literal is used after it was sent on a channel`
	}
}

// Allocating inside the loop gives each iteration its own value.
func loopFresh(ch chan *Job) {
	for i := range 3 {
		j := &Job{ID: i}
		ch <- j
	}
}

func newJob() *Job { return &Job{Data: make([]byte, 4)} }

// A constructor that returns a fresh allocation keeps ownership with the caller.
func sendConstructed(ch chan *Job) {
	j := newJob()
	ch <- j
}

func keep(j *Job) { registry = append(registry, j) }

// A callee that retains its argument breaks ownership.
func sendAfterKeep(ch chan *Job) {
	j := &Job{}
	keep(j)
	ch <- j // want `SAFE004: composite literal is sent on a channel but also escapes: passed to .*keep, which may retain or share it`
}

func inspect(j *Job) int { return j.ID + len(j.Data) }

// A callee that only reads its argument does not break ownership.
func sendAfterInspect(ch chan *Job) {
	j := &Job{}
	_ = inspect(j)
	ch <- j
}

// A pointer captured by a goroutine is moved with the goroutine.
func capturedPointer() {
	j := &Job{}
	go func() {
		j.ID = 1
	}()
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine`
}

// The escape hatch suppresses a finding and needs a reason.
func hatch() {
	x := 0
	_ = x
	go func() { x = 1 }()
	//safe:unsafe the test only checks suppression
	x = 2
}

// A closure deferred before a move runs after it, at return.
func deferredUse(ch chan *Job) {
	j := &Job{}
	defer func() { j.ID = 1 }() // want `SAFE003: this deferred call uses the closure, which runs when the function returns, after it was sent on a channel`
	ch <- j
}

// A deferred call that does not touch the moved value is fine.
func deferredOther(ch chan *Job, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	j := &Job{}
	ch <- j
}
