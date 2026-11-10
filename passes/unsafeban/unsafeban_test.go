package unsafeban_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/unsafeban"
)

func TestUnsafeBan(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), unsafeban.Analyzer, "u")
}

// TestDotImportAndAssembly checks that a dot import of unsafe and a function without a Go body are
// both caught.
func TestDotImportAndAssembly(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), unsafeban.Analyzer, "dot")
}
