// WITNESS: race
//
// An imm *Config is shared with a reader goroutine. A method of Config returns &c.port; the writer
// stores through that *int. The FieldAddr escapes by Return, and the store is rooted at a call
// result, so neither function shows a write to imm memory.
package main

import "sync"

// Config is a valid imm type.
//
//gocap:imm
type Config struct {
	port int
}

func (c *Config) portAddr() *int { return &c.port }

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
	p := c.portAddr()
	for i := 0; i < 1000; i++ {
		*p = i // MISS(SAFE005): writes the shared imm value's port through the accessor's interior pointer
	}
	wg.Wait()
}
