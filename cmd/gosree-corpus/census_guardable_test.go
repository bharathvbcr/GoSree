package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A field the census classes G (written after construction, every access under a lock) is the field
// a //gosree:guarded_by row would cover, and the imm rule (passes/ownership/imm.go) takes a guarded
// field at any type. The census already skips what a guarded field holds; a G field must be treated
// the same, or a lock-protected store of per-request values carries those values' writes into every
// type that holds the store. A field that is only half locked, or not locked, is still walked.
const firewallSrc = `package gen

import "sync"

// B is written after construction, so every hatch that reaches it is W.
type B struct{ n int }

func (b *B) Bump() { b.n++ }

// Store's map is written and read only under its lock: G, as with a guarded_by row.
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

// Guarded is Store with the guarded_by row written out.
type Guarded struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	m map[string]*B
}

// Slot is a pointer field assigned and read only under its lock: G.
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

// Pub's map is written under its lock but read without one: C-written, and still walked to B.
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

// Loose's map is written with no lock at all: C-written, and still walked to B.
type Loose struct{ m map[string]*B }

func (l *Loose) Put(k string, b *B) { l.m[k] = b }

// Fixed's pointer is set in the constructor only, so it is not G: B is still reached.
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

// Self-synchronised dependencies held by pointer: the holder takes the dependency's class.
type HStore struct{ s *Store }
type HGuarded struct{ g *Guarded }
type HSlot struct{ s *Slot }
type HPub struct{ p *Pub }
type HLoose struct{ l *Loose }
type HFixed struct{ f *Fixed }

func Serve(a *HStore, b *HGuarded, c *HSlot, d *HPub, e *HLoose, f *HFixed) {
}
`

var firewallNames = []string{"a", "b", "c", "d", "e", "f"}

func firewallCensus(t *testing.T, order []string) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.com/gen\n\ngo 1.27\n", "gen.go": firewallSrc} {
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

func TestCensusDoesNotWalkPastAGuardableField(t *testing.T) {
	got := firewallCensus(t, firewallNames)
	want := map[string][]string{
		"a": {classG},                // *Store: locked map of *B, as if guarded_by
		"b": {classOK},               // *Guarded: the guarded_by row itself
		"c": {classG},                // *Slot: locked pointer field
		"d": {classCWritten, classW}, // *Pub: read without the lock, so B still counts
		"e": {classCWritten, classW}, // *Loose: unguarded, so B still counts
		"f": {classW},                // *Fixed: never written after construction, so not G
	}
	for n, w := range want {
		if !slices.Equal(got[n], w) {
			t.Errorf("hatch %s: closure %v, want %v", n, got[n], w)
		}
	}
}

func TestCensusGuardableOrderIndependent(t *testing.T) {
	base := firewallCensus(t, firewallNames)
	order := slices.Clone(firewallNames)
	slices.Reverse(order)
	got := firewallCensus(t, order)
	for n, w := range base {
		if !slices.Equal(got[n], w) {
			t.Errorf("%s: %v reversed, %v first", n, got[n], w)
		}
	}
}
