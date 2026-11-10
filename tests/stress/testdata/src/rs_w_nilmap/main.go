// WITNESS: panic
//
// A zero value that needed a constructor: Cache's map is made only by NewCache, and a Cache
// declared with var panics on its first Put. Rust has no implicit zero value; a struct is built by
// naming every field or calling a constructor. gocap has no rule.
package main

type Cache struct{ items map[string]int }

func NewCache() *Cache { return &Cache{items: map[string]int{}} }

func (c *Cache) Put(k string, v int) {
	c.items[k] = v // MISS(NORULE): assignment to entry in nil map for a zero Cache
}

func main() {
	var c Cache
	c.Put("k", 1)
	_ = NewCache()
}
