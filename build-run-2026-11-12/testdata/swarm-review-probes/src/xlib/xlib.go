// Package xlib: a type whose getter looks like a read but lazily writes.
package xlib

import "sync"

type T struct {
	n     int
	cache *int
	mu    sync.Mutex
	hits  int
	Plain int
}

// Get reads n but lazily fills cache (a write).
func (t *T) Get() int {
	if t.cache == nil {
		v := t.n
		t.cache = &v
	}
	return *t.cache
}

// Size only reads.
func (t *T) Size() int { return t.n }

// Hit is self-locked.
func (t *T) Hit() { t.mu.Lock(); t.hits++; t.mu.Unlock() }
