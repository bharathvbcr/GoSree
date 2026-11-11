package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

func TestRangeOverFuncBodies(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errrangefunc")
}

func TestDeferredCloseOnImplicitReturn(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errdeferend")
}

func TestNilerrConcreteErrorPointerObserved(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errnilerrptr")
}

func TestSameLineFallbackDoesNotMisattribute(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errsameline")
}

func TestShadowReadWindow(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errshadowwin")
}

func TestRangeFuncBodyDeferredClose(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errrangedefer")
}
