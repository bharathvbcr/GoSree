// Package rs_alias: Rust's core rule is aliasing XOR mutation, enforced on every reference with no
// annotation. gosree's borrow rules (BORROW002/003) apply only to borrows the programmer marks
// (//gosree:mub, //gosree:rob, cap.Mut, cap.Ref), and its README says "Inside one goroutine,
// aliasing is left alone." So every function here is NORULE for plain Go: each one is code Rust's
// borrow checker rejects.
package rs_alias

import "sync"

// 1. iterator invalidation: a pointer into a slice kept across an append that reallocates. The
// write through p lands in the old backing array and is lost (rs_w_stale_ptr runs it).
type Item struct{ N int }

func StalePointer() int {
	items := make([]Item, 1, 1)
	p := &items[0]
	items = append(items, Item{}) // MISS(NORULE): reallocates while p borrows items[0]; Rust: cannot borrow mutably while borrowed
	p.N = 42                      // MISS(NORULE): writes the old array; items[0].N stays 0
	return items[0].N
}

// 2. map mutation during range: Go allows it with unspecified results.
func Prune(m map[string]int) {
	for k, v := range m {
		if v == 0 {
			delete(m, k)
		}
		m[k+"_seen"] = v // MISS(NORULE): an insert during range may or may not be visited; Rust forbids mutating while iterating
	}
}

// 3. append through a sub-slice overwrites the parent's next element.
func Overwrite() []int {
	s := []int{1, 2, 3}
	a := s[:2]
	b := append(a, 99) // MISS(NORULE): fits in cap, so s[2] becomes 99; Rust: a borrows s, s cannot be written through it
	_ = b
	return s
}

// 4. a callback that modifies the collection being iterated.
type Bus struct{ subs []func() }

func (b *Bus) Subscribe(f func()) { b.subs = append(b.subs, f) }

func (b *Bus) Publish() {
	for _, f := range b.subs {
		f() // MISS(NORULE): f may call Subscribe and change b.subs mid-iteration; Rust: b.subs is borrowed
	}
}

// 5. a self-referential struct broken by a copy.
type Buffer struct {
	data [64]byte
	cur  []byte // points into data
}

func NewBuffer() Buffer {
	var b Buffer
	b.cur = b.data[:0]
	return b // MISS(NORULE): the returned copy's cur still points into the local's data; Rust forbids moving a borrowed value
}

// 6. a value receiver mutates a copy: the write is lost.
type Counter struct{ n int }

func (c Counter) Inc() { c.n++ } // MISS(NORULE): mutates the receiver copy; Rust needs &mut self to write

// 7. range variable copy: the writes are lost.
func ResetAll(items []Item) {
	for _, it := range items {
		it.N = 0 // MISS(NORULE): writes the loop copy, not items[i]
	}
}

// 8. a mutex and a WaitGroup copied by value. go vet's copylocks reports both; Rust's Mutex is
// not Copy and moving it out of a shared struct is a compile error.
type Guarded struct {
	mu sync.Mutex
	n  int
}

func (g Guarded) Get() int { // MISS(NORULE): value receiver copies g.mu; vet copylocks reports it
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n
}

func Wait(wg sync.WaitGroup) { // MISS(NORULE): the WaitGroup is copied; Wait waits on the copy; vet copylocks reports it
	wg.Wait()
}
