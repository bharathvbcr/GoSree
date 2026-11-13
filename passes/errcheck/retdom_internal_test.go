package errcheck

import (
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/buildssa"
)

// retDomOf lowers nothing: it builds the parent index of the function named fn in pkg and returns
// whether its retDom is nil.
func retDomIsNil(t *testing.T, pkg, fn string) bool {
	t.Helper()
	var got, seen bool
	probe := &analysis.Analyzer{
		Name:     "retdomprobe",
		Doc:      "records whether the parent index of a function has a return dominator",
		Requires: []*analysis.Analyzer{buildssa.Analyzer},
		Run: func(pass *analysis.Pass) (any, error) {
			res := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
			for _, f := range res.SrcFuncs {
				if f.Name() == fn {
					b := newPackageBuilder(pass, res.SrcFuncs)
					got, seen = b.parentIndexFor(f).retDom == nil, true
				}
			}
			return nil, nil
		},
	}
	analysistest.Run(t, analysistest.TestData(), probe, pkg)
	if !seen {
		t.Fatalf("function %s not found", fn)
	}
	return got
}

// A function with a defer has a Recover block that ends in a return, has no dominator and comes
// before the other return blocks. No block dominates every return then, so retDom must be nil and
// stay nil: resetting it at the next return block let a call that one return path skips pass for
// one that dominates every return.
func TestParentIndexRetDomIsStickyNil(t *testing.T) {
	if !retDomIsNil(t, "errretdom", "F") {
		t.Fatal("retDom is not nil for a function whose Recover block precedes its other returns")
	}
}
