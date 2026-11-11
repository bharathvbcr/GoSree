// Package mv_shareable probes values the checker treats as shareable by assumption
// (internal/kinds/kinds.go overrides: error, context.Context, context.CancelFunc, net.Listener).
// A shareable value gets no alias class (graph.val returns nil), so a send or capture of one is a
// transfer of nothing, and boxing a pointer into one (graph.go MakeInterface/ChangeType: join with
// a nil class is a no-op) records no escape. The override table promises the *type* is safe to
// share; it is applied to every value of the interface or func type, including ones that box
// mutable user memory.
package mv_shareable

import (
	"context"
	"net"
	"time"
)

type Job struct{ ID int }

// error override: a mutable error struct sent as an error (WITNESS mv_w_error).
type JobErr struct {
	Attempts int
	J        *Job
}

func (e *JobErr) Error() string { return "job" }

func errorSend(ch chan error) {
	e := &JobErr{}
	ch <- e
	e.Attempts++ // MISS(SAFE003): e was sent on ch boxed as an error; the receiver can assert it back and write it
}

// control: the same pointer sent with its own type.
func errorSendTyped(ch chan *JobErr) {
	e := &JobErr{}
	ch <- e
	e.Attempts++ // HIT(SAFE003)
}

// SAFE004: j is moved on ch after it already escaped into an error sent on errs.
func escapeViaError(ch chan *Job, errs chan error) {
	j := &Job{}
	errs <- &JobErr{J: j}
	ch <- j // MISS(SAFE004): j is also reachable from the error sent on errs; two goroutines now hold it
}

// control: the same escape through a typed channel is reported.
func escapeViaTyped(ch chan *Job, errs chan *JobErr) {
	j := &Job{}
	errs <- &JobErr{J: j}
	ch <- j // HIT(SAFE003)
}

// context.Context override: a pointer carried by context.WithValue (WITNESS mv_w_ctxvalue).
type key struct{}

func ctxValue() {
	j := &Job{}
	ctx := context.WithValue(context.Background(), key{}, j)
	go func() { ctx.Value(key{}).(*Job).ID++ }()
	j.ID = 1 // HIT(SAFE003): the goroutine reaches j through ctx and writes it
}

// control: the same pointer captured directly.
func ctxControl() {
	j := &Job{}
	go func() { j.ID++ }()
	j.ID = 1 // HIT(SAFE003)
}

// a user context type that carries a mutable map, sent as a context.Context.
type mapCtx struct {
	context.Context
	m map[string]int
}

func ctxCustom(ch chan context.Context) {
	c := mapCtx{Context: context.Background(), m: map[string]int{}}
	ch <- c
	c.m["a"] = 1 // MISS(SAFE003): c.m is shared with the receiver of ch through the context value
}

// net.Listener override: a user listener with mutable state.
type fakeLn struct{ accepted int }

func (l *fakeLn) Accept() (net.Conn, error) { l.accepted++; return nil, nil }
func (l *fakeLn) Close() error              { return nil }
func (l *fakeLn) Addr() net.Addr            { return nil }

func listener(ch chan net.Listener) {
	l := &fakeLn{}
	ch <- l
	l.accepted++ // MISS(SAFE003): the receiver's Accept writes l.accepted too
}

// context.CancelFunc override: any func() converted to CancelFunc is shareable, closure and all
// (WITNESS mv_w_cancelfunc).
func cancelFunc(ch chan context.CancelFunc) {
	j := &Job{}
	ch <- func() { j.ID++ }
	j.ID = 1 // MISS(SAFE003): the closure sent on ch writes j
}

// control: the same closure sent as a plain func().
func plainFunc(ch chan func()) {
	j := &Job{}
	ch <- func() { j.ID++ }
	j.ID = 1 // HIT(SAFE003)
}

// control: time.Time is a value; moving it shares nothing.
func timeValue(ch chan time.Time) {
	t := time.Now()
	ch <- t
	_ = t.Unix()
}

// control: a struct of an int and a channel, by value, then wait on the reply.
type Req struct {
	N     int
	Reply chan int
}

func reqReply(ch chan Req) int {
	r := Req{N: 1, Reply: make(chan int)}
	ch <- r
	return <-r.Reply
}

// control: a buffered channel sent as a value, then a pointer sent into it: the second send is the
// move and is checked.
func bufferedChan(ch chan chan *Job) {
	j := &Job{}
	inner := make(chan *Job, 1)
	ch <- inner
	inner <- j
	j.ID = 1 // HIT(SAFE003)
}
