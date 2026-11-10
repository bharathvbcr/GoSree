// Package rs_conc: concurrency bugs beyond data races. Safe Rust does NOT prevent most of these
// either: deadlocks (lock order, re-locking a std Mutex, which deadlocks or panics) are possible
// in safe Rust. What Rust does prevent: unlocking a mutex you do not hold (the guard is the only
// way to unlock), a negative counter on a join handle (there is none), and mixing atomic and plain
// access to one variable (an AtomicI64 has no plain access). gocap's SAFE009 checks access to
// //safe:guarded_by memory; the question here is whether its lockset notices the lock misuse.
package rs_conc

import (
	"sync"
	"sync/atomic"
)

// 1. lock-order deadlock: Transfer(a, b) and Transfer(b, a) on two goroutines.
type Acct struct {
	mu  sync.Mutex
	bal int //safe:guarded_by mu
}

func Transfer(from, to *Acct, n int) {
	from.mu.Lock()
	to.mu.Lock() // MISS(NORULE): the order depends on the arguments; Rust deadlocks too (a draw)
	from.bal -= n
	to.bal += n
	to.mu.Unlock()
	from.mu.Unlock()
}

// 2. double Lock on a non-reentrant mutex: the lockset knows mu is held at the second Lock.
func (a *Acct) Double() {
	a.mu.Lock()
	a.mu.Lock() // MISS(NORULE): self-deadlock; SAFE009's must-lockset holds a.mu here and says nothing; Rust deadlocks or panics (a draw)
	a.bal++
	a.mu.Unlock()
	a.mu.Unlock()
}

// 3. unlock of an unlocked mutex: a fatal error at run time.
func (a *Acct) Release() {
	a.mu.Unlock() // MISS(NORULE): fatal error: sync: unlock of unlocked mutex; Rust has no unlock without a guard
}

// 4. WaitGroup negative counter.
func Spawn(n int) {
	var wg sync.WaitGroup
	for range n {
		go func() {
			defer wg.Done() // MISS(NORULE): Add was never called: panic: sync: negative WaitGroup counter
		}()
	}
	wg.Wait()
}

// 5. mixed atomic and plain access to one variable.
type Stats struct{ hits int64 }

func (s *Stats) Hit()        { atomic.AddInt64(&s.hits, 1) }
func (s *Stats) Read() int64 { return s.hits } // MISS(NORULE): a plain read of an atomically written field; Rust's AtomicI64 has no plain read

// Through a parameter the sharing itself is caught.
func Count(s *Stats) int64 {
	go s.Hit()      // HIT(SAFE002)
	return s.Read() // HIT(SAFE003)
}

// Through a package variable: passing &requests as a go argument is caught...
var requests int64

func Serve() int64 {
	go atomic.AddInt64(&requests, 1) // HIT(SAFE002) HIT(SAFE004)
	return requests
}

// ...but the same add inside a closure is not: an atomic add is "synchronised, not a write" to
// SAFE006, and a plain read is not a write at all (rs_w_mixedatomic runs it).
var served int64

func ServeClosure() int64 {
	go func() { atomic.AddInt64(&served, 1) }()
	return served // MISS(NORULE): a plain read racing an atomic add; the race detector reports it
}
