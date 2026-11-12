// Package sp_launder converts mutable memory to a type on the shared-by-design override table
// (internal/kinds/kinds.go) and hands it to another goroutine. The table names interfaces and func
// types (error, context.Context, context.CancelFunc, net.Listener, testing.TB) by type, so any value
// that converts to one of them is classified shareable, whatever it carries.
package sp_launder

import (
	"context"
	"net"
	"sync"
)

type Job struct{ ID int }

// control: the pointer itself crosses.
func plainPointer() {
	j := &Job{}
	go func(p *Job) { p.ID = 2 }(j)
	j.ID = 1 // HIT(SAFE003)
}

// error is an override ("assumed: error values are not mutated after creation"); a pointer boxed
// as error is no longer tracked.
type jobErr struct{ j *Job }

func (e *jobErr) Error() string { return "job" }

func viaError() {
	j := &Job{}
	var err error = &jobErr{j: j}
	go func(e error) { e.(*jobErr).j.ID = 2 }(err)
	j.ID = 1 // HIT(SAFE003)
}

// error sent on a channel: same laundering through a send.
func errorSend(ch chan error) {
	j := &Job{}
	ch <- &jobErr{j: j}
	j.ID = 1 // HIT(SAFE003)
}

// context.CancelFunc is an override; every func() converts to it, so a closure over a local is
// handed to a goroutine as "shareable".
func viaCancelFunc() {
	n := 0
	f := context.CancelFunc(func() { n++ })
	go run(f)
	n = 5 // MISS(SAFE003): any func() converts to context.CancelFunc, an override, so a closure over n crosses as shareable. Witness sp_w_cancelfunc
}

func run(f context.CancelFunc) { f() }

// control: the same closure as a plain func() argument is caught.
func viaPlainFunc() {
	n := 0
	f := func() { n++ }
	go runPlain(f)
	n = 5 // HIT(SAFE003)
}

func runPlain(f func()) { f() }

// context.Context is an override; context.WithValue carries an arbitrary mutable value inside it.
type key struct{}

func viaWithValue(ctx context.Context) {
	j := &Job{}
	ctx = context.WithValue(ctx, key{}, j)
	go func() { ctx.Value(key{}).(*Job).ID = 2 }()
	j.ID = 1 // HIT(SAFE003): context.WithValue stores j in a Context, an override; the goroutine reads it back and writes it. Witness sp_w_ctxvalue
}

// a user type implementing context.Context with a mutable field is shareable once boxed.
type jobCtx struct {
	context.Context
	j *Job
}

func viaCustomContext(ctx context.Context) {
	j := &Job{}
	var c context.Context = jobCtx{Context: ctx, j: j}
	go func() { c.(jobCtx).j.ID = 2 }()
	j.ID = 1 // MISS(SAFE003): a user type implementing context.Context with a mutable field is shareable once boxed
}

// net.Listener is an override; any type with Accept/Close/Addr is "documented safe".
type fakeListener struct {
	n int
	m map[string]int
}

func (l *fakeListener) Accept() (net.Conn, error) { l.n++; l.m["a"]++; return nil, nil }
func (l *fakeListener) Close() error              { return nil }
func (l *fakeListener) Addr() net.Addr            { return nil }

func viaListener() {
	fl := &fakeListener{m: map[string]int{}}
	var l net.Listener = fl
	go func() { _, _ = l.Accept() }()
	fl.n = 5 // MISS(SAFE003): any type with Accept/Close/Addr boxed as net.Listener is shareable; Accept writes fl.n on the other goroutine
}

// sync.Pool and sync.Map are pointer overrides: what they hold is shared, but storing into them is
// an ordinary call (sync.Pool.Put is seed_moves #14).
func viaSyncMap(m *sync.Map) {
	j := &Job{}
	m.Store("k", j)
	go func() {
		v, _ := m.Load("k")
		v.(*Job).ID = 2 // HIT(SAFE005)
	}()
	j.ID = 1 // HIT(SAFE003): Store into a *sync.Map (pointer override) publishes j to every goroutine holding the map. Witness sp_w_syncmap
}
