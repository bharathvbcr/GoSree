package stale_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/annot/stale"
)

// TestStaleOwnershipSupp runs the stale analyzer over the ownership pass's supp fixture, which
// declares eight //gosree:unsafe scopes: six mute an ownership finding, rawPointer's mutes a SAFE001
// (unsafeban), and cleanTransferInScope's mutes nothing. Exactly that one is stale. The fixture's // want
// lines are for the ownership pass, so it is loaded directly instead of through analysistest.
func TestStaleOwnershipSupp(t *testing.T) {
	gopath, err := filepath.Abs(filepath.Join("..", "..", "..", "passes", "ownership", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  filepath.Join(gopath, "src", "supp"),
		Env:  append(os.Environ(), "GOPATH="+gopath, "GO111MODULE=off", "GOPROXY=off", "GOWORK=off"),
	}
	pkgs, err := packages.Load(cfg, "supp")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("supp fixture did not load")
	}
	graph, err := checker.Analyze([]*analysis.Analyzer{stale.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Roots) != 1 || graph.Roots[0].Err != nil {
		t.Fatalf("stale analysis failed: %v", graph.Roots)
	}
	hatches := graph.Roots[0].Result.([]annot.Hatch)
	if len(hatches) != 1 {
		t.Fatalf("stale hatches in supp: got %d, want 1 (cleanTransferInScope)", len(hatches))
	}
	if got := pkgs[0].Fset.Position(hatches[0].Pos); !containsFunc(pkgs[0], got.Line, "cleanTransferInScope") {
		t.Errorf("stale hatch at %s, want the one in cleanTransferInScope", got)
	}
}

// containsFunc reports whether line lies inside the function name declared in pkg.
func containsFunc(pkg *packages.Package, line int, name string) bool {
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
				start, end := pkg.Fset.Position(fn.Pos()).Line, pkg.Fset.Position(fn.End()).Line
				if line >= start && line <= end {
					return true
				}
			}
		}
	}
	return false
}

// TestStale checks that a hatch is stale only when no pass muted a finding through it: hatches that
// mute an unsafeban, ownership, borrow-conflict or borrow-storage finding are not stale.
func TestStale(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), stale.Analyzer, "st")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := len(results[0].Result.([]annot.Hatch)); got != 1 {
		t.Errorf("stale hatches: got %d, want 1 (plain)", got)
	}
}
