package errc7ddadvfp

import "io"

type H struct{ w io.WriteCloser }

func open() io.WriteCloser { return nil }

func (h *H) reset() { h.w = open() }

func swapW(p *io.WriteCloser) { *p = open() }

// NEW FALSE POSITIVES: nothing here is reported on 90fa6e9, and in every function the Close is of
// a resource that was not written.

// A method replaces the resource: the call is not a store the path can see.
func callReset(h *H) {
	_, _ = h.w.Write(nil)
	h.reset()
	h.w.Close()
}

// The same through a pointer to the field; the plain twin (a local whose address escapes) too.
func ptrArgReset(h *H) {
	_, _ = h.w.Write(nil)
	swapW(&h.w)
	h.w.Close()
}

func ptrArgResetLocal(w io.WriteCloser) {
	_, _ = w.Write(nil)
	swapW(&w)
	w.Close()
}

// An unknown alias of the holder replaces the resource.
func otherHolderParam(h, alias *H) {
	_, _ = h.w.Write(nil)
	alias.w = open()
	h.w.Close()
}

// The holder variable is reassigned. When it is a register (not captured) the twin is clean ...
func holderReassigned(h, other *H) {
	_, _ = h.w.Write(nil)
	h = other
	h.w.Close()
}

// ... when another closure captures it, its loads share one root and the old write is carried over.
func holderReassignedCaptured(h, other *H) {
	f := func() { _ = h }
	_, _ = h.w.Write(nil)
	h = other
	h.w.Close()
	f()
}

// A closure that closes is returned on the one path where nothing was written.
func escapeBranch(w io.WriteCloser, c bool) func() {
	if c {
		_, _ = w.Write(nil)
		return nil
	}
	return func() { w.Close() }
}

// A closure that closes is returned after the resource was written and replaced.
func escapeReplaced(w io.WriteCloser) func() {
	_, _ = w.Write(nil)
	w = open()
	return func() { w.Close() }
}

// A closure that closes by defer runs the Close when it returns, before the write that follows.
func deferInClosureThenWrite(w io.WriteCloser) {
	cl := func() { defer w.Close() }
	cl()
	_, _ = w.Write(nil)
}

func deferInClosureThenWriteTwin(w io.WriteCloser) {
	w.Close()
	_, _ = w.Write(nil)
}

// A write replayed at a use that does not run the closure.
func compareIsNotARun(w io.WriteCloser) {
	f := func() { _, _ = w.Write(nil) }
	if f == nil {
		return
	}
	w.Close()
}

type cbHolder struct{ cb func() }

// The closure is stored before the Close and called after it: the write is after the Close.
func registeredThenClosed(w io.WriteCloser, c *cbHolder) {
	c.cb = func() { _, _ = w.Write(nil) }
	w.Close()
	c.cb()
}

func registeredThenClosedTwin(w io.WriteCloser) {
	w.Close()
	_, _ = w.Write(nil)
}
