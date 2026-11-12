// Package advfvbreak3b3c documents BREAKs of commit aabf10f (resourceSlot): a store through a
// captured variable does not clear the shared written flag, so a Close on the freshly assigned,
// never-written resource reports ERR005. The correct behavior is no ERR005 anywhere in this
// package (the same shapes on non-captured locals are silent, since each SSA value has its own
// slot); the test that runs it fails until that holds. Only the ERR003 below is expected.

package advfvbreak3b3c

import "io"

func open() io.WriteCloser { return nil }

type ce struct{}

func (*ce) Error() string               { return "ce" }
func (*ce) Write(p []byte) (int, error) { return 0, nil }
func (*ce) Close() error                { return nil }

func mk() *ce { return nil }

func seq(n int) func(func(int) bool) {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

// P1a: the captured variable is written, then reassigned to a different resource that is never
// written, then closed. The close is on a fresh, never-written resource.
func reassignThenClose(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		w = open()
		defer w.Close() // P1a
	}
}

// P1a2: same, direct close.
func reassignThenCloseDirect(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		w = open()
		w.Close() // P1a2
	}
}

// P4d2: generic, reassigned FreeVar.
func genericReassign[T io.WriteCloser](w T, n T) func() {
	return func() {
		_, _ = w.Write(nil)
		w = n
		w.Close() // P4d2
	}
}

// P5d: write after the loop (documented miss), reassignment in body.
func reassignInBody(w io.WriteCloser, mkw func() io.WriteCloser) {
	for range seq(2) {
		_, _ = w.Write(nil)
		w = mkw()
		defer w.Close() // P5d new resource never written in body
	}
}

// C5 (replacedByCall) moved to errfvswap, where its false positive is expected and documented.

func dualCapturedReassign() error {
	e := mk()
	f := func() error {
		_, _ = e.Write(nil)
		e = mk()
		if e != nil {
			return nil // want "ERR003"
		}
		e.Close()
		return nil
	}
	return f()
}
