package errrangedefer

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

// Fall off the end of the body.
func fallOff(ws []io.WriteCloser) {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
	}
}

// break out of the body.
func withBreak(ws []io.WriteCloser) {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
		if x == 1 {
			break
		}
	}
}

// continue.
func withContinue(ws []io.WriteCloser) {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
		if x == 1 {
			continue
		}
	}
}

// return from the enclosing function out of the body.
func earlyReturn(ws []io.WriteCloser) int {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
		if x == 1 {
			return x
		}
	}
	return 0
}

// Never written: no finding on any exit.
func neverWritten(ws []io.WriteCloser) {
	for x := range seq(3) {
		defer ws[x].Close()
	}
}
