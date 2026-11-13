// Package errfe6efp holds shapes the closure replay must leave exactly as they were, in both
// directions: nothing here is reported, the Close either dropping nothing or on a closure the replay
// does not track. The package has no want markers. A shape marked MISS drops a flush error that
// is not reported, as before ft-fe6e: the closure is not tracked, so it is not replayed.
package errfe6efp

import "io"

func open() io.WriteCloser { return nil }

type holder struct{ cb func() }

func sink(f func()) { f() }

// A closure that writes a variable other than the one closed.
func otherVariable(a, b io.WriteCloser) {
	func() { _, _ = a.Write(nil) }()
	b.Close()
}

// The closure replaces the resource after writing it: the Close is on a fresh one, as in the
// advfvbreak3b3c pin.
func closureReassigns(w io.WriteCloser) {
	func() {
		_, _ = w.Write(nil)
		w = open()
	}()
	w.Close()
}

func closureReassignsThenWrites(w io.WriteCloser) {
	func() {
		w = open()
		_, _ = w.Write(nil)
	}()
	w.Close() // MISS: the closure stores to the captured variable
}

// A closure inside the closure replaces it.
func innerReassigns(w io.WriteCloser) {
	func() {
		_, _ = w.Write(nil)
		func() { w = open() }()
	}()
	w.Close()
}

// The Close in the closure comes before the closure's own write, and nothing was written before.
func closeBeforeOwnWrite(w io.WriteCloser) {
	func() {
		w.Close()
		_, _ = w.Write(nil)
	}()
}

// Not written anywhere.
func neverWritten(w io.WriteCloser) {
	func() { w.Close() }()
	defer w.Close()
}

// The creator writes after a closure that defers the Close has returned.
func writeAfterDeferredCall(w io.WriteCloser) {
	cl := func() { defer w.Close() }
	cl()
	_, _ = w.Write(nil)
}

// A closure on one branch only is not replayed.
func calledOnOneBranch(w io.WriteCloser, c bool) {
	write := func() { _, _ = w.Write(nil) }
	if c {
		write()
	}
	w.Close() // MISS: the call does not dominate the return
}

// The closure value is used more than once, escapes, or is run some other way: not replayed.
func calledTwice(w io.WriteCloser) {
	write := func() { _, _ = w.Write(nil) }
	write()
	write()
	w.Close() // MISS
}

func passedToCall(w io.WriteCloser) {
	sink(func() { _, _ = w.Write(nil) })
	w.Close() // MISS
}

func storedInField(w io.WriteCloser, h *holder) {
	h.cb = func() { _, _ = w.Write(nil) }
	h.cb()
	w.Close() // MISS
}

func deferredClosure(w io.WriteCloser) {
	defer func() { _, _ = w.Write(nil) }()
	w.Close() // MISS
}

func goClosure(w io.WriteCloser, done chan struct{}) {
	go func() {
		_, _ = w.Write(nil)
		close(done)
	}()
	<-done
	w.Close() // MISS
}

func closureClosesDeferred(w io.WriteCloser) {
	_, _ = w.Write(nil)
	defer func() { w.Close() }() // MISS
}

func closureClosesStored(w io.WriteCloser, h *holder) {
	_, _ = w.Write(nil)
	h.cb = func() { w.Close() } // MISS
	h.cb()
}

func returned(w io.WriteCloser) func() {
	_, _ = w.Write(nil)
	return func() { w.Close() } // MISS
}

// The address of the captured variable is taken in the closure.
func addressTaken(w io.WriteCloser, swap func(*io.WriteCloser)) {
	func() {
		_, _ = w.Write(nil)
		swap(&w)
	}()
	w.Close()
}

// The creator's own write is later reset by a reassignment before the closure that closes runs.
func reassignedBeforeCall(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w = open()
	func() { w.Close() }()
}

// Closing with the error checked is never reported.
func closeChecked(w io.WriteCloser) error {
	func() { _, _ = w.Write(nil) }()
	return w.Close()
}
