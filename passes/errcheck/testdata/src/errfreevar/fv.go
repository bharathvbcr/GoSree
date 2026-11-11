package errfreevar

import (
	"errors"
	"io"
	"os"
)

func work() error { return errors.New("x") }

func workV() (int, error) { return 1, nil }

// The reported shape: a closure defers Close on a captured resource and writes to it.
func capturedDeferClose(w io.WriteCloser) func() {
	return func() {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

// Same with an explicit return.
func capturedDeferCloseReturn(w io.WriteCloser) func() {
	return func() {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
		return
	}
}

// Write first, then the deferred close.
func capturedWriteThenDefer(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		defer w.Close() // want "ERR005"
	}
}

// A direct (non-deferred) close after a write.
func capturedDirectClose(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}
}

// Never written through the captured resource: no finding.
func capturedNeverWritten(w io.WriteCloser) func() {
	return func() {
		defer w.Close()
	}
}

// Captured *os.File local variable.
func capturedLocalFile() func() {
	f, _ := os.Create("x")
	return func() {
		defer f.Close() // want "ERR005"
		_, _ = f.WriteString("x")
	}
}

// Captured error variables: none of these report.

func closureAssignsErr() error {
	var err error
	func() { err = work() }()
	if err != nil {
		return err
	}
	return nil
}

func closureReturnsCapturedErr() error {
	err := work()
	f := func() error { return err }
	return f()
}

func runAll(fs ...func() error) error {
	for _, f := range fs {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func groupStyle() error {
	var first error
	set := func(e error) {
		if first == nil {
			first = e
		}
	}
	return runAll(func() error {
		set(work())
		return first
	})
}

func closureInLoop() error {
	var err error
	step := func() { err = work() }
	for i := 0; i < 3; i++ {
		step()
		if err != nil {
			return err
		}
	}
	return nil
}

func deferAssignsNamedResult() (err error) {
	defer func() {
		if e := work(); e != nil && err == nil {
			err = e
		}
	}()
	return nil
}

// A captured error checked and handled inside the closure.
func closureChecksCaptured() error {
	err := work()
	f := func() error {
		if err != nil {
			return err
		}
		return nil
	}
	return f()
}

// The closure reassigns the captured error between two checks of it; the second return nil
// is a lost error only if the first check proved nothing about the second.
func closureReassignsBetweenChecks() error {
	var err error
	f := func() error {
		if err != nil {
			return err
		}
		err = work()
		if err != nil {
			return nil // want "ERR003"
		}
		return nil
	}
	return f()
}

// A call in the closure may reassign the captured variable through another closure.
func closureCallMutatesCaptured() error {
	var err error
	reset := func() { err = work() }
	f := func() error {
		if err == nil {
			reset()
			if err != nil {
				return nil // want "ERR003"
			}
		}
		return nil
	}
	return f()
}
