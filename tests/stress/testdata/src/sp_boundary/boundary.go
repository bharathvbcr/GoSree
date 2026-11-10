// Package sp_boundary maps which standard-library callbacks gocap treats as a goroutine boundary.
// Every function captures a local in a callback and keeps writing it in the parent. The spawner
// table (passes/ownership/spawn.go) names time.AfterFunc, context.AfterFunc, WaitGroup.Go, errgroup
// and http.Handle(Func); anything else is an ordinary call.
package sp_boundary

import (
	"bytes"
	"context"
	"expvar"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

type Job struct{ ID int }

// control: a listed spawner.
func afterFunc() {
	j := &Job{}
	time.AfterFunc(time.Millisecond, func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
}

// control: a listed spawner with context.
func ctxAfterFunc(ctx context.Context) {
	j := &Job{}
	context.AfterFunc(ctx, func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
}

// runtime.AddCleanup runs cleanup on a runtime goroutine (mcleanup.go: "cleanup ... is run on a
// separate goroutine"), like SetFinalizer (seed_moves #12).
func addCleanup() {
	j := &Job{}
	k := new(int)
	runtime.AddCleanup(k, func(int) { j.ID = 2 }, 0)
	j.ID = 1 // MISS(SAFE003): runtime.AddCleanup runs the cleanup on the cleanup goroutine (runtime/mcleanup.go); not in the spawner table. Witness sp_w_cleanup
}

// time.NewTimer + a goroutine that waits on C: an ordinary go statement, so it is caught.
func timerGoroutine() {
	j := &Job{}
	t := time.NewTimer(time.Millisecond)
	go func() { // FP(SAFE002): time.NewTimer returns a new timer nothing else holds; reading t.C in the goroutine is the documented use
		<-t.C
		j.ID = 2
	}()
	j.ID = 1 // HIT(SAFE003)
}

// sync.OnceFunc returns a function that may be called from several goroutines; the wrapped body
// runs on whichever goroutine calls first.
func onceFunc() {
	j := &Job{}
	f := sync.OnceFunc(func() { j.ID = 2 })
	go f()
	j.ID = 1 // HIT(SAFE003)
}

// sync.OnceValue: the same, with a result.
func onceValue() {
	j := &Job{}
	f := sync.OnceValue(func() int { j.ID = 2; return j.ID })
	go func() { _ = f() }()
	j.ID = 1 // HIT(SAFE003)
}

// iter.Pull runs the sequence on a coroutine, but control alternates strictly (coroswitch is a
// synchronisation), so the parent's write between next() calls does not race. Must stay clean.
func iterPull() {
	j := &Job{}
	seq := func(yield func(int) bool) {
		j.ID++
		yield(j.ID)
	}
	next, stop := iter.Pull(iter.Seq[int](seq))
	defer stop()
	j.ID = 1
	_, _ = next()
	j.ID = 3
}

// http.Server.ConnState is called on the connection's goroutine, for every connection, concurrently.
func connState() *http.Server {
	n := 0
	srv := &http.Server{ConnState: func(_ net.Conn, _ http.ConnState) { n++ }} // MISS(SAFE008): ConnState is called on every connection goroutine (net/http/server.go conn.setState), so n++ races with itself
	n = 5
	return srv
}

// httptrace hooks run on the transport's goroutines (GotConn may run on a dial goroutine).
func traceHooks(ctx context.Context) context.Context {
	n := 0
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { n++ }} // MISS(SAFE008): hooks "may be called concurrently from different goroutines" (net/http/httptrace/trace.go); a trace shared by concurrent requests races on n
	n = 2
	return httptrace.WithClientTrace(ctx, trace)
}

// sync.Pool.New is called by Get on whichever goroutine finds the pool empty.
func poolNew() *sync.Pool {
	n := 0
	p := &sync.Pool{New: func() any { n++; return new(int) }} // MISS(SAFE008): Pool.New runs on every goroutine whose Get finds the pool empty, so n++ races with itself. Witness sp_w_poolnew
	n = 5
	return p
}

// expvar.Func is called on the HTTP handler's goroutine for every /debug/vars request.
func expvarFunc() {
	n := 0
	expvar.Publish("sp_boundary_n", expvar.Func(func() any { n++; return n }))
	n = 5 // MISS(SAFE003): Publish makes the Func reachable from the /debug/vars handler goroutines at once
}

// testing: subtests that call t.Parallel run at the same time as each other (testing.go: Run
// "blocks until f returns or calls t.Parallel").
func subtestParallel(t *testing.T) {
	n := 0
	for _, name := range []string{"a", "b"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			n++ // MISS(SAFE008): the two parallel subtests run concurrently and both write n
		})
	}
}

// testing: b.RunParallel runs body on GOMAXPROCS goroutines at once.
func runParallel(b *testing.B) {
	n := 0
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n++ // MISS(SAFE008): b.RunParallel runs body on GOMAXPROCS goroutines at once. Witness sp_w_runparallel
		}
	})
}

// slog: a Handler's Handle is called by every goroutine that logs through the Logger.
type countHandler struct {
	slog.Handler
	n int
}

func (h *countHandler) Handle(ctx context.Context, r slog.Record) error {
	h.n++ // MISS(SAFE008): slog.Logger calls Handler.Handle from every goroutine that logs; the receiver field is written without a lock
	return h.Handler.Handle(ctx, r)
}

func slogHandler(base slog.Handler) *slog.Logger {
	return slog.New(&countHandler{Handler: base})
}

// os/exec: a Stdout that is not an *os.File is filled by a goroutine Start creates (os/exec docs:
// "a goroutine reads from the process ... and delivers that data to the corresponding Writer").
func execStdout() error {
	var buf bytes.Buffer
	cmd := exec.Command("true")
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return err
	}
	buf.WriteString("x") // MISS(SAFE003): Start handed buf to a copying goroutine that writes it until Wait
	return cmd.Wait()
}

// FP checks: synchronous callbacks must stay clean.
func sortSlice(xs []int) {
	n := 0
	sort.Slice(xs, func(i, j int) bool { n++; return xs[i] < xs[j] })
	n = 5
	_ = n
}

func slicesSortFunc(xs []int) {
	n := 0
	slices.SortFunc(xs, func(a, b int) int { n++; return a - b })
	n = 5
	_ = n
}

func syncMapRange(m *sync.Map) {
	n := 0
	m.Range(func(k, v any) bool { n++; return true })
	n = 5
	_ = n
}

func onceDo(o *sync.Once) {
	j := &Job{}
	o.Do(func() { j.ID = 2 })
	j.ID = 1
}

func rangeFunc() {
	j := &Job{}
	for x := range slices.Values([]int{1, 2}) {
		j.ID += x
	}
	j.ID = 1
}
