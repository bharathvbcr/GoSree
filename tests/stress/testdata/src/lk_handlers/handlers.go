// Package lk_handlers attacks SAFE008: an HTTP handler runs once per request, concurrently with
// itself, so a write to memory its invocations share (receiver fields, captured variables) races.
// RULES.md (SAFE008) covers "net/http.Handler or http.HandlerFunc" and CONCURRENCY_MODEL.md 1.4
// handlers registered "via http.HandleFunc or router frameworks".
package lk_handlers

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Counter struct{ n int }

func (c *Counter) ServeHTTP(http.ResponseWriter, *http.Request) {
	c.n++ // HIT(SAFE008)
}

// Control: a handler value registered directly.
func direct(mux *http.ServeMux) {
	mux.Handle("/c", &Counter{})
}

type Wrapped struct{ n int }

func (c *Wrapped) ServeHTTP(http.ResponseWriter, *http.Request) {
	c.n++ // MISS(SAFE008): registered through http.StripPrefix; every request shares c (RULES.md SAFE008 covers any net/http.Handler)
}

// A handler wrapped by a standard decorator before registration: the registered value is a call
// result, which handlerRoots does not resolve.
func stripped(mux *http.ServeMux) {
	mux.Handle("/w/", http.StripPrefix("/w", &Wrapped{}))
}

type Served struct{ n int }

func (c *Served) ServeHTTP(http.ResponseWriter, *http.Request) {
	c.n++ // MISS(SAFE008): http.Server.Handler serves every connection concurrently; the field store is not a listed registration (RULES.md SAFE008 covers any net/http.Handler)
}

// A handler handed to an http.Server: not a listed registration.
func server() *http.Server {
	return &http.Server{Handler: &Served{}, ReadHeaderTimeout: time.Second}
}

// A handler built by a factory: the registered func is a call result.
func makeHandler() http.HandlerFunc {
	hits := 0
	return func(http.ResponseWriter, *http.Request) {
		hits++ // MISS(SAFE008): the handler is a factory result; handlerRoots stops at the call (RULES.md SAFE008 covers any net/http.Handler)
	}
}

func factory(mux *http.ServeMux) {
	mux.HandleFunc("/f", makeHandler())
}

// Control: the same closure registered in place.
func inPlace(mux *http.ServeMux) {
	hits := 0
	mux.HandleFunc("/i", func(http.ResponseWriter, *http.Request) {
		hits++ // HIT(SAFE008)
	})
}

// Handlers kept in a map and registered in a loop.
func routes(mux *http.ServeMux) {
	seen := 0
	table := map[string]http.HandlerFunc{
		"/a": func(http.ResponseWriter, *http.Request) {
			seen++ // MISS(SAFE008): registered from a route table in a loop; the handler value is a map iteration result (RULES.md SAFE008 covers any net/http.Handler)
		},
	}
	for p, h := range table {
		mux.HandleFunc(p, h) // FP(SAFE003): each iteration registers a different handler once; nothing is used after its registration
	}
}

func run(f func()) { f() }

// A write in a closure the handler hands to a helper: sharedRoot does not look into a closure's
// bindings, so the helper's dynamic call is never followed.
func viaHelper(mux *http.ServeMux) {
	total := 0
	mux.HandleFunc("/h", func(http.ResponseWriter, *http.Request) {
		run(func() { total++ }) // MISS(SAFE008): the closure runs in the handler through run; sharedRoot does not look into closure bindings (RULES.md SAFE008 covers any net/http.Handler)
	})
}

// Control: the same closure called in place.
func calledInPlace(mux *http.ServeMux) {
	total := 0
	mux.HandleFunc("/p", func(http.ResponseWriter, *http.Request) {
		func() { total++ }() // HIT(SAFE008)
	})
}

// A write through a func-valued field of the receiver. The dynamic call is not followed; since
// 2026-11-10 the call itself is reported, because the receiver it is handed may be written.
type Hooked struct {
	n    int
	hook func()
}

func (h *Hooked) ServeHTTP(http.ResponseWriter, *http.Request) {
	h.hook() // HIT(SAFE008)
}

func hooked(mux *http.ServeMux) {
	h := &Hooked{}
	h.hook = func() { h.n++ } // runs on every request through h.hook(); reported at the call
	mux.Handle("/k", h)
}

// A user-defined HandlerFunc twin.
type HF func(http.ResponseWriter, *http.Request)

func (f HF) ServeHTTP(w http.ResponseWriter, r *http.Request) { f(w, r) }

func twin(mux *http.ServeMux) {
	n := 0
	mux.Handle("/t", HF(func(http.ResponseWriter, *http.Request) {
		n++ // HIT(SAFE008)
	}))
}

// A lazily initialised receiver field.
type Lazy struct{ cache map[string]int }

func (l *Lazy) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	if l.cache == nil {
		l.cache = map[string]int{} // HIT(SAFE008)
	}
	l.cache[r.URL.Path]++ // HIT(SAFE008)
}

func lazy(mux *http.ServeMux) { mux.Handle("/l", &Lazy{}) }

// --- Not findings ---

type Safe struct {
	mu   sync.Mutex
	n    int //safe:guarded_by mu
	hits atomic.Int64
	m    sync.Map
	ch   chan int
}

func (s *Safe) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	s.hits.Add(1)
	s.m.Store(r.URL.Path, 1)
	select {
	case s.ch <- 1:
	default:
	}
	local := map[string]int{}
	local[r.URL.Path]++
}

func safe(mux *http.ServeMux) { mux.Handle("/s", &Safe{ch: make(chan int, 1)}) }
