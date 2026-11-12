package errcheck

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// TestFindingWithNoPositionIsReported: a finding the solver returns for a site that has no position
// used to be skipped, so it read as no finding. It is now reported at the function the solver found
// it in, with a message that says so. Two such findings in one function are two findings, a repeat
// is one, and a finding whose site has a position is reported there as before. A finding on site 0,
// which is no place in the source, is still not reported, and is counted where it was not.
func TestFindingWithNoPositionIsReported(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n\nfunc f() {}\n\nfunc g() {}\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset, types.NewPackage("p", "p"), []*ast.File{file}, ssa.BuilderMode(0))
	if err != nil {
		t.Fatal(err)
	}
	f, g := pkg.Func("f"), pkg.Func("g")
	b := newPackageBuilder(&analysis.Pass{Fset: fset, Files: []*ast.File{file}}, []*ssa.Function{f, g})
	open := b.addSite(token.NoPos, "os.Open", "")
	read := b.addSite(token.NoPos, "io.ReadAll", "")
	placed := b.addSite(g.Pos(), "os.Remove", "")
	summary := &Summary{}
	diags := b.diagnostics([]gusseterr.Finding{
		{FuncIdx: 0, SiteID: open, RuleCode: gusseterr.RuleERR001},
		{FuncIdx: 0, SiteID: read, RuleCode: gusseterr.RuleERR001},
		{FuncIdx: 0, SiteID: open, RuleCode: gusseterr.RuleERR001}, // the first again
		{FuncIdx: 1, SiteID: 0, RuleCode: gusseterr.RuleERR003},    // site 0: on no site of the source
		{FuncIdx: 1, SiteID: placed, RuleCode: gusseterr.RuleERR001},
	}, &annot.Index{}, summary)

	want := []struct {
		pos      token.Pos
		contains string
		located  bool
	}{
		{f.Pos(), "os.Open", false},
		{f.Pos(), "io.ReadAll", false},
		{g.Pos(), "os.Remove", true},
	}
	if len(diags) != len(want) {
		t.Fatalf("%d diagnostics, want %d (two with no position of their own, one with): %v", len(diags), len(want), diags)
	}
	if summary.FindingsUnsited != 1 {
		t.Errorf("FindingsUnsited = %d, want 1: the finding on site 0 is counted", summary.FindingsUnsited)
	}
	for i, w := range want {
		d := diags[i]
		if d.pos != w.pos || !strings.Contains(d.msg, w.contains) || strings.HasSuffix(d.msg, unlocatedNote) == w.located {
			t.Errorf("diagnostic %d is %q at %s; want one about %s at %s, saying it is reported at its function: %v", i, d.msg, fset.Position(d.pos), w.contains, fset.Position(w.pos), !w.located)
		}
	}
}
