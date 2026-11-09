package annot_test

import (
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
	if got := ix.UnsafeScopes(); got != 1 {
		t.Errorf("UnsafeScopes: got %d, want 1 (the reasonless one still applies; the dangling one does not)", got)
	}
}
