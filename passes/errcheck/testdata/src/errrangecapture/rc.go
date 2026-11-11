package errrangecapture

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

// The body defers Close on a variable of the enclosing function that is written inside the body.
func writtenInBody(w io.WriteCloser) {
	for range seq(2) {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

// MISS: a deferred call in a range-over-func body runs when the enclosing function returns (Go
// spec, "Defer statements": deferred functions run immediately before the surrounding function
// returns; confirmed with go1.27.3, where "after loop" prints before the body's deferred call), so
// the write below happens before the close and the close drops a flush error. The analysis
// resolves a body's deferred closes at the yield function's synthetic return, where this write is
// not visible, so nothing is reported. This test expects no diagnostic; when the close is
// resolved at the enclosing function's exits, add the want here.
func writtenAfterLoop(w io.WriteCloser) {
	for range seq(2) {
		defer w.Close()
	}
	_, _ = w.Write(nil)
}
