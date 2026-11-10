// WITNESS: race
//
// A package variable of a user struct type is written only through its pointer-receiver method.
// The store inside the method is through the receiver, not rooted at the global, so SAFE006
// never sees a write.
package main

import "sync"

type stats struct{ n int }

func (s *stats) bump() { s.n++ }

var global stats // HIT(SAFE006) global.bump() writes global.n from two goroutines; missed until 2026-11-10

func main() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			global.bump()
		}
	}()
	for i := 0; i < 1000; i++ {
		global.bump()
	}
	wg.Wait()
}
