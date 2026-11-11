// Package adv2fvstore pins 9244bb2 behavior for stores through a captured variable (identical in
// both engines). P1a, P1c, P1d, P1g were PRE-EXISTING misses (a store of a written value or a
// writable producer into a captured variable was not tracked) that the store now carrying the
// stored value's written state reports, correctly; P2b, P3a, P3c were false positives at aabf10f that the reset now removes (correct).
// The P2a, P2c, P3b, P3d, P3e ERR005 are correct and unchanged from aabf10f.
package adv2fvstore

import (
	"io"
	"os"
)

func open() io.WriteCloser { return nil }

// P1a: other written in the closure, stored into captured w, closed. Correct: ERR005.
func p1a(w io.WriteCloser, other io.WriteCloser) func() {
	return func() {
		_, _ = other.Write(nil)
		w = other
		defer w.Close() // P1a // want "ERR005"
	}
}

// P1c: local other, written, then stored.
func p1c(w io.WriteCloser) func() {
	return func() {
		other := open()
		_, _ = other.Write(nil)
		w = other
		w.Close() // P1c // want "ERR005"
	}
}

// P1d: writable producer os.Create stored into captured file; never written. Correct: ERR005 (writable).
func p1d() func() {
	var f *os.File
	return func() {
		f, _ = os.Create("x") // want "ERR002"
		defer f.Close()       // P1d // want "ERR005"
	}
}

// P1e: os.Create then write then close; no reassign through FreeVar after.
func p1e() func() {
	var f *os.File
	return func() {
		f, _ = os.Create("x") // want "ERR002"
		_, _ = f.Write(nil)
		f.Close() // P1e // want "ERR005"
	}
}

// P1f: store of os.Create, write after, close, in a captured file.
func p1f(f *os.File) func() {
	return func() {
		f, _ = os.Create("x") // want "ERR002"
		_, _ = f.Write(nil)
		defer f.Close() // P1f // want "ERR005"
	}
}

// P1g: os.OpenFile writable flags stored, closed without write.
func p1g(f *os.File) func() {
	return func() {
		f, _ = os.OpenFile("x", os.O_WRONLY, 0) // want "ERR002"
		f.Close()                               // P1g // want "ERR005"
	}
}

// P2a: write, conditional store, close. Not-taken path is written. Correct: ERR005.
func p2a(w io.WriteCloser, c bool) func() {
	return func() {
		_, _ = w.Write(nil)
		if c {
			w = open()
		}
		w.Close() // P2a // want "ERR005"
	}
}

// P2b: write only on one branch, reassign on that same branch, close after join.
func p2b(w io.WriteCloser, c bool) func() {
	return func() {
		if c {
			_, _ = w.Write(nil)
			w = open()
		}
		w.Close() // P2b
	}
}

// P2c: write on one branch, reassign on the other; join close. Correct: ERR005 (c path wrote).
func p2c(w io.WriteCloser, c bool) func() {
	return func() {
		if c {
			_, _ = w.Write(nil)
		} else {
			w = open()
		}
		w.Close() // P2c // want "ERR005"
	}
}

// P3a: loop write then reassign, close after loop.
func p3a(w io.WriteCloser, n int) func() {
	return func() {
		for i := 0; i < n; i++ {
			_, _ = w.Write(nil) // want "ERR003"
			w = open()
		}
		w.Close() // P3a
	}
}

// P3b: reassign, write, close in loop body.
func p3b(w io.WriteCloser, n int) func() {
	return func() {
		for i := 0; i < n; i++ {
			w = open()
			_, _ = w.Write(nil) // want "ERR003"
			w.Close()           // P3b // want "ERR005"
		}
	}
}

// P3c: reassign, close in loop body, write in a later iteration only.
func p3c(w io.WriteCloser, n int) func() {
	return func() {
		for i := 0; i < n; i++ {
			w = open()
			w.Close()           // P3c
			_, _ = w.Write(nil) // want "ERR003"
		}
	}
}

// P3d: store in loop, write after the loop, close.
func p3d(w io.WriteCloser, n int) func() {
	return func() {
		for i := 0; i < n; i++ {
			w = open()
		}
		_, _ = w.Write(nil)
		w.Close() // P3d // want "ERR005"
	}
}

// P3e: store of the same written variable in a loop (w = w).
func p3e(w io.WriteCloser, n int) func() {
	return func() {
		_, _ = w.Write(nil)
		for i := 0; i < n; i++ {
			w = w
		}
		w.Close() // P3e // want "ERR005"
	}
}
