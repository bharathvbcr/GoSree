package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	gocapBin  string
	buildOnce sync.Once
	buildErr  error
)

func getGocapBin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gocap-test-*")
		if err != nil {
			buildErr = err
			return
		}
		gocapBin = filepath.Join(tmpDir, "gocap")
		cmd := exec.Command("go", "build", "-o", gocapBin, "github.com/bharathvbcr/gocap/cmd/gocap")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("failed to build gocap: %v\nOutput: %s", err, string(out))
		}
	})
	if buildErr != nil {
		t.Fatalf("setup failed: %v", buildErr)
	}
	return gocapBin
}

// TestGocapFlags verifies that -flags returns JSON describing all registered analyzers.
func TestGocapFlags(t *testing.T) {
	bin := getGocapBin(t)
	cmd := exec.Command(bin, "-flags")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to run gocap -flags: %v", err)
	}

	var flags []struct {
		Name  string `json:"Name"`
		Usage string `json:"Usage"`
	}
	if err := json.Unmarshal(out, &flags); err != nil {
		t.Fatalf("invalid JSON from gocap -flags: %v\nOutput: %s", err, string(out))
	}

	expected := map[string]bool{
		"borrow":        false,
		"safeannot":     false,
		"safeerr":       false,
		"safeownership": false,
		"safeunsafe":    false,
	}
	for _, f := range flags {
		if _, ok := expected[f.Name]; ok {
			expected[f.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected analyzer flag -%s not found in gocap -flags output", name)
		}
	}
}

// TestGocapHelp verifies standard multichecker help output.
func TestGocapHelp(t *testing.T) {
	bin := getGocapBin(t)
	cmd := exec.Command(bin, "-help")
	out, _ := cmd.CombinedOutput()
	helpStr := string(out)

	analyzers := []string{"borrow", "safeannot", "safeerr", "safeownership", "safeunsafe"}
	for _, a := range analyzers {
		if !strings.Contains(helpStr, "-"+a) {
			t.Errorf("help output missing flag -%s", a)
		}
	}
}

// TestGocapSafeErrViolation verifies that gocap CLI reports ERR001-ERR005 from safeerr.
func TestGocapSafeErrViolation(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	errPkg := filepath.Join(repoRoot, "passes", "errcheck", "testdata", "src", "errbasic")

	cmd := exec.Command(bin, errPkg)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code on errbasic package, got exit 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Errorf("expected exit code 3, got %v", err)
	}
	outStr := string(out)
	for _, code := range []string{"ERR001", "ERR002", "ERR003", "ERR004", "ERR005"} {
		if !strings.Contains(outStr, code) {
			t.Errorf("expected %s in gocap CLI output, got:\n%s", code, outStr)
		}
	}
}

// TestGocapCleanPackage verifies that clean packages return exit code 0.
func TestGocapCleanPackage(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cleanPkg := filepath.Join(repoRoot, "tests", "e2e", "testdata", "src", "t1_valid")

	cmd := exec.Command(bin, cleanPkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 on clean package, got %v\nOutput: %s", err, string(out))
	}
	if len(bytes.TrimSpace(out)) > 0 {
		t.Errorf("expected empty output for clean package, got: %s", string(out))
	}
}

// TestGocapDiagnosticsReport verifies exit code 3 and diagnostic emission on violations.
func TestGocapDiagnosticsReport(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	violationPkg := filepath.Join(repoRoot, "passes", "borrow", "testdata", "src", "borrows")

	cmd := exec.Command(bin, violationPkg)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code on package with violations, got exit 0")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Errorf("expected exit code 3 (analysis diagnostic findings), got %v", err)
	}

	outStr := string(out)
	expectedCategories := []string{"BORROW001", "BORROW002", "BORROW003", "BORROW004", "SAFE007"}
	for _, cat := range expectedCategories {
		if !strings.Contains(outStr, cat) {
			t.Errorf("expected diagnostic category %s in output, got:\n%s", cat, outStr)
		}
	}
}

// TestGocapFlagDisabling verifies that -<analyzer>=false suppresses that pass's diagnostics.
func TestGocapFlagDisabling(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	violationPkg := filepath.Join(repoRoot, "passes", "borrow", "testdata", "src", "borrows")

	cmd := exec.Command(bin, "-borrow=false", violationPkg)
	out, _ := cmd.CombinedOutput()
	outStr := string(out)

	if strings.Contains(outStr, "BORROW001") || strings.Contains(outStr, "BORROW002") {
		t.Errorf("expected borrow diagnostics to be suppressed with -borrow=false, got:\n%s", outStr)
	}
	// SAFE007 should still be present
	if !strings.Contains(outStr, "SAFE007") {
		t.Errorf("expected SAFE007 to remain reported when only -borrow=false, got:\n%s", outStr)
	}
}

// TestGoVetIntegration verifies vettool execution via go vet.
func TestGoVetIntegration(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cleanPkg := filepath.Join(repoRoot, "tests", "e2e", "testdata", "src", "t1_valid")

	cmd := exec.Command("go", "vet", "-vettool="+bin, cleanPkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet -vettool failed: %v\nOutput: %s", err, string(out))
	}
}

// TestGocapChallengerM4Coverage verifies that gocap CLI catches all BORROW001-004 categories on challenger_m4_coverage.
func TestGocapChallengerM4Coverage(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	pkgPath := filepath.Join(repoRoot, "passes", "borrow", "testdata", "src", "challenger_m4_coverage")

	cmd := exec.Command(bin, pkgPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code on challenger_m4_coverage, got exit 0")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Errorf("expected exit code 3, got %v", err)
	}

	outStr := string(out)
	categories := []string{"BORROW001", "BORROW002", "BORROW003", "BORROW004"}
	for _, cat := range categories {
		if !strings.Contains(outStr, cat) {
			t.Errorf("expected diagnostic category %s in CLI output, got:\n%s", cat, outStr)
		}
	}
}

// TestGocapUnsafebanViolation verifies that gocap CLI reports SAFE001 on unsafe packages.
func TestGocapUnsafebanViolation(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	unsafePkg := filepath.Join(repoRoot, "passes", "unsafeban", "testdata", "src", "u")

	cmd := exec.Command(bin, unsafePkg)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code on unsafe package, got exit 0")
	}

	outStr := string(out)
	if !strings.Contains(outStr, "SAFE001") {
		t.Errorf("expected SAFE001 diagnostic from unsafeban pass, got:\n%s", outStr)
	}
}

// TestGocapMultiPackageInvocation verifies analyzing multiple packages in a single CLI run.
func TestGocapMultiPackageInvocation(t *testing.T) {
	bin := getGocapBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cleanPkg := filepath.Join(repoRoot, "tests", "e2e", "testdata", "src", "t1_valid")
	violationPkg := filepath.Join(repoRoot, "passes", "borrow", "testdata", "src", "borrows")

	cmd := exec.Command(bin, cleanPkg, violationPkg)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected exit code 3 when one of multiple packages has violations")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Errorf("expected exit code 3, got %v", err)
	}

	outStr := string(out)
	if !strings.Contains(outStr, "BORROW001") {
		t.Errorf("expected BORROW001 from second package, got:\n%s", outStr)
	}
}

// TestGocapInvalidFlag verifies that an unrecognized flag returns a non-zero exit code and error.
func TestGocapInvalidFlag(t *testing.T) {
	bin := getGocapBin(t)
	cmd := exec.Command(bin, "-nonexistentflag")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected error for invalid flag, got exit 0")
	}
	outStr := string(out)
	if !strings.Contains(outStr, "flag provided but not defined") {
		t.Errorf("expected flag error message, got:\n%s", outStr)
	}
}
