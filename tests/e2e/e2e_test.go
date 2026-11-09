package e2e_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/internal/annot"
	"github.com/bharathvbcr/gocap/passes/ownership"
	"github.com/bharathvbcr/gocap/passes/unsafeban"
)

// ActiveAnalyzers tracks the analyzers available in the current milestone.
var (
	annotAnalyzer     = annot.Analyzer
	unsafebanAnalyzer = unsafeban.Analyzer
	ownershipAnalyzer = ownership.Analyzer
	// borrowAnalyzer is initialized when passes/borrow is available in M1.
	borrowAnalyzer *analysis.Analyzer = nil
)

// VerifyFixtureSyntax parses all Go files in the given testdata package to verify
// 100% standard Go syntax compatibility and well-formedness.
func VerifyFixtureSyntax(t *testing.T, pkgName string) {
	t.Helper()
	testdataDir := analysistest.TestData()
	pkgDir := filepath.Join(testdataDir, "src", pkgName)

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("failed to read fixture package directory %s: %v", pkgDir, err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		path := filepath.Join(pkgDir, entry.Name())
		_, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("syntax verification failed for fixture %s: %v", path, err)
		}
	}
}

// RunTierTest executes analysistest against the specified package if the analyzer is available,
// otherwise validates syntax and marks the test as pending the designated milestone.
func RunTierTest(t *testing.T, a *analysis.Analyzer, pkgName string, milestone string) {
	t.Helper()
	// Always verify standard Go syntax first
	VerifyFixtureSyntax(t, pkgName)

	if a == nil {
		t.Skipf("[PENDING] Analyzer for %s awaits %s implementation", pkgName, milestone)
		return
	}

	analysistest.Run(t, analysistest.TestData(), a, pkgName)
}
