package borrow_test

import (
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

// Two struct variables of one name hold borrows in different fields, and a third variable of that
// name matches both. The field the report names must not depend on map iteration order. The
// analyzer is run repeatedly; with two candidates a map-ordered first match differs between runs
// with probability 1/2 each, so 40 runs make a false pass vanishingly unlikely.
func TestStorageMessageDeterministicWithShadowedStructs(t *testing.T) {
	pkgs := loadBorrowTempModule(t, map[string]string{"p/p.go": `package p

type S struct{ a, z *int }

func f(ch chan S) {
	x := 1
	//gosree:mut
	b := &x
	{
		s := S{a: b}
		f1 := func() { _ = s }
		f1()
	}
	{
		s := S{z: b}
		f2 := func() { _ = s }
		f2()
	}
	{
		s := S{}
		f3 := func() { _ = s }
		f3()
		ch <- s
	}
}

func g(ch chan S) {
	x := 1
	//gosree:mut
	b := &x
	{
		s := S{a: b}
		f1 := func() { _ = s }
		f1()
	}
	{
		s := S{z: b}
		f2 := func() { _ = s }
		f2()
	}
	{
		s := S{}
		f3 := func() { _ = s }
		f3()
		select {
		case ch <- s:
		}
	}
}
`})
	run := func() string {
		g, err := checker.Analyze([]*analysis.Analyzer{borrow.Analyzer}, pkgs, nil)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		var lines []string
		for _, act := range g.Roots {
			for _, d := range act.Diagnostics {
				p := act.Package.Fset.Position(d.Pos)
				lines = append(lines, p.String()+": "+d.Message)
			}
		}
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	first := run()
	t.Logf("diagnostics:\n%s", first)
	distinct := map[string]bool{first: true}
	for i := 1; i < 40; i++ {
		distinct[run()] = true
	}
	for d := range distinct {
		t.Logf("variant:\n%s", d)
	}
	if !strings.Contains(first, "field 'a'") {
		t.Fatalf("the smallest candidate field is the stable choice; got:\n%s", first)
	}
	if len(distinct) > 1 {
		t.Fatalf("%d distinct renderings", len(distinct))
	}
}
