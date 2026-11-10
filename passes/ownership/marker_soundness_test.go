package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

func TestMarkerSoundness(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "marker_ownership")
}
