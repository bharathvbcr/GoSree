// Package lk_fp is correct, idiomatic locking that SAFE009 should accept.
package lk_fp

import (
	"maps"
	"slices"
	"sort"
	"sync"
)

type S struct {
	mu    sync.Mutex
	n     int            //gosree:guarded_by mu
	m     map[string]int //gosree:guarded_by mu
	data  []int          //gosree:guarded_by mu
	ready bool           //gosree:guarded_by mu
	cond  *sync.Cond
}

// A constructor that delegates filling the object to a helper before publishing it: nothing else
// can see s yet, but the exemption only covers allocations made in the accessing function itself.
func New() *S {
	s := &S{}
	s.cond = sync.NewCond(&s.mu)
	s.reset()
	return s
}

func (s *S) reset() {
	s.m = map[string]int{} // FP(SAFE009): reset only runs from New, before s is published
	s.n = 0                // FP(SAFE009): same
}

// Control: the constructor writing directly is exempt.
func NewDirect() *S {
	s := &S{}
	s.m = map[string]int{}
	return s
}

// A deferred closure registered after the deferred Unlock runs first, still under the lock.
func (s *S) deferredUnderLock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.n-- }() // FP(SAFE009): runs before the deferred Unlock; entryLocks only inherits for *ssa.Call, not *ssa.Defer
	s.n++
}

// sort.Slice runs its less function synchronously, under the caller's lock.
func (s *S) sortUnderLock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	sort.Slice(s.data, func(i, j int) bool { return s.data[i] < s.data[j] }) // FP(SAFE009): sort.Slice calls less synchronously under the lock and does not retain the slice
}

// The textbook sync.Cond wait loop: Cond.L is &s.mu.
func (s *S) waitReady() {
	s.cond.L.Lock()
	for !s.ready { // FP(SAFE009): s.cond.L is &s.mu, locked on the line above
		s.cond.Wait()
	}
	s.cond.L.Unlock()
}

// Control: the same loop locking s.mu directly is accepted.
func (s *S) waitReadyDirect() {
	s.mu.Lock()
	for !s.ready {
		s.cond.Wait()
	}
	s.mu.Unlock()
}

// Copy under the lock, use the copy after: slices.Clone and maps.Clone return fresh memory.
func (s *S) snapshot() ([]int, map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.data), maps.Clone(s.m) // FP(SAFE009): maps.Clone returns a fresh map and keeps nothing (slices.Clone is accepted)
}

// Control: a manual copy.
func (s *S) snapshotManual() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, len(s.data))
	copy(out, s.data)
	return out
}

// A sharded map: each shard is locked and used through the same index expression.
type shard struct {
	mu sync.Mutex
	m  map[string]int //gosree:guarded_by mu
}

type Sharded struct {
	shards [8]shard
}

func (c *Sharded) put(k string, v int) {
	i := len(k) % len(c.shards)
	c.shards[i].mu.Lock()
	c.shards[i].m[k] = v // FP(SAFE009): the same shard; pathOf has no IndexAddr step, so each index expression is its own root
	c.shards[i].mu.Unlock()
}

// Control: through a pointer to the shard.
func (c *Sharded) putPtr(k string, v int) {
	sh := &c.shards[len(k)%len(c.shards)]
	sh.mu.Lock()
	sh.m[k] = v
	sh.mu.Unlock()
}

// Control: a generic box.
type Box[T any] struct {
	mu sync.Mutex
	v  T //gosree:guarded_by mu
}

func (b *Box[T]) Set(v T) {
	b.mu.Lock()
	b.v = v
	b.mu.Unlock()
}

func (b *Box[T]) setUnlocked(v T) {
	b.v = v // HIT(SAFE009)
}
