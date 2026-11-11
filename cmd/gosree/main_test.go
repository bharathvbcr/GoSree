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
	gosreeBin string
	buildOnce sync.Once
	buildErr  error
)

func getGosreeBin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gosree-test-*")
		if err != nil {
			buildErr = err
			return
		}
		gosreeBin = filepath.Join(tmpDir, "gosree")
		cmd := exec.Command("go", "build", "-o", gosreeBin, "github.com/bharathvbcr/gosree/cmd/gosree")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("failed to build gosree: %v\nOutput: %s", err, string(out))
		}
	})
	if buildErr != nil {
		t.Fatalf("setup failed: %v", buildErr)
	}
	return gosreeBin
}

// TestGosreeFlags verifies that -flags returns JSON describing all registered analyzers.
func TestGosreeFlags(t *testing.T) {
	bin := getGosreeBin(t)
	cmd := exec.Command(bin, "-flags")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to run gosree -flags: %v", err)
	}

	var flags []struct {
		Name  string `json:"Name"`
		Usage string `json:"Usage"`
	}
	if err := json.Unmarshal(out, &flags); err != nil {
		t.Fatalf("invalid JSON from gosree -flags: %v\nOutput: %s", err, string(out))
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
			t.Errorf("expected analyzer flag -%s not found in gosree -flags output", name)
		}
	}
}

// TestGosreeHelp verifies standard multichecker help output.
func TestGosreeHelp(t *testing.T) {
	bin := getGosreeBin(t)
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

// TestGosreeSafeErrViolation verifies that gosree CLI reports ERR001-ERR005 from safeerr.
func TestGosreeSafeErrViolation(t *testing.T) {
	bin := getGosreeBin(t)
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
			t.Errorf("expected %s in gosree CLI output, got:\n%s", code, outStr)
		}
	}
}

// TestGosreeCleanPackage verifies that clean packages return exit code 0.
func TestGosreeCleanPackage(t *testing.T) {
	bin := getGosreeBin(t)
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

// TestGosreeDiagnosticsReport verifies exit code 3 and diagnostic emission on violations.
func TestGosreeDiagnosticsReport(t *testing.T) {
	bin := getGosreeBin(t)
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

// TestGosreeFlagDisabling verifies that -<analyzer>=false suppresses that pass's diagnostics.
func TestGosreeFlagDisabling(t *testing.T) {
	bin := getGosreeBin(t)
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
	bin := getGosreeBin(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cleanPkg := filepath.Join(repoRoot, "tests", "e2e", "testdata", "src", "t1_valid")

	cmd := exec.Command("go", "vet", "-vettool="+bin, cleanPkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet -vettool failed: %v\nOutput: %s", err, string(out))
	}
}

// TestGosreeChallengerM4Coverage verifies that gosree CLI catches all BORROW001-004 categories on challenger_m4_coverage.
func TestGosreeChallengerM4Coverage(t *testing.T) {
	bin := getGosreeBin(t)
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

// TestGosreeUnsafebanViolation verifies that gosree CLI reports SAFE001 on unsafe packages.
func TestGosreeUnsafebanViolation(t *testing.T) {
	bin := getGosreeBin(t)
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

// TestGosreeMultiPackageInvocation verifies analyzing multiple packages in a single CLI run.
func TestGosreeMultiPackageInvocation(t *testing.T) {
	bin := getGosreeBin(t)
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

// TestGosreeInvalidFlag verifies that an unrecognized flag returns a non-zero exit code and error.
func TestGosreeInvalidFlag(t *testing.T) {
	bin := getGosreeBin(t)
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

func TestGoSreeCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gosree")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build gosree: %v\n%s", err, string(out))
	}

	cmd := exec.Command(bin, "-flags")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gosree -flags failed: %v", err)
	}
	var flags []struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(out, &flags); err != nil {
		t.Fatalf("unmarshal -flags output: %v", err)
	}
	want := map[string]bool{
		"borrow":        false,
		"safeannot":     false,
		"safeerr":       false,
		"safeownership": false,
		"safeunsafe":    false,
	}
	for _, f := range flags {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("gosree missing analyzer flag -%s", name)
		}
	}
}

func TestGosreeJSONDedupTestVariant(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gosree")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build gosree: %v\n%s", err, string(out))
	}

	modDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module testdedup\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgSrc := `package b

var G *int

func Set(p *int) { G = p }
`
	testSrc := `package b

import "testing"

func TestInVariant(t *testing.T) {
	Set(new(int))
	x := new(int)
	ch := make(chan *int, 1)
	ch <- x
	*x = 1
}
`
	if err := os.WriteFile(filepath.Join(modDir, "b.go"), []byte(pkgSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "b_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-json", "./...")
	cmd.Dir = modDir
	out, err := cmd.Output()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 3 {
		t.Fatalf("expected exit status 3, got err=%v\noutput:\n%s", err, string(out))
	}

	var tree map[string]map[string][]struct {
		Posn    string `json:"posn"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out, &tree); err != nil {
		t.Fatalf("unmarshal -json output: %v\n%s", err, string(out))
	}

	counts := map[string]int{}
	hasBGo := false
	hasBTest := false
	for _, byAnalyzer := range tree {
		for _, diags := range byAnalyzer {
			for _, d := range diags {
				key := d.Posn + "|" + d.Message
				counts[key]++
				if strings.Contains(d.Posn, "b.go:") && strings.HasPrefix(d.Message, "SAFE006:") {
					hasBGo = true
				}
				if strings.Contains(d.Posn, "b_test.go:") && strings.HasPrefix(d.Message, "SAFE003:") {
					hasBTest = true
				}
			}
		}
	}
	if !hasBGo {
		t.Errorf("missing b.go SAFE006 finding in -json output:\n%s", string(out))
	}
	if !hasBTest {
		t.Errorf("missing b_test.go SAFE003 finding in -json output:\n%s", string(out))
	}
	for k, n := range counts {
		if n > 1 {
			t.Errorf("duplicate finding emitted %d times in -json output: %s", n, k)
		}
	}
}
