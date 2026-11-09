package callbacks

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

type Job struct{ ID int }

// time.AfterFunc runs its callback on another goroutine: the closure's captures move with it.
func afterFunc() {
	j := &Job{}
	time.AfterFunc(time.Millisecond, func() { j.ID = 1 })
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by time\.AfterFunc`
}

// Two callbacks capture the same variable (TestRaceAfterFunc3 in the race corpus).
func twoAfterFuncs() {
	x := 0
	_ = x
	time.AfterFunc(time.Millisecond, func() { x = 1 })
	time.AfterFunc(time.Millisecond, func() { x = 2 }) // want `SAFE003: variable x is used after it was captured by a goroutine started by time\.AfterFunc`
}

func contextAfterFunc(ctx context.Context) {
	j := &Job{}
	context.AfterFunc(ctx, func() { j.ID = 1 })
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by context\.AfterFunc`
}

// Stopping the timer does not hand j back: Stop returning false means the callback already started.
func afterFuncStopped() {
	j := &Job{}
	t := time.AfterFunc(time.Hour, func() { j.ID = 1 })
	t.Stop()
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by time\.AfterFunc`
}

// A callback hands its memory back through a channel it alone closes, like a go statement.
func afterFuncJoin() {
	done := make(chan struct{})
	j := &Job{}
	time.AfterFunc(time.Millisecond, func() {
		j.ID = 1
		close(done)
	})
	<-done
	j.ID = 2
}

// A one-shot callback owns what it captured, so it may move it on.
func afterFuncOwnsCapture(out chan *Job) {
	j := &Job{}
	time.AfterFunc(time.Millisecond, func() { out <- j })
}

func waitGroupGo() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 1 })
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by \(\*sync\.WaitGroup\)\.Go`
	wg.Wait()
}

// Wait returns only after every function wg.Go started has returned: it hands j back.
func waitGroupJoin() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 1 })
	wg.Wait()
	j.ID = 2
}

// A Wait on a different WaitGroup is not a join.
func waitGroupOtherWait() {
	var wg, other sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 1 })
	other.Wait()
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by \(\*sync\.WaitGroup\)\.Go \(line \d+\) \(after a synchronisation point`
	wg.Wait()
}

// A callback that keeps the memory somewhere else does not hand it back at Wait.
var kept *Job

func waitGroupKeeps() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { kept = j }) // want `SAFE006: package variable kept is written outside init`
	wg.Wait()
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by \(\*sync\.WaitGroup\)\.Go \(line \d+\) \(after a synchronisation point`
}

func errgroupGo() error {
	var g errgroup.Group
	j := &Job{}
	g.Go(func() error { j.ID = 1; return nil })
	j.ID = 2 // want `SAFE003: variable j is used after it was captured by a goroutine started by \(\*golang\.org/x/sync/errgroup\.Group\)\.Go`
	return g.Wait()
}

func errgroupJoin() error {
	var g errgroup.Group
	j := &Job{}
	g.Go(func() error { j.ID = 1; return nil })
	err := g.Wait()
	j.ID = 2
	return err
}

// A handler runs on every request's goroutine, concurrently with the code that registered it.
func handleFunc() {
	hits := map[string]int{}
	http.HandleFunc("/", func(_ http.ResponseWriter, r *http.Request) { hits[r.URL.Path]++ }) // want `SAFE008: captured variable hits is written here by a handler registered with net/http\.HandleFunc`
	hits["/"] = 0                                                                             // want `SAFE003: variable hits is used after it was captured by a handler registered with net/http\.HandleFunc`
}

func muxHandleFunc(mux *http.ServeMux) {
	j := &Job{}
	mux.HandleFunc("/j", func(http.ResponseWriter, *http.Request) { j.ID++ }) // want `SAFE008: captured variable j is written here by a handler registered with \(\*net/http\.ServeMux\)\.HandleFunc`
	j.ID = 0                                                                  // want `SAFE003: variable j is used after it was captured by a handler registered with \(\*net/http\.ServeMux\)\.HandleFunc`
}

type counter struct{ n int }

func (c *counter) ServeHTTP(http.ResponseWriter, *http.Request) { c.n++ } // want ServeHTTP:"ownership summary" `SAFE008: receiver c is written here by a handler registered with net/http\.Handle`

func handle() {
	c := &counter{}
	http.Handle("/c", c)
	c.n = 0 // want `SAFE003: composite literal is used after it was registered as a handler with net/http\.Handle`
}

// Handler invocations run concurrently with each other, so a handler does not own its captures.
func handlerSharesCapture(out chan *Job) {
	j := &Job{}
	http.HandleFunc("/s", func(http.ResponseWriter, *http.Request) {
		out <- j // want `SAFE002: captured variable j is sent on a channel but is not owned here`
	})
}

// A package variable written in a handler is shared with every request goroutine.
var served int

func registerCounter() {
	http.HandleFunc("/n", func(http.ResponseWriter, *http.Request) {
		served++ // want `SAFE006: package variable served is written outside init`
	})
}

// sync.Once.Do runs its function before returning, on the caller's goroutine: not a spawn.
func onceDo(once *sync.Once) {
	j := &Job{}
	once.Do(func() { j.ID = 1 })
	j.ID = 2
}
