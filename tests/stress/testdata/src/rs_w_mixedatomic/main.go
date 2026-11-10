// WITNESS: race
//
// Mixed atomic and plain access to one variable: the workers count with atomic.AddInt64, the
// reporter reads the counter plainly. Rust's AtomicI64 has no plain access, so this cannot be
// written. gosree's SAFE006 treats atomic functions as synchronised rather than as writes, and a
// plain read is not a write, so the package variable is never reported.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

var requests int64

func main() {
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 1000 {
				atomic.AddInt64(&requests, 1)
			}
		})
	}
	fmt.Println("so far:", requests) // MISS(NORULE): a plain read racing the atomic adds
	wg.Wait()
}
