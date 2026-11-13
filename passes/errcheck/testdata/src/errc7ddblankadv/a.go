package errc7ddblankadv

import (
	"io"
	"os"
)

func g() error          { return nil }
func mk() []byte        { return nil }
func must(error) []byte { return nil }

// ft-c7dd adversary cases for the all-blank discard (ported from beb8a61, ERR003 only).


func e3SameLine(w io.Writer) error {
	var err error
	for i := 0; i < 2; i++ {
		_, _ = w.Write(nil); err = g() // want "ERR003"
	}
	return err
}

func e3SameLineTwin(w io.Writer) error {
	var err error
	_, _ = w.Write(nil)
	err = g()
	err = g() // want "ERR003"
	return err
}

func e3Nested(w io.Writer) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(must(g()))
	}
}

func e3NestedArgs(w io.Writer) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(mk())
	}
}

func e3MultiLine(w io.Writer) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(
			nil,
		)
	}
}

func e3MultiLineReal(w io.Writer) {
	var err error
	for i := 0; i < 2; i++ {
		_, err = w.Write( // want "ERR003"
			nil,
		)
	}
	_ = err
}

func e3ClosureInLoop(w io.Writer) {
	for i := 0; i < 2; i++ {
		func() { _, _ = w.Write(nil) }()
	}
}

func e3ClosureInLoopReal(w io.Writer) {
	var err error
	for i := 0; i < 2; i++ {
		func() { _, err = w.Write(nil) }() // not reported on 90fa6e9 either
	}
	_ = err
}

func e3Generic[T io.Writer](w T) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(nil)
	}
}

func e3GenericFunc[T any](f func() (T, error)) {
	for i := 0; i < 2; i++ {
		_, _ = f()
	}
}

func e3GenericReal[T any](f func() (T, error)) {
	var err error
	for i := 0; i < 2; i++ {
		_, err = f() // want "ERR003"
	}
	_ = err
}

func e3MethodValue(w io.Writer) {
	wr := w.Write
	for i := 0; i < 2; i++ {
		_, _ = wr(nil)
	}
}

func e3MethodValueReal(w io.Writer) {
	wr := w.Write
	var err error
	for i := 0; i < 2; i++ {
		_, err = wr(nil) // want "ERR003"
	}
	_ = err
}

func e3BoundN(w io.Writer) int {
	var n int
	for i := 0; i < 2; i++ {
		n, _ = w.Write(nil)
	}
	return n // want "ERR002"
}

func e3TwoSingles() {
	for i := 0; i < 2; i++ {
		_, _ = g(), g()
	}
}

func e3RealOverwrite(w io.Writer) {
	var err error
	for i := 0; i < 2; i++ {
		_, err = w.Write(nil) // want "ERR003"
	}
	_ = err
}

func e3IfInit(w io.Writer) {
	for i := 0; i < 2; i++ {
		if _, _ = w.Write(nil); i > 0 {
			continue
		}
	}
}

func e3Plain(w io.Writer) error {
	var err error
	for i := 0; i < 2; i++ {
		err = g() // want "ERR003"
	}
	return err
}


// Discards of calls that are not an error-returning Write.
func e3Create() {
	for i := 0; i < 2; i++ {
		_, _ = os.Create("x")
	}
}

func e3Copy(r io.Reader) {
	for i := 0; i < 2; i++ {
		_, _ = io.Copy(io.Discard, r)
	}
}
