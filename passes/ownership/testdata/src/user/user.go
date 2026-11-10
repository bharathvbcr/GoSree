package user

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"lib"
)

func freshFromOtherPackage(ch chan *lib.Thing) {
	t := lib.NewThing()
	_ = lib.Size(t)
	ch <- t
}

func sharedFromOtherPackage(ch chan *lib.Thing) {
	t := lib.Shared()
	ch <- t // want `SAFE002: the result of Shared is sent on a channel but is not owned here: it may alias result of lib.Shared`
}

func keptByOtherPackage(ch chan *lib.Thing) {
	t := lib.NewThing()
	lib.Keep(t)
	ch <- t // want `SAFE004: the result of NewThing is sent on a channel but also escapes: passed to lib.Keep`
}

func consumedByOtherPackage(ch chan *lib.Thing) {
	t := lib.NewThing()
	lib.Consume(ch, t)
	_ = lib.Size(t) // want `SAFE003: the result of NewThing is used after it was passed to owned parameter 1 of lib.Consume`
}

// Standard-library summaries come from the same facts: bytes.Buffer methods do not retain the
// receiver, so a filled buffer can still be moved.
func stdlibBuffer(ch chan *bytes.Buffer) {
	b := new(bytes.Buffer)
	b.WriteString("hello")
	ch <- b
}

// Function overrides (internal/kinds/funcs.go) replace a summary with a documented assumption:
// io.ReadAll returns a slice it allocated, and a CancelFunc may be called from any goroutine.
func overriddenFresh(ch chan []byte, done chan context.CancelFunc, r io.Reader) {
	b, _ := io.ReadAll(r)
	ch <- b
	_, cancel := context.WithCancel(context.Background())
	done <- cancel
}

// Peek returns memory of its receiver: the result is linked to r, a parameter, so it is not owned,
// and it shares r's buffer, which Peek hands to the underlying Reader.
func peekAliases(ch chan []byte, r *bufio.Reader) {
	b, _ := r.Peek(1)
	ch <- b // want `SAFE002: this value is sent on a channel but is not owned here: it may alias parameter r` `SAFE004: this value is sent on a channel but also escapes: passed to \(\*bufio\.Reader\)\.Peek`
}

func seq(yield func(int) bool) {
	_ = yield(1) && yield(2)
}

// The body of a range-over-func loop is a closure, but it never leaves this goroutine.
func rangeFunc(ch chan *lib.Thing) {
	t := lib.NewThing()
	total := 0
	for x := range seq {
		total += x + lib.Size(t)
	}
	ch <- t
}

// append shares its source's backing array, so moving the result moves the source too.
func appendMove(ch chan []int) {
	a := make([]int, 0, 4)
	b := append(a, 1)
	ch <- b
	a = append(a, 2) // want `SAFE003: slice made here is used after it was sent on a channel`
	_ = a
}
