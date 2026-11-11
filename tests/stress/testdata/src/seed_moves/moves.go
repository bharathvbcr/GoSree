// Package seed_moves is the worked example for the stress suite: one mechanism per function, each
// line marked with what the checker does today.
package seed_moves

import (
	"runtime"
	"sync"
	"sync/atomic"
)

type Job struct {
	ID   int
	Tags []string
	Next *Job
}

func sink(j *Job) { j.ID++ }

// 1. baseline: use after send.
func baseline(ch chan *Job) {
	j := &Job{}
	ch <- j
	j.ID = 1 // HIT(SAFE003)
}

// 2. alias made before the send, used after.
func aliasBefore(ch chan *Job) {
	j := &Job{}
	k := j
	ch <- j
	k.ID = 1 // HIT(SAFE003)
}

// 3. interior pointer kept, whole moved.
func interior(ch chan *Job) {
	j := &Job{}
	p := &j.ID
	ch <- j
	*p = 1 // HIT(SAFE003)
}

// 4. slice backing array shared after the struct is sent.
func sliceShare(ch chan *Job) {
	tags := []string{"a", "b"}
	j := &Job{Tags: tags}
	ch <- j
	tags[0] = "x" // HIT(SAFE003)
}

// 5. sub-slice sent, parent written.
func subSlice(ch chan []int) {
	buf := make([]int, 8)
	ch <- buf[:4]
	buf[0] = 1 // HIT(SAFE003)
}

// 6. struct value copy carrying a pointer.
type Box struct{ P *Job }

func boxCopy(ch chan Box) {
	j := &Job{}
	b := Box{P: j}
	ch <- b
	j.ID = 1 // HIT(SAFE003)
}

// 7. interface smuggling.
func viaInterface(ch chan any) {
	j := &Job{}
	var x any = j
	ch <- x
	j.ID = 1 // HIT(SAFE003)
}

// 8. closure capture after go.
func closureCapture() {
	j := &Job{}
	go func() { j.ID = 2 }()
	j.ID = 1 // HIT(SAFE003)
}

// 9. method value spawn.
func (j *Job) bump() { j.ID++ }

func methodValue() {
	j := &Job{}
	f := j.bump
	go f()
	j.ID = 1 // HIT(SAFE003)
}

// 10. linked structure: send head, keep tail pointer.
func linked(ch chan *Job) {
	tail := &Job{}
	head := &Job{Next: tail}
	ch <- head
	tail.ID = 1 // HIT(SAFE003)
}

// 11. map shared.
func mapShare(ch chan map[string]int) {
	m := map[string]int{}
	ch <- m
	m["a"] = 1 // HIT(SAFE003)
}

// 12. goroutine via runtime finalizer / cleanup callback.
func cleanup() {
	j := &Job{}
	k := &Job{}
	runtime.SetFinalizer(k, func(*Job) { j.ID = 2 })
	j.ID = 1 // MISS(SAFE003): the finalizer runs on the finalizer goroutine and writes j, which this goroutine also writes
}

// 13. atomic.Pointer publication.
var published atomic.Pointer[Job]

func publish() {
	j := &Job{}
	published.Store(j)
	j.ID = 1 // HIT(SAFE003): j was published through atomic.Pointer.Store; any goroutine may load and use it
}

// 14. sync.Pool put then use.
var pool = sync.Pool{New: func() any { return &Job{} }}

func poolUse() {
	j := pool.Get().(*Job)
	pool.Put(j)
	j.ID = 1 // HIT(SAFE003): j was returned to the pool; another goroutine may Get it
}

// 15. loop: send in one iteration, use in the next.
func loopCarry(ch chan *Job) {
	j := &Job{}
	for i := 0; i < 2; i++ {
		j.ID = i // HIT(SAFE003)
		ch <- j
	}
}

// 16. send through select.
func selectSend(ch chan *Job, done chan struct{}) {
	j := &Job{}
	select {
	case ch <- j:
	case <-done:
	}
	j.ID = 1 // HIT(SAFE003)
}

// 17. deferred use after move.
func deferredUse(ch chan *Job) {
	j := &Job{}
	defer sink(j) // HIT(SAFE003)
	ch <- j
}

// 18. array of pointers.
func arrayElem(ch chan [2]*Job) {
	j := &Job{}
	arr := [2]*Job{j, nil}
	ch <- arr
	j.ID = 1 // HIT(SAFE003)
}

// 19. channel of channels.
func chanOfChan(outer chan chan *Job) {
	inner := make(chan *Job, 1)
	j := &Job{}
	inner <- j
	outer <- inner
	j.ID = 1 // HIT(SAFE003)
}

// 20. generic send helper.
func send[T any](ch chan T, v T) { ch <- v } // HIT(SAFE002)

func genericSend(ch chan *Job) {
	j := &Job{}
	send(ch, j)
	j.ID = 1
}
