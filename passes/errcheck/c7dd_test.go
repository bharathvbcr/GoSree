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

// ft-c7dd: the all-blank discard against the shapes an adversary tried: the same line as a real
// overwrite, nested calls, multi-line calls, a closure in a loop, generics, a method value,
// `_, _ = g(), g()`, and the real `_, err =` overwrites that must still report.
func TestC7ddBlankDiscardAdversary(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errc7ddblankadv")
}

// ft-8f3c: a deferred Close is skipped when the function ends in os.Exit or log.Fatal*, so ERR005
// is not reported on those paths; panic, runtime.Goexit, log.Panic* and every normal return keep it.
func TestExitSkipsDeferredClose(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errexitdefer")
}
