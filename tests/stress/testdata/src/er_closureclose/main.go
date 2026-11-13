// Package er_closureclose stress-tests ERR005 where the write and the Close that drops its error
// sit in different functions, or are reached through a field or a captured pointer. Each marked
// line is a Close error dropped on a resource that was written; none is reported today, because a
// closure and its creator are lowered separately and a resource reached through a field has no
// written flag shared between its loads. The fix was tried on run2/c7dd-err005-wip and set aside
// because it reported Close of resources that were not written; card ft-fe6eb6d8 owns it.
package er_closureclose

import (
	"io"
	"iter"
)

type holder struct {
	w io.WriteCloser
	n int
}

func open() io.WriteCloser { return nil }

func seq(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

// The creator defers Close and a closure it calls does the write.
func creatorDefersClosureWrites(w io.WriteCloser) {
	defer w.Close() // MISS(ERR005): ft-fe6eb6d8: the write is in a closure, a different function from the Close
	func() { _, _ = w.Write(nil) }()
}

// The creator writes and a closure it calls closes.
func closureClosesCreatorWrites(w io.WriteCloser) {
	_, _ = w.Write(nil)
	func() { w.Close() }() // MISS(ERR005): ft-fe6eb6d8: the Close is in a closure, a different function from the write
}

func deferredClosureCloses(w io.WriteCloser) {
	defer func() { w.Close() }() // MISS(ERR005): ft-fe6eb6d8: the Close is in a deferred closure, a different function from the write
	_, _ = w.Write(nil)
}

func localCapturedByClosure() {
	w := open()
	cl := func() { defer w.Close() } // MISS(ERR005): ft-fe6eb6d8: the Close is in a closure the creator runs after writing
	_, _ = w.Write(nil)
	cl()
}

// A field of a captured struct pointer, and its twin without a closure.
func fieldThroughCapturedPointer(h *holder) func() {
	return func() {
		_, _ = h.w.Write(nil)
		defer h.w.Close() // MISS(ERR005): ft-fe6eb6d8: the two loads of h.w are different SSA values with no shared written flag
	}
}

func fieldThroughCapturedPointerTwin(h *holder) {
	_, _ = h.w.Write(nil)
	defer h.w.Close() // MISS(ERR005): ft-fe6eb6d8: the two loads of h.w are different SSA values with no shared written flag
}

func fieldOfCapturedByRef() func() {
	var h holder
	h.w = open()
	return func() {
		_, _ = h.w.Write(nil)
		defer h.w.Close() // MISS(ERR005): ft-fe6eb6d8: the two loads of h.w are different SSA values with no shared written flag
		h.n++
	}
}

// A captured pointer to an interface, and its twin without a closure.
func derefOfCapturedPointer(pw *io.WriteCloser) func() {
	return func() {
		_, _ = (*pw).Write(nil)
		defer (*pw).Close() // MISS(ERR005): ft-fe6eb6d8: the two loads of *pw are different SSA values with no shared written flag
	}
}

func derefOfCapturedPointerTwin(pw *io.WriteCloser) {
	_, _ = (*pw).Write(nil)
	defer (*pw).Close() // MISS(ERR005): ft-fe6eb6d8: the two loads of *pw are different SSA values with no shared written flag
}

// The write is in an outer closure and the Close in a closure nested in it.
func nestedClosureCloses(w io.WriteCloser) func() func() {
	return func() func() {
		_, _ = w.Write(nil)
		return func() {
			w.Close() // MISS(ERR005): ft-fe6eb6d8: the Close is in a closure nested in the one that writes
		}
	}
}

// Range-over-func: the body is a closure of its own.
func deferBeforeLoopBodyWrites(w io.WriteCloser) {
	defer w.Close() // MISS(ERR005): ft-fe6eb6d8: the write is in the range-over-func body, a closure; the plain for loop reports this
	for range seq(2) {
		_, _ = w.Write(nil)
	}
}

func bodyWritesClosureCloses(w io.WriteCloser) {
	for range seq(2) {
		_, _ = w.Write(nil)
		func() { w.Close() }() // MISS(ERR005): ft-fe6eb6d8: the Close is in a closure inside the body that writes
	}
}

func writeInClosureDeferInBody(w io.WriteCloser) {
	write := func() { _, _ = w.Write(nil) }
	for range seq(2) {
		defer w.Close() // MISS(ERR005): ft-fe6eb6d8: the write is in another closure that the body calls
		write()
	}
}

func writeAfterLoopInClosure(w io.WriteCloser) func() {
	return func() {
		for range seq(2) {
			defer w.Close() // MISS(ERR005): ft-fe6eb6d8: the body's deferred close is resolved at the yield function's return, which does not see the write after the loop
		}
		_, _ = w.Write(nil)
	}
}
