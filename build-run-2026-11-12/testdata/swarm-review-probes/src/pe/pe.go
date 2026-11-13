// Package pe: per-element fan-out, the fixture shape and a racy twin that differs only in the index.
package pe

import "sync"

type Item struct{ ID int }

// P1: the fixture shape (race-free).
func P1(n int) {
	items := make([]Item, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i]
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
}

// P2: identical except the index is a loop-invariant value: every goroutine writes the same element.
func P2(n, k int) {
	items := make([]Item, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[k]
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
}
