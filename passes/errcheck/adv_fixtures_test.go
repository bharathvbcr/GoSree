package errcheck_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

func TestAdvRangeFunc(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advrangefunc")
}

// BREAK (H2/H4): a deferred close inside a range-over-func body whose write follows the defer is
// missed when the body falls off its end; the same shape outside a range loop reports.
func TestAdvRangeFuncDeferFallOff(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advrangefuncdefer")
}

func TestAdvDeferEnd(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advdeferend")
}

func TestAdvNilerr(t *testing.T) {
	for _, pkg := range []string{"advnilerr", "advnilerrother", "advnilerrbefore"} {
		t.Run(pkg, func(t *testing.T) {
			analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, pkg)
		})
	}
}

// BREAK (M1): a concrete error pointer captured by a closure is not observed by a field read.
func TestAdvNilerrCapturedPointer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advnilerrcap")
}

// Same-line shapes (the fixture is deliberately not gofmt-ed: several statements per line).
func TestAdvSameLine(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "advsameline")
}
