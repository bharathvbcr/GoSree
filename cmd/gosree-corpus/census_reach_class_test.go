package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A type that merely contains a module struct keeps its own class beside the struct's: the new
// child edges add to a field's classification, they never replace it.
const reachClassSrc = `package gen

import (
	"bytes"
	"sync/atomic"
)

type B struct{ n int }

func (b *B) Bump() { b.n++ }

// External (non-module) struct types, plain and generic.
type XArr struct{ a [2]bytes.Buffer }
type XArrArr struct{ a [2][3]bytes.Buffer }
type XArrPtr struct{ a [2]*bytes.Buffer }
type XExtGeneric struct{ p atomic.Pointer[B] }
type XSlice struct{ bs []*B }
type XMap struct{ m map[string]*B }
type XArrB struct{ a [2]*B }

// Values stored into an interface field.
type SMap struct{ v any }
type SSlice struct{ v any }
type SExtGeneric struct{ v any }
type SStruct struct{ v any }
type SPtr struct{ v any }

func NewSMap() *SMap               { return &SMap{v: map[string]*B{}} }
func NewSSlice() *SSlice           { return &SSlice{v: []*B{}} }
func NewSExtGeneric() *SExtGeneric { return &SExtGeneric{v: atomic.Pointer[B]{}} }
func NewSStruct() *SStruct         { return &SStruct{v: B{}} }
func NewSPtr() *SPtr               { return &SPtr{v: &B{}} }

func Serve(a *XArr, b *XArrArr, c *XArrPtr, d *XExtGeneric, e *XSlice, f *XMap, g *XArrB, h *SMap, i *SSlice, j *SExtGeneric, k *SStruct, l *SPtr) {
}
`

var reachClassNames = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}

func reachClassCensus(t *testing.T, order []string) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.com/gen\n\ngo 1.27\n", "gen.go": reachClassSrc} {
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
		out[h.Name] = h.Closure
	}
	return out
}

func TestCensusReachKeepsTheFieldsOwnClass(t *testing.T) {
	got := reachClassCensus(t, reachClassNames)
	want := map[string][]string{
		"a": {classX},               // [2]bytes.Buffer
		"b": {classX},               // [2][3]bytes.Buffer
		"c": {classX},               // [2]*bytes.Buffer
		"d": {classW, classX},       // atomic.Pointer[B]: external generic plus B's write
		"e": {classCFrozen, classW}, // []*B
		"f": {classCFrozen, classW}, // map[string]*B
		"g": {classW},               // [2]*B: only children
		"h": {classW, classX},       // any holding map[string]*B
		"i": {classW, classX},       // any holding []*B
		"j": {classW, classX},       // any holding atomic.Pointer[B]
		"k": {classW},               // any holding B: a pure child
		"l": {classW},               // any holding *B: a pure child
	}
	for n, w := range want {
		g := slices.Clone(got[n])
		slices.Sort(g)
		slices.Sort(w)
		if !slices.Equal(g, w) {
			t.Errorf("hatch %s: closure %v, want %v", n, g, w)
		}
	}
}

func TestCensusReachClassOrderIndependent(t *testing.T) {
	base := reachClassCensus(t, reachClassNames)
	order := slices.Clone(reachClassNames)
	slices.Reverse(order)
	got := reachClassCensus(t, order)
	for n, w := range base {
		if !slices.Equal(got[n], w) {
			t.Errorf("%s: %v reversed, %v first", n, got[n], w)
		}
	}
}
