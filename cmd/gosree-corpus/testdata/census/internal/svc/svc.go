// Package svc has one dependency type per census class, each the root of a hatch in the test table.
package svc

import (
	"bytes"
	"sort"
	"strings"
	"sync"
)

// Frozen holds only slots the frozen rule allows: a string, a func, and an interface field whose
// only store is a *Leaf.
type Frozen struct {
	Name  string
	Log   func(string)
	Inner Doer
}

type Doer interface{ Do() }

// Leaf is a candidate too.
type Leaf struct{ ID int }

func (*Leaf) Do() {}

func NewFrozen() *Frozen { return &Frozen{Name: "f", Inner: &Leaf{ID: 1}} }

// Published is written under its lock and read without it: publish under lock (W-lock).
type Published struct {
	mu     sync.Mutex
	client *Leaf
}

func (p *Published) Set(l *Leaf) {
	p.mu.Lock()
	p.client = l
	p.mu.Unlock()
}

func (p *Published) Use() int { return p.client.ID }

// Guardable is written and read only under its lock: a guarded_by row clears it (G).
type Guardable struct {
	mu sync.Mutex
	n  int
}

func (g *Guardable) Inc() {
	g.mu.Lock()
	g.n++
	g.mu.Unlock()
}

func (g *Guardable) Get() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.get()
}

// get runs under its caller's lock.
func (g *Guardable) get() int { return g.n }

// Setter is written after construction with no lock (W).
type Setter struct{ name string }

func (s *Setter) Rename(n string) { s.name = n }

// Table has an unguarded map written after construction (C-written).
type Table struct{ rows map[string]int }

func (t *Table) Put(k string) { t.rows[k]++ }

// Names fills its collections in its constructor and only reads them afterwards (C-frozen).
type Names struct {
	list []string
	idx  map[string]int
}

func NewNames() *Names { return &Names{list: []string{"a"}, idx: map[string]int{"a": 0}} }

func (n *Names) Has(s string) bool { _, ok := n.idx[s]; return ok && len(n.list) > 0 }

// Joined hands the slice to strings.Join, whose summary neither writes nor keeps it.
func (n *Names) Joined() string { return strings.Join(n.list, ",") }

func (n *Names) Each(f func(string)) {
	for _, s := range n.list {
		f(s)
	}
}

// Sorter hands its slice to a function whose summary says it writes it (C-written).
type Sorter struct{ keys []string }

func (s *Sorter) Sort() { sort.Strings(s.keys) }

// Queue's slice is appended to and read only under its lock: a guarded_by row clears it (G).
type Queue struct {
	mu    sync.Mutex
	items []int
}

func (q *Queue) Push(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, v)
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Store is a module interface; memStore is its only implementation in the module.
type Store interface{ Fetch() int }

type memStore struct{ n int }

func (m *memStore) Fetch() int { return m.n }

// Wired holds a module interface set from a parameter: the census follows the module's
// implementations instead of calling it external.
type Wired struct{ st Store }

func NewWired(s Store) *Wired { return &Wired{st: s} }

// External holds a type from outside the module (X).
type External struct{ buf *bytes.Buffer }

// Holder reaches Published through a pointer field, so it inherits W-lock.
type Holder struct{ dep *Published }

// Serve is the function the test table's hatches are scoped to.
func Serve(f *Frozen, p *Published, g *Guardable, s *Setter, t *Table, e *External, h *Holder, n *Names, so *Sorter, w *Wired, q *Queue) {
	go func() { _, _, _, _, _, _, _, _, _, _, _ = f, p, g, s, t, e, h, n, so, w, q }()
}
