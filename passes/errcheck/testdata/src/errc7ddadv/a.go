package errc7ddadv

import (
	"io"
	"iter"
	"os"
	"slices"
	"sort"
)

type H struct{ w io.WriteCloser }
type H2 struct {
	H
	n int
}
type EW struct{ io.WriteCloser }
type EF struct{ *os.File }
type IW interface {
	Write([]byte) (int, error)
	Close() error
}
type HI struct{ w IW }
type G[T io.WriteCloser] struct{ w T }

func open() io.WriteCloser { return nil }
func g() error             { return nil }
func mk() []byte           { return nil }
func must(error) []byte    { return nil }

func seq(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

// ================= 1. access paths: aliasing =================

func ap01Alias(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	h.w.Close() // want "ERR005"
}

func ap01AliasTwin(w io.WriteCloser) {
	a := w
	_, _ = a.Write(nil)
	w.Close() // want "ERR005"
}

func ap02PtrAlias(h *H) {
	p := &h.w
	_, _ = (*p).Write(nil)
	h.w.Close() // want "ERR005"
}

func ap02PtrAliasTwin(w io.WriteCloser) {
	p := &w
	_, _ = (*p).Write(nil)
	w.Close() // want "ERR005"
}

// A reassignment through the alias is a store to the same path: the written flag is reset.
func ap03StoreThroughAlias(h *H) {
	p := &h.w
	_, _ = h.w.Write(nil)
	*p = open()
	h.w.Close()
}

func ap03StoreThroughAliasTwin(w io.WriteCloser) {
	p := &w
	_, _ = w.Write(nil)
	*p = open()
	w.Close()
}

// Two holders share one resource: the write and the Close are on different paths.
// MISS (pre-existing: 90fa6e9 also reports nothing).
func ap04TwoHolders(w io.WriteCloser, h1, h2 *H) {
	h1.w = w
	h2.w = w
	_, _ = h1.w.Write(nil)
	h2.w.Close()
}

func ap04TwoHoldersTwin(w io.WriteCloser) {
	a, b := w, w
	_, _ = a.Write(nil)
	b.Close() // want "ERR005"
}

// Embedded fields and promoted methods.
func ap05EmbeddedIface(e *EW) {
	_, _ = e.Write(nil)
	e.Close() // want "ERR005"
}

func ap05EmbeddedFile(e *EF) {
	_, _ = e.Write(nil)
	e.Close() // want "ERR005"
}

func ap05EmbeddedValue() {
	var e EW
	e.WriteCloser = open()
	_, _ = e.Write(nil)
	e.Close() // want "ERR005"
}

func ap05EmbeddedTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// Map values and slice elements.
func ap06MapElem(m map[string]io.WriteCloser, k string) {
	_, _ = m[k].Write(nil)
	m[k].Close() // MISS (pre-existing)
}

func ap06MapElemTwin(m map[string]io.WriteCloser, k string) {
	w := m[k]
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

func ap06SliceElem(s []io.WriteCloser, i int) {
	_, _ = s[i].Write(nil)
	s[i].Close() // MISS (pre-existing)
}

func ap06SliceElemField(s []*H, i int) {
	_, _ = s[i].w.Write(nil)
	s[i].w.Close() // MISS (pre-existing)
}

func ap06SliceElemTwin(s []io.WriteCloser, i int) {
	w := s[i]
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// Distinct elements are not the written one.
func ap06SliceOther(s []*H) {
	_, _ = s[0].w.Write(nil)
	s[1].w.Close()
}

// Interface-typed fields and generic holders.
func ap07IfaceField(h *HI) {
	_, _ = h.w.Write(nil)
	h.w.Close() // want "ERR005"
}

func ap07Generic[T io.WriteCloser](h *G[T]) {
	_, _ = h.w.Write(nil)
	h.w.Close() // want "ERR005"
}

func ap07GenericTwin[T io.WriteCloser](w T) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

func ap07GenericUse() {
	ap07Generic(&G[*os.File]{})
}

// Different fields of one holder are different resources.
type two struct{ a, b io.WriteCloser }

func ap08OtherField(t *two) {
	_, _ = t.a.Write(nil)
	t.b.Close()
}

// ================= 1b. reassignment the path cannot see =================

// The holder replaces its resource inside a method: the Close is of a fresh, unwritten resource.
// The holder variable itself is reassigned between the write and the Close: another resource.
func ap10HolderReassigned(h, other *H) {
	_, _ = h.w.Write(nil)
	h = other
	h.w.Close()
}

func ap10HolderReassignedTwin(w, other io.WriteCloser) {
	_, _ = w.Write(nil)
	w = other
	w.Close()
}

// A loop over holders: each holder is written or closed, never both.
func ap11LoopHolders(hs []*H, done []bool) {
	for i, h := range hs {
		if done[i] {
			h.w.Close()
		} else {
			_, _ = h.w.Write(nil)
		}
	}
}

// FALSE POSITIVE, pre-existing on 90fa6e9 and pinned here: the flag of a slot defined inside the
// loop survives the back edge, so the Close of one element sees the write of another. The field
// form above is clean.
func ap11LoopHoldersTwin(ws []io.WriteCloser, done []bool) {
	for i, w := range ws {
		if done[i] {
			w.Close() // want "ERR005"
		} else {
			_, _ = w.Write(nil)
		}
	}
}

// ================= 2. ERR003 blank =================

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

// ================= 3. closure replay =================

func c01LoopCreated(w io.WriteCloser) {
	var fs []func()
	for i := 0; i < 2; i++ {
		fs = append(fs, func() { _, _ = w.Write(nil) })
	}
	for _, f := range fs {
		f()
	}
	w.Close() // want "ERR005"
}

func c01LoopCreatedTwin(w io.WriteCloser) {
	for i := 0; i < 2; i++ {
		_, _ = w.Write(nil)
	}
	w.Close() // want "ERR005"
}

type cbHolder struct{ cb func() }

func c02Field(w io.WriteCloser, c *cbHolder) {
	c.cb = func() { _, _ = w.Write(nil) }
	c.cb()
	w.Close() // want "ERR005"
}

func c02FieldCloser(w io.WriteCloser, c *cbHolder) {
	_, _ = w.Write(nil)
	c.cb = func() { w.Close() } // want "ERR005"
	c.cb()
}

func c02FieldCloserTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

func c03Recursive(w io.WriteCloser) {
	var f func(n int)
	f = func(n int) {
		if n > 0 {
			_, _ = w.Write(nil)
			f(n - 1)
		}
	}
	f(2)
	w.Close() // want "ERR005"
}

func c03RecursiveTwin(w io.WriteCloser) {
	for n := 2; n > 0; n-- {
		_, _ = w.Write(nil)
	}
	w.Close() // want "ERR005"
}

func c04SortSlice(w io.WriteCloser, s []int) {
	sort.Slice(s, func(i, j int) bool {
		_, _ = w.Write(nil)
		return s[i] < s[j]
	})
	w.Close() // want "ERR005"
}

func c04SortFunc(w io.WriteCloser, s []int) {
	slices.SortFunc(s, func(a, b int) int {
		_, _ = w.Write(nil)
		return a - b
	})
	w.Close() // want "ERR005"
}

func c04SortSliceTwin(w io.WriteCloser, s []int) {
	_, _ = w.Write(nil)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	w.Close() // want "ERR005"
}

func c04SortSliceNoWrite(w io.WriteCloser, s []int) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	w.Close()
}

func c05GoWrite(w io.WriteCloser, done chan struct{}) {
	defer w.Close() // want "ERR005"
	go func() {
		_, _ = w.Write(nil)
		close(done)
	}()
	<-done
}

func c05GoWriteTwin(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	_, _ = w.Write(nil)
}

func c05GoClose(w io.WriteCloser, done chan struct{}) {
	_, _ = w.Write(nil)
	go func() {
		w.Close() // want "ERR005"
		close(done)
	}()
	<-done
}

func c05GoNoWrite(w io.WriteCloser, done chan struct{}) {
	defer w.Close()
	go func() { close(done) }()
	<-done
}

func c06DeferLoop() {
	for i := 0; i < 2; i++ {
		w := open()
		defer func() { w.Close() }() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

func c06DeferLoopTwin() {
	for i := 0; i < 2; i++ {
		w := open()
		defer w.Close() // want "ERR005"
		_, _ = w.Write(nil)
	}
}

func c06DeferLoopNoWrite(ws []io.WriteCloser) {
	for _, w := range ws {
		defer func() { w.Close() }()
	}
}

func c07RangeBreak(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	for i := range seq(3) {
		if i == 1 {
			break
		}
		_, _ = w.Write(nil)
	}
}

func c07RangeBreakTwin(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	for i := 0; i < 3; i++ {
		if i == 1 {
			break
		}
		_, _ = w.Write(nil)
	}
}

func c07RangeReturn(w io.WriteCloser) error {
	for i := range seq(3) {
		if i == 1 {
			return nil
		}
		_, _ = w.Write(nil)
	}
	return w.Close() // ok: checked
}

func c07RangeReturnDrop(w io.WriteCloser) {
	for i := range seq(3) {
		if i == 1 {
			return
		}
		_, _ = w.Write(nil)
	}
	w.Close() // want "ERR005"
}

func c07Nested(w io.WriteCloser) {
	for range seq(2) {
		for range seq(2) {
			_, _ = w.Write(nil)
		}
	}
	w.Close() // want "ERR005"
}

func c07NestedTwin(w io.WriteCloser) {
	for range 2 {
		for range 2 {
			_, _ = w.Write(nil)
		}
	}
	w.Close() // want "ERR005"
}

func c07CloseInBody(w io.WriteCloser) {
	_, _ = w.Write(nil)
	for range seq(1) {
		w.Close() // want "ERR005"
	}
}

func c07CloseInBodyTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	for range 1 {
		w.Close() // want "ERR005"
	}
}

func c07CloseInBodyNoWrite(w io.WriteCloser) {
	for range seq(1) {
		w.Close()
	}
}

func c07WriteAfterClose(w io.WriteCloser) {
	for range seq(1) {
		w.Close()
	}
	_, _ = w.Write(nil)
}

func c07CloseBeforeWriteLoop(w io.WriteCloser) {
	w.Close()
	for range seq(1) {
		_, _ = w.Write(nil)
	}
}

func c08WriteAndCloseTwice(w io.WriteCloser) {
	cl := func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}
	cl()
	cl()
}

func c08WriteAndCloseOnce(w io.WriteCloser) {
	func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}()
}

func c09Panic(w io.WriteCloser, c bool) {
	func() {
		if c {
			panic("x")
		}
		_, _ = w.Write(nil)
	}()
	w.Close() // want "ERR005"
}

func c09Recover(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	defer func() {
		recover()
		_, _ = w.Write(nil)
	}()
}

// The closure is created before the Close but only runs after it.
func c10WriteAfterClose(w io.WriteCloser) {
	f := func() { _, _ = w.Write(nil) }
	w.Close()
	f()
}

func c10WriteAfterCloseTwin(w io.WriteCloser) {
	w.Close()
	_, _ = w.Write(nil)
}

// Branches: the writing closure runs only on one side.
func c11Branch(w io.WriteCloser, fast bool) {
	write := func() { _, _ = w.Write(nil) }
	if fast {
		write()
	} else {
		w.Close()
	}
}

func c11BranchTwin(w io.WriteCloser, fast bool) {
	if fast {
		_, _ = w.Write(nil)
	} else {
		w.Close()
	}
}

// The creator replaces the resource after the closure ran.
func c12ReplaceAfter(w io.WriteCloser) {
	f := func() { _, _ = w.Write(nil) }
	f()
	w = open()
	w.Close()
}

func c12ReplaceAfterTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w = open()
	w.Close()
}

// Closures over different holders.
func c13OtherResource(a, b io.WriteCloser) {
	func() { _, _ = a.Write(nil) }()
	b.Close()
}

func c13OtherResourceField(a, b *H) {
	func() { _, _ = a.w.Write(nil) }()
	b.w.Close()
}

func c13SameHolderFields(h *two) {
	func() { _, _ = h.a.Write(nil) }()
	h.b.Close()
}

// ================= more =================

// Written, then the closure is returned: it closes a written resource.
func s01EscapeWritten(w io.WriteCloser) func() {
	_, _ = w.Write(nil)
	return func() { w.Close() } // want "ERR005"
}

func c14DeferInClosureThenWriteTwin(w io.WriteCloser) {
	w.Close()
	_, _ = w.Write(nil)
}

func c14DeferInClosureAfterWrite(w io.WriteCloser) {
	_, _ = w.Write(nil)
	func() { defer w.Close() }() // want "ERR005"
}

// A write between two calls of a closure that closes by defer.
func c14DeferTwice(w io.WriteCloser) {
	cl := func() { defer w.Close() } // want "ERR005"
	cl()
	_, _ = w.Write(nil)
	cl() // closes a written resource
}

// Bound method values.
func b01BoundWrite(w io.WriteCloser) {
	wr := w.Write
	_, _ = wr(nil)
	w.Close() // MISS (pre-existing): a bound method value is not a write of w
}

func b01BoundClose(w io.WriteCloser) {
	_, _ = w.Write(nil)
	cl := w.Close
	cl() // want "ERR001"
}

func b01BoundWriteUnused(w io.WriteCloser) {
	wr := w.Write
	_ = wr
	w.Close()
}

// Nested struct fields (a chain of field addresses).
type outer struct{ in H }

func ap14Flat(h *H) {
	_, _ = h.w.Write(nil)
	h.w.Close() // want "ERR005"
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
