// WITNESS: race
//
// A method with a pointer receiver called on a guarded struct-valued field: the call passes
// &s.buf, which is neither a load nor a write at the call site, and inside WriteString the write
// is through a receiver parameter. One goroutine writes the guarded buffer with no lock while
// another writes it under the lock.
package main

import (
	"bytes"
	"sync"
)

// Log is imm apart from its guarded field, so goroutines may share it.
//
//gosree:imm
type Log struct {
	mu  sync.Mutex
	buf bytes.Buffer //gosree:guarded_by mu
}

func (l *Log) locked(s string) {
	l.mu.Lock()
	l.buf.WriteString(s)
	l.mu.Unlock()
}

func (l *Log) unlocked(s string) {
	l.buf.WriteString(s) // MISS(SAFE009): writes the guarded buffer with no lock held
}

func main() {
	l := &Log{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			l.unlocked("a")
		}
	}()
	for range 200 {
		l.locked("b")
	}
	wg.Wait()
}
