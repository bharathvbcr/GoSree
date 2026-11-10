package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/ownership"
)

func TestChallengerM2Gate4Imm(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "challenger_m2_gate4_imm")
}
