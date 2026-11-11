// Package er_w_rangecapture stress-tests ERR005 for a deferred Close inside a range-over-func
// loop body. A defer in the body runs when the enclosing function returns, not when the body
// returns.
package er_w_rangecapture

import (
	"io"
	"iter"
)

func seq(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

// Closure capturing a resource, defer Close, then Write.
func capturedClosure(w io.WriteCloser) func() {
	return func() {
		defer w.Close() // HIT(ERR005)
		_, _ = w.Write(nil)
	}
}

// Written inside the body, closed by a deferred call in the body.
func writtenInBody(w io.WriteCloser) {
	for range seq(2) {
		defer w.Close() // HIT(ERR005)
		_, _ = w.Write(nil)
	}
}

// The write is after the loop but runs before the deferred close.
func writtenAfterLoop(w io.WriteCloser) {
	for range seq(2) {
		defer w.Close() // MISS(ERR005): the body's deferred close is resolved at the yield function's return, which does not see the write after the loop
	}
	_, _ = w.Write(nil)
}

func open() io.WriteCloser { return nil }

// The captured variable is replaced by another closure between the write and the close.
func replacedByCall(w io.WriteCloser) func() {
	swap := func() { w = open() }
	return func() {
		_, _ = w.Write(nil)
		swap()
		w.Close() // FP(ERR005): the store is in another closure and calls that run it cannot be identified soundly
	}
}
