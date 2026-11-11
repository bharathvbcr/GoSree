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

// TestCrossPackageUnsafePointerAliasAndReturn checks that re-exported unsafe.Pointer aliases,
// defined types, and helper functions cannot smuggle pointer reinterpretation without importing "unsafe".
func TestCrossPackageUnsafePointerAliasAndReturn(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), unsafeban.Analyzer, "bridge", "crosspkg")
}
