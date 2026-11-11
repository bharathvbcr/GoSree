package errfvswap

import "io"

func open() io.WriteCloser { return nil }

// FP: the captured variable is replaced by another closure called between the write and the
// close, so the closed resource was never written. The store happens in a different function, and
// no sound test identifies which calls run a closure bound to the same cell (the callee can be any
// function value), while resetting on every call would lose real findings such as a write, an
// unrelated call, then a close. The analysis keeps the written flag, so ERR005 is reported.
func replacedByCall(w io.WriteCloser) func() {
	swap := func() { w = open() }
	return func() {
		_, _ = w.Write(nil)
		swap()
		w.Close() // want "ERR005"
	}
}

// An unrelated call between the write and the close does not clear the flag.
func unrelatedCall(w io.WriteCloser, log func()) func() {
	return func() {
		_, _ = w.Write(nil)
		log()
		w.Close() // want "ERR005"
	}
}
