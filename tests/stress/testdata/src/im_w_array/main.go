// WITNESS: race
//
// A *Config pointing into an array of imm values (&a[0]) is shared with a reader goroutine, which
// an imm pointer may be without a move. The writer overwrites the whole array through its
// pointer: the store is through a *[2]Config, which is not recognised as an imm write.
package main

import "sync"

// Config is a valid imm type.
//
//gosree:imm
type Config struct {
	port int
}

func main() {
	a := new([2]Config)
	shared := &a[0]
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := 0
		for i := 0; i < 1000; i++ {
			s += shared.port
		}
		_ = s
	}()
	for i := 0; i < 1000; i++ {
		*a = [2]Config{} // MISS(SAFE005): rewrites a[0], the imm value the reader holds
	}
	wg.Wait()
}
