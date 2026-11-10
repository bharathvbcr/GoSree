// Command gocap checks Go packages for ownership and memory-safety violations.
//
//	gocap ./...
//	go vet -vettool=$(command -v gocap) ./...
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/bharathvbcr/gocap/internal/annot"
	"github.com/bharathvbcr/gocap/passes/borrow"
	"github.com/bharathvbcr/gocap/passes/ownership"
	"github.com/bharathvbcr/gocap/passes/unsafeban"
)

func main() {
	multichecker.Main(annot.Analyzer, unsafeban.Analyzer, ownership.Analyzer, borrow.Analyzer)
}
