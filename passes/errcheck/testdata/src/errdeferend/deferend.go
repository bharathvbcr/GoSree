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

// Exits that are not a Return block. When the resource is not yet writable at registration these
// produce no finding (the deferred close only resolves at Return blocks).
func exitsProcess(w io.WriteCloser) {
	defer w.Close()
	_, _ = w.Write([]byte("x"))
	os.Exit(0)
}

func panicsAfterDefer(w io.WriteCloser) {
	defer w.Close()
	_, _ = w.Write([]byte("x"))
	panic("boom")
}

// When the resource is already writable at registration (os.Create) the finding does not depend
// on the exit kind, so these report today. The os.Exit one is questionable, since deferred calls
// do not run on os.Exit; it is pinned here as characterized behavior, not endorsed.
func createThenExit(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	os.Exit(0)
}

func createThenPanic(path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close() // want "ERR005"
	panic("boom")
}
