package borrow

import (
	"go/ast"
	"go/token"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// A borrow-conflict finding with no position of its own is reported at a valid fallback position,
// not dropped, and its message says it was placed there. (The storage checker holds its own
// unpositioned findings back until the function is done, and reports only those its syntax walk
// has not already reported at their source line: see flushPending.)
func TestReportWithoutPositionFallsBack(t *testing.T) {
	fset := token.NewFileSet()
	file := fset.AddFile("p.go", -1, 100)
	root := &ast.File{Package: file.Pos(0), Name: ast.NewIdent("p")}

	var got []analysis.Diagnostic
	pass := &analysis.Pass{
		Fset:   fset,
		Files:  []*ast.File{root},
		Report: func(d analysis.Diagnostic) { got = append(got, d) },
	}
	ix := &annot.Index{}

	bc := &borrowChecker{pass: pass, ix: ix, reports: map[reportKey]bool{}}
	bc.report(token.NoPos, DanglingReturn, "cannot return reference to local stack variable")
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics, want 1: a finding without a position was dropped", len(got))
	}
	if !got[0].Pos.IsValid() {
		t.Fatalf("diagnostic reported at an invalid position")
	}
	if !strings.Contains(got[0].Message, "no source position") {
		t.Fatalf("message %q does not say the finding was placed for want of a position", got[0].Message)
	}
}
