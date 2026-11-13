package errcheck

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/buildssa"
)

// retDomOf builds the parent index of the function named fn in pkg and returns its retDom.
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

// errretdomDiags is "line: message" for every diagnostic of errretdom.
func errretdomDiags(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, r := range analysistest.Run(discardT{}, analysistest.TestData(), Analyzer, "errretdom") {
		for _, d := range r.Diagnostics {
			out = append(out, fmt.Sprintf("%d: %s", r.Pass.Fset.Position(d.Pos).Line, d.Message))
		}
	}
	return out
}

// The Recover block is entered only by a recovered panic, and its return reloads the function's
// result cells (go/ssa createRecoverBlock: a load of each named or synthetic result alloc, then a
// Return), which the pair plan already keeps out of any plan. It has no dominator and comes before
// the other returns, so it is left out of the return-dominance walk; a defer does not disable the
// plan. Any other climb to nil would stay nil (parentIndexFor's seenRet).
//
//	F: its early `return 1, nil` precedes the closure call, so the call does not dominate every
//	   normal return and the plan stays rejected: the closure's store is reported (ERR002);
//	G: the call dominates every normal return, so the pair is tracked and nothing is reported.
func TestRecoverBlockIsLeftOutOfReturnDominance(t *testing.T) {
	for _, fn := range []string{"F", "G"} {
		if retDomIsNil(t, "errretdom", fn) {
			t.Errorf("%s: retDom is nil: the Recover block was counted among the returns", fn)
		}
	}
	const gLine = 34 // the line of `func G` in testdata/src/errretdom/a.go
	var onF, onG []string
	for _, d := range errretdomDiags(t) {
		var line int
		fmt.Sscanf(d, "%d:", &line)
		if line < gLine {
			onF = append(onF, d)
		} else {
			onG = append(onG, d)
		}
	}
	if len(onF) != 1 || !strings.Contains(onF[0], "ERR002") {
		t.Errorf("F: want exactly one ERR002 at the closure's store, got %v", onF)
	}
	if len(onG) != 0 {
		t.Errorf("G: want no diagnostic (the plan is accepted), got %v", onG)
	}
}
