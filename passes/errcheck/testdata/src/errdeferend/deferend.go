package errdeferend

import (
	"io"
	"os"
)

// The resource is not known to be writable when the close is deferred; it becomes writable
// afterwards. The deferred close resolves on exit, and a void function that falls off the end
// exits through an implicit return with no source position.

func fallOffEnd(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	_, _ = w.Write([]byte("x"))
}

func fallOffEndBranch(w io.WriteCloser, cond bool) {
	defer w.Close() // want "ERR005"
	if cond {
		_, _ = w.Write([]byte("x"))
	}
}

// Explicit return keeps working.
func explicitReturn(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	_, _ = w.Write([]byte("x"))
	return
}

// Closure that falls off the end.
func closureFallOff() func(io.WriteCloser) {
	return func(w io.WriteCloser) {
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
	}
}

// Never written: no finding on the fall-off exit.
func neverWritten(w io.WriteCloser) {
	defer w.Close()
	println("idle")
}

// Read-only open: no finding.
func readOnlyFallOff(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	println(f.Name())
}

// os.Exit never runs deferred calls: no finding (characterized behavior, unchanged).
func exitsProcess(w io.WriteCloser) {
	defer w.Close()
	_, _ = w.Write([]byte("x"))
	os.Exit(0)
}

// A panic exit is not a Return block: no finding (characterized behavior, unchanged).
func panicsAfterDefer(w io.WriteCloser) {
	defer w.Close()
	_, _ = w.Write([]byte("x"))
	panic("boom")
}
