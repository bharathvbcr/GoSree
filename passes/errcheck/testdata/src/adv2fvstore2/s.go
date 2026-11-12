// Package adv2fvstore2: shapes that hold (store then write then close, identical before and after).
package adv2fvstore2

import "io"

func open() io.WriteCloser { return nil }

// Q1: straight line store, write, close. Correct: ERR005.
func q1(w io.WriteCloser) func() {
	return func() {
		w = open()
		_, _ = w.Write(nil)
		w.Close() // Q1 // want "ERR005"
	}
}

// Q2: same with defer.
func q2(w io.WriteCloser) func() {
	return func() {
		w = open()
		_, _ = w.Write(nil)
		defer w.Close() // Q2 // want "ERR005"
	}
}

// Q3: loop, store, write, close.
func q3(w io.WriteCloser, n int) func() {
	return func() {
		for i := 0; i < n; i++ {
			w = open()
			_, _ = w.Write(nil) // want "ERR003"
			w.Close()           // Q3 // want "ERR005"
		}
	}
}

// Q4: store of a written value then write again then close.
func q4(w io.WriteCloser, o io.WriteCloser) func() {
	return func() {
		w = o
		_, _ = w.Write(nil)
		w.Close() // Q4 // want "ERR005"
	}
}

// Q5: write, store, write, close.
func q5(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		w = open()
		_, _ = w.Write(nil)
		w.Close() // Q5 // want "ERR005"
	}
}

// Q6: conditional store then write then close.
func q6(w io.WriteCloser, c bool) func() {
	return func() {
		if c {
			w = open()
		}
		_, _ = w.Write(nil)
		w.Close() // Q6 // want "ERR005"
	}
}
