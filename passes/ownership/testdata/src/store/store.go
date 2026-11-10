// Package store is called by the handlers test package: its summaries say which parameters each
// function may write.
package store

import (
	"store/inner"
	"sync"
)

type Store struct {
	mu   sync.Mutex
	data map[string]int
	n    int
}

// clone returns a shallow copy: it shares s's pointees, not s.
func (s *Store) clone() *Store {
	cp := *s
	return &cp
}

// With writes only the copy. Its result is linked to s one pointer down (the shared map), so the
// write to cp.n is not a write of s.
func (s *Store) With(n int) *Store { // want With:"ownership summary"
	cp := s.clone()
	cp.n = n
	return cp
}

// Put writes the receiver's map, under a mutex nothing declares guards it.
func (s *Store) Put(k string, v int) { // want Put:"ownership summary"
	s.mu.Lock()
	if s.data == nil {
		s.data = map[string]int{}
	}
	s.data[k] = v
	s.mu.Unlock()
}

// Get only reads.
func (s *Store) Get(k string) int { // want Get:"ownership summary"
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[k]
}

// Touch only locks and unlocks: a mutex is shareable, so calls on it write nothing gocap tracks.
func (s *Store) Touch() { // want Touch:"ownership summary"
	s.mu.Lock()
	s.mu.Unlock()
}

// Remember, Forget and Reset write a map they are given.
func Remember(m map[string]int, k string) { m[k]++ } // want Remember:"ownership summary"

func Forget(m map[string]int, k string) { delete(m, k) } // want Forget:"ownership summary"

func Reset(m map[string]int) { clear(m) } // want Reset:"ownership summary"

// Grow appends to the slice it is given, which writes its backing array when it has room.
func Grow(s []int, v int) []int { return append(s, v) } // want Grow:"ownership summary"

// Fill copies into the slice it is given.
func Fill(s []int, v int) { copy(s, []int{v}) } // want Fill:"ownership summary"

// Size only reads.
func Size(s []int) int { return len(s) } // want Size:"ownership summary"

// Forward writes through another package: summaries carry writes across packages as facts.
func Forward(s *Store) { inner.Set(&s.data) } // want Forward:"ownership summary"

// Counter's map is guarded: SAFE009 checks its accesses here, so Hit is not a writer of c.
type Counter struct {
	mu   sync.Mutex
	hits map[string]int //safe:guarded_by mu
}

func NewCounter() *Counter { return &Counter{hits: map[string]int{}} } // want NewCounter:"ownership summary"

func (c *Counter) Hit(k string) { // want Hit:"ownership summary"
	c.mu.Lock()
	c.hits[k]++
	c.mu.Unlock()
}
