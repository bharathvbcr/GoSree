package errcheck_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// Adversarial review of 4b7ffd3: every closure shape is written next to its no-closure twin (C_x and
// T_x). The markers state what the twin does; a C_x whose diagnostics differ from its twin's is a
// finding.
func TestAdv420dClosurePair(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errclosurepairadv")
}

// bigClosureParent builds one function with n closures, each storing a (v, err) pair into captured
// variables. Even pairs are checked before use; odd pairs are used unchecked (ERR002 at the use).
// The function has a single return, so every closure call dominates it and every closure is
// tracked.
func bigClosureParent(n int) string {
	var b strings.Builder
	b.WriteString(`package big

import "errors"

type T struct{ n int }

var errNo = errors.New("no")

func workV() (*T, error) { return nil, errNo }

func use(t *T) int { return t.n }

func Big() int {
	n := 0
`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\tvar v%[1]d *T\n\tvar e%[1]d error\n\tfunc() { v%[1]d, e%[1]d = workV() }()\n", i)
		if i%2 == 0 {
			fmt.Fprintf(&b, "\tif e%[1]d == nil {\n\t\tn += use(v%[1]d)\n\t}\n", i)
		} else {
			fmt.Fprintf(&b, "\tn += use(v%[1]d) // want \"ERR002\"\n\t_ = e%[1]d\n", i)
		}
	}
	b.WriteString("\treturn n\n}\n")
	return b.String()
}

// Many tracked closures in one parent: the diagnostics stay exact (odd pairs reported at the use,
// even pairs clean) and the run completes. The wall time of each size is logged, not asserted.
func TestAdv420dManyClosures(t *testing.T) {
	for _, n := range []int{250, 500, 1000} {
		dir, cleanup, err := analysistest.WriteFiles(map[string]string{"big/big.go": bigClosureParent(n)})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		analysistest.Run(t, dir, errcheck.Analyzer, "big")
		t.Logf("n=%d closures: %v", n, time.Since(start))
		cleanup()
	}
}

// recordT collects analysistest failures instead of failing the test.
type recordT struct{ msgs []string }

func (r *recordT) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// Past about 1,200 closures in one function the solver refuses the function loudly (GSE1, blocks x
// slots over its cap), with and without the closure plan. The refusal must stay loud at 2,000 to
// 8,000 closures, and lowering them (one plan per closure) must finish; the wall time of the best of
// three runs is logged to show the growth, not asserted.
func TestAdv420dClosurePlanScaling(t *testing.T) {
	for _, n := range []int{2000, 4000, 8000} {
		dir, cleanup, err := analysistest.WriteFiles(map[string]string{"big/big.go": bigClosureParent(n)})
		if err != nil {
			t.Fatal(err)
		}
		best := time.Duration(1 << 62)
		for rep := 0; rep < 3; rep++ {
			rec := &recordT{}
			start := time.Now()
			analysistest.Run(rec, dir, errcheck.Analyzer, "big")
			if d := time.Since(start); d < best {
				best = d
			}
			if len(rec.msgs) == 0 || !strings.Contains(rec.msgs[0], "GSE1 limit exceeded") {
				t.Fatalf("n=%d: want the loud GSE1 refusal first, got %q", n, rec.msgs)
			}
		}
		t.Logf("n=%d closures: best of 3 %v", n, best)
		cleanup()
	}
}

// BREAK (4b7ffd3): a closure that assigns named results moves the obligation to the enclosing
// function, whose Return lowering only checks a literal-nil or blanked error. Every shape here is
// reported without the closure and was reported at the closure's store before the fix. This test
// asserts the correct behavior and fails until the return path accounts for cell-loaded errors.
func TestAdv420dBreakNamedResult(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errclosurepairbreak")
}
