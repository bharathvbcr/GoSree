// WITNESS: race
//
// A local imm variable is captured by a goroutine's closure (capture by reference) and then
// reassigned. checkImmAddr lets a MakeClosure take the address, and reassigning a local imm
// variable is allowed, so the reader's loads race with the reassignment.
package main

import "sync"

// Config is a valid imm type.
//
//gosree:imm
type Config struct {
	port int
}

func main() {
	c := Config{port: 1}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := 0
		for i := 0; i < 1000; i++ {
			s += c.port
		}
		_ = s
	}()
	for i := 0; i < 1000; i++ {
		c = Config{port: i} // MISS(SAFE005): c is captured by the running goroutine; reassigning it writes imm memory it reads
	}
	wg.Wait()
}
