// Package rs_send: Rust's Send and Sync traits decide statically which types may cross or be shared
// between threads; Rc, RefCell and a &mut to a non-Sync buffer cannot. gosree's analogue is
// ownership: a non-shareable value that crosses a goroutine boundary must be owned and is moved
// (SAFE002/SAFE003). This file records what is caught and one accepted route per type.
package rs_send

import (
	"bytes"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
)

// 1. *bytes.Buffer captured by a goroutine and used by both: caught.
func BufferCaptured() string {
	var buf bytes.Buffer
	go func() { buf.WriteString("child") }()
	buf.WriteString("parent") // HIT(SAFE003)
	return buf.String()
}

// 2. *rand.Rand in a package variable assigned only at initialisation, used from goroutines. The
// README scoped it out ("SAFE006 sees direct writes only ... A write through a method of another
// package ... does not" count); since 2026-11-10 a method that may write its receiver counts.
var rng = rand.New(rand.NewSource(1)) // HIT(SAFE006)

func Roll() int {
	go func() { _ = rng.Intn(6) }()
	return rng.Intn(6) // *rand.Rand is not safe for concurrent use; reported at the declaration
}

// 3. a map shared through an atomic.Pointer: Store publishes it, Load hands it back as if fresh.
var routes atomic.Pointer[map[string]int]

func Publish() {
	m := map[string]int{}
	routes.Store(&m)
	go func() {
		r := *routes.Load()
		r["child"] = 1
	}()
	m["parent"] = 2 // MISS(SAFE003): m was published by Store and the goroutine writes it through Load: concurrent map writes
}

// 4. *strings.Builder shared through a sync.Pool: Put then keep using it.
var builders = sync.Pool{New: func() any { return new(strings.Builder) }}

func Render(name string) string {
	b := builders.Get().(*strings.Builder)
	b.Reset()
	builders.Put(b)
	b.WriteString(name) // MISS(SAFE003): b was handed back to the pool, whose next Get may be on another goroutine
	return b.String()
}
