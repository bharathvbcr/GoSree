package gates_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var (
	builtBinsOnce sync.Once
	gosreeBin     string
	gateBin       string
	buildBinsErr  error
)

func getRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	return root
}

func getGateAndGosree(t *testing.T) (string, string) {
	t.Helper()
	builtBinsOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gosree-gates-integ-*")
		if err != nil {
			buildBinsErr = err
			return
		}
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			buildBinsErr = err
			return
		}
		gosreeBin = filepath.Join(tmpDir, "gosree")
		gateBin = filepath.Join(tmpDir, "gosree-gate")

		// Build gosree
		cmd1 := exec.Command("go", "build", "-o", gosreeBin, "./cmd/gosree")
		cmd1.Dir = repoRoot
		if out, err := cmd1.CombinedOutput(); err != nil {
			buildBinsErr = fmt.Errorf("failed to build gosree: %v\n%s", err, string(out))
			return
		}

		// Build gosree-gate
		cmd2 := exec.Command("go", "build", "-o", gateBin, "./cmd/gosree-gate")
		cmd2.Dir = repoRoot
		if out, err := cmd2.CombinedOutput(); err != nil {
			buildBinsErr = fmt.Errorf("failed to build gosree-gate: %v\n%s", err, string(out))
			return
		}
	})
	if buildBinsErr != nil {
		t.Fatalf("setup failed: %v", buildBinsErr)
	}
	return gateBin, gosreeBin
}

type cmdResult struct {
	stdout string
	stderr string
	code   int
}

func runCommand(t *testing.T, dir string, env []string, name string, args ...string) cmdResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), env...)
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
	return cmdResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   code,
	}
}
