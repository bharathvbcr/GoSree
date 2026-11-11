package main

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// Probe 2: every type in a reference cycle that runs through one of the new edges (a generic type
// argument, an array, a map key, a channel, an interface-slot store of an instance, an unnamed struct,
// a named slice) gets one class whatever the row order. The census is run over many row orders of one
// loaded module.
const adv2OrderSrc = `package gen

import "sync/atomic"

type Box[T any] struct{ p *T }
type Pair[K comparable, V any] struct {
	k K
	v V
}

// Cycle 1: through a type argument and a slice of pointers. CB is written.
type CA struct{ x Box[*CB] }
type CB struct {
	ys []*CA
	n  int
}

func (b *CB) Bump() { b.n++ }

// Cycle 2: through a map key and a channel. CD is written.
type CC struct{ m map[CD]int }
type CD struct {
	c chan CC
	w int
}

func (d *CD) Set() { d.w = 1 }

// Cycle 3: through an interface-slot store of an instance. CF is written.
type CE struct{ v any }
type CF struct {
	e *CE
	k int
}

func (f *CF) Set() { f.k = 1 }
func NewCE() *CE   { return &CE{v: Box[*CF]{}} }

// Cycle 4: through an array and a pointer. CH is written.
type CG struct{ a [2]CH }
type CH struct {
	g *CG
	z int
}

func (h *CH) Set() { h.z = 1 }

// Cycle 5: through an external generic. CJ is written.
type CI struct{ x atomic.Pointer[CJ] }
type CJ struct {
	i *CI
	q int
}

func (j *CJ) Set() { j.q = 1 }

// Cycle 6: through an unnamed struct. CL is written.
type CK struct{ s struct{ k *CL } }
type CL struct {
	k CK
	w []int
}

func (l *CL) Add() { l.w = append(l.w, 1) }

// Cycle 7: through a named slice. CM is written.
type CM struct{ n NS }
type NS []*CM

func (m *CM) Set() { m.n = nil }

// Cycle 8: through nested instances, with no write anywhere.
type CP struct{ x Box[Pair[string, *CQ]] }
type CQ struct{ y map[string]Box[*CP] }

// Downstream of cycle 1 but not in it.
type CZ struct{ a Pair[int, CA] }

func Serve(ca *CA, cb *CB, cc *CC, cd *CD, ce *CE, cf *CF, cg *CG, ch *CH, ci *CI, cj *CJ, ck *CK, cl *CL, cm *CM, cp *CP, cq *CQ, cz *CZ, r1 Box[*CB], r2 Pair[string, *CQ]) {
}
`

var adv2OrderNames = []string{"ca", "cb", "cc", "cd", "ce", "cf", "cg", "ch", "ci", "cj", "ck", "cl", "cm", "cp", "cq", "cz", "r1", "r2"}

var adv2OrderCycles = [][]string{{"ca", "cb"}, {"cc", "cd"}, {"ce", "cf"}, {"cg", "ch"}, {"ci", "cj"}, {"ck", "cl"}, {"cm"}, {"cp", "cq"}}

func TestAdv2CycleClassesOrderIndependent(t *testing.T) {
	m := adv2Load(t, adv2OrderSrc)
	base, _ := m.run(t, adv2OrderNames)
	adv2Dump(t, "order", base)
	want := adv2Classes(base)
	t.Logf("classes: %v", want)

	// Every member of a cycle has the cycle's class.
	for _, cyc := range adv2OrderCycles {
		for _, n := range cyc[1:] {
			if !slices.Equal(want[n], want[cyc[0]]) {
				t.Errorf("cycle %v: %s = %v, %s = %v", cyc, cyc[0], want[cyc[0]], n, want[n])
			}
		}
	}
	// Written cycles are W (CL: C-written beside), the unwritten cycle has no W.
	for _, n := range []string{"ca", "cb", "cc", "cd", "ce", "cf", "cg", "ch", "ci", "cj", "ck", "cl", "cm", "cz"} {
		if !slices.Contains(want[n], classW) {
			t.Errorf("%s = %v, want W", n, want[n])
		}
	}
	if slices.Contains(want["cp"], classW) || slices.Contains(want["cq"], classW) {
		t.Errorf("cp %v / cq %v: no W is reachable", want["cp"], want["cq"])
	}

	typeClosures := func(rep CensusReport) map[string][]string {
		out := map[string][]string{}
		for _, ct := range rep.Types {
			c := slices.Clone(ct.Closure)
			slices.Sort(c)
			own := slices.Clone(ct.Own)
			slices.Sort(own)
			out[ct.Type] = append(append(c, "|"), own...)
		}
		return out
	}
	wantTypes := typeClosures(base)

	rng := rand.New(rand.NewPCG(1138, 1111))
	orders := [][]string{}
	rev := slices.Clone(adv2OrderNames)
	slices.Reverse(rev)
	orders = append(orders, rev)
	for i := range adv2OrderNames {
		orders = append(orders, slices.Concat(adv2OrderNames[i:], adv2OrderNames[:i]))
	}
	for range 40 {
		o := slices.Clone(adv2OrderNames)
		rng.Shuffle(len(o), func(i, j int) { o[i], o[j] = o[j], o[i] })
		orders = append(orders, o)
	}
	for _, o := range orders {
		rep, _ := m.run(t, o)
		got := adv2Classes(rep)
		for n, w := range want {
			if !slices.Equal(got[n], w) {
				t.Fatalf("order %v: %s = %v, first order %v", o, n, got[n], w)
			}
		}
		gt := typeClosures(rep)
		for ty, w := range wantTypes {
			if !slices.Equal(gt[ty], w) {
				t.Fatalf("order %v: type %s = %v, first order %v", o, ty, gt[ty], w)
			}
		}
	}
	t.Logf("%d row orders agree", len(orders))
}
