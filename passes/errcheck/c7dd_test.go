package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// ft-c7dd: `_, _ = w.Write(nil)` blanks the error explicitly, so a loop must not report it as an
// overwritten unchecked error; a real overwrite in a loop is still reported.
func TestC7ddBlankDiscardInLoop(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errc7ddblank")
}
