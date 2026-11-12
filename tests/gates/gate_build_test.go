package gates_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoBuildGateCleanPackages(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	// 1. Build clean library package (t1_valid)
	t.Run("clean library package t1_valid", func(t *testing.T) {
		tmpDir := t.TempDir()
		outArchive := filepath.Join(tmpDir, "t1_valid.a")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-o", outArchive, "./tests/e2e/testdata/src/t1_valid")
		if res.code != 0 {
			t.Fatalf("expected build exit 0 on clean package, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
		if fi, err := os.Stat(outArchive); err != nil || fi.Size() == 0 {
			t.Fatalf("expected output archive %s to exist with non-zero size, err: %v", outArchive, err)
		}
	})

	// 2. Build clean executable main package
	t.Run("clean main package executable", func(t *testing.T) {
		tmpDir := t.TempDir()
		cleanSrc := filepath.Join(tmpDir, "main.go")
		if err := os.WriteFile(cleanSrc, []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"gates pass\")\n}\n"), 0o644); err != nil {
			t.Fatalf("failed to write clean main.go: %v", err)
		}
		outBin := filepath.Join(tmpDir, "clean_app")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-o", outBin, cleanSrc)
		if res.code != 0 {
			t.Fatalf("expected build exit 0 on clean main, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
		if fi, err := os.Stat(outBin); err != nil || fi.Size() == 0 {
			t.Fatalf("expected output executable %s to exist with non-zero size, err: %v", outBin, err)
		}

		// Run the compiled binary
		runRes := runCommand(t, tmpDir, nil, outBin)
		if runRes.code != 0 || !strings.Contains(runRes.stdout, "gates pass") {
			t.Fatalf("clean binary execution failed: code=%d, stdout=%s, stderr=%s", runRes.code, runRes.stdout, runRes.stderr)
		}
	})
}

func TestGoBuildGateHaltsOnConcurrencyWitnesses(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	witnesses := []struct {
		name        string
		relPath     string
		wantFinding string
	}{
		{
			name:        "witness sp_w_error",
			relPath:     "./tests/stress/testdata/src/sp_w_error",
			wantFinding: "SAFE003",
		},
		{
			name:        "witness rs_torn_slice",
			relPath:     "./tests/stress/testdata/src/rs_torn_slice",
			wantFinding: "SAFE005",
		},
		{
			name:        "witness rs_torn_iface",
			relPath:     "./tests/stress/testdata/src/rs_torn_iface",
			wantFinding: "SAFE005",
		},
		{
			name:        "witness rs_w_mapwrites",
			relPath:     "./tests/stress/testdata/src/rs_w_mapwrites",
			wantFinding: "SAFE003",
		},
	}

	for _, w := range witnesses {
		t.Run(w.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			outBin := filepath.Join(tmpDir, "should_not_exist")

			res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
				"go", "build", "-toolexec="+gateBin, "-a", "-o", outBin, w.relPath)

			// Gate verification: build MUST fail with non-zero exit code
			if res.code == 0 {
				t.Fatalf("gate breached: expected build failure on %s, got exit 0", w.name)
			}

			// Diagnostics verification: error message must be surfaced on stderr
			combinedErr := res.stdout + res.stderr
			if !strings.Contains(combinedErr, w.wantFinding) {
				t.Errorf("expected diagnostic %s in output, got:\n%s", w.wantFinding, combinedErr)
			}

			// Object suppression verification: binary MUST NOT be created
			if _, err := os.Stat(outBin); !os.IsNotExist(err) {
				t.Fatalf("gate breached: output binary %s was created despite safety violations!", outBin)
			}
		})
	}
}

func TestGoBuildGateHaltsOnRuleViolations(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	rulePackages := []struct {
		name        string
		relPath     string
		wantFinding string
	}{
		{
			name:        "unsafeban u",
			relPath:     "./passes/unsafeban/testdata/src/u",
			wantFinding: "SAFE001",
		},
		{
			name:        "borrow rules",
			relPath:     "./passes/borrow/testdata/src/borrows",
			wantFinding: "BORROW",
		},
		{
			name:        "errcheck rules",
			relPath:     "./passes/errcheck/testdata/src/errbasic",
			wantFinding: "ERR001",
		},
	}

	for _, rp := range rulePackages {
		t.Run(rp.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			outArchive := filepath.Join(tmpDir, "should_not_exist.a")

			res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
				"go", "build", "-toolexec="+gateBin, "-a", "-o", outArchive, rp.relPath)

			// Gate verification: build MUST fail with non-zero exit code
			if res.code == 0 {
				t.Fatalf("gate breached: expected build failure on %s, got exit 0", rp.name)
			}

			// Diagnostics verification: rule code must be surfaced
			combinedErr := res.stdout + res.stderr
			if !strings.Contains(combinedErr, rp.wantFinding) {
				t.Errorf("expected diagnostic %s in output, got:\n%s", rp.wantFinding, combinedErr)
			}

			// Object suppression verification: archive MUST NOT be created
			if _, err := os.Stat(outArchive); !os.IsNotExist(err) {
				t.Fatalf("gate breached: output archive %s was created despite rule violations!", outArchive)
			}
		})
	}
}

// TestGoBuildGateEvasionResistance verifies that go build -toolexec cannot be bypassed
// using compiler flag injection such as -gcflags="-std".
func TestGoBuildGateEvasionResistance(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	// 1. Evasion attempt via -gcflags="-std" on rule violation (SAFE001)
	t.Run("evasion_gcflags_std_rule_violation_halt", func(t *testing.T) {
		tmpDir := t.TempDir()
		targetArchive := filepath.Join(tmpDir, "evaded_u.a")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetArchive, "./passes/unsafeban/testdata/src/u")

		if res.code == 0 {
			t.Fatalf("gate breached: expected build failure with -gcflags=-std, got exit 0")
		}
		combined := res.stdout + res.stderr
		if !strings.Contains(combined, "SAFE001") {
			t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", combined)
		}
		if _, err := os.Stat(targetArchive); !os.IsNotExist(err) {
			t.Fatalf("gate breached: target archive %s was created despite SAFE001 violation via -gcflags=-std!", targetArchive)
		}
	})

	// 2. Evasion attempt via -gcflags="-std" on concurrency witness (SAFE003)
	t.Run("evasion_gcflags_std_concurrency_witness_halt", func(t *testing.T) {
		tmpDir := t.TempDir()
		targetBin := filepath.Join(tmpDir, "evaded_sp")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetBin, "./tests/stress/testdata/src/sp_w_error")

		if res.code == 0 {
			t.Fatalf("gate breached: expected build failure with -gcflags=-std, got exit 0")
		}
		combined := res.stdout + res.stderr
		if !strings.Contains(combined, "SAFE003") {
			t.Errorf("expected SAFE003 diagnostic on stderr, got:\n%s", combined)
		}
		if _, err := os.Stat(targetBin); !os.IsNotExist(err) {
			t.Fatalf("gate breached: target binary %s was created despite SAFE003 violation via -gcflags=-std!", targetBin)
		}
	})

	// 3. Regression test: clean package with -gcflags="-std" builds successfully
	t.Run("clean_package_gcflags_std_pass", func(t *testing.T) {
		tmpDir := t.TempDir()
		targetArchive := filepath.Join(tmpDir, "clean_std.a")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetArchive, "./tests/e2e/testdata/src/t1_valid")

		if res.code != 0 {
			t.Fatalf("expected build exit 0 on clean package with -gcflags=-std, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
		if fi, err := os.Stat(targetArchive); err != nil || fi.Size() == 0 {
			t.Fatalf("expected clean archive %s to exist with non-zero size", targetArchive)
		}
	})

	// 4. Evasion attempt: mixed files with stdlib symlink and -gcflags="-std"
	t.Run("evasion_symlink_mixed_file_halt", func(t *testing.T) {
		gorootOut, err := exec.Command("go", "env", "GOROOT").CombinedOutput()
		if err != nil {
			t.Fatalf("failed to get GOROOT: %v", err)
		}
		goroot := strings.TrimSpace(string(gorootOut))
		stdFile := filepath.Join(goroot, "src", "cmd", "cover", "doc.go")
		if _, err := os.Stat(stdFile); err != nil {
			t.Skipf("stdlib file %s not found: %v", stdFile, err)
		}

		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module attackmixed\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		badCode := `package main

import "unsafe"

func main() {
	var v int
	_ = unsafe.Pointer(&v)
}
`
		if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(badCode), 0o644); err != nil {
			t.Fatal(err)
		}

		// Symlink stdlib file into user directory
		if err := os.Symlink(stdFile, filepath.Join(tmpDir, "doc.go")); err != nil {
			t.Fatal(err)
		}

		targetBin := filepath.Join(tmpDir, "evaded_mixed_bin")
		res := runCommand(t, tmpDir, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetBin, ".")

		if res.code == 0 {
			t.Fatalf("gate breached: symlink stdlib evasion succeeded with exit code 0!")
		}
		combined := res.stdout + res.stderr
		if !strings.Contains(combined, "SAFE001") {
			t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", combined)
		}
		if _, err := os.Stat(targetBin); !os.IsNotExist(err) {
			t.Fatalf("gate breached: target binary %s was created despite SAFE001 violation via symlink evasion!", targetBin)
		}
	})
}
