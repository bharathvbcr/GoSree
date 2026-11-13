package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

func TestFreeVarCells(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errfreevar")
}

func TestRangeFuncCapturedResource(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errrangecapture")
}

func TestFreeVarReplacedByCallKnownFP(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errfvswap")
}

// A closure that assigns a captured (v, err) pair moves the obligation to the enclosing function:
// the enclosing function's check of err discharges the use of v, and an unchecked use is reported
// there. Shapes the enclosing function cannot track stay reported at the closure's store.
func TestClosureAssignedPair(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errclosurepair")
}
