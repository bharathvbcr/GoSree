package borrow_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/borrow"
)

func TestBorrows(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "borrows")
}
