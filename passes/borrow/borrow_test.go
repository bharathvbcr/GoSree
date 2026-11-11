package borrow_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

func TestBorrows(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "borrows")
}

// TestRecursiveTypesTerminate runs the storage checker over values whose types
// refer to themselves without a pointer in the cycle. Before canHoldBorrow kept a
// seen set it recursed until the goroutine stack overflowed (found on ScholarLM).
func TestRecursiveTypesTerminate(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), borrow.Analyzer, "recursive_types")
}
