package advfvrange3b3c

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

// P5a: the body writes; the close is deferred BEFORE the loop in the enclosing function.
func deferBeforeLoopBodyWrites(w io.WriteCloser) {
	defer w.Close() // P5a
	for range seq(2) {
		_, _ = w.Write(nil)
	}
}

// P5a2: control without range-over-func.
func deferBeforeLoopControl(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	for i := 0; i < 2; i++ {
		_, _ = w.Write(nil) // want "ERR003"
	}
}

// P5b: closure inside a range-over-func body capturing a resource.
func closureInBody(w io.WriteCloser) {
	for range seq(2) {
		f := func() {
			defer w.Close() // want "ERR005"
			_, _ = w.Write(nil)
		}
		f()
	}
}

// P5c: body writes, closure in the body closes the captured var.
func bodyWritesClosureCloses(w io.WriteCloser) {
	for range seq(2) {
		_, _ = w.Write(nil)
		func() { w.Close() }() // P5c
	}
}

// P5e: variable captured by the body AND by a closure, with write in the closure.
func writeInClosureDeferInBody(w io.WriteCloser) {
	write := func() { _, _ = w.Write(nil) }
	for range seq(2) {
		defer w.Close() // P5e
		write()
	}
}

// P5f: range-over-func inside a closure.
func rangeInClosure(w io.WriteCloser) func() {
	return func() {
		for range seq(2) {
			defer w.Close() // want "ERR005"
			_, _ = w.Write(nil)
		}
	}
}

// P5g: range-over-func inside a closure, write after the loop.
func rangeInClosureWriteAfter(w io.WriteCloser) func() {
	return func() {
		for range seq(2) {
			defer w.Close() // P5g
		}
		_, _ = w.Write(nil)
	}
}
