package borrow_test

import (
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

// A struct that holds the same borrow in several fields, reached through every sink the storage
// checker knows (channel send, select send, map store, return, call argument, field store). The
// rendered diagnostics must be byte-identical across 20 runs. HOLDS: it also passes on the unfixed 9161d81 (this fixture's holders are reached in
// field order there, so the map-ordered fallbacks the fix replaced are not what it exercises).
func TestAdvStorageDeterministicManyHolders(t *testing.T) {
	pkgs := loadBorrowTempModule(t, map[string]string{"p/p.go": `package p

type S struct{ z, m, a, q *int }

type T struct{ s S }

var global S

func sink(s S) {}

func send(ch chan S, ch2 chan S) {
	x := 1
	//gosree:mut
	b := &x
	s := S{z: b, m: b, a: b, q: b}
	ch <- s
	select {
	case ch <- s:
	case ch2 <- s:
	}
}

func store(m map[string]S, mm map[S]int) {
	x := 1
	//gosree:mut
	b := &x
	s := S{z: b, m: b, a: b, q: b}
	m["k"] = s
	mm[s] = 1
	global = s
}

func ret() S {
	x := 1
	//gosree:mut
	b := &x
	s := S{z: b, m: b, a: b, q: b}
	return s
}

func nested(ch chan T) {
	x := 1
	//gosree:mut
	b := &x
	t := T{s: S{z: b, m: b, a: b, q: b}}
	ch <- t
	sink(t.s)
}

func shadows(ch chan S) {
	x := 1
	//gosree:mut
	b := &x
	{
		s := S{z: b}
		_ = s
	}
	{
		s := S{q: b}
		_ = s
	}
	{
		s := S{a: b, m: b}
		_ = func() { _ = s }
		ch <- s
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
				lines = append(lines, act.Package.Fset.Position(d.Pos).String()+": "+d.Message)
			}
		}
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	first := run()
	t.Logf("diagnostics:\n%s", first)
	if first == "" {
		t.Fatal("fixture produced no diagnostics; it tests nothing")
	}
	for i := 1; i < 20; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d differs:\n%s\n--- vs first ---\n%s", i, got, first)
		}
	}
}
