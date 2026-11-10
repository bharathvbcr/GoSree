package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestChallengerM4AdversarialCoverage executes white-box adversarial coverage hardening
// tests covering recursive/mutually recursive immutability, switch/select goroutines,
// multi-branch select cases, channels passing closures/capabilities, and CFG phi edges.
func TestChallengerM4AdversarialCoverage(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "challenger_m4")
}
