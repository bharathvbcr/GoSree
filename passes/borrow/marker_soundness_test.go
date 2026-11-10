package borrow_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

func TestMarkerSoundness(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "marker_soundness")
}
