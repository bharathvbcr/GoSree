package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
	"github.com/bharathvbcr/gosree/passes/errcheck"
)

func TestSafeErrBasic(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errbasic")
	if len(results) == 0 {
		t.Fatal("expected analysis results for errbasic")
	}
	sum, ok := results[0].Result.(*errcheck.Summary)
	if !ok || sum == nil {
		t.Fatalf("unexpected summary result: %#v", results[0].Result)
	}
	if sum.FunctionsAnalyzed == 0 {
		t.Fatal("expected FunctionsAnalyzed > 0")
	}
	// NativeActive is fixed by build constraints: true exactly when the Rust engine is linked
	// (unix && cgo). The summary must say which engine solved the package.
	if want := gusseterr.NativeActive(); sum.UsedRustGusset != want {
		t.Fatalf("UsedRustGusset = %v, want %v (native engine linked: %v)", sum.UsedRustGusset, want, want)
	}
}

func TestSafeErrCrossPackageFacts(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errxpkg/a", "errxpkg/b")
}
