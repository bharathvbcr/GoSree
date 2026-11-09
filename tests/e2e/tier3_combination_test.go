package e2e_test

import (
	"testing"
)

// =========================================================================
// Tier 3: Pairwise Cross-Feature Interactions
// =========================================================================

// TestTier3_CrossFeatureCombinations exercises 8 pairwise combinations:
// 1. \iso with second-class borrows
// 2. \imm with methods and read-only borrows
// 3. Fork-join with channel sends and goroutine captures
// 4. Transient stack struct tuples with function parameter borrows
// 5. Multiple goroutines sharing \imm while transferring disjoint \iso tokens
// 6. Method receiver mutable borrow chained with local read-only borrow
// 7. Synchronous closures vs asynchronous goroutine closures
// 8. Channel select statements with \iso send and fallback branches
func TestTier3_CrossFeatureCombinations(t *testing.T) {
	RunTierTest(t, borrowAnalyzer, "t3_combinations", "Milestone 1 & 2")
}
