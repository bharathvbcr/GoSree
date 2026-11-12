package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const advCensusSrc = `package gen

import "sync"

type Shape interface{ Area() int }

// A and B are one cycle through an interface slot and an embedded field; B has a write.
type A struct {
	Shape
	b *B
}
type B struct {
	a  *A
	mu sync.Mutex
	n  int
}

func (b *B) Bump() { b.n++ }

// C and D are a second cycle through a slice and a map, reached from A (so downstream of B's write).
type C struct{ ds []*D }
type D struct {
	cs map[string]*C
	e  *E
}

// E refers to itself and to the write-free C/D cycle only.
type E struct {
	self *E
	d    *D
}

type Box[T any] struct{ p *T }

type G struct{ box Box[A] }

type Sq struct{ c *C }

func (s *Sq) Area() int { return 1 }

type H struct{ bs []*B }
type I struct{ m map[string]*B }
type J struct{ B }
type K struct{ b Box[B] }
type L struct{ b [2]B }
type M struct{ b *Box[B] }

func Serve(h *H, i *I, j *J, k *K, l *L, m *M, pa *A, pb *B, pc *C, pd *D, pe *E, pg *G, ps *Sq) {}
`

func advCensus(t *testing.T, order []string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gen\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte(advCensusSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs := loadCensusModule(t, dir)
	rep, err := censusOf(pkgs, summaries(analyze(pkgs)), hatchRows("gen.go", "func Serve(", order), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, h := range rep.Hatches {
		out[h.Name] = fmt.Sprintf("%s %v", h.Worst, h.Closure)
	}
	return out
}

// Hatches over a table whose types reference each other through embedded fields, interface slots,
// slices, maps and generic wrappers: the class of each must not depend on row order, and a row listed
// twice or an empty table must not fail.
func TestAdvCensusMixedEdgesOrderIndependent(t *testing.T) {
	names := []string{"h", "i", "j", "k", "l", "m", "pa", "pb", "pc", "pd", "pe", "pg", "ps"}
	base := advCensus(t, names)
	for i := range 6 {
		order := slices.Clone(names)
		// deterministic scramble
		for j := range order {
			k := (j*7 + i*3) % len(order)
			order[j], order[k] = order[k], order[j]
		}
		got := advCensus(t, order)
		for n, want := range base {
			if got[n] != want {
				t.Fatalf("order %v: %s = %s, in the first order %s", order, n, got[n], want)
			}
		}
	}
	dup := advCensus(t, append(slices.Clone(names), names...))
	for n, want := range base {
		if dup[n] != want {
			t.Errorf("duplicate rows: %s = %s, want %s", n, dup[n], want)
		}
	}
	t.Logf("classes: %v", base)
	if got := advCensus(t, nil); len(got) != 0 {
		t.Errorf("empty table produced %v", got)
	}
}
