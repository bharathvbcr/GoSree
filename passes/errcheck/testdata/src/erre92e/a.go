// Package erre92e pins what ERR005 reports on the tip (55b1e86) for every shape the per-store
// versioning design in DESIGN_ERR005_PATHS.md touches, and says what the design intends.
//
// The expectation markers state TODAY's output exactly, so the package passes on the tip in both
// engines.
// Each function carries one `// e92e-target:` line:
//
//	report       the design reports the Close. Today's marker says whether that is a win (no marker yet)
//	             or a shape that must keep reporting (a marker already).
//	stay silent  the design must leave this Close unreported; it is silent today and a design that
//	             reports it has added a false positive.
//	leave        a shape the design does not touch; today's output must not change.
package erre92e

import (
	"io"
	"sync"
)

type H struct{ w io.WriteCloser }

type HH struct {
	w1 io.WriteCloser
	w2 io.WriteCloser
}

type outer struct{ in H }

type H2 struct {
	H
	n int
}

type EW struct{ io.WriteCloser }

type IW interface {
	Write([]byte) (int, error)
	Close() error
}

type HI struct{ w IW }

type G[T io.WriteCloser] struct{ w T }

type node struct {
	w    io.WriteCloser
	next *node
}

func open() io.WriteCloser { return nil }

func note() {}

func sink(*H) {}

func (h *H) reset() { h.w = open() }

func swapW(p *io.WriteCloser) { *p = open() }

// ============ 1. the basic win: two loads of one field ============

// e92e-target: report
func basicWin(h *H) {
	_, _ = h.w.Write(nil)
	h.w.Close()
}

// e92e-target: report
func basicWinTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// e92e-target: report
func basicWinDefer(h *H) {
	_, _ = h.w.Write(nil)
	defer h.w.Close()
}

// e92e-target: report
func basicWinDeferTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	defer w.Close() // want "ERR005"
}

// e92e-target: report
func basicDeref(pw *io.WriteCloser) {
	_, _ = (*pw).Write(nil)
	(*pw).Close()
}

// e92e-target: report
func basicDerefTwin(w io.WriteCloser) {
	pw := &w
	_, _ = (*pw).Write(nil)
	(*pw).Close() // want "ERR005"
}

// e92e-target: report
func basicIfaceField(h *HI) {
	_, _ = h.w.Write(nil)
	h.w.Close()
}

// e92e-target: report
func basicGeneric[T io.WriteCloser](h *G[T]) {
	_, _ = h.w.Write(nil)
	h.w.Close()
}

// e92e-target: report
func basicPromoted(e *EW) {
	_, _ = e.Write(nil)
	e.Close()
}

// e92e-target: report
func basicPromotedTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// ============ 2. nested field chains ============

// e92e-target: report
func nestedChain(o *outer) {
	_, _ = o.in.w.Write(nil)
	o.in.w.Close()
}

// e92e-target: report
func nestedChainValue() {
	var o outer
	o.in.w = open()
	_, _ = o.in.w.Write(nil)
	o.in.w.Close()
}

// e92e-target: report
func nestedEmbedded(h *H2) {
	_, _ = h.w.Write(nil)
	h.H.w.Close()
}

// e92e-target: report
func nestedFlat(h *H) {
	_, _ = h.w.Write(nil)
	h.w.Close()
}

// ============ 3. aliases ============

// e92e-target: report
func aliasLoadThenClose(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	h.w.Close()
}

// e92e-target: report
func aliasLoadThenCloseTwin(w io.WriteCloser) {
	a := w
	_, _ = a.Write(nil)
	w.Close() // want "ERR005"
}

// e92e-target: report
func aliasPtrToField(h *H) {
	p := &h.w
	_, _ = (*p).Write(nil)
	h.w.Close()
}

// The alias is one SSA value: written and closed through it. Reported today and it must stay.
// e92e-target: report
func aliasSameValue(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	a.Close() // want "ERR005"
}

// ft-c7dd's alias-miss class (3 misses of 69eb7b6): the alias holds the written resource, the
// field is replaced, the alias closes. Reported today; path keying made it silent. It must stay.
// e92e-target: report
func aliasThenReplace(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	h.w = open()
	a.Close() // want "ERR005"
}

// e92e-target: report
func aliasThenReplaceTwin(cur io.WriteCloser) {
	a := cur
	_, _ = a.Write(nil)
	cur = open()
	a.Close() // want "ERR005"
}

// e92e-target: report
func aliasThenReplacePtr(p *io.WriteCloser) {
	a := *p
	_, _ = a.Write(nil)
	*p = open()
	a.Close() // want "ERR005"
}

// e92e-target: report
func aliasThenReplaceLoop(h *H) {
	for i := 0; i < 2; i++ {
		a := h.w
		_, _ = a.Write(nil)
		h.w = open()
		a.Close() // want "ERR005"
	}
}

// The alias is closed after a call replaced the field: the alias still holds the written object.
// e92e-target: report
func aliasThenCallReset(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	h.reset()
	a.Close() // want "ERR005"
}

// A store through a pointer to the field replaces it, the old load is still the written object.
// e92e-target: report
func aliasThenSwapW(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	swapW(&h.w)
	a.Close() // want "ERR005"
}

// ============ 4. stale flag: the field is replaced between the write and the Close ============

// e92e-target: stay silent
func staleStore(h *H) {
	_, _ = h.w.Write(nil)
	h.w = open()
	h.w.Close()
}

// e92e-target: stay silent
func staleCallReset(h *H) {
	_, _ = h.w.Write(nil)
	h.reset()
	h.w.Close()
}

// e92e-target: stay silent
func staleSwapWField(h *H) {
	_, _ = h.w.Write(nil)
	swapW(&h.w)
	h.w.Close()
}

// e92e-target: stay silent
func staleSwapWLocal(w io.WriteCloser) {
	_, _ = w.Write(nil)
	swapW(&w)
	w.Close()
}

// An unknown alias of the holder replaces the field.
// e92e-target: stay silent
func staleOtherHolder(h, alias *H) {
	_, _ = h.w.Write(nil)
	alias.w = open()
	h.w.Close()
}

// A pointer taken earlier stores through to the field.
// e92e-target: stay silent
func staleStoreThroughAlias(h *H) {
	p := &h.w
	_, _ = h.w.Write(nil)
	*p = open()
	h.w.Close()
}

// The whole struct is replaced.
// e92e-target: stay silent
func staleWholeStruct(h *H) {
	_, _ = h.w.Write(nil)
	*h = H{}
	h.w.Close()
}

// The inner struct of a chain is replaced.
// e92e-target: stay silent
func staleInnerStruct(o *outer, other *outer) {
	_, _ = o.in.w.Write(nil)
	o.in = other.in
	o.in.w.Close()
}

// The holder variable is reassigned. In a register the Close is of another holder.
// e92e-target: stay silent
func staleHolderReassigned(h, other *H) {
	_, _ = h.w.Write(nil)
	h = other
	h.w.Close()
}

// ... captured by a closure, the holder is a cell shared by every load and the old write was carried
// over (ft-c7dd's shared-root class, 5 FPs of 69eb7b6).
// e92e-target: stay silent
func staleHolderReassignedCaptured(h, other *H) {
	f := func() { _ = h }
	_, _ = h.w.Write(nil)
	h = other
	h.w.Close()
	f()
}

// A closure the creator calls replaces the field.
// e92e-target: stay silent
func staleClosureReplaces(h *H) {
	_, _ = h.w.Write(nil)
	f := func() { h.w = open() }
	f()
	h.w.Close()
}

// A goroutine replaces the field.
// e92e-target: stay silent
func staleGoroutineReplaces(h *H) {
	var wg sync.WaitGroup
	wg.Add(1)
	_, _ = h.w.Write(nil)
	go func() {
		defer wg.Done()
		h.w = open()
	}()
	wg.Wait()
	h.w.Close()
}

// A deferred Close binds the receiver when it is registered, the write after that is another object.
// e92e-target: stay silent
func staleDeferBindsEarly(h *H) {
	defer h.w.Close()
	h.w = open()
	_, _ = h.w.Write(nil)
}

// The alias closes the old object, the write went to the new one.
// e92e-target: stay silent
func staleAliasOldObject(h *H) {
	a := h.w
	h.w = open()
	_, _ = h.w.Write(nil)
	a.Close()
}

// ============ 5. a loop: the holder or the field is a new object each iteration ============

// The holder is a new object on each iteration: the Close of the next iteration is not of what the
// last one wrote. The entry version of the path must be reset where the holder is defined.
// e92e-target: stay silent
func loopHolderChanges(head *node) {
	for n := head; n != nil; n = n.next {
		n.w.Close()
		_, _ = n.w.Write(nil)
	}
}

// Written and closed in the same iteration, on one path.
// e92e-target: report
func loopHolderSameIteration(head *node, c bool) {
	for n := head; n != nil; n = n.next {
		if c {
			_, _ = n.w.Write(nil)
		}
		n.w.Close()
	}
}

// Each iteration writes the field it holds and closes it: the Close is of a written resource.
// e92e-target: report
func loopWriteThenClose(h *H) {
	for i := 0; i < 2; i++ {
		_, _ = h.w.Write(nil)
		h.w.Close()
	}
}

// e92e-target: report
func loopWriteThenCloseTwin(w io.WriteCloser) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}
}

// The field is replaced on each iteration after the Close, the write of the next is the new object.
// e92e-target: stay silent
func loopReplaceAfterClose(h *H) {
	for i := 0; i < 2; i++ {
		h.w.Close()
		h.w = open()
		_, _ = h.w.Write(nil)
	}
}

// ============ 6. joins ============

// Written on one path, the field replaced on the other: the join may be written, as a variable is.
// e92e-target: report
func joinWriteOrReplace(h *H, c bool) {
	if c {
		_, _ = h.w.Write(nil)
	} else {
		h.w = open()
	}
	h.w.Close()
}

// e92e-target: report
func joinWriteOrReplaceTwin(w io.WriteCloser, c bool) {
	if c {
		_, _ = w.Write(nil)
	} else {
		w = open()
	}
	w.Close() // want "ERR005"
}

// e92e-target: report
func joinReplaceOrWriteThenWrite(h *H, c bool) {
	_, _ = h.w.Write(nil)
	if c {
		h.w = open()
	}
	h.w.Close()
}

// e92e-target: report
func joinReplaceOrWriteThenWriteTwin(w io.WriteCloser, c bool) {
	_, _ = w.Write(nil)
	if c {
		w = open()
	}
	w.Close() // want "ERR005"
}

// e92e-target: stay silent
func joinBothReplace(h *H, c bool) {
	_, _ = h.w.Write(nil)
	if c {
		h.w = open()
	} else {
		h.reset()
	}
	h.w.Close()
}

// ============ 7. calls between the write and the Close ============

// A call that cannot reach the holder: the holder is a local whose address never escapes.
// e92e-target: report
func privateLocalCall() {
	var o H
	o.w = open()
	_, _ = o.w.Write(nil)
	note()
	o.w.Close()
}

// A call after the address of the local escaped may replace the field.
// e92e-target: stay silent
func privateLocalEscaped() {
	var o H
	o.w = open()
	_, _ = o.w.Write(nil)
	sink(&o)
	o.w.Close()
}

// A parameter's field and a call that takes nothing: a shared holder, so the call may replace it.
// e92e-target: stay silent
func sharedParamCall(h *H) {
	_, _ = h.w.Write(nil)
	note()
	h.w.Close()
}

// e92e-target: report
func privateLocalNoCall() {
	var o H
	o.w = open()
	_, _ = o.w.Write(nil)
	o.w.Close()
}

// ============ 8. different paths and different holders ============

// A different field of the same holder.
// e92e-target: stay silent
func otherField(h *HH) {
	_, _ = h.w1.Write(nil)
	h.w2.Close()
}

// e92e-target: report
func sameFieldOfPair(h *HH) {
	_, _ = h.w1.Write(nil)
	h.w1.Close()
}

// Two holders: the write and the Close are on different paths.
// e92e-target: stay silent
func twoHolders(h1, h2 *H) {
	_, _ = h1.w.Write(nil)
	h2.w.Close()
}

// Two holders share one resource. Missed today and by the design (no alias analysis).
// e92e-target: leave
func twoHoldersShareResource(w io.WriteCloser, h1, h2 *H) {
	h1.w = w
	h2.w = w
	_, _ = h1.w.Write(nil)
	h2.w.Close()
}

// Close before the write.
// e92e-target: stay silent
func closeThenWrite(h *H) {
	h.w.Close()
	_, _ = h.w.Write(nil)
}

// e92e-target: report
func twoClosesOfWritten(h *H) {
	_, _ = h.w.Write(nil)
	h.w.Close()
	h.w.Close()
}

// ============ 9. map and slice elements: out of the first cut ============

// e92e-target: leave
func mapElem(m map[string]io.WriteCloser, k string) {
	_, _ = m[k].Write(nil)
	m[k].Close()
}

// e92e-target: report
func mapElemTwin(m map[string]io.WriteCloser, k string) {
	w := m[k]
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// e92e-target: leave
func sliceElem(s []io.WriteCloser, i int) {
	_, _ = s[i].Write(nil)
	s[i].Close()
}

// e92e-target: leave
func sliceElemField(s []*H, i int) {
	_, _ = s[i].w.Write(nil)
	s[i].w.Close()
}

// e92e-target: report
func sliceElemTwin(s []io.WriteCloser, i int) {
	w := s[i]
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// e92e-target: stay silent
func sliceOther(s []*H) {
	_, _ = s[0].w.Write(nil)
	s[1].w.Close()
}

// ============ 10. a bound method value as a write ============

// The bound Write is called: a write of w.
// e92e-target: report
func boundWriteCalled(w io.WriteCloser) {
	wr := w.Write
	_, _ = wr(nil)
	w.Close()
}

// e92e-target: report
func boundWriteCalledField(h *H) {
	wr := h.w.Write
	_, _ = wr(nil)
	h.w.Close()
}

// The bound Write is never called.
// e92e-target: stay silent
func boundWriteUnused(w io.WriteCloser) {
	wr := w.Write
	_ = wr
	w.Close()
}

// The bound Write is handed away: whether it runs is unknown.
// e92e-target: leave
func boundWriteEscapes(w io.WriteCloser, use func(func([]byte) (int, error))) {
	wr := w.Write
	use(wr)
	w.Close()
}

// ============ 11. closures and fields (the er_closureclose markers) ============

type holder struct {
	w io.WriteCloser
	n int
}

// e92e-target: report
func closureFieldOfCapturedPointer(h *holder) func() {
	return func() {
		_, _ = h.w.Write(nil)
		defer h.w.Close()
	}
}

// e92e-target: report
func closureFieldOfCapturedByRef() func() {
	var h holder
	h.w = open()
	return func() {
		_, _ = h.w.Write(nil)
		defer h.w.Close()
		h.n++
	}
}

// e92e-target: report
func closureDerefOfCapturedPointer(pw *io.WriteCloser) func() {
	return func() {
		_, _ = (*pw).Write(nil)
		defer (*pw).Close()
	}
}

// The creator writes the field and a closure it calls once closes it.
// e92e-target: report
func creatorWritesFieldClosureCloses(h *H) {
	_, _ = h.w.Write(nil)
	func() {
		h.w.Close()
	}()
}

// A closure the creator calls once writes the field, the creator closes it.
// e92e-target: report
func closureWritesFieldCreatorCloses(h *H) {
	func() { _, _ = h.w.Write(nil) }()
	h.w.Close()
}

// The closure replaces the field it writes: it is not replayed.
// e92e-target: stay silent
func closureWritesAndReplaces(h *H) {
	func() {
		_, _ = h.w.Write(nil)
		h.w = open()
	}()
	h.w.Close()
}

// The shapes below are closure flow, not paths: the design leaves them (ft-e92e names them as the
// markers that keep their reason).

// e92e-target: leave
func closureDeferredClose(w io.WriteCloser) {
	defer func() { w.Close() }()
	_, _ = w.Write(nil)
}

// e92e-target: leave
func closureNestedCloses(w io.WriteCloser) func() func() {
	return func() func() {
		_, _ = w.Write(nil)
		return func() {
			w.Close()
		}
	}
}

// e92e-target: leave
func closureWriteInOtherClosure(w io.WriteCloser) {
	write := func() { _, _ = w.Write(nil) }
	for i := 0; i < 2; i++ {
		defer w.Close()
		write()
	}
}
