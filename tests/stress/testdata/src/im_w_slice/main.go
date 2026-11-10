// WITNESS: race
//
// An imm value with an array field is shared with a reader goroutine. The writer slices the
// array field (c.arr[:]) and writes through the slice: the store walk stops at the ssa.Slice.
package main

import "sync"

// Config is a valid imm type: a fixed-size array of ints is pointer-free.
//
//gocap:imm
type Config struct {
	arr [4]int
}

func main() {
	c := &Config{arr: [4]int{1, 2, 3, 4}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := 0
		for i := 0; i < 1000; i++ {
			s += c.arr[0]
		}
		_ = s
	}()
	view := c.arr[:]
	for i := 0; i < 1000; i++ {
		view[0] = i // HIT(SAFE005): writes c.arr[0] of the shared imm value through a slice of the array field
	}
	wg.Wait()
}
