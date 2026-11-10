// Package handlers tests SAFE008: a handler runs on every request's goroutine, concurrently with
// itself, so a write through memory it shares with its other invocations (what it captured, or its
// receiver) is a race between two requests.
package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"store"
	"sync"
	"sync/atomic"
)

type Handler struct {
	n     int
	cfg   *Config
	mu    sync.Mutex
	count atomic.Int64
}

type Config struct{ count int }

// A method value registered as a handler shares its receiver.
func (h *Handler) Handle(http.ResponseWriter, *http.Request) { // want Handle:"ownership summary"
	h.n++ // want `SAFE008: the method value's receiver is written here by a handler registered with \(\*net/http\.ServeMux\)\.HandleFunc`
}

func boundMethod(mux *http.ServeMux) {
	h := &Handler{}
	mux.HandleFunc("/h", h.Handle)
}

// http.HandlerFunc(f) boxed into a Handler is the same closure.
func handlerFunc() {
	n := 0
	http.Handle("/f", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		n++ // want `SAFE008: captured variable n is written here by a handler registered with net/http\.Handle`
	}))
}

// delete, clear and append write into what they are given.
func builtins() {
	m := map[string]int{}
	s := []int{1}
	t := []int{2}
	http.HandleFunc("/b", func(http.ResponseWriter, *http.Request) {
		delete(m, "k")   // want `SAFE008: captured variable m is written here`
		clear(s)         // want `SAFE008: captured variable s is written here`
		t = append(t, 3) // want `SAFE008: captured variable t is written here`
	})
}

// Aliases and pointers loaded from the shared memory lead back to it.
func aliases() {
	h := &Handler{cfg: &Config{}}
	http.HandleFunc("/a", func(http.ResponseWriter, *http.Request) {
		p := h
		p.n++         // want `SAFE008: captured variable h is written here`
		h.cfg.count++ // want `SAFE008: captured variable h is written here`
	})
}

// Writes in functions of this package that the handler calls with the shared memory count too.
func (h *Handler) bump() {
	h.n++ // want `SAFE008: captured variable h is written here by a handler registered with net/http\.HandleFunc`
}

func inc(p *int) {
	*p++ // want `SAFE008: captured variable x is written here by a handler registered with net/http\.HandleFunc`
}

func transitive() {
	h := &Handler{}
	x := 0
	http.HandleFunc("/t", func(http.ResponseWriter, *http.Request) {
		h.bump()
		inc(&x)
	})
}

// A write under a lock is still SAFE008 when nothing says the lock guards the field.
func lockedWrite() {
	h := &Handler{}
	http.HandleFunc("/l", func(http.ResponseWriter, *http.Request) {
		h.mu.Lock()
		h.n++ // want `SAFE008: captured variable h is written here`
		h.mu.Unlock()
	})
}

// With //safe:guarded_by, SAFE009 owns the field: a locked write is accepted, an unlocked one is
// SAFE009, not SAFE008.
type Counter struct {
	mu   sync.Mutex
	hits map[string]int //safe:guarded_by mu
}

func guardedWrite() {
	c := &Counter{hits: map[string]int{}}
	http.HandleFunc("/c", func(_ http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.hits[r.URL.Path]++
		c.mu.Unlock()
	})
	d := &Counter{hits: map[string]int{}}
	http.HandleFunc("/u", func(_ http.ResponseWriter, r *http.Request) {
		d.hits[r.URL.Path]++ // want `SAFE009: field hits is guarded by mu but is written here without holding it`
	})
}

// --- Not findings ---

// Memory made per request belongs to that request.
func perRequest() {
	http.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]int{}
		m[r.URL.Path]++
		var buf bytes.Buffer
		buf.WriteString("ok")
		w.Header()["X-Test"] = []string{"1"}
		_, _ = w.Write(buf.Bytes())
	})
}

// Atomics and locks are calls into sync, not stores.
func synchronised() {
	h := &Handler{}
	http.HandleFunc("/s", func(http.ResponseWriter, *http.Request) {
		h.count.Add(1)
		h.mu.Lock()
		h.mu.Unlock()
	})
}

// Reading shared memory is not a write.
func readOnly() {
	cfg := &Config{count: 3}
	http.HandleFunc("/r", func(w http.ResponseWriter, _ *http.Request) {
		if cfg.count > 0 {
			_, _ = io.WriteString(w, "ok")
		}
	})
}

// A named function has nothing to share.
func serve(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }

func namedFunction() { http.HandleFunc("/n", serve) }

// Calls through an interface are not followed (a stated gap).
func notFollowed() {
	var sink io.Writer = &bytes.Buffer{}
	http.HandleFunc("/x", func(http.ResponseWriter, *http.Request) {
		_, _ = sink.Write([]byte("x"))
	})
}

// --- Calls into other packages: the callee's summary says whether it writes ---

func stdlibWrites() {
	s := []int{3, 1}
	http.HandleFunc("/sort", func(http.ResponseWriter, *http.Request) {
		sort.Ints(s) // want `SAFE008: captured variable s is passed to sort\.Ints, which may write it, by a handler registered with net/http\.HandleFunc`
	})
}

type Service struct {
	st    *store.Store
	c     *store.Counter
	cache map[string]int
	items []int
}

func crossPackage() {
	h := &Service{st: &store.Store{}, c: store.NewCounter(), cache: map[string]int{}}
	http.HandleFunc("/cp", func(_ http.ResponseWriter, r *http.Request) {
		h.st.Put("k", 1)                    // want `SAFE008: captured variable h is passed to \(\*store\.Store\)\.Put, which may write it`
		store.Remember(h.cache, r.URL.Path) // want `SAFE008: captured variable h is passed to store\.Remember, which may write it`
		store.Forget(h.cache, "k")          // want `SAFE008: captured variable h is passed to store\.Forget`
		store.Reset(h.cache)                // want `SAFE008: captured variable h is passed to store\.Reset`
		h.items = store.Grow(h.items, 1)    // want `SAFE008: captured variable h is passed to store\.Grow`
		store.Fill(h.items, 2)              // want `SAFE008: captured variable h is passed to store\.Fill`
		store.Forward(h.st)                 // want `SAFE008: captured variable h is passed to store\.Forward, which may write it`
		h.c.Hit(r.URL.Path)                 // guarded: SAFE009 checks Counter inside store
		_ = h.st.Get("k")                   // reads only
		h.st.Touch()                        // only locks its mutex: sync calls are not writes
		fmt.Println(h.st)                   // a variadic slice holds h.st; formatting does not write it
		store.Show(h.st)                    // the same, one call further away
		_ = h.st.With(2)                    // writes a copy, not h.st
		_ = store.Size(h.items)             // reads only
	})
}

// json.Marshal reaches its argument through reflection and interface calls, which its summary
// counts as writes.
func marshal() {
	cfg := &Config{}
	http.HandleFunc("/j", func(http.ResponseWriter, *http.Request) {
		_, _ = json.Marshal(cfg) // want `SAFE008: captured variable cfg is passed to encoding/json\.Marshal, which may write it, by`
	})
}

// A callee gocap could not analyse may write anything it is given, and the finding says why. A
// shareable argument (a mutex) is exempt even then.
func unknownCallee() {
	h := &Handler{}
	http.HandleFunc("/u", func(http.ResponseWriter, *http.Request) {
		store.Poke(&h.n) // want `SAFE008: captured variable h is passed to store\.Poke, which may write it \(summary unavailable\)`
		store.Hold(&h.mu)
	})
}

type stats struct{ hits int64 }

// sync/atomic functions have no Go body either, but they are synchronised accesses, like the
// methods of the atomic types.
func atomicCounter() {
	s := &stats{}
	http.HandleFunc("/at", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt64(&s.hits, 1)
		_ = atomic.LoadInt64(&s.hits)
	})
}

// Memory each request makes for itself may be written by anything.
func perRequestCallee() {
	http.HandleFunc("/pr", func(_ http.ResponseWriter, r *http.Request) {
		s := &store.Store{}
		s.Put(r.URL.Path, 1)
		xs := []int{3, 1}
		sort.Ints(xs)
	})
}

// A goroutine the handler starts is checked by the transfer rules, not by SAFE008.
func innerGoroutine() {
	h := &Handler{}
	http.HandleFunc("/g", func(http.ResponseWriter, *http.Request) {
		go func() { h.n++ }() // want `SAFE002: the closure is captured by a goroutine but is not owned here`
	})
}
