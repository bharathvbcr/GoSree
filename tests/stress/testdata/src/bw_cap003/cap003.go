// Package bw_cap003 attacks CAP003: a stack-scoped borrow crossing a goroutine boundary.
//
// Several CAP003 misses below are still rejected by the ownership pass as SAFE002 (the goroutine
// takes a value this frame does not own). They are recorded because CAP003 is the rule that names
// the hazard, and because SAFE002 goes away once the spawner's parameter is marked iso (case 6b).
package bw_cap003

import (
	"sync"
	"time"
)

type Tuple struct{ p *int }

func (t Tuple) bump() { *t.p++ }

// 1. control: a goroutine closure capturing the borrow.
//
//gosree:mub b
func captureDirect(b *int) {
	go func() { *b = 1 }() // HIT(CAP003)
}

// 2. the borrow inside the transient stack tuple, captured by a goroutine closure: the captured
// variable is t, which is not itself annotated.
//
//gosree:mub b
func captureViaTuple(b *int) {
	t := Tuple{p: b}
	go func() { *t.p = 1 }() // HIT(SAFE002) MISS(CAP003): t.p is b; the goroutine writes through it while the caller may still use b
}

// 3. the tuple passed as a go-statement argument.
//
//gosree:mub b
func tupleAsGoArg(b *int) {
	t := Tuple{p: b}
	go Tuple.bump(t) // HIT(BORROW001) HIT(SAFE002)
}

// 4. a method value bound to the tuple, then started as a goroutine.
//
//gosree:mub b
func methodValueOfTuple(b *int) {
	t := Tuple{p: b}
	f := t.bump
	go f() // HIT(SAFE002) MISS(CAP003): f's receiver is t, which holds b
}

// 5. a closure capturing a closure that captures the borrow.
//
//gosree:mub b
func closureOfClosure(b *int) {
	inner := func() { *b = 1 }
	go func() { inner() }() // HIT(CAP003): inner captures b, and the goroutine calls inner
}

// 6. a helper that spawns: the borrow is passed into an ordinary call, and the callee starts the
// goroutine with its own (unannotated) parameter. The callee is rejected (SAFE002, it does not own
// p); the call site that hands it a \mub borrow is not.
func spawn(p *int) { go func() { *p = 1 }() } // HIT(SAFE002)

//gosree:mub b
func viaSpawningHelper(b *int) {
	spawn(b) // MISS(CAP003): spawn starts a goroutine that writes through b while this frame may still use it
}

// 6b. control: the same spawner with its parameter marked iso, which makes the callee clean. The
// caller passing a \mub borrow (which it does not own) to the iso parameter is then caught.
//
//gosree:iso p
func spawnIso(p *int) { go func() { *p = 1 }() }

//gosree:mub b
func viaIsoSpawner(b *int) {
	spawnIso(b) // HIT(SAFE002)
	*b = 2      // HIT(SAFE003)
}

// 7. WaitGroup.Go as the spawner.
//
//gosree:mub b
func viaWaitGroupGo(b *int) {
	var wg sync.WaitGroup
	wg.Go(func() { *b = 1 }) // HIT(CAP003)
	wg.Wait()
}

// 8. time.AfterFunc as the spawner.
//
//gosree:mub b
func viaAfterFunc(b *int) {
	time.AfterFunc(time.Millisecond, func() { *b = 1 }) // HIT(CAP003)
}

// 9. a generic helper that starts the goroutine.
func goWith[T any](v T, f func(T)) { go f(v) } // HIT(SAFE002)

//gosree:mub b
func viaGeneric(b *int) {
	goWith(b, func(p *int) { *p = 1 }) // MISS(CAP003): goWith starts f(b) on a new goroutine
}

// 10. a local borrow (not a parameter) inside a struct sent on a channel.
func tupleSent(ch chan Tuple) {
	x := 0
	b := &x           //gosree:mut
	ch <- Tuple{p: b} // HIT(BORROW001)
	*b = 2            // HIT(SAFE003)
}
