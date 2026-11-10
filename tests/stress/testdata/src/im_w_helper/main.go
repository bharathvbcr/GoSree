// WITNESS: race
//
// An imm *Config is shared with a reader goroutine. The writer passes &c.port to a helper that
// stores through a plain *int. Missed by the tree of 2026-11-10 morning; caught since the
// concurrent checkImmFieldCall change. Kept as a regression lock with its runtime conviction.
package main

import "sync"

// Config is a valid imm type.
//
//gosree:imm
type Config struct {
	port int
}

func setInt(p *int, v int) { *p = v }

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
	for i := 0; i < 1000; i++ {
		setInt(&c.port, i) // HIT(SAFE005)
	}
	wg.Wait()
}
