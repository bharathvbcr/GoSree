package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// The closure replay of ft-fe6e: a write or a discarded Close inside a closure the creator runs at
// one dominating call is replayed at that call. Each Close is reported once.
func TestClosureReplayReports(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errfe6e")
}

// Shapes the replay does not track, and shapes it tracks that drop nothing, behave exactly as before
// ft-fe6e: no diagnostic at all, which the unmodified base also gives.
func TestClosureReplayLeavesUntrackedShapesAlone(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errfe6efp")
}
