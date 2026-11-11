package advfvctl3b3c

import "io"

func open() io.WriteCloser { return nil }

// C1: non-captured local reassigned between write and close (the unification the fix extends).
func localReassign() {
	w := open()
	_, _ = w.Write(nil)
	w = open()
	defer w.Close() // C1
}

// C2: parameter reassigned between write and close.
func paramReassign(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w = open()
	w.Close() // C2
}

// C3: pointer-to-interface captured by value; *pw reassigned between write and close.
func ptrIface(pw *io.WriteCloser) func() {
	return func() {
		_, _ = (*pw).Write(nil)
		*pw = open()
		(*pw).Close() // C3
	}
}

// C4: same without reassignment (true positive).
func ptrIfaceNoReassign(pw *io.WriteCloser) func() {
	return func() {
		_, _ = (*pw).Write(nil)
		(*pw).Close() // C4
	}
}

// C6: captured resource closed in the closure; write only in a different branch-free earlier call
// of a *different* closure variable of the same name (shadow in closure body).
func shadowParam(w io.WriteCloser) func(w io.WriteCloser) {
	_, _ = w.Write(nil)
	return func(w io.WriteCloser) {
		w.Close() // C6 param, not the captured one
	}
}
