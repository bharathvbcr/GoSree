package errcheck_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// TestPackageOverOldFunctionLimit runs safeerr on one package of more than 20,000 functions (the
// solver used to refuse any package over 16,384: ScholarLM's internal/wisdev with its tests has
// 18,973 and failed the whole run with "func count 18973 exceeds 16384"). Two chains of
// 10,050 functions each return what the next one returns, declared callers first, so a
// summary reaches the head only after crossing every function: the chain ending in a function
// that may return nil reports ERR004 where its head is boxed into an error, the chain ending
// in a fresh pointer reports nothing, and an unrelated dropped error still reports ERR001.
func TestPackageOverOldFunctionLimit(t *testing.T) {
	const chain = 10_050
	var src strings.Builder
	src.WriteString(`package big

type MyErr struct{ Code int }

func (e *MyErr) Error() string { return "my error" }

func mayNil(fail bool) *MyErr {
	if fail {
		return &MyErr{Code: 1}
	}
	return nil
}

func fresh() *MyErr { return &MyErr{Code: 2} }

func fallible() error { return nil }

func dropped() {
	fallible() // want "ERR001"
}

func boxMayNil(fail bool) error {
	return a0(fail) // want "ERR004"
}

func boxFresh() error {
	return b0()
}
`)
	for i := range chain {
		next, nextB := fmt.Sprintf("a%d(fail)", i+1), fmt.Sprintf("b%d()", i+1)
		if i == chain-1 {
			next, nextB = "mayNil(fail)", "fresh()"
		}
		fmt.Fprintf(&src, "\nfunc a%d(fail bool) *MyErr { return %s }\n", i, next)
		fmt.Fprintf(&src, "\nfunc b%d() *MyErr { return %s }\n", i, nextB)
	}
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"big/big.go": src.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	results := analysistest.Run(t, dir, errcheck.Analyzer, "big")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("safeerr failed: %v", results[0].Err)
	}
	sum, ok := results[0].Result.(*errcheck.Summary)
	if !ok {
		t.Fatalf("result is %T, want *errcheck.Summary", results[0].Result)
	}
	if want := 2*chain + 7; sum.FunctionsAnalyzed != want {
		t.Fatalf("FunctionsAnalyzed = %d, want %d", sum.FunctionsAnalyzed, want)
	}
	if sum.FindingsReported != 2 {
		t.Fatalf("FindingsReported = %d, want 2 (one ERR001, one ERR004)", sum.FindingsReported)
	}
}
