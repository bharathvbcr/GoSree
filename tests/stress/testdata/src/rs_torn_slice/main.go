// WITNESS: panic
//
// An out-of-bounds read in Go with no unsafe, no cgo and no reflection, in a program gocap
// accepts without a word.
//
// A slice is three words: pointer, length, capacity. The router is published through an
// atomic.Pointer, and an admin toggle flips the live router in place between a 1-route
// maintenance table and the 4096-route full table. A request that copies the field while a flip
// is half-written gets the 1-element slice's pointer with the 4096-element slice's length: the
// bounds check passes and routes[len(routes)-1] reads 32 KiB past the end of an 8-byte
// allocation. Rust rejects the program at compile time (the router is written while shared);
// the Go compiler accepts it; so does gocap.
//
// The atomic makes swapping the pointer safe, not writing what it points to. To the checker, Load
// is an ordinary method call returning a pointer nobody else is known to hold.
//
// Measured on an M5 Pro (darwin/arm64), load average 11 to 20: every run of the built binary
// convicted within a millisecond. An earlier version wrote `r.routes = short; r.routes = big`
// back to back; the compiler merges those into one store of the second value, so the field
// changed only once per run, and only the first write could tear. That, not the hardware, made
// it convict in some runs (within 100 microseconds) and never in others.
//
// rs_torn_slice_caught is the same race through a plain package variable: there SAFE006 fires.
package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

const (
	maintMark = 0xA1A1A1A1A1A1A1A1
	fullMark  = 0xB2B2B2B2B2B2B2B2
)

type Router struct{ routes []uint64 }

var live atomic.Pointer[Router]

var (
	maint = []uint64{maintMark}
	full  = fullTable(4096)
)

func fullTable(n int) []uint64 {
	t := make([]uint64, n)
	for i := range t {
		t[i] = fullMark
	}
	return t
}

func setMaintenance(on bool) {
	r := live.Load()
	if on {
		r.routes = maint // MISS(NORULE): r came out of a shared cell; this goroutine does not own it
	} else {
		r.routes = full // MISS(NORULE): the same write; requests read the field concurrently
	}
}

func handle() {
	routes := live.Load().routes // MISS(NORULE): an unsynchronised read of a field another goroutine writes
	last := routes[len(routes)-1]
	if len(routes) == len(full) && last != fullMark {
		panic(fmt.Sprintf("out-of-bounds read: element %d of a 1-element slice is %#x", len(routes)-1, last))
	}
	if len(routes) == 1 && last != maintMark {
		panic(fmt.Sprintf("torn slice header: the 1-route table points into the full table (%#x)", last))
	}
}

func main() {
	live.Store(&Router{routes: full})
	go func() {
		for i := 0; ; i++ {
			setMaintenance(i%2 == 0)
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
