package advrangefuncdefer

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

// BREAK: the non-range equivalent (errdeferend.fallOffEnd) reports ERR005 on the deferred close.
// A range-over-func body falls off its end through the synthetic yield return (`return true`),
// which is not a result-less return, so the deferred close never resolves.
func deferInBodyLateWrite(ws []io.WriteCloser) {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
	}
}

// Control: same shape, no range-over-func; reports.
func control(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	_, _ = w.Write([]byte("x"))
}
