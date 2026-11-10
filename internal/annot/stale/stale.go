// Package stale reports //gosree:unsafe escape hatches that mute no finding of any rule (SAFE007).
//
// Whether a hatch is stale is known only once every pass that can mute through it has run on the
// package, so this analyzer requires all of them; the analysis driver runs it after they finish. It
// must be listed with them wherever they run (cmd/gosree, the stress harness).
package stale

import (
	"reflect"

	"golang.org/x/tools/go/analysis"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/passes/borrow"
	"github.com/bharathvbcr/gosree/passes/errcheck"
	"github.com/bharathvbcr/gosree/passes/ownership"
	"github.com/bharathvbcr/gosree/passes/unsafeban"
)

// Analyzer reports stale escape hatches and returns them, in source order, as a []annot.Hatch: the
// stale count a harness should use, since it is taken after every muting pass. Its Requires list is
// every analyzer that mutes findings through annot.Index.Mute; a new muting pass must be added to it.
var Analyzer = &analysis.Analyzer{
	Name:     "safestale",
	Doc:      "report //gosree:unsafe escape hatches that mute no finding of any rule (" + annot.CategoryMalformed + ")",
	Requires: []*analysis.Analyzer{annot.Analyzer, unsafeban.Analyzer, ownership.Analyzer, borrow.Analyzer, errcheck.Analyzer},
	Run: func(pass *analysis.Pass) (any, error) {
		ix := pass.ResultOf[annot.Analyzer].(*annot.Index)
		ix.ReportStale(pass)
		return ix.StaleHatches(), nil
	},
	ResultType: reflect.TypeFor[[]annot.Hatch](),
}
