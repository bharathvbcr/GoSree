package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/ownership"
)

func TestMoves(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "moves")
}

func TestGlobals(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "globals")
}

func TestImm(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "imm")
}

// TestCrossPackage checks that summaries travel as facts, including from the standard library.
func TestCrossPackage(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "user")
}
