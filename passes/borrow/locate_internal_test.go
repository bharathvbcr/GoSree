package borrow

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// TestReportNeverDropsForWantOfPosition: both report functions of this pass returned without a word
// when handed a finding with no position, so such a finding read as no finding. Each is now
// reported: by the borrow checker at the function it is checking, by the storage checker (which has
// no function) at the package clause, with a message that says so. Two distinct findings there are
// two findings, a repeat is one, and findings that have a position are keyed as before.
func TestReportNeverDropsForWantOfPosition(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n\nfunc f() {}\n\nfunc g() {}\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset, types.NewPackage("p", "p"), []*ast.File{file}, ssa.BuilderMode(0))
	if err != nil {
		t.Fatal(err)
	}
	fn := pkg.Func("f")
	var got []analysis.Diagnostic
	pass := &analysis.Pass{Fset: fset, Files: []*ast.File{file}, Report: func(d analysis.Diagnostic) { got = append(got, d) }}
	check := func(what string, at token.Pos, where string) {
		t.Helper()
		if len(got) != 2 {
			t.Fatalf("%s: %d diagnostics for two distinct findings with no position (one repeated), want 2: %v", what, len(got), got)
		}
		for i, text := range []string{"first", "second"} {
			if got[i].Pos != at || !strings.Contains(got[i].Message, text) || !strings.HasSuffix(got[i].Message, "so it is reported at "+where) {
				t.Errorf("%s: diagnostic %d is %q at %s, want %q at %s ending in %q", what, i, got[i].Message, fset.Position(got[i].Pos), text, fset.Position(at), where)
			}
		}
		got = nil
	}

	bc := &borrowChecker{pass: pass, ix: &annot.Index{}, fn: fn, reports: map[reportKey]bool{}}
	bc.report(token.NoPos, ConflictingBorrow, "first")
	bc.report(token.NoPos, ConflictingBorrow, "second")
	bc.report(token.NoPos, ConflictingBorrow, "first")
	check("borrow checker", fn.Pos(), "the function it is in")

	sc := &storageChecker{pass: pass, ix: &annot.Index{}, lineReports: map[reportLineKey]bool{}}
	sc.report(token.NoPos, StorageViolation, "first")
	sc.report(token.NoPos, StorageViolation, "second")
	sc.report(token.NoPos, StorageViolation, "first")
	check("storage checker", file.Name.Pos(), "the package clause")

	// While a function is being checked, a finding with no position waits. The checks run on the
	// syntax and again on the SSA, so it is usually a finding the syntax check reports with a
	// position: where the function has one of the same rule, it is that finding again and is left
	// out; where it has none, it is reported at the function.
	//
	// The match is by rule and function, not by what the finding says: the two checks word the
	// same defect differently ("slice append cannot store" and "slice cannot store"). So a second,
	// different defect of that rule with no position ("a different one" below) is taken for a
	// repeat too, and stays unreported until the first is fixed. That is a known limit, pinned
	// here so that changing it is a decision.
	g := pkg.Func("g")
	sc.cur = fn
	sc.report(token.NoPos, StorageViolation, "seen twice")
	sc.report(token.NoPos, StorageViolation, "a different one")
	sc.report(fn.Syntax().Pos(), StorageViolation, "seen twice")
	sc.cur = g
	sc.report(token.NoPos, StorageViolation, "seen once")
	sc.cur = nil
	sc.flushPending()
	if len(got) != 2 || got[0].Pos != fn.Syntax().Pos() || got[0].Message != StorageViolation+": seen twice" ||
		got[1].Pos != g.Pos() || got[1].Message != StorageViolation+": seen once; the code this is about has no source position, so it is reported at the function it is in" {
		t.Errorf("findings that waited: got %v; want the located one in f alone, and g's reported at g", got)
	}
	got = nil

	// A finding with a position is one finding per position and rule, whatever it says.
	bc.report(fn.Pos(), ConflictingBorrow, "first")
	bc.report(fn.Pos(), ConflictingBorrow, "second")
	sc.report(g.Pos(), StorageViolation, "first")
	sc.report(g.Pos(), StorageViolation, "second")
	if len(got) != 2 || got[0].Message != ConflictingBorrow+": first" || got[1].Message != StorageViolation+": first" {
		t.Errorf("findings with a position: got %v, want the first of each rule, unchanged", got)
	}
}
