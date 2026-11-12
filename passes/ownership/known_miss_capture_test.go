package ownership_test

import "testing"

// A known miss, pinned so that closing it is a decision and not an accident. Memory loaded from a
// shared container is read-only, and a write through it is reported where the write stands in the
// function that loaded it. A closure that captures the loaded value and writes through it is not
// followed: the write is in the closure's body, where the value is a captured variable and not a
// load. Found on 2026-11-11 while looking at calls of a variable that holds one of several
// closures; not fixed.
func TestWriteOfLoadedMemoryThroughACaptureIsAKnownMiss(t *testing.T) {
	const head = `package p

import "sync/atomic"

type T struct{ n int }

var cfg atomic.Pointer[T]

func run(f func()) { f() }
`
	use := func(body string) []string {
		return advDiagLines(t, head+"\nfunc use(k int) {\n\tv := cfg.Load()\n\t"+body+"\n}\n", "SAFE005")
	}
	if got := use("v.n += k"); len(got) == 0 {
		t.Fatal("a write through the loaded value in the function that loaded it is no longer reported: that one was a hit")
	}
	for _, body := range []string{
		"f := func() { v.n += k }\n\tf()",
		"run(func() { v.n += k })",
	} {
		if got := use(body); len(got) != 0 {
			t.Fatalf("the miss is closed (good): update this test and tests/stress/FINDINGS.md. %q got %v", body, got)
		}
	}
}

// The same gap in the handler write check: a handler's closure that captures the value the
// handler shares with every request, and writes it, is not followed. Pinned with the one above.
func TestHandlerWriteThroughACaptureIsAKnownMiss(t *testing.T) {
	const head = `package p

import "net/http"

type H struct{ n int }

func reg(mux *http.ServeMux, h *H) { mux.HandleFunc("/", h.serve) }
`
	serve := func(body string) []string {
		return advDiagLines(t, head+"\nfunc (h *H) serve(w http.ResponseWriter, r *http.Request) {\n\t"+body+"\n}\n", "SAFE008")
	}
	if got := serve("h.n++"); len(got) == 0 {
		t.Fatal("a write of h in the handler itself is no longer reported: that one was a hit")
	}
	if got := serve("f := func() { h.n++ }\n\tf()"); len(got) != 0 {
		t.Fatalf("the miss is closed (good): update this test and tests/stress/FINDINGS.md. Got %v", got)
	}
}
