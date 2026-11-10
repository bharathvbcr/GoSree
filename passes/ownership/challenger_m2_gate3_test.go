package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

func TestChallengerM2Gate3Adversarial(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "challenger_m2_gate3")
}
