package annot_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/internal/annot"
)

func TestDirectivesLegacy(t *testing.T) {
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
	if got := ix.UnsafeScopes(); got != 1 {
		t.Errorf("UnsafeScopes: got %d, want 1 (the reasonless one still applies; the dangling one does not)", got)
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
