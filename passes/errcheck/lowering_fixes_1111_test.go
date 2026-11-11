package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

func TestRangeOverFuncBodies(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errrangefunc")
}
