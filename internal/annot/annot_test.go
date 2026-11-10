package annot_test

import (
	"os"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/internal/annot"
)

func TestDirectives(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "a")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	ix := results[0].Result.(*annot.Index)
	if got := len(ix.IsoVars); got != 1 {
		t.Errorf("IsoVars: got %d, want 1 (only p of named)", got)
	}
	if got := len(ix.ImmTypes); got != 1 {
		t.Errorf("ImmTypes: got %d, want 1 (only T)", got)
	}
	// A malformed directive has no effect: the reasonless hatch is reported and not installed, and the
	// dangling one attaches to nothing.
	if got := ix.UnsafeScopes(); got != 0 {
		t.Errorf("UnsafeScopes: got %d, want 0 (neither the reasonless nor the dangling hatch applies)", got)
	}
}

// TestGuardedDirectives checks //safe:guarded_by and //safe:holds: what they attach to, what the mutex
// may be, and the malformed cases.
func TestGuardedDirectives(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "g")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	ix := results[0].Result.(*annot.Index)
	byMutex := map[string]int{}
	for _, mu := range ix.Guarded {
		byMutex[mu.Name()]++
	}
	// S.n and S.m by mu, S.r by rw, E.n by the embedded Mutex, count, total and local c by gmu,
	// local x and inner by lmu, local y and z by lrw.
	want := map[string]int{"mu": 2, "rw": 1, "Mutex": 1, "gmu": 3, "lmu": 2, "lrw": 2}
	if len(byMutex) != len(want) {
		t.Errorf("Guarded by mutex: got %v, want %v", byMutex, want)
	}
	for mu, n := range want {
		if byMutex[mu] != n {
			t.Errorf("Guarded by %s: got %d, want %d (all: %v)", mu, byMutex[mu], n, byMutex)
		}
	}
	if n := ix.HoldsDirectives(); n != 2 {
		t.Errorf("Holds: got %d, want 2 (bumpLocked, addLocked)", n)
	}
}

// TestNearMissesAndMalformed checks that a comment read as a directive is applied or reported, never
// dropped, that prose is left alone, and that a reported directive has no effect.
func TestNearMissesAndMalformed(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "n")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	ix := results[0].Result.(*annot.Index)
	guarded := map[string]bool{}
	for v := range ix.Guarded {
		guarded[v.Name()] = true
	}
	if len(guarded) != 1 || !guarded["c"] {
		t.Errorf("Guarded: got %v, want only c (the tab-separated directive; the near misses have no effect)", guarded)
	}
	if got := len(ix.IsoVars); got != 0 {
		t.Errorf("IsoVars: got %d, want 0 (both iso directives are malformed)", got)
	}
	if got := len(ix.ImmTypes); got != 0 {
		t.Errorf("ImmTypes: got %d, want 0 (imm with arguments is malformed)", got)
	}
	mub, rob := map[string]bool{}, map[string]bool{}
	for v := range ix.MubVars {
		mub[v.Name()] = true
	}
	for v := range ix.RobVars {
		rob[v.Name()] = true
	}
	if len(mub) != 2 || !mub["a"] || !mub["b"] {
		t.Errorf("MubVars: got %v, want a and b (the inline form after a two-name field)", mub)
	}
	if !rob["c"] || rob["r"] {
		t.Errorf("RobVars: got %v, want c and not the result r", rob)
	}
	// The binding's first directive applies; the conflicting second one has no effect.
	if !rob["b"] {
		t.Errorf("RobVars: got %v, want binding b from the first directive", rob)
	}
	// The tab-separated hatch and the first of the two identical ones.
	if got := ix.UnsafeScopes(); got != 2 {
		t.Errorf("UnsafeScopes: got %d, want 2", got)
	}
}

// TestHatchSpans checks which statements a hatch covers: every line commented "in" must be suppressed
// and every line commented "out" must not.
func TestHatchSpans(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "h")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	pass := results[0].Pass
	ix := results[0].Result.(*annot.Index)
	checked := 0
	for _, f := range pass.Files {
		tf := pass.Fset.File(f.Pos())
		src, err := os.ReadFile(tf.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				want := c.Text == "// in"
				if !want && c.Text != "// out" {
					continue
				}
				// The first non-blank byte of the comment's line.
				line := pass.Fset.PositionFor(c.Pos(), false).Line
				off := tf.Offset(tf.LineStart(line))
				for src[off] == '\t' || src[off] == ' ' {
					off++
				}
				pos := tf.Pos(off)
				if got := ix.Suppressed(pos); got != want {
					t.Errorf("%s: Suppressed = %v, want %v", pass.Fset.Position(pos), got, want)
				}
				checked++
			}
		}
	}
	if checked < 10 {
		t.Errorf("checked %d lines, want at least 10", checked)
	}
}

func TestBorrowDirectives(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "b")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	ix := results[0].Result.(*annot.Index)
	if got := len(ix.MubVars); got < 10 {
		t.Errorf("MubVars: got %d, want at least 10", got)
	}
	if got := len(ix.RobVars); got < 7 {
		t.Errorf("RobVars: got %d, want at least 7", got)
	}
	if got := len(ix.Borrows); got < 15 {
		t.Errorf("Borrows: got %d, want at least 15", got)
	}
	if got := len(ix.IsoVars); got != 2 {
		t.Errorf("IsoVars: got %d, want 2 (canonicalIso and conflictIsoMut)", got)
	}
	if got := len(ix.ImmTypes); got != 1 {
		t.Errorf("ImmTypes: got %d, want 1", got)
	}
	if got := ix.UnsafeScopes(); got != 1 {
		t.Errorf("UnsafeScopes: got %d, want 1", got)
	}
}
