// WITNESS: race
//
// A value published through an atomic.Pointer is shared with every goroutine that loads it, but
// Store is an ordinary method call to the checker, not a transfer: the publisher keeps writing
// the value and nothing is reported.
package main

import (
	"sync"
	"sync/atomic"
)

type Config struct{ Limit int }

var current atomic.Pointer[Config]

func main() {
	var wg sync.WaitGroup
	wg.Add(1)
	started := make(chan struct{})
	go func() {
		defer wg.Done()
		<-started
		for i := 0; i < 1000; i++ {
			if c := current.Load(); c != nil {
				_ = c.Limit
			}
		}
	}()

	c := &Config{Limit: 1}
	current.Store(c)
	close(started)
	for i := 0; i < 1000; i++ {
		c.Limit = i // HIT(SAFE003): c was published by current.Store and is read by the other goroutine
	}
	wg.Wait()
}
