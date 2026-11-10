// Package borrow implements second-class borrow checking and local SSA non-lexical lifetime analysis.
package borrow

import (
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// Summary holds the results of the borrow checker pass.
type Summary struct {
	StorageViolations int
	Conflicts         int
}

// Analyzer checks for second-class borrow storage constraints and local SSA borrow conflicts with NLL.
var Analyzer = &analysis.Analyzer{
	Name:       "borrow",
	Doc:        "check second-class borrows and local SSA non-lexical lifetimes (BORROW001-BORROW004)",
	Run:        run,
	Requires:   []*analysis.Analyzer{annot.Analyzer, buildssa.Analyzer},
	ResultType: reflect.TypeFor[*Summary](),
}

func run(pass *analysis.Pass) (any, error) {
	ix := pass.ResultOf[annot.Analyzer].(*annot.Index)
	ssaProg := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)

	CheckStorage(pass, ix, ssaProg)
	CheckBorrows(pass, ix, ssaProg)

	return &Summary{}, nil
}
