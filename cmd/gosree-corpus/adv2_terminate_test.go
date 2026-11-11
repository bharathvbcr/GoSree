package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Probe 1: termination and blow-up of reach (and the closure walk built on it).

// adv2Timed runs the census over src with a hard timeout, so a hang fails the test instead of the run.
func adv2Timed(t *testing.T, src string, names []string, limit time.Duration) (CensusReport, time.Duration, bool) {
	t.Helper()
	m := adv2Load(t, src)
	type res struct {
		rep CensusReport
		d   time.Duration
	}
	ch := make(chan res, 1)
	go func() {
		rep, d := m.run(t, names)
		ch <- res{rep, d}
	}()
	select {
	case r := <-ch:
		return r.rep, r.d, true
	case <-time.After(limit):
		return CensusReport{}, limit, false
	}
}

const adv2Prelude = `package gen

type B struct{ n int }

func (b *B) Bump() { b.n++ }

type Box[T any] struct{ p *T }
type Pair[X, Y any] struct {
	a *X
	b *Y
}

func Serve(`

// Box nested n deep: Box[Box[...Box[B]...]], as a field and as a value stored into an interface slot.
func adv2Deep(n int) string {
	ty := "B"
	for range n {
		ty = "Box[" + ty + "]"
	}
	return adv2Prelude + `h *H, s *S) {}

type H struct{ b ` + ty + ` }
type S struct{ v any }

func NewS() *S { return &S{v: ` + ty + `{}} }
`
}

func TestAdv2DeepGenericsTerminate(t *testing.T) {
	for _, n := range []int{50, 200} {
		rep, d, ok := adv2Timed(t, adv2Deep(n), []string{"h", "s"}, 60*time.Second)
		if !ok {
			t.Fatalf("depth %d: census did not finish in 60s", n)
		}
		t.Logf("depth %d: %v classes %v", n, d, adv2Classes(rep))
		if d > time.Second {
			t.Errorf("depth %d: %v, want under 1s", n, d)
		}
		for h, c := range adv2Classes(rep) {
			if !slices.Contains(c, classW) {
				t.Errorf("depth %d: %s = %v, want to reach B (W)", n, h, c)
			}
		}
	}
}

// A type expression that is a DAG as a Go type but a tree when walked: P1 = Pair[B, B],
// P2 = Pair[P1, P1], ... The expansion has 2^k leaves (see adv2_reach_test.go for reach itself).
func adv2PairDAG(k int) string {
	var b strings.Builder
	b.WriteString(adv2Prelude + "h *H) {}\n\ntype P1 = Pair[B, B]\n")
	for i := 2; i <= k; i++ {
		fmt.Fprintf(&b, "type P%d = Pair[P%d, P%d]\n", i, i-1, i-1)
	}
	fmt.Fprintf(&b, "type H struct{ p *P%d }\n", k)
	return b.String()
}

// The unnamed-type version: U0 = *B, U1 = struct{ a, b U0 }, ... (aliases of type literals).
func adv2StructDAG(k int) string {
	var b strings.Builder
	b.WriteString(adv2Prelude + "h *H) {}\n\ntype U0 = *B\n")
	for i := 1; i <= k; i++ {
		fmt.Fprintf(&b, "type U%d = struct{ a, b U%d }\n", i, i-1)
	}
	fmt.Fprintf(&b, "type H struct{ u U%d }\n", k)
	return b.String()
}

const adv2RecSrc = adv2Prelude + `a *HA, b *HB, c *HC, d *HD, e *HE, f *HF, g *HG, h *HH, i *HI, j *HJ, k *HK, l *HL) {}

type A[T any] struct{ b *Bx[T] }
type Bx[T any] struct {
	a *A[T]
	v T
}
type HA struct{ x *A[B] }
type HB struct{ x A[A[B]] }

type T []T
type M map[string]M
type F func() F
type HC struct {
	t T
	b B
}
type HD struct {
	m M
	b B
}
type HE struct {
	f F
	b B
}

type TB []TB
type HF struct{ v any }

func NewHF() *HF { return &HF{v: TB{}} }

// A generic instantiated with itself.
type G[X any] struct{ x *X }
type S struct {
	g G[S]
	b B
}
type HG struct{ s S }

// A generic named slice and map that contain themselves.
type L[X any] []L[X]
type MM[X any] map[string]MM[X]
type HH struct {
	l L[B]
	m MM[B]
}

// Go rejects an expanding instantiation (type X[P any] struct{ f *X[Box[P]] }: "instantiation
// cycle"), so it needs no case. A generic whose argument is a different instance of itself:
type X[P any] struct{ f *X[P] }
type HI struct{ x X[X[B]] }

// Mutually recursive named non-struct types with a struct in each.
type N1 []N2
type N2 map[string]N1
type HJ struct {
	n N1
	b B
}
type HK struct{ n map[string][]*N1 }

// A self-referential struct reached through a chain of named non-structs.
type R struct{ r1 R1 }
type R1 []R2
type R2 [2]*R
type HL struct{ r R }
`

func TestAdv2RecursiveTypesTerminate(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	rep, d, ok := adv2Timed(t, adv2RecSrc, names, 60*time.Second)
	if !ok {
		t.Fatal("census did not finish in 60s")
	}
	t.Logf("recursive types: %v %v", d, adv2Classes(rep))
	if d > time.Second {
		t.Errorf("%v, want under 1s", d)
	}
	got := adv2Classes(rep)
	// h's HC, HD, HE keep B (W) beside the recursive named type; hatches with no B stay OK-ish.
	for _, n := range []string{"c", "d", "e", "g", "j"} {
		if !slices.Contains(got[n], classW) {
			t.Errorf("hatch %s = %v, want W (it holds a B by value)", n, got[n])
		}
	}
	for _, n := range []string{"a", "b", "h", "i"} {
		if !slices.Contains(got[n], classW) {
			t.Errorf("hatch %s = %v, want W (B is the type argument)", n, got[n])
		}
	}
}
