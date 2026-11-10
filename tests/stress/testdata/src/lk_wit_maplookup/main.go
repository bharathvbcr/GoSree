// WITNESS: race
//
// A guarded map copied out under the lock and read after Unlock. The map header copy is a register,
// and a map lookup (ssa.Lookup) is neither a load nor a write, so the read is never checked.
package main

import (
	"strconv"
	"sync"
)

// Store is imm apart from its guarded field, so goroutines may share it.
//
//gosree:imm
type Store struct {
	mu sync.Mutex
	m  map[string]int //gosree:guarded_by mu
}

func (s *Store) get(k string) int {
	s.mu.Lock()
	m := s.m
	s.mu.Unlock()
	return m[k] // MISS(SAFE009): reads the guarded map after Unlock
}

func (s *Store) put(k string, v int) {
	s.mu.Lock()
	s.m[k] = v
	s.mu.Unlock()
}

func main() {
	s := &Store{m: map[string]int{}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			s.put(strconv.Itoa(i), i)
		}
	}()
	t := 0
	for i := range 200 {
		t += s.get(strconv.Itoa(i))
	}
	wg.Wait()
	_ = t
}
