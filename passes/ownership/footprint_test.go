package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestFootprint checks the access-footprint refinement of SAFE003 (footprint.go): uses after a move
// that cannot race with the goroutine — disjoint fields or constant indices, reads on both sides, a
// mutex both sides hold, atomics on both sides, code a folded branch skips — are not reported, and
// every racy or unfollowable shape still is.
func TestFootprint(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "footprint")
}
