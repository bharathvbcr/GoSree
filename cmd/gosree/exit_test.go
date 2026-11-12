package main_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	exitTestBinOnce sync.Once
	exitTestBinPath string
	exitTestBinErr  error
)

func getExitGosreeBin(t *testing.T) string {
	t.Helper()
	exitTestBinOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gosree-exit-test-*")
		if err != nil {
			exitTestBinErr = err
			return
		}
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			exitTestBinErr = err
			return
		}
		bin := filepath.Join(tmpDir, "gosree")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/gosree")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			exitTestBinErr = err
			t.Logf("build failed: %s", string(out))
			return
		}
		exitTestBinPath = bin
	})
	if exitTestBinErr != nil {
		t.Fatalf("failed to build gosree binary: %v", exitTestBinErr)
	}
	return exitTestBinPath
}

func runGosree(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	bin := getExitGosreeBin(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
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

func TestExitCode0_Clean(t *testing.T) {
	cleanPkg := "./" + filepath.Join("tests", "e2e", "testdata", "src", "t1_valid")

	// 1. Text mode
	stdout, stderr, code := runGosree(t, cleanPkg)
	if code != 0 {
		t.Fatalf("expected exit code 0 on clean package, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if len(strings.TrimSpace(stdout)) != 0 {
		t.Errorf("expected empty stdout, got: %s", stdout)
	}

	// 2. JSON mode
	stdout, stderr, code = runGosree(t, "-json", cleanPkg)
	if code != 0 {
		t.Fatalf("expected exit code 0 on clean package with -json, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	var tree map[string]any
	if err := json.Unmarshal([]byte(stdout), &tree); err != nil {
		t.Fatalf("invalid json on clean package: %v\noutput: %s", err, stdout)
	}
}

func TestExitCode1_AnalysisFailure(t *testing.T) {
	// 1. Nonexistent package directory
	stdout, stderr, code := runGosree(t, "./does_not_exist_xyz123")
	if code != 1 {
		t.Fatalf("expected exit code 1 on nonexistent package, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "does not exist") && !strings.Contains(stderr, "matched no packages") && !strings.Contains(stderr, "cannot find") && !strings.Contains(stderr, "err") {
		t.Logf("stderr: %s", stderr)
	}

	// 2. Syntax error package in temporary directory
	tmpDir := t.TempDir()
	syntaxFile := filepath.Join(tmpDir, "bad.go")
	if err := os.WriteFile(syntaxFile, []byte("package bad\nfunc broken( {\n"), 0o644); err != nil {
		t.Fatalf("failed to write bad file: %v", err)
	}
	stdout, stderr, code = runGosree(t, syntaxFile)
	if code != 1 {
		t.Fatalf("expected exit code 1 on syntax error, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "syntax error") && !strings.Contains(stderr, "expected") {
		t.Errorf("expected syntax error diagnostic on stderr, got: %s", stderr)
	}

	// 3. Syntax error with -json
	stdout, stderr, code = runGosree(t, "-json", syntaxFile)
	if code != 1 {
		t.Fatalf("expected exit code 1 on syntax error with -json, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestExitCode2_UsageAndBadFlags(t *testing.T) {
	cleanPkg := "./" + filepath.Join("tests", "e2e", "testdata", "src", "t1_valid")

	cases := []struct {
		name        string
		args        []string
		wantStderr  string
		exactStderr bool
	}{
		{
			name:       "no arguments",
			args:       nil,
			wantStderr: "no package arguments provided",
		},
		{
			name:       "flag with no package arguments",
			args:       []string{"-borrow"},
			wantStderr: "no package arguments provided",
		},
		{
			name:       "double dash without package arguments",
			args:       []string{"--"},
			wantStderr: "no package arguments provided",
		},
		{
			name:       "-json with no package arguments",
			args:       []string{"-json"},
			wantStderr: "no package arguments provided",
		},
		{
			name:       "refused flag -fix",
			args:       []string{"-fix", cleanPkg},
			wantStderr: "-fix is not supported",
		},
		{
			name:       "refused flag -diff",
			args:       []string{"-diff", cleanPkg},
			wantStderr: "-diff is not supported",
		},
		{
			name:       "refused flag space-separated -tags",
			args:       []string{"-tags", "tag1 tag2", cleanPkg},
			wantStderr: "-tags takes a comma-separated list",
		},
		{
			name:       "unknown flag",
			args:       []string{"-someinvalidflagxyz", cleanPkg},
			wantStderr: "flag provided but not defined",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runGosree(t, tc.args...)
			if code != 2 {
				t.Fatalf("expected exit code 2, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("expected stderr to contain %q, got:\n%s", tc.wantStderr, stderr)
			}
		})
	}
}

func TestExitCode3_Findings(t *testing.T) {
	cases := []struct {
		name        string
		pkgPath     string
		wantFinding string
	}{
		{
			name:        "borrow rules",
			pkgPath:     "./" + filepath.Join("passes", "borrow", "testdata", "src", "borrows"),
			wantFinding: "BORROW001",
		},
		{
			name:        "safeerr rules",
			pkgPath:     "./" + filepath.Join("passes", "errcheck", "testdata", "src", "errbasic"),
			wantFinding: "ERR001",
		},
		{
			name:        "unsafeban rules",
			pkgPath:     "./" + filepath.Join("passes", "unsafeban", "testdata", "src", "u"),
			wantFinding: "SAFE001",
		},
		{
			name:        "runtime witness sp_w_error",
			pkgPath:     "./" + filepath.Join("tests", "stress", "testdata", "src", "sp_w_error"),
			wantFinding: "SAFE003",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Text mode
			stdout, stderr, code := runGosree(t, tc.pkgPath)
			if code != 3 {
				t.Fatalf("expected exit code 3 on findings, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.wantFinding) {
				t.Errorf("expected %s in stderr, got:\n%s", tc.wantFinding, stderr)
			}

			// JSON mode
			stdout, stderr, code = runGosree(t, "-json", tc.pkgPath)
			if code != 3 {
				t.Fatalf("expected exit code 3 on findings in -json mode, got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, tc.wantFinding) {
				t.Errorf("expected %s in json stdout, got:\n%s", tc.wantFinding, stdout)
			}
			var tree map[string]any
			if err := json.Unmarshal([]byte(stdout), &tree); err != nil {
				t.Fatalf("invalid json output: %v\noutput: %s", err, stdout)
			}
		})
	}
}

func TestExitMetaCommands(t *testing.T) {
	metaCases := []struct {
		name       string
		args       []string
		wantStdout string
	}{
		{
			name:       "-flags query",
			args:       []string{"-flags"},
			wantStdout: "borrow",
		},
		{
			name:       "-V=full query",
			args:       []string{"-V=full"},
			wantStdout: "buildID=",
		},
		{
			name:       "-help query",
			args:       []string{"-help"},
			wantStdout: "", // printed to stderr by flag package
		},
		{
			name:       "help subcommand",
			args:       []string{"help"},
			wantStdout: "",
		},
	}

	for _, tc := range metaCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runGosree(t, tc.args...)
			if code != 0 {
				t.Fatalf("expected exit code 0 for meta command %v, got %d\nstdout: %s\nstderr: %s", tc.args, code, stdout, stderr)
			}
			if tc.wantStdout != "" && !strings.Contains(stdout, tc.wantStdout) {
				t.Errorf("expected stdout to contain %q, got:\n%s", tc.wantStdout, stdout)
			}
		})
	}
}
