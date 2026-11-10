// Package rs_init: Rust has no implicit zero value. A struct is built by a literal naming every
// field or by a constructor, and a struct with private fields can be built only inside its module.
// Go makes the zero value of every type, so a type that needs a constructor can be used without
// one. gocap has no rule.
package rs_init

import "sync"

// 1. a zero-value struct that needs a constructor.
type Cache struct {
	mu    sync.Mutex
	items map[string][]byte
	limit int
}

func NewCache(limit int) *Cache {
	return &Cache{items: make(map[string][]byte), limit: limit}
}

func (c *Cache) Put(k string, v []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.limit {
		clear(c.items)
	}
	c.items[k] = v // MISS(NORULE): a Cache{} (or var c Cache) has a nil map: this write panics
}

func Use() {
	var c Cache // MISS(NORULE): the zero value bypasses NewCache; Rust would need every field named
	c.Put("k", nil)
}

// 2. an uninitialised map field written through a struct literal that names other fields.
type Index struct {
	name string
	byID map[int]string
}

func Build(ids []int) *Index {
	ix := &Index{name: "ids"}
	for _, id := range ids {
		ix.byID[id] = "" // MISS(NORULE): byID was never made; Rust's literal must name byID
	}
	return ix
}
