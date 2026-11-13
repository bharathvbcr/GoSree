package errcheck

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

type discardT struct{}

func (discardT) Errorf(string, ...any) {}

// closurePlanWork lowers one function holding n tracked closures (and one return per closure, so
// the parent has n return blocks) and returns the plan work counter. The solver may refuse the
// function past its size cap; the counter is read before solving.
func closurePlanWork(t *testing.T, n int) uint64 {
	t.Helper()
	var b strings.Builder
	b.WriteString("package big\n\nimport \"errors\"\n\ntype T struct{ n int }\n\nfunc workV() (*T, error) { return nil, errors.New(\"x\") }\n\nfunc use(t *T) int { return t.n }\n\nfunc Big(c []bool) int {\n\tn := 0\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\tvar v%[1]d *T\n\tvar e%[1]d error\n\tfunc() { v%[1]d, e%[1]d = workV() }()\n\tif e%[1]d == nil {\n\t\tn += use(v%[1]d)\n\t}\n\tif c[%[1]d] {\n\t\treturn n\n\t}\n", i)
	}
	b.WriteString("\treturn n\n}\n")
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"big/big.go": b.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var work uint64
	closurePlanWorkHook = func(w uint64) { work = w }
	defer func() { closurePlanWorkHook = nil }()
	analysistest.Run(discardT{}, dir, Analyzer, "big")
	return work
}

// Building the plans costs the size of the closures, not closures times the size of the parent: the
// work per closure is the same constant at 500, 2,000 and 8,000 closures in one parent.
func TestClosurePlanWorkIsLinear(t *testing.T) {
	var per []uint64
	for _, n := range []int{500, 2000, 8000} {
		w := closurePlanWork(t, n)
		if w == 0 {
			t.Fatalf("n=%d: no plan work recorded", n)
		}
		t.Logf("n=%d closures: plan work %d (%d per closure)", n, w, w/uint64(n))
		per = append(per, w/uint64(n))
	}
	for i := 1; i < len(per); i++ {
		if per[i] > per[0]+per[0]/10 {
			t.Fatalf("work per closure grows with the parent: %v", per)
		}
	}
}
