package borrow_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

func TestChallengerM1Gate4(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "challenger_m1_gate4")
}
