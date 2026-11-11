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
