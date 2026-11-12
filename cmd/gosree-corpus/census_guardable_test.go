package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A field the census classes G (written after construction, every access in a function that takes a
// lock) is still walked: G does not mean that what is read from the field stays under the lock. Store
// below hands out the *B it keeps from a locked getter, and two goroutines calling Get("k").Bump() race
// on B.n (go test -race reports it, and gosree reports SAFE009 once the guarded_by row is written).
// Cutting the walk at G fields hid exactly that write, so the hatches over Store and Slot must keep
// B's W.
const guardableSrc = `package gen

import "sync"

// B is written after construction, so every hatch that reaches it is W.
type B struct{ n int }

func (b *B) Bump() { b.n++ }

// Store's map is written and read only under its lock (G), and Get hands out what it holds.
type Store struct {
	mu sync.Mutex
	m  map[string]*B
}

func (s *Store) Put(k string, b *B) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = b
}

func (s *Store) Get(k string) *B {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k]
}

// Use is the race: two goroutines Bump the same *B obtained from the store.
func Use(s *Store) {
	done := make(chan bool)
	go func() { s.Get("k").Bump(); done <- true }()
	s.Get("k").Bump()
	<-done
}

// Slot is a pointer field assigned and read only under its lock (G), and Cur hands it out.
type Slot struct {
	mu  sync.Mutex
	cur *B
}

func (s *Slot) Swap(b *B) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur = b
}

func (s *Slot) Cur() *B {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Pub's map is written under its lock but read without one: C-written, and walked to B.
type Pub struct {
	mu sync.Mutex
	m  map[string]*B
}

func (p *Pub) Put(k string, b *B) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[k] = b
}

func (p *Pub) Peek(k string) *B { return p.m[k] }

// Fixed's pointer is set in the constructor only, so it is not G, and B is reached.
type Fixed struct {
	mu  sync.Mutex
	cur *B
}

func NewFixed() *Fixed { return &Fixed{cur: &B{}} }

func (f *Fixed) Cur() *B {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

type HStore struct{ s *Store }
type HSlot struct{ s *Slot }
type HPub struct{ p *Pub }
type HFixed struct{ f *Fixed }

func Serve(a *HStore, c *HSlot, d *HPub, f *HFixed) {
}
`

var guardableNames = []string{"a", "c", "d", "f"}

func guardableCensus(t *testing.T, order []string) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.com/gen\n\ngo 1.27\n", "gen.go": guardableSrc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkgs := loadCensusModule(t, dir)
	rep, err := censusOf(pkgs, summaries(analyze(pkgs)), hatchRows("gen.go", "func Serve(", order), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, h := range rep.Hatches {
		cl := slices.Clone(h.Closure)
		slices.Sort(cl)
		out[h.Name] = cl
	}
	return out
}

func TestCensusWalksPastAGuardableField(t *testing.T) {
	got := guardableCensus(t, guardableNames)
	want := map[string][]string{
		"a": {classG, classW},        // *Store: G, and the *B its getter hands out is written
		"c": {classG, classW},        // *Slot: the same through a pointer field
		"d": {classCWritten, classW}, // *Pub: read without the lock
		"f": {classW},                // *Fixed: never written after construction, so not G
	}
	for n, w := range want {
		if !slices.Equal(got[n], w) {
			t.Errorf("hatch %s: closure %v, want %v", n, got[n], w)
		}
	}
}
