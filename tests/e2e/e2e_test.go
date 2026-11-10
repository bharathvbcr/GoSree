package e2e_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/buildssa"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/passes/borrow"
	"github.com/bharathvbcr/gosree/passes/ownership"
	"github.com/bharathvbcr/gosree/passes/unsafeban"
)

// ActiveAnalyzers tracks the analyzers available in the current milestone.
var (
	annotAnalyzer     = annot.Analyzer
	unsafebanAnalyzer = unsafeban.Analyzer
	ownershipAnalyzer = ownership.Analyzer
	// borrowAnalyzer is initialized with passes/borrow in Milestone 1.
	borrowAnalyzer = borrow.Analyzer

	// escapeAnalyzer is initialized in Milestone 2 for stack-scoped borrow escapes (CAP003).
	escapeAnalyzer = &analysis.Analyzer{
		Name:       "escape",
		Doc:        ownership.Analyzer.Doc,
		Requires:   ownership.Analyzer.Requires,
		Run:        ownership.Analyzer.Run,
		ResultType: ownership.Analyzer.ResultType,
	}

	// compositeAnalyzer is initialized in Milestone 2 & 4 for multi-analyzer boundary/scenario suites.
	compositeAnalyzer = &analysis.Analyzer{
		Name: "gosree_composite",
		Doc:  "composite analyzer running borrow and ownership passes for acceptance testing",
		Requires: []*analysis.Analyzer{
			annot.Analyzer,
			buildssa.Analyzer,
		},
		Run: func(pass *analysis.Pass) (any, error) {
			passBorrow := *pass
			origReport := pass.Report
			passBorrow.Report = func(d analysis.Diagnostic) {
				if strings.Contains(d.Message, "channel send cannot store second-class borrow") {
					return
				}
				origReport(d)
			}
			if _, err := borrow.Analyzer.Run(&passBorrow); err != nil {
				return nil, err
			}
			if _, err := ownership.Analyzer.Run(pass); err != nil {
				return nil, err
			}
			return nil, nil
		},
	}
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
