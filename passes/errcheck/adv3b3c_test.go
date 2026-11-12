package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// BREAK (aabf10f): a store through a captured variable does not clear the shared written flag, so
// closing the newly assigned, never-written resource reports ERR005. This test asserts the correct
// behavior (no ERR005) and fails until resourceSlot accounts for stores through the FreeVar.
func TestAdv3b3cReassignFalsePositive(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advfvbreak3b3c")
}

// Current behavior of the captured-resource shapes that hold (true positives and silent cases) and
// of pre-existing misses (nothing is reported for the interprocedural, field-load and
// range-over-func-body shapes; see the comments in the fixtures).
func TestAdv3b3cCapturedResources(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advfv3b3c")
}

func TestAdv3b3cRangeFunc(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advfvrange3b3c")
}

func TestAdv3b3cControls(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advfvctl3b3c")
}

// ERR001-ERR004 on closures over captured error variables are identical before and after aabf10f
// (diffed against 6b401b9 in both engines); only the ERR005 lines in this package are new.
func TestAdv3b3cCapturedErrors(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advfverr3b3c")
}
