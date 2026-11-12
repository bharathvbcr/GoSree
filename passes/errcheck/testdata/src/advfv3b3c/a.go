package advfv3b3c

import (
	"io"
	"os"
)

func open() io.WriteCloser { return nil }

// ---- Probe 1: shared written flag ----

// P1b: two closures share one captured variable; one writes, the other closes.
func twoClosures(w io.WriteCloser) (func(), func()) {
	write := func() { _, _ = w.Write(nil) }
	cl := func() { defer w.Close() } // P1b
	return write, cl
}

// P1c: a captured reader that is only read.
func readOnly(r io.ReadCloser) func() {
	return func() {
		defer r.Close() // P1c must stay silent
		_, _ = r.Read(nil)
	}
}

// P1d: close with the error checked.
func checkedClose(w io.WriteCloser) func() error {
	return func() error {
		_, _ = w.Write(nil)
		if err := w.Close(); err != nil { // P1d silent
			return err
		}
		return nil
	}
}

// P1d2: deferred close in a func literal that checks the error.
func checkedDeferClose(w io.WriteCloser) func() (err error) {
	return func() (err error) {
		_, _ = w.Write(nil)
		defer func() {
			if cerr := w.Close(); cerr != nil && err == nil { // P1d2 silent
				err = cerr
			}
		}()
		return nil
	}
}

// P1e: write through the captured variable in a branch; close in the other branch only.
func branchWrite(w io.WriteCloser, c bool) func() {
	return func() {
		if c {
			_, _ = w.Write(nil)
		} else {
			w.Close() // P1e never written on this path
		}
	}
}

// P1f: reassigned in a loop: write old, reassign, close new each iteration.
func loopReassign(w io.WriteCloser) func() {
	return func() {
		for i := 0; i < 2; i++ {
			w.Close() // want "ERR005"
			w = open()
			_, _ = w.Write(nil) // want "ERR003"
		}
	}
}

// P1g: the closure writes, then the enclosing function replaces the variable before calling Close
// in another closure.
func replacedBetween(w io.WriteCloser) {
	write := func() { _, _ = w.Write(nil) }
	write()
	w = open()
	cl := func() { w.Close() } // P1g
	cl()
}

// ---- Probe 3: interprocedural ----

// P3a: the enclosing function defers Close; a closure it calls does the write.
func enclosingDefersClosureWrites(f *os.File) {
	defer f.Close() // P3a
	func() { _, _ = f.Write(nil) }()
}

// P3b: the closure closes; the enclosing function writes.
func closureClosesEnclosingWrites(f *os.File) {
	_, _ = f.Write(nil)
	func() { f.Close() }() // P3b
}

// P3b2: deferred closure closes, enclosing writes.
func deferredClosureCloses(f *os.File) {
	defer func() { f.Close() }() // P3b2
	_, _ = f.Write(nil)
}

// P3c: local captured var, closure defers close, enclosing writes after creating closure.
func localCaptured() {
	f, _ := os.Create("x")           // want "ERR002"
	cl := func() { defer f.Close() } // P3c
	_, _ = f.Write(nil)
	cl()
}

// ---- Probe 4: odd loads ----

type holder struct {
	w io.WriteCloser
	n int
}

// P4a: FreeVar captured by value (never reassigned, so SSA captures the value, no load).
func byValue(w io.WriteCloser) func() {
	return func() {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

// P4b: field load through a captured struct pointer.
func fieldLoad(h *holder) func() {
	return func() {
		_, _ = h.w.Write(nil)
		defer h.w.Close() // P4b
	}
}

// P4b2: field load through a captured struct value that is mutated (so captured by reference).
func fieldLoadByRef() func() {
	var h holder
	h.w = open()
	return func() {
		_, _ = h.w.Write(nil)
		defer h.w.Close() // P4b2
		h.n++
	}
}

// P4b3: field reassigned between write and close.
func fieldReassign(h *holder) func() {
	return func() {
		_, _ = h.w.Write(nil)
		h.w = open()
		h.w.Close() // P4b3 new, never-written resource
	}
}

// P4c: interface-typed FreeVar holding *os.File.
func ifaceFile() func() {
	var w io.WriteCloser
	w, _ = os.Create("x")
	return func() {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

// P4d: generic closure.
func generic[T io.WriteCloser](w T) func() {
	return func() {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

// P4e: nested closure: a FreeVar of a FreeVar.
func nested(w io.WriteCloser) func() func() {
	return func() func() {
		return func() {
			defer w.Close() // want "ERR005"
			_, _ = w.Write(nil)
		}
	}
}

// P4e2: nested, write in outer closure, close in inner.
func nestedSplit(w io.WriteCloser) func() func() {
	return func() func() {
		_, _ = w.Write(nil)
		return func() {
			w.Close() // P4e2
		}
	}
}

// P4e3: nested, reassigned in the inner closure.
func nestedReassign(w io.WriteCloser) func() func() {
	return func() func() {
		_, _ = w.Write(nil)
		return func() {
			w = open()
			w.Close() // P4e3
		}
	}
}

// P4f: goroutine closure.
func goroutine(w io.WriteCloser) {
	go func() {
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}()
}

// P4g: method value bound to a captured receiver.
type res struct{ w io.WriteCloser }

func (r *res) close() error { return r.w.Close() }

func methodValue(r *res) func() {
	cl := r.close
	return func() {
		_, _ = r.w.Write(nil)
		cl() // want "ERR001"
	}
}

// P4h: same slot loaded twice, second load is of a differently-typed var of the same name.
func shadowed(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		{
			w := open()
			defer w.Close() // P4h new local, never written
		}
	}
}

// P4i: bound Close method value on a captured variable.
func boundClose(w io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		c := w.Close
		defer c() // want "ERR001"
	}
}
