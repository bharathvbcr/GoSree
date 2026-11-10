// WITNESS: race
//
// An imm *Config is shared with a reader goroutine (no move, by design). The writer converts the
// pointer to *Mutable, a defined type with the same underlying struct and no annotation, and
// writes through it: a plain Go conversion, no unsafe, and gocap sees a write to a Mutable.
package main

import "sync"

// Config is a valid imm type.
//
//gocap:imm
type Config struct {
	port int
}

// Mutable has Config's underlying type and no annotation.
type Mutable Config

func main() {
	c := &Config{port: 1}
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
	m := (*Mutable)(c)
	for i := 0; i < 1000; i++ {
		m.port = i // MISS(SAFE005): writes the shared imm *Config through a *Mutable conversion
	}
	wg.Wait()
}
