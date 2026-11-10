// WITNESS: panic
//
// fatal error: concurrent map writes, in a program gosree accepts. The session table is published
// through an atomic.Pointer, and both the publisher and a worker keep writing the map they share.
// Rust rejects it: a HashMap is not Sync, so sharing it needs Arc<Mutex<_>>. gosree treats
// Store as an ordinary call and Load as returning memory nobody else holds.
package main

import (
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

var sessions atomic.Pointer[map[string]int]

func touch(id int) {
	m := *sessions.Load()
	m[strconv.Itoa(id%64)] = id // MISS(NORULE): a write to a map other goroutines write; Load hands it out as if owned
}

func main() {
	m := map[string]int{}
	sessions.Store(&m)
	go func() {
		for i := 0; ; i++ {
			touch(i)
		}
	}()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		for i := 0; i < 1000; i++ {
			m[strconv.Itoa(i%64)] = -i // MISS(SAFE003): m was published by Store and is written by the worker
		}
	}
	// The runtime's map-write check is best effort: reaching the deadline means it never fired.
	os.Exit(0)
}
