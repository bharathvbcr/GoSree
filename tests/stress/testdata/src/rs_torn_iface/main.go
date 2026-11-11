// WITNESS: panic
//
// Type confusion in Go with no unsafe, no cgo and no reflection, in a program gosree accepts
// without a word.
//
// An interface value is two words: a type word and a data word. The route is published through an
// atomic.Pointer, and a toggle flips its codec in place between a *Gzip and a *Plain. A request
// that reads the field while a flip is half-written calls Gzip's method on a Plain's memory (or
// the reverse). Rust rejects the program (the route is written while it is shared); gosree accepts
// it. The two codecs have the same layout, so the confusion is detected by a magic word instead of
// crashing; with different layouts it reads or writes past the object.
//
// An earlier version of this witness never tore: it wrote `c.v = d; c.v = n` back to back, which
// the compiler merges into one store, so the field never changed. Alternating per call fixes it.
package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

const (
	gzipMark  = 0x6A6A6A6A6A6A6A6A
	plainMark = 0x5B5B5B5B5B5B5B5B
)

type Codec interface{ Check() }

type Gzip struct{ magic uint64 }

func (g *Gzip) Check() {
	if g.magic != gzipMark {
		panic(fmt.Sprintf("type confusion: (*Gzip).Check ran on memory holding %#x, a *Plain", g.magic))
	}
}

type Plain struct{ magic uint64 }

func (p *Plain) Check() {
	if p.magic != plainMark {
		panic(fmt.Sprintf("type confusion: (*Plain).Check ran on memory holding %#x, a *Gzip", p.magic))
	}
}

type Route struct{ codec Codec }

var active atomic.Pointer[Route]

var (
	gzip  = &Gzip{magic: gzipMark}
	plain = &Plain{magic: plainMark}
)

func setCompression(on bool) {
	r := active.Load()
	if on {
		r.codec = gzip // HIT(SAFE005): r came out of a shared cell; this goroutine does not own it
	} else {
		r.codec = plain // HIT(SAFE005): the same write; requests call through the field concurrently
	}
}

func handle() {
	active.Load().codec.Check() // read-only under \imm; setCompression's writes above are rejected by SAFE005
}

func main() {
	active.Store(&Route{codec: plain})
	go func() {
		for i := 0; ; i++ {
			setCompression(i%2 == 0)
		}
	}()
	for range 2 {
		go func() {
			for {
				handle()
			}
		}()
	}
	// A torn read panics in a handler and ends the process. Reaching the deadline means it never
	// happened: exit cleanly so the suite reports that the witness witnessed nothing.
	time.Sleep(20 * time.Second)
	os.Exit(0)
}
