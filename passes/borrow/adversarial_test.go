package borrow_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

func TestAdversarial(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "adversarial")
}

func TestAdversarialCFG(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "adversarial_cfg")
}
