package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// BREAK (9244bb2): the written-flag reset on a store through a captured variable loses the true
// ERR005 when the stored value is itself written (w = other after other.Write). Asserts the correct
// behavior, so it fails until the reset accounts for the stored value.
func TestAdv23b3cStoreOfWrittenValue(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "adv2fvbreak")
}

// PRE-EXISTING and HOLDS shapes for stores through a captured variable; see the fixture comments.
func TestAdv23b3cStoreShapes(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "adv2fvstore", "adv2fvstore2")
}

// ERR001-ERR004 around captured error variables are unchanged by the reset.
func TestAdv23b3cCapturedErrRules(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "adv2fverr")
}
