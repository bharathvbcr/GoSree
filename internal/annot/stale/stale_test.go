package stale_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/annot/stale"
)

// TestStale checks that a hatch is stale only when no pass muted a finding through it. A hatch that
// mutes only a borrow finding is not covered here: the borrow pass asks annot.Index.Suppressed and
// never calls Mute, so such a hatch reads as stale until the borrow pass records its mutes.
func TestStale(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), stale.Analyzer, "st")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := len(results[0].Result.([]annot.Hatch)); got != 1 {
		t.Errorf("stale hatches: got %d, want 1 (plain)", got)
	}
}
