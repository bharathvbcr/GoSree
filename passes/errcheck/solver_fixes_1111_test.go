package errcheck_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// TestLoopPhiTypedNilReachesReturn is the H1 regression: a typed-nil pointer produced inside a
// loop reaches the loop-header phi only through the back edge, so the header must be re-solved
// when that edge state changes. Both engines must report ERR004 on the return.
func TestLoopPhiTypedNilReachesReturn(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

type MyErr struct{ Code int }

func (e *MyErr) Error() string { return "my error" }

func lookup(k string) *MyErr {
	if k == "" {
		return nil
	}
	return &MyErr{Code: 1}
}

func loopPhi(keys []string) error {
	e := &MyErr{}
	for _, k := range keys {
		e = lookup(k)
	}
	return e // want "ERR004"
}

func loopPhiAlwaysNonNil(keys []string) error {
	e := &MyErr{}
	for range keys {
		e = &MyErr{Code: 2}
	}
	return e
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, errcheck.Analyzer, "p")
}

// TestTupleAssignedOnBothBranchesThenOneCheck is the H3 regression (docs/RULES.md, ERR002: a
// use is rejected only on a CFG path where err == nil has not been established). Each branch
// binds the same (value, err) variables from its own call; the single check after the join
// establishes err == nil for whichever call ran, so the use is clean.
func TestTupleAssignedOnBothBranchesThenOneCheck(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

import "errors"

type item struct{ Value int }

func fetchA() (*item, error) { return &item{Value: 1}, nil }
func fetchB() (*item, error) { return nil, errors.New("b") }

func bothBranchesOneCheck(c bool) int {
	var it *item
	var err error
	if c {
		it, err = fetchA()
	} else {
		it, err = fetchB()
	}
	if err != nil {
		return 0
	}
	return it.Value
}

// Two binds on ONE path: on the retry path f's error is never proven nil, so the use is still
// rejected. Merging the two tuples would hide it.
func retryOverwritesErr(retry bool) int {
	a, err := fetchA()
	if retry {
		_, err = fetchB() // want "ERR003"
	}
	if err != nil {
		return 0
	}
	return a.Value // want "ERR002"
}

func bothBranchesNoCheck(c bool) int {
	var it *item
	var err error
	if c {
		it, err = fetchA()
	} else {
		it, err = fetchB()
	}
	_ = err
	return it.Value // want "ERR002"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, errcheck.Analyzer, "p")
}

type recordingT struct{ errs []string }

func (r *recordingT) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// TestOversizedFunctionFailsThePackageWithAnError: one function of N sequential
// `if err := step(i); err != nil { return err }` lowers to 2N+1 blocks and 2N+1 slots, so the
// solver state grows as N^2. N=1500 is 3,001 x 3,001 = 9.0M cells, over the 2^23 budget; the
// package must fail with an error naming the budget (never run out of memory), and a function
// just under it must still be analyzed.
func TestOversizedFunctionFailsThePackageWithAnError(t *testing.T) {
	build := func(n int) string {
		var b strings.Builder
		b.WriteString("package p\n\nfunc step(i int) error { return nil }\n\nfunc F() error {\n")
		for i := range n {
			fmt.Fprintf(&b, "\tif err := step(%d); err != nil {\n\t\treturn err\n\t}\n", i)
		}
		b.WriteString("\treturn nil\n}\n")
		return b.String()
	}

	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"big/p.go": build(1500)})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	rec := &recordingT{}
	analysistest.Run(rec, dir, errcheck.Analyzer, "big")
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], "safeerr solver failed") || !strings.Contains(rec.errs[0], "state cells") {
		t.Fatalf("N=1500: errors = %q, want one 'safeerr solver failed ... state cells' error", rec.errs)
	}

	dir2, cleanup2, err := analysistest.WriteFiles(map[string]string{"ok/p.go": build(300)})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	rec2 := &recordingT{}
	analysistest.Run(rec2, dir2, errcheck.Analyzer, "ok")
	if len(rec2.errs) != 0 {
		t.Fatalf("N=300 must be analyzed cleanly, got %q", rec2.errs)
	}
}
