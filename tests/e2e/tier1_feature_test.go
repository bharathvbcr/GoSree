package e2e_test

import (
	"testing"
)

// =========================================================================
// Tier 1: Feature Coverage (AC1 – AC7)
// =========================================================================

// AC1: Borrows in Struct Fields or Global Variables (BORROW001)
func TestTier1_StorageConstraints(t *testing.T) {
	RunTierTest(t, borrowAnalyzer, "t1_storage", "Milestone 1")
}

// AC2: Conflicting Borrows & Aliasing (BORROW002)
func TestTier1_ConflictingBorrows(t *testing.T) {
	RunTierTest(t, borrowAnalyzer, "t1_conflicts", "Milestone 1")
}

// AC2: Owner Mutation / Move Invalidation (BORROW003) & Dangling Stack Return (BORROW004)
func TestTier1_OwnerAndDangling(t *testing.T) {
	RunTierTest(t, borrowAnalyzer, "t1_owner_dangling", "Milestone 1")
}

// AC3: Valid Second-Class Borrows Local to Scope & NLL Expiry (Clean Pass)
func TestTier1_ValidBorrowsNLL(t *testing.T) {
	RunTierTest(t, borrowAnalyzer, "t1_valid", "Milestone 1")
}

// AC4: \iso Reference Capability Across Channels & Goroutines (SAFE003)
func TestTier1_IsoMoveSemantics(t *testing.T) {
	RunTierTest(t, ownershipAnalyzer, "t1_iso", "Milestone 2")
}

// AC4: Fork-Join Synchronization Recovery (WaitGroup & Channel Joins)
func TestTier1_ForkJoinRecovery(t *testing.T) {
	RunTierTest(t, ownershipAnalyzer, "t1_forkjoin", "Milestone 2")
}

// AC5: \imm Deep Immutability Verification & Lock-Free Sharing (SAFE005)
func TestTier1_ImmDeepImmutability(t *testing.T) {
	RunTierTest(t, ownershipAnalyzer, "t1_imm", "Milestone 2")
}

// AC6: Stack-Scoped Borrow Goroutine & Channel Escape Ban (CAP003)
func TestTier1_StackBorrowEscape(t *testing.T) {
	RunTierTest(t, borrowAnalyzer, "t1_escape", "Milestone 2")
}

// AC7: Zero-Cost Comment Directives & Toolchain Compatibility
func TestTier1_DirectivesErasure(t *testing.T) {
	RunTierTest(t, annotAnalyzer, "t1_erasure", "Milestone 3")
}
