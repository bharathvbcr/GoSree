package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
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
	RunTierTest(t, escapeAnalyzer, "t1_escape", "Milestone 2")
}

// AC7: Zero-Cost Comment Directives & Toolchain Compatibility
func TestTier1_DirectivesErasure(t *testing.T) {
	RunTierTest(t, annotAnalyzer, "t1_erasure", "Milestone 3")

	t.Run("ZeroCostAssemblyEquivalence", func(t *testing.T) {
		testdataDir := analysistest.TestData()
		srcPath := filepath.Join(testdataDir, "src", "t1_erasure", "erasure.go")
		content, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", srcPath, err)
		}

		tmpDir := t.TempDir()
		origFile := filepath.Join(tmpDir, "orig.go")
		strippedFile := filepath.Join(tmpDir, "stripped.go")

		if err := os.WriteFile(origFile, content, 0644); err != nil {
			t.Fatal(err)
		}

		// Strip directive comments by replacing directive lines with blank lines
		// to maintain line numbers for bit-for-bit identical opcode and DWARF alignment
		var strippedLines []string
		for _, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//gocap:") || strings.HasPrefix(trimmed, "//safe:") {
				strippedLines = append(strippedLines, "")
				continue
			}
			strippedLines = append(strippedLines, line)
		}
		if err := os.WriteFile(strippedFile, []byte(strings.Join(strippedLines, "\n")), 0644); err != nil {
			t.Fatal(err)
		}

		asmOrig, err := exec.Command("go", "tool", "compile", "-S", "-o", filepath.Join(tmpDir, "orig.o"), origFile).CombinedOutput()
		if err != nil {
			t.Fatalf("compile orig failed: %v", err)
		}
		asmStripped, err := exec.Command("go", "tool", "compile", "-S", "-o", filepath.Join(tmpDir, "stripped.o"), strippedFile).CombinedOutput()
		if err != nil {
			t.Fatalf("compile stripped failed: %v", err)
		}

		// Normalize file paths from assembly output
		normOrig := strings.ReplaceAll(string(asmOrig), origFile, "FILE.go")
		normStripped := strings.ReplaceAll(string(asmStripped), strippedFile, "FILE.go")

		if normOrig != normStripped {
			t.Errorf("zero-cost directive erasure violated: compiled assembly diff detected")
		}
	})
}
