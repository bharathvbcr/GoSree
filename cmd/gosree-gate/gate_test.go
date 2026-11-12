package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	builtGateOnce sync.Once
	gateBinPath   string
	gosreeBinPath string
	buildGateErr  error
)

func getGateAndGosreeBins(t *testing.T) (string, string) {
	t.Helper()
	builtGateOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gosree-gate-test-*")
		if err != nil {
			buildGateErr = err
			return
		}
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			buildGateErr = err
			return
		}
		gosreeBinPath = filepath.Join(tmpDir, "gosree")
		gateBinPath = filepath.Join(tmpDir, "gosree-gate")

		// Build gosree
		cmd1 := exec.Command("go", "build", "-o", gosreeBinPath, "./cmd/gosree")
		cmd1.Dir = repoRoot
		if out, err := cmd1.CombinedOutput(); err != nil {
			buildGateErr = err
			t.Logf("build gosree failed: %s", string(out))
			return
		}

		// Build gosree-gate
		cmd2 := exec.Command("go", "build", "-o", gateBinPath, "./cmd/gosree-gate")
		cmd2.Dir = repoRoot
		if out, err := cmd2.CombinedOutput(); err != nil {
			buildGateErr = err
			t.Logf("build gosree-gate failed: %s", string(out))
			return
		}
	})
	if buildGateErr != nil {
		t.Fatalf("setup failed: %v", buildGateErr)
	}
	return gateBinPath, gosreeBinPath
}

func getGoCompileTool(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "tool", "-n", "compile").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to locate go tool compile: %v\noutput: %s", err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func runGate(t *testing.T, env []string, dir string, args ...string) (string, string, int) {
	t.Helper()
	gateBin, gosreeBin := getGateAndGosreeBins(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, gateBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GOSREE_BIN="+gosreeBin)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("command failed to start: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func TestGateNoArgs(t *testing.T) {
	_, stderr, code := runGate(t, nil, "")
	if code != 2 {
		t.Fatalf("expected exit code 2 on empty invocation, got %d", code)
	}
	if !strings.Contains(stderr, "Usage: gosree-gate") {
		t.Errorf("expected usage message on stderr, got: %s", stderr)
	}
}

func TestGatePassthroughNonCompile(t *testing.T) {
	stdout, stderr, code := runGate(t, nil, "", "echo", "passthrough test")
	if code != 0 {
		t.Fatalf("expected exit 0 for echo passthrough, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "passthrough test") {
		t.Errorf("expected echo output on stdout, got: %s", stdout)
	}
}

func TestGatePassthroughCompilerVersion(t *testing.T) {
	compileTool := getGoCompileTool(t)
	// Query version directly from compiler
	wantOut, err := exec.Command(compileTool, "-V=full").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to run compile -V=full: %v", err)
	}

	stdout, stderr, code := runGate(t, nil, "", compileTool, "-V=full")
	if code != 0 {
		t.Fatalf("expected exit 0 for compile -V=full, got %d\nstderr: %s", code, stderr)
	}
	if stdout != string(wantOut) {
		t.Errorf("version output mismatch:\ngot:  %q\nwant: %q", stdout, string(wantOut))
	}
}

func TestGatePassthroughStdlib(t *testing.T) {
	compileTool := getGoCompileTool(t)
	// Invocations with -std must not be intercepted by static analysis
	stdout, stderr, code := runGate(t, nil, "", compileTool, "-std", "-V=full")
	if code != 0 {
		t.Fatalf("expected exit 0 for -std compile passthrough, got %d\nstderr: %s\nstdout: %s", code, stderr, stdout)
	}
}

func TestGateInterceptHaltOnFindings(t *testing.T) {
	compileTool := getGoCompileTool(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	tmpDir := t.TempDir()
	outObj := filepath.Join(tmpDir, "_pkg_.a")
	vulnSource := filepath.Join(repoRoot, "passes", "unsafeban", "testdata", "src", "u", "u.go")

	_, stderr, code := runGate(t, nil, repoRoot, compileTool, "-o", outObj, "-p", "u", vulnSource)
	if code != 3 {
		t.Fatalf("expected exit code 3 (halt on findings), got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "SAFE001") {
		t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", stderr)
	}

	// Verify that the compiler was aborted BEFORE creating the object file
	if _, err := os.Stat(outObj); !os.IsNotExist(err) {
		t.Errorf("expected object file %s to NOT be created, but it exists", outObj)
	}
}

func TestGateCleanPackagePasses(t *testing.T) {
	compileTool := getGoCompileTool(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	tmpDir := t.TempDir()
	cleanFile := filepath.Join(tmpDir, "clean.go")
	if err := os.WriteFile(cleanFile, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("failed to write clean file: %v", err)
	}

	outObj := filepath.Join(tmpDir, "_pkg_.a")
	_, stderr, code := runGate(t, nil, repoRoot, compileTool, "-p", "main", "-complete", "-pack", "-o", outObj, cleanFile)
	if code != 0 {
		t.Fatalf("expected clean package to compile and exit 0, got %d\nstderr: %s", code, stderr)
	}

	// Verify that the compiler ran and emitted the object file
	if fi, err := os.Stat(outObj); err != nil || fi.Size() == 0 {
		t.Errorf("expected object file %s to exist with non-zero size, err: %v", outObj, err)
	}
}

// TestGateEvasionStdFlagRejectedOnUserCode verifies that passing -std to the compiler
// tool invocation does not bypass static analysis when compiling user code.
func TestGateEvasionStdFlagRejectedOnUserCode(t *testing.T) {
	compileTool := getGoCompileTool(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	tmpDir := t.TempDir()
	outObj := filepath.Join(tmpDir, "_pkg_.a")
	vulnSource := filepath.Join(repoRoot, "passes", "unsafeban", "testdata", "src", "u", "u.go")

	// Direct compiler invocation with -std on a user source file MUST NOT bypass analysis
	_, stderr, code := runGate(t, nil, repoRoot, compileTool, "-std", "-o", outObj, "-p", "u", vulnSource)
	if code != 3 {
		t.Fatalf("gate breached: expected exit code 3 (halt on findings) with -std, got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "SAFE001") {
		t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", stderr)
	}

	// Verify that the compiler was aborted BEFORE creating the object file
	if _, err := os.Stat(outObj); !os.IsNotExist(err) {
		t.Errorf("gate breached: object file %s was created despite SAFE001 violation with -std!", outObj)
	}
}

// TestGateStdFlagCleanUserPackage verifies that passing -std to a clean user package
// does not cause a crash or false rejection.
func TestGateStdFlagCleanUserPackage(t *testing.T) {
	compileTool := getGoCompileTool(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	tmpDir := t.TempDir()
	cleanFile := filepath.Join(tmpDir, "clean.go")
	if err := os.WriteFile(cleanFile, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("failed to write clean file: %v", err)
	}

	outObj := filepath.Join(tmpDir, "_pkg_.a")
	_, stderr, code := runGate(t, nil, repoRoot, compileTool, "-std", "-p", "main", "-complete", "-pack", "-o", outObj, cleanFile)
	if code != 0 {
		t.Fatalf("expected clean package to compile with -std and exit 0, got %d\nstderr: %s", code, stderr)
	}
	if fi, err := os.Stat(outObj); err != nil || fi.Size() == 0 {
		t.Errorf("expected object file %s to exist with non-zero size, err: %v", outObj, err)
	}
}

// TestFileOriginatesInDir_SymlinkOutsideDir verifies that a file or symlink
// residing outside a directory is never considered to originate within that directory,
// even if the symlink resolves to a target inside the directory.
func TestFileOriginatesInDir_SymlinkOutsideDir(t *testing.T) {
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Skip("GOROOT not available")
	}

	tmpDir := t.TempDir()

	// 1. Literal file outside GOROOT
	userFile := filepath.Join(tmpDir, "user.go")
	if err := os.WriteFile(userFile, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fileOriginatesInDir(userFile, goroot) {
		t.Errorf("fileOriginatesInDir returned true for user file outside GOROOT: %s", userFile)
	}

	// 2. Symlink in user directory pointing to a genuine stdlib file in GOROOT
	stdFile := filepath.Join(goroot, "src", "fmt", "print.go")
	if _, err := os.Stat(stdFile); err != nil {
		t.Skipf("stdlib file %s not found: %v", stdFile, err)
	}
	symlinkFile := filepath.Join(tmpDir, "symlink_print.go")
	if err := os.Symlink(stdFile, symlinkFile); err != nil {
		t.Fatal(err)
	}
	if fileOriginatesInDir(symlinkFile, goroot) {
		t.Errorf("fileOriginatesInDir returned true for symlink outside GOROOT: %s -> %s", symlinkFile, stdFile)
	}

	// 3. Genuine stdlib file inside GOROOT must return true
	if !fileOriginatesInDir(stdFile, goroot) {
		t.Errorf("fileOriginatesInDir returned false for genuine stdlib file in GOROOT: %s", stdFile)
	}
}

// TestIsStandardLibrary_PureGoUnitTests tests isStandardLibrary with pure Go compilation units.
func TestIsStandardLibrary_PureGoUnitTests(t *testing.T) {
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Skip("GOROOT not available")
	}

	tmpDir := t.TempDir()
	stdFile := filepath.Join(goroot, "src", "fmt", "print.go")
	if _, err := os.Stat(stdFile); err != nil {
		t.Skipf("stdlib file %s not found: %v", stdFile, err)
	}

	userFile := filepath.Join(tmpDir, "user.go")
	if err := os.WriteFile(userFile, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	symlinkFile := filepath.Join(tmpDir, "print_symlink.go")
	if err := os.Symlink(stdFile, symlinkFile); err != nil {
		t.Fatal(err)
	}

	stdArgs := []string{"compile", "-std", "-p", "fmt"}
	nonStdArgs := []string{"compile", "-p", "fmt"}

	// Case 1: Empty goFiles returns false
	if isStandardLibrary(stdArgs, nil) {
		t.Error("expected isStandardLibrary to return false for empty goFiles")
	}

	// Case 2: Genuine stdlib file without -std returns false
	if isStandardLibrary(nonStdArgs, []string{stdFile}) {
		t.Error("expected isStandardLibrary to return false when -std is absent")
	}

	// Case 3: Genuine stdlib file with -std returns true
	if !isStandardLibrary(stdArgs, []string{stdFile}) {
		t.Error("expected isStandardLibrary to return true for genuine stdlib file with -std")
	}

	// Case 4: User file outside GOROOT with -std returns false
	if isStandardLibrary(stdArgs, []string{userFile}) {
		t.Error("expected isStandardLibrary to return false for user file outside GOROOT with -std")
	}

	// Case 5: Symlink in user directory pointing to stdlib file with -std returns false
	if isStandardLibrary(stdArgs, []string{symlinkFile}) {
		t.Error("expected isStandardLibrary to return false for symlink outside GOROOT with -std")
	}

	// Case 6: Mixed files (genuine stdlib file + user file) with -std returns false
	if isStandardLibrary(stdArgs, []string{stdFile, userFile}) {
		t.Error("expected isStandardLibrary to return false for mixed stdlib and user files")
	}

	// Case 7: Mixed files (genuine stdlib file + symlink) with -std returns false
	if isStandardLibrary(stdArgs, []string{stdFile, symlinkFile}) {
		t.Error("expected isStandardLibrary to return false for mixed stdlib and symlink files")
	}

	// Case 8: Mixed files (user file + symlink) with -std returns false
	if isStandardLibrary(stdArgs, []string{userFile, symlinkFile}) {
		t.Error("expected isStandardLibrary to return false for mixed user file and symlink")
	}
}

// TestGateSymlinkMixedFileStdlibEvasionHalt verifies that a compilation unit combining
// user code with a symlink to a stdlib file and -std is halted before object emission.
func TestGateSymlinkMixedFileStdlibEvasionHalt(t *testing.T) {
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Skip("GOROOT not available")
	}
	compileTool := getGoCompileTool(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	tmpDir := t.TempDir()
	vulnSource := filepath.Join(tmpDir, "bad.go")
	vulnCode := `package main

import "unsafe"

func main() {
	var x int
	_ = unsafe.Pointer(&x)
}
`
	if err := os.WriteFile(vulnSource, []byte(vulnCode), 0o644); err != nil {
		t.Fatal(err)
	}

	// Symlink to a stdlib main package file
	docStdFile := filepath.Join(goroot, "src", "cmd", "cover", "doc.go")
	if _, err := os.Stat(docStdFile); err != nil {
		t.Skipf("stdlib file %s not found: %v", docStdFile, err)
	}
	symlinkFile := filepath.Join(tmpDir, "doc.go")
	if err := os.Symlink(docStdFile, symlinkFile); err != nil {
		t.Fatal(err)
	}

	outObj := filepath.Join(tmpDir, "_pkg_.a")
	_, stderr, code := runGate(t, nil, repoRoot, compileTool, "-std", "-o", outObj, "-p", "main", vulnSource, symlinkFile)
	if code != 3 {
		t.Fatalf("gate breached: expected exit code 3 (halt on findings), got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "SAFE001") {
		t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", stderr)
	}

	// Verify that the compiler was aborted BEFORE creating the object file
	if _, err := os.Stat(outObj); !os.IsNotExist(err) {
		t.Errorf("gate breached: object file %s was created despite SAFE001 violation with symlink stdlib evasion!", outObj)
	}
}
