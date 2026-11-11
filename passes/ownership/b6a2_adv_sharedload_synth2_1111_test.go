package ownership_test

import (
	"strings"
	"testing"
)

// More synthetic shapes for the shared-load write check: the write sits in a function that
// st.followable accepts but sourceFuncs does not list (closures inside generic instances,
// range-over-func yield bodies inside generics, promoted-method wrappers, interface method values).
func TestAdvSharedLoadWriteInSyntheticFunctions2(t *testing.T) {
	cases := map[string]string{
		"PRE-EXISTING miss: closure inside generic function": `package p

import "sync/atomic"

type Cfg struct{ X any }

var live atomic.Pointer[Cfg]

func each[T any](c *Cfg, v T) {
	func() { c.X = v }()
}

func use() {
	c := live.Load()
	each(c, 1)
}
`,
		"PRE-EXISTING miss: deferred closure inside generic function": `package p

import "sync/atomic"

type Cfg struct{ X any }

var live atomic.Pointer[Cfg]

func each[T any](c *Cfg, v T) {
	defer func() { c.X = v }()
}

func use() {
	c := live.Load()
	each(c, "s")
}
`,
		"PRE-EXISTING miss: range-over-func yield inside generic": `package p

import (
	"iter"
	"sync/atomic"
)

type Cfg struct{ X any }

var live atomic.Pointer[Cfg]

func fill[T any](c *Cfg, seq iter.Seq[T]) {
	for v := range seq {
		c.X = v
	}
}

func use(s iter.Seq[int]) {
	c := live.Load()
	fill(c, s)
}
`,
		"promoted method wrapper": `package p

import "sync/atomic"

type Cfg struct{ X int }

func (c *Cfg) Mutate() { c.X++ }

type W struct{ *Cfg }

var live atomic.Pointer[W]

func use() {
	w := live.Load()
	w.Mutate()
}
`,
		"PRE-EXISTING miss: method value of generic type closure": `package p

import "sync/atomic"

type Cfg[T any] struct{ X T }

func (c *Cfg[T]) Set(v T) { func() { c.X = v }() }

var live atomic.Pointer[Cfg[int]]

func use() {
	c := live.Load()
	f := c.Set
	f(2)
}
`,
		"generic calling generic": `package p

import "sync/atomic"

type Cfg struct{ X any }

var live atomic.Pointer[Cfg]

func inner[T any](c *Cfg, v T) { c.X = v }
func outer[T any](c *Cfg, v T) { inner(c, v) }

func use() {
	c := live.Load()
	outer(c, 1)
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got := advDiagLines(t, src, "published atomic values are read-only")
			t.Logf("%s: %v", name, got)
			// The PRE-EXISTING rows are misses on 9161d81 and on the fix alike: a write inside a
			// closure of a generic function or generic method is never reached (cause not investigated;
			// the closure lives in an instance that no checkSharedLoadWrites path visits). They are
			// pinned so a change in either direction is noticed. The
			// other rows are HOLDS: reported on both.
			wantMiss := strings.HasPrefix(name, "PRE-EXISTING miss")
			if wantMiss && len(got) != 0 {
				t.Fatalf("%s: the pre-existing miss is gone, update this test: %v", name, got)
			}
			if !wantMiss && len(got) == 0 {
				t.Fatalf("%s: a write through a shared Load result was not reported", name)
			}
		})
	}
}
