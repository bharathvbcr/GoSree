package errcheck_test

import (
	"runtime"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/errcheck"
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
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if !sum.UsedRustGusset {
			t.Fatal("expected UsedRustGusset == true on unix/cgo build")
		}
	}
}

func TestSafeErrCrossPackageFacts(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errxpkg/a", "errxpkg/b")
}
