package advrangefunc

import (
	"io"
	"iter"
	"os"
)

func fail() error { return nil }

func seq(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

func seq2() iter.Seq2[int, int] {
	return func(yield func(int, int) bool) { yield(0, 0) }
}

type S struct{ n int }

func (s S) Items(yield func(int) bool) {
	for i := range s.n {
		if !yield(i) {
			return
		}
	}
}

func (s *S) All() iter.Seq[int] { return s.Items }

func gen[T any](xs []T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, x := range xs {
			if !yield(x) {
				return
			}
		}
	}
}

// ERR001 after break/continue/return in the same body.
func ctl() error {
	for x := range seq(3) {
		if x == 1 {
			continue
		}
		if x == 2 {
			fail() // want "ERR001"
			break
		}
		if x == 3 {
			return nil
		}
		fail() // want "ERR001"
	}
	return nil
}

// Labelled break out of nested range-over-func.
func labelled() {
outer:
	for x := range seq(3) {
		for y := range seq(3) {
			if x == y {
				fail() // want "ERR001"
				break outer
			}
			fail() // want "ERR001"
		}
	}
}

// goto inside body.
func withGoto() {
	for x := range seq(3) {
		if x > 1 {
			goto done
		}
		fail() // want "ERR001"
	done:
		_ = x
	}
}

// Method value iterator and bound-method range.
func methodValue(s *S) {
	for x := range s.All() {
		fail() // want "ERR001"
		_ = x
	}
	for x := range s.Items {
		fail() // want "ERR001"
		_ = x
	}
}

// Generic iterator.
func generic(xs []string) {
	for x := range gen(xs) {
		fail() // want "ERR001"
		_ = x
	}
}

// Body that panics.
func bodyPanics() {
	for x := range seq(3) {
		if x > 1 {
			fail() // want "ERR001"
			panic("x")
		}
	}
}

// Deferred close of a writable resource inside the body attaches to the enclosing function.
func deferInBody(names []string) error {
	for x := range seq(3) {
		f, err := os.Create(names[x])
		if err != nil {
			return err
		}
		defer f.Close() // want "ERR005"
	}
	return nil
}

// Same, but a void function and early return from within the body.
func deferInBodyEarly(ws []io.WriteCloser) {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close() // want "ERR005"
		_, _ = w.Write([]byte("x"))
		if x == 1 {
			return
		}
	}
}

// Never written: clean.
func deferInBodyNoWrite(ws []io.WriteCloser) {
	for x := range seq(3) {
		w := ws[x]
		defer w.Close()
		_ = x
	}
}

// Range over Seq2 with a handled error: nothing.
func seq2Clean() error {
	for k, v := range seq2() {
		if err := fail(); err != nil {
			return err
		}
		_, _ = k, v
	}
	return nil
}

// Seq2 ERR005 in body.
func seq2Close(ws []io.WriteCloser) {
	for k := range seq2() {
		w := ws[k]
		_, _ = w.Write([]byte("x"))
		w.Close() // want "ERR005"
	}
}
