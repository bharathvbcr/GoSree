package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Probes 3 and 5: over-reach, false classes, and rootClasses on generic roots. Each row is one
// hatch whose root is a struct with the given body (or a root type given directly). "want" is the
// correct closure; "old" is what the pre-fix census (6b401b9) gave for the same row, recorded by
// swapping its census.go in; it is documentation, not asserted.

const adv2ShapePrelude = `package gen

import (
	"bytes"
	"sync/atomic"
)

type B struct{ n int }

func (b *B) Bump() { b.n++ }

type Q struct{ n int }

type Box[T any] struct{ p *T }
type Pair[K comparable, V any] struct {
	k K
	v V
}
type BB = Box[B]
type GA[T any] = Box[T]
type IM interface{ M(B) *B }
type Shape interface{ Area() int }
type Wr struct{ n int }

func (w *Wr) Area() int { return w.n }
func (w *Wr) Set(v int) { w.n = v }

type Impl2 struct{ n int }

func (Impl2) M() B { return B{} }

type OC[T interface{ M() B }] struct{ n int }
type TC[T interface{ *B }] struct{ p T }

var _ = bytes.NewBuffer
var _ atomic.Int32
`

type adv2Shape struct {
	name string
	body string   // struct body of the hatch's type
	want []string // correct closure
	old  []string // closure at 6b401b9 (documentation)
	note string
}

var adv2Shapes = []adv2Shape{
	{"s01", "f func(B) B", []string{classOK}, []string{classOK}, "func field: B only in the signature"},
	{"s02", "m map[string]func(B)", []string{classCFrozen}, []string{classCFrozen}, "func element of a map"},
	{"s03", "i IM", []string{classOK}, []string{classOK}, "interface slot whose method signature names B, no stores"},
	{"s04", "o OC[Impl2]", []string{classOK}, []string{classOK}, "B only in a type parameter constraint"},
	{"s05", "c chan B", []string{classW}, []string{classOK}, "channel of B by value"},
	{"s06", "c <-chan *B", []string{classW}, []string{classOK}, "receive-only channel of *B"},
	{"s07", "m map[B]int", []string{classCFrozen, classW}, []string{classCFrozen}, "module struct as a map key"},
	{"s08", "m map[Box[B]]string", []string{classCFrozen, classW}, []string{classCFrozen}, "generic instance as a map key"},
	{"s09", "Box[B]", []string{classCFrozen, classW}, []string{classCFrozen}, "embedded generic instance"},
	{"s10", "*Box[B]", []string{classCFrozen, classW}, []string{classCFrozen}, "embedded pointer to a generic instance"},
	{"s11", "x BB", []string{classCFrozen, classW}, []string{classCFrozen}, "alias of a generic instance"},
	{"s12", "x GA[B]", []string{classCFrozen, classW}, []string{classCFrozen}, "generic alias instantiated with B"},
	{"s13", "x atomic.Pointer[bytes.Buffer]", []string{classX}, []string{classX}, "external generic over an external type"},
	{"s14", "x atomic.Pointer[B]", []string{classW, classX}, []string{classX}, "external generic over a module type"},
	{"s16", "x []func() B", []string{classCFrozen}, []string{classCFrozen}, "func elements"},
	{"s17", "x Pair[string, func(B)]", []string{classOK}, []string{classOK}, "func type argument"},
	{"s18", "x func(Box[B])", []string{classOK}, []string{classOK}, "func parameter"},
	{"s19", "x [0]B", []string{classW}, []string{classOK}, "zero-length array of B"},
	{"s20", "x map[string]chan Box[B]", []string{classCFrozen, classW}, []string{classCFrozen}, "map of channels of generic instances"},
	{"s21", "x []atomic.Pointer[B]", []string{classCFrozen, classW}, []string{classCFrozen}, "slice of an external generic over B"},
	{"s22", "x *[2][]map[string]*B", []string{classCFrozen, classW}, []string{classCFrozen}, "pointer, array, slice, map, pointer"},
	{"s23", "x interface{ M(B) }", []string{classOK}, []string{classOK}, "interface literal with B in a method"},
	{"s24", "x struct{ f func(B) }", []string{classX}, []string{classX}, "unnamed struct holding a func"},
	{"s25", "x TC[*B]", []string{classOK, classW}, []string{classOK, classW}, "B only as a type argument bound by a constraint"},
	{"s26", "x *atomic.Pointer[B]", []string{classW}, []string{classOK}, "pointerOverride shareable over B"},
	{"s28", "x []Shape", []string{classCFrozen}, []string{classCFrozen}, "PRE-EXISTING: a slice of a module interface does not walk its implementers (Wr is written)"},
	{"s29", "x Box[Shape]", []string{classCFrozen}, []string{classCFrozen}, "PRE-EXISTING: a module interface as a type argument does not walk its implementers"},
	{"s27", "x *atomic.Pointer[bytes.Buffer]", []string{classOK}, []string{classOK}, "pointerOverride shareable, no module type"},
}

func adv2ShapeSrc() (string, []string) {
	var b strings.Builder
	b.WriteString(adv2ShapePrelude)
	var params, names []string
	for _, s := range adv2Shapes {
		fmt.Fprintf(&b, "\ntype %s struct{ %s }\n", s.name, s.body)
		params = append(params, fmt.Sprintf("%s *%s", s.name, s.name))
		names = append(names, s.name)
	}
	// Roots that are themselves generic instances (probe 5).
	b.WriteString("\ntype Lst[T any] struct {\n\tnext *Lst[T]\n\tv    T\n}\n")
	roots := []struct{ name, typ string }{
		{"r1", "*Box[B]"}, {"r2", "Box[Box[B]]"}, {"r3", "*Pair[string, B]"}, {"r4", "Lst[B]"},
		{"r5", "*Box[Q]"}, {"r6", "Pair[B, B]"}, {"r7", "*Box[Box[Q]]"},
		// Roots that are not module structs but contain one: classified directly.
		{"r8", "[]*B"}, {"r9", "map[string]*B"}, {"r10", "atomic.Pointer[B]"}, {"r11", "[2]B"}, {"r12", "chan *B"},
	}
	for _, r := range roots {
		params = append(params, r.name+" "+r.typ)
		names = append(names, r.name)
	}
	fmt.Fprintf(&b, "\nfunc Serve(%s) {}\n", strings.Join(params, ", "))
	return b.String(), names
}

func adv2ShapeRun(t *testing.T) (map[string][]string, CensusReport) {
	t.Helper()
	src, names := adv2ShapeSrc()
	m := adv2Load(t, src)
	rep, _ := m.run(t, names)
	adv2Dump(t, "shapes", rep)
	return adv2Classes(rep), rep
}

// Probe 3: no shape reaches B through a func signature, an interface method, or a constraint, and
// the shapes that do reach it give exactly the classes in the table.
func TestAdv2ShapesOverReach(t *testing.T) {
	got, _ := adv2ShapeRun(t)
	for _, s := range adv2Shapes {
		w := slices.Clone(s.want)
		slices.Sort(w)
		if !slices.Equal(got[s.name], w) {
			t.Errorf("%s (%s): closure %v, want %v (pre-fix %v)", s.name, s.note, got[s.name], w, s.old)
		}
	}
}

// Probe 5: for a root that is a generic instance the classes are exactly the union of the origin's
// classes and each type argument's, sorted, without duplicates.
func TestAdv2RootClassesUnionSortedNoDuplicates(t *testing.T) {
	got, _ := adv2ShapeRun(t)
	want := map[string][]string{
		"r1": {classCFrozen, classW}, // Box (C-frozen) + B (W)
		"r2": {classCFrozen, classW},
		"r3": {classOK, classW},       // Pair: k string, v V (a slot with no stores): OK; + B
		"r4": {classOK, classW},       // Lst: next is a self child, v a slot with no stores
		"r5": {classCFrozen, classOK}, // Box (C-frozen) + Q (OK)
		"r6": {classOK, classW},
		"r7": {classCFrozen, classOK},
	}
	for n, w := range want {
		g := got[n]
		if !slices.Equal(g, w) {
			t.Errorf("%s: closure %v, want %v", n, g, w)
		}
		if !slices.IsSorted(g) {
			t.Errorf("%s: closure %v is not sorted", n, g)
		}
		if len(slices.Compact(slices.Clone(g))) != len(g) {
			t.Errorf("%s: closure %v has duplicates", n, g)
		}
	}
}

// PRE-EXISTING (asserts today's behaviour, which is also the pre-fix behaviour): rootClasses walks
// only a module struct root. A root that merely contains a module struct (a slice of pointers to B, a
// map, an array, a channel, an external generic over B) is classified directly, so its hatch never
// sees B's W, although the same type as a field would (s14, s21, s22 above). reach is not applied
// to the root. The hatch classes are what they were at 6b401b9.
func TestAdv2NonStructRootIsNotReached(t *testing.T) {
	got, _ := adv2ShapeRun(t)
	want := map[string][]string{
		"r8":  {classX},  // []*B
		"r9":  {classX},  // map[string]*B
		"r10": {classX},  // atomic.Pointer[B] (field form is {W, X})
		"r11": {classOK}, // [2]B: Shareable, B's write unseen
		"r12": {classOK}, // chan *B: Shareable
	}
	for n, w := range want {
		if !slices.Equal(got[n], w) {
			t.Errorf("%s: closure %v, want today's %v", n, got[n], w)
		}
	}
}
