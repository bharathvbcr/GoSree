package advdeferend

import (
	"io"
	"os"
)

func fail() error { return nil }

// Infinite loop end: the deferred close never runs; no finding expected when written only later.
func infiniteLoop(w io.WriteCloser) {
	defer w.Close()
	_, _ = w.Write([]byte("x"))
	for {
	}
}

func selectForever(w io.WriteCloser) {
	defer w.Close()
	_, _ = w.Write([]byte("x"))
	select {}
}

// A recover wrapper must not change the verdict of the real exit.
func withRecover(w io.WriteCloser) {
	defer func() { _ = recover() }()
	defer w.Close() // want "ERR005"
	_, _ = w.Write([]byte("x"))
}

// Recover wrapper but the resource is never written: clean.
func withRecoverNoWrite(w io.WriteCloser) {
	defer func() { _ = recover() }()
	defer w.Close()
	println("x")
}

// Two exits, one diagnostic.
func twoExits(w io.WriteCloser, c bool) {
	defer w.Close() // want "ERR005"
	_, _ = w.Write([]byte("x"))
	if c {
		return
	}
	println("end")
}

// Write only on a path that never reaches an exit (panics): clean.
func writeThenPanicOnly(w io.WriteCloser, c bool) {
	defer w.Close()
	if c {
		_, _ = w.Write([]byte("x"))
		panic("boom")
	}
}

func init() {
	f, err := os.Open("x")
	if err != nil {
		return
	}
	defer f.Close()
	println(f.Name())
}

// Generic void function.
func generic[T io.WriteCloser](w T) {
	defer w.Close() // want "ERR005"
	_, _ = w.Write([]byte("x"))
}

type T struct{ w io.WriteCloser }

func (t *T) done() {
	defer t.w.Close()
	_, _ = t.w.Write([]byte("x"))
}

// Method value and expression: synthetic thunks have no source; nothing may appear for them.
func thunks(t *T) {
	f := t.done
	f()
	g := (*T).done
	g(t)
}

// Named result, explicit bare return.
func namedBare(w io.WriteCloser) (err error) {
	defer w.Close() // want "ERR005"
	_, err = w.Write([]byte("x"))
	return
}

// Void function with a handled deferred close: clean.
func handled(w io.WriteCloser) {
	defer func() { _ = w.Close() }()
	_, _ = w.Write([]byte("x"))
}

// A void function that only ever falls off the end with unrelated calls: clean.
func plain() {
	_ = fail()
}

// Close site fallback: close on a var and a different var written.
func differentVar(a, b io.WriteCloser) {
	defer a.Close()
	_, _ = b.Write([]byte("x"))
}
