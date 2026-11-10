// WITNESS: race
//
// An imm type may hold an error: kinds.go assumes error values are never mutated ("convention,
// not checked"). Here the error is a *counter that is written through a type assertion while a
// reader goroutine holds the shared imm value. The assumption is documented, so this is the
// runtime evidence that it is load-bearing, not a contradiction of the docs.
package main

import "sync"

type counter struct{ n int }

func (c *counter) Error() string { return "e" }

// Holder is accepted as imm because error is on the override table.
//
//gocap:imm
type Holder struct {
	err error
}

func main() {
	h := &Holder{err: &counter{}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := 0
		for i := 0; i < 1000; i++ {
			s += h.err.(*counter).n
		}
		_ = s
	}()
	for i := 0; i < 1000; i++ {
		h.err.(*counter).n++ // MISS(SAFE005): writes memory reachable from a shared imm value through its error field
	}
	wg.Wait()
}
