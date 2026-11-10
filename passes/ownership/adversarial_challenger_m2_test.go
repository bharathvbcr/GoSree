package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/ownership"
)

func TestAdversarialChallengerM2(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "adversarial_m2")
}
