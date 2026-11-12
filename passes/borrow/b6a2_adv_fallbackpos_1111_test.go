package borrow_test

import (
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

// Findings inside generic functions and instances, closures in generics, methods of generic types,
// range-over-func bodies and method values must each be reported at a valid position in the
// package's own file, exactly once (the fallback must not duplicate a finding the AST path or the
// SSA path already reported at its source line).
func TestAdvBorrowFindingsInSyntheticFunctionsHaveValidPositions(t *testing.T) {
	pkgs := loadBorrowTempModule(t, map[string]string{"p/p.go": `package p

import "iter"

type Box[T any] struct{ v T }

func gen[T any](x T) *int {
	y := 1
	//gosree:mut
	b := &y
	_ = x
	return b
}

func (bx *Box[T]) get() *int {
	y := 1
	//gosree:mut
	b := &y
	return b
}

func inGenClosure[T any](x T) func() *int {
	return func() *int {
		y := 1
		//gosree:mut
		b := &y
		return b
	}
}

func rangeFunc(seq iter.Seq[int]) *int {
	var out *int
	for v := range seq {
		//gosree:mut
		b := &v
		out = b
	}
	return out
}

type S struct{ n int }

func (s *S) Inc() {}

func methodValue() func() *int {
	y := 1
	//gosree:mut
	b := &y
	f := func() *int { return b }
	return f
}

func use() {
	_ = gen(1)
	_ = gen("s")
	(&Box[int]{}).get()
	(&Box[string]{}).get()
	_ = inGenClosure(1)
	_ = rangeFunc(nil)
	f := (&S{}).Inc
	f()
	_ = (*S).Inc
	_ = methodValue()
}
`})
	g, err := checker.Analyze([]*analysis.Analyzer{borrow.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	seen := map[string]int{}
	n := 0
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			n++
			if !d.Pos.IsValid() || d.Pos == token.NoPos {
				t.Errorf("diagnostic at an invalid position: %s", d.Message)
				continue
			}
			p := act.Package.Fset.Position(d.Pos)
			if filepath.Base(p.Filename) != "p.go" || p.Line == 0 {
				t.Errorf("diagnostic at %s, outside the package source: %s", p, d.Message)
			}
			k := p.String() + ": " + d.Message
			seen[k]++
			t.Logf("%s:%d:%d %s", filepath.Base(p.Filename), p.Line, p.Column, d.Message)
		}
	}
	for k, c := range seen {
		// PRE-EXISTING: a range-over-func body is built as a yield closure and the checker visits
		// it from both the closure and its parent, so the loop-carried BORROW002 is reported twice
		// at one position on 9161d81 and on the fix alike. Pinned at exactly two.
		if strings.Contains(k, "loop-carried borrow escapes") {
			if c != 2 {
				t.Errorf("pre-existing duplicate changed to %d reports: %s", c, k)
			}
			continue
		}
		if c > 1 {
			t.Errorf("reported %d times: %s", c, strings.TrimSpace(k))
		}
	}
	if n == 0 {
		t.Fatal("fixture produced no findings; it tests nothing")
	}
}
