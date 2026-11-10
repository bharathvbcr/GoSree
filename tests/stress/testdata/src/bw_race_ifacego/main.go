// WITNESS: race
//
// A \mut borrow captured by a closure handed to sync.WaitGroup.Go through an interface. CAP003 is
// reported for the direct `wg.Go(...)` call (bw_cap003 case 7), but spawnOf returns early for an
// interface method call (cc.IsInvoke() in passes/ownership/spawn.go), so the same spawner reached
// by dynamic dispatch is an ordinary call. The goroutine and the owner frame then both write x
// through the "exclusive" borrow.
package main

import (
	"fmt"
	"sync"
)

type goer interface{ Go(func()) }

func main() {
	x := 0
	b := &x //gosree:mut
	var wg sync.WaitGroup
	var g goer = &wg
	g.Go(func() { *b = 1 }) // MISS(CAP003): the closure capturing the \mut borrow b runs on a new goroutine (WaitGroup.Go via interface dispatch)
	*b = 2
	wg.Wait()
	fmt.Println(x)
}
