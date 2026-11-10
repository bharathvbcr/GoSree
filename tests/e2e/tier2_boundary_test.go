package e2e_test

import (
	"testing"
)

// =========================================================================
// Tier 2: Boundary & Corner Cases
// =========================================================================

// TestTier2_BoundaryAndCornerCases exercises the 25 edge and boundary test cases
// spanning empty structs, loops, defers, multiple returns, interface boxing,
// method receivers, and slice index borrows.
func TestTier2_BoundaryAndCornerCases(t *testing.T) {
	// Evaluates under compositeAnalyzer when M2 lands; falls back for move/imm sub-cases
	RunTierTest(t, compositeAnalyzer, "t2_boundaries", "Milestone 2")
}
