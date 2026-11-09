// Package guarded tests SAFE009: memory annotated //safe:guarded_by is read or written only while its
// mutex is held, and nothing read from it leaves the lock.
package guarded

import (
	"fmt"
	"sync"
)

type Cache struct {
	mu    sync.Mutex
	hits  map[string]int //safe:guarded_by mu
	n     int            //safe:guarded_by mu
	rw    sync.RWMutex
	data  []int //safe:guarded_by rw
	other sync.Mutex
	free  int
}

// --- Accepted ---

// A constructor: no other goroutine can see c before it is returned.
func newCache() *Cache {
	c := &Cache{hits: map[string]int{}}
	c.n = 1
	return c
}

func (c *Cache) inc(k string) {
	c.mu.Lock()
	c.hits[k]++
	c.n++
	c.free++
	c.mu.Unlock()
}

// A deferred Unlock keeps the lock held to the end; an int read out is a copy.
func (c *Cache) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits[k]
}

// RLock is enough to read.
func (c *Cache) len() int {
	c.rw.RLock()
	defer c.rw.RUnlock()
	return len(c.data)
}

func (c *Cache) try() {
	if c.mu.TryLock() {
		c.n++
		c.mu.Unlock()
	}
}

func (c *Cache) tryOrReturn() {
	if !c.mu.TryLock() {
		return
	}
	c.n++
	c.mu.Unlock()
}

// A helper whose callers hold the lock.
//
//safe:holds mu
func (c *Cache) evictLocked() { clear(c.hits) }

func (c *Cache) evict() {
	c.mu.Lock()
	c.evictLocked()
	c.mu.Unlock()
}

// A closure called in place runs under the caller's lock.
func (c *Cache) inPlace() {
	c.mu.Lock()
	func() { c.n++ }()
	c.mu.Unlock()
}

type Embedded struct {
	sync.Mutex
	n int //safe:guarded_by Mutex
}

func (e *Embedded) inc() {
	e.Lock()
	e.n++
	e.Unlock()
}

var (
	gmu   sync.Mutex
	total int //safe:guarded_by gmu
)

// Initialisation runs before any other goroutine.
func init() { total = 1 }

func addTotal() {
	gmu.Lock()
	total++
	gmu.Unlock()
}

// --- SAFE009 ---

func (c *Cache) unlocked() {
	c.n++ // want `SAFE009: field n is guarded by mu but is written here without holding it`
}

func (c *Cache) writeUnderRLock() {
	c.rw.RLock()
	c.data[0] = 1 // want `SAFE009: field data is guarded by rw but is written here without holding it`
	c.rw.RUnlock()
}

func (c *Cache) readAfterUnlock() int {
	c.mu.Lock()
	c.mu.Unlock()
	return c.n // want `SAFE009: field n is guarded by mu but is read here without holding it`
}

func (c *Cache) wrongMutex() {
	c.other.Lock()
	c.n++ // want `SAFE009: field n is guarded by mu but is written here without holding it`
	c.other.Unlock()
}

func (c *Cache) evictUnlocked() {
	c.evictLocked() // want `SAFE009: evictLocked requires mu to be held \(//safe:holds mu\), but it is not held here`
}

// A function of another package that receives c may unlock c.mu.
func (c *Cache) passedAway() {
	c.mu.Lock()
	fmt.Println(c)
	c.n++ // want `SAFE009: field n is guarded by mu but is written here without holding it`
	c.mu.Unlock()
}

func (c *Cache) leak() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits // want `SAFE009: field hits is guarded by mu, but a value read from it leaves its lock here: returned`
}

// With a defer every return goes through one result slot; only the return that reads guarded memory
// leaks it (the shape of a lazily built cache getter).
func (c *Cache) cacheOrNil() map[string]int {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hits == nil {
		c.hits = map[string]int{}
	}
	return c.hits // want `SAFE009: field hits is guarded by mu, but a value read from it leaves its lock here: returned`
}

func (c *Cache) store(dst *map[string]int) {
	c.mu.Lock()
	*dst = c.hits // want `SAFE009: field hits is guarded by mu, but a value read from it leaves its lock here: stored outside the guarded object`
	c.mu.Unlock()
}

// A closure that is not called in place starts with no lock held.
func (c *Cache) later() func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	return func() {
		c.n++ // want `SAFE009: field n is guarded by mu but is written here without holding it`
	}
}

func addTotalUnlocked() {
	total++ // want `SAFE009: package variable total is guarded by gmu but is written here without holding it`
}

// --- SAFE007 ---

//safe:holds mu
func (c *Cache) badLocked() {
	c.mu.Unlock() // want `SAFE007: badLocked is //safe:holds mu but unlocks it here`
	c.mu.Lock()
}
