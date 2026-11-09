package unsafeban_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/unsafeban"
)

func TestUnsafeBan(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), unsafeban.Analyzer, "u")
}
