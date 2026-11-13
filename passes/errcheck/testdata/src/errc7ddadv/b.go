package errc7ddadv

import (
	"io"
	"sync"
)

// Package-level closures and initializers.
var global = func() func() {
	var w io.WriteCloser
	return func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}
}()

// A closure two layers down writes, the creator closes; the middle layer only passes it on.
func w01TwoLayers(w io.WriteCloser) {
	func() {
		func() { _, _ = w.Write(nil) }()
	}()
	w.Close() // want "ERR005"
}

func w01TwoLayersCloseInside(w io.WriteCloser) {
	_, _ = w.Write(nil)
	func() {
		func() { w.Close() }() // want "ERR005"
	}()
}

func w01TwoLayersTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// A closure over a holder reached through a field, written in a goroutine, closed after Wait.
func w02Goroutine(h *H) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = h.w.Write(nil)
	}()
	wg.Wait()
	h.w.Close() // want "ERR005"
}

func w02GoroutineTwin(w io.WriteCloser) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = w.Write(nil)
	}()
	wg.Wait()
	w.Close() // want "ERR005"
}

// The closure takes the resource as a parameter: nothing is captured.
func w03Param(w io.WriteCloser) {
	func(x io.WriteCloser) { _, _ = x.Write(nil) }(w)
	w.Close()
}

func w03ParamTwin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

// Generic closure over a type parameter.
func w04Generic[T io.WriteCloser](w T) {
	func() { _, _ = w.Write(nil) }()
	w.Close() // want "ERR005"
}

// Two closures write, a third closes, all called in order.
func w06ThreeClosures(w io.WriteCloser) {
	a := func() { _, _ = w.Write(nil) }
	b := func() { _, _ = w.Write(nil) }
	c := func() { w.Close() } // want "ERR005"
	a()
	b()
	c()
}

// The closure that closes is also called from a second closure.
func w07CloseCalledFromClosure(w io.WriteCloser) {
	_, _ = w.Write(nil)
	cl := func() { w.Close() } // want "ERR005"
	func() { cl() }()
}

// The closure closes a field and a variable, written in the creator.
func w08TwoCloses(h *H, w io.WriteCloser) {
	_, _ = h.w.Write(nil)
	_, _ = w.Write(nil)
	func() {
		h.w.Close() // want "ERR005"
		w.Close()   // want "ERR005"
	}()
}
