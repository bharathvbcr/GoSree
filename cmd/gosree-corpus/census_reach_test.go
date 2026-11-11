package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const reachSrc = `package gen

// B is written after construction, so every hatch that reaches it is W.
type B struct{ n int }

func (b *B) Bump() { b.n++ }

// Quiet has no writes anywhere.
type Quiet struct{ n int }

type Box[T any] struct{ p *T }
type Pair[K comparable, V any] struct {
	k K
	v V
}
type List[T any] struct {
	next *List[T]
	v    T
}
type Bs []*B

type HBox struct{ b Box[B] }
type HArr struct{ b [2]B }
type HArrArr struct{ b [2][3]*B }
type HSlicePtr struct{ bs []*B }
type HMapPtr struct{ m map[string]*B }
type HMapKey struct{ m map[string]Box[B] }
type HPtrBox struct{ b *Box[B] }
type HChan struct{ c chan *B }
type HNamedSlice struct{ b Bs }
type HPair struct{ p Pair[string, B] }
type HList struct{ l *List[B] }
type HNested struct{ b Box[Box[B]] }
type HAnon struct{ s struct{ b B } }

type QBox struct{ b Box[Quiet] }
type QList struct{ l *List[Quiet] }
type QArr struct{ a [2]Quiet }

// Recursive generics, including a cycle between two generic types, must terminate.
type Rec[T any] struct {
	next *Rec[T]
	kids []Rec[T]
}
type HRec struct{ r *Rec[B] }
type Cyc[T any] struct {
	other *Cyc2[T]
	v     T
}
type Cyc2[T any] struct{ back *Cyc[T] }
type HCyc struct{ c *Cyc[B] }

func Serve(a *HBox, b *HArr, c *HArrArr, d *HSlicePtr, e *HMapPtr, f *HMapKey, g *HPtrBox, h *HChan, i *HNamedSlice, j *HPair, k *HList, l *HNested, m *HAnon,
	n *QBox, o *QList, p *QArr, q *HRec, r *HCyc, s *Box[B], t *List[B]) {
}
`

var reachNames = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t"}

func reachCensus(t *testing.T, order []string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.com/gen\n\ngo 1.27\n", "gen.go": reachSrc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkgs := loadCensusModule(t, dir)
	rep, err := censusOf(pkgs, summaries(analyze(pkgs)), hatchRows("gen.go", "func Serve(", order), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, h := range rep.Hatches {
		out[h.Name] = h.Worst
	}
	return out
}

// A hatch reaches a type written after construction through any type expression that names it: a
// generic wrapper's type argument, an array, a slice or map of pointers, a channel, a named slice.
func TestCensusReachesTypesThroughTypeStructure(t *testing.T) {
	got := reachCensus(t, reachNames)
	want := map[string]string{
		"a": classW,       // Box[B]
		"b": classW,       // [2]B
		"c": classW,       // [2][3]*B
		"d": classW,       // []*B
		"e": classW,       // map[string]*B
		"f": classW,       // map[string]Box[B]
		"g": classW,       // *Box[B]
		"h": classW,       // chan *B
		"i": classW,       // type Bs []*B
		"j": classW,       // Pair[string, B]
		"k": classW,       // *List[B], a generic cycle
		"l": classW,       // Box[Box[B]]
		"m": classW,       // struct{ b B }
		"n": classCFrozen, // Box[Quiet]: Box's *T reads as an unguarded pointer field, nothing written
		"o": classOK,      // List[Quiet]: nothing written anywhere
		"p": classOK,
		"q": classW, // Rec[B]: the argument is B
		"r": classW,
		"s": classW, // root Box[B]
		"t": classW, // root List[B]
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("hatch %s: worst %q, want %q", n, got[n], w)
		}
	}
}

// The generic shapes must give the same classes in any row order.
func TestCensusReachOrderIndependent(t *testing.T) {
	base := reachCensus(t, reachNames)
	for i := range 4 {
		order := slices.Clone(reachNames)
		for j := range order {
			k := (j*7 + i*3 + 1) % len(order)
			order[j], order[k] = order[k], order[j]
		}
		got := reachCensus(t, order)
		for n, w := range base {
			if got[n] != w {
				t.Fatalf("order %v: %s = %s, first order %s", order, n, got[n], w)
			}
		}
	}
}
