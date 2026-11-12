package gates_test

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestChallengerBuildGateStrictHalt verifies that under go build -toolexec,
// any compilation unit containing errors or safety witnesses is strictly halted
// and that NO .a archive or binary is ever emitted to disk.
func TestChallengerBuildGateStrictHalt(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	testCases := []struct {
		name        string
		relPkg      string
		wantFinding string
	}{
		{
			name:        "unsafeban_SAFE001",
			relPkg:      "./passes/unsafeban/testdata/src/u",
			wantFinding: "SAFE001",
		},
		{
			name:        "borrow_BORROW001",
			relPkg:      "./passes/borrow/testdata/src/borrows",
			wantFinding: "BORROW",
		},
		{
			name:        "errcheck_ERR001",
			relPkg:      "./passes/errcheck/testdata/src/errbasic",
			wantFinding: "ERR001",
		},
		{
			name:        "witness_sp_w_error_SAFE003",
			relPkg:      "./tests/stress/testdata/src/sp_w_error",
			wantFinding: "SAFE003",
		},
		{
			name:        "witness_rs_torn_slice_SAFE005",
			relPkg:      "./tests/stress/testdata/src/rs_torn_slice",
			wantFinding: "SAFE005",
		},
		{
			name:        "witness_rs_torn_iface_SAFE005",
			relPkg:      "./tests/stress/testdata/src/rs_torn_iface",
			wantFinding: "SAFE005",
		},
		{
			name:        "witness_rs_w_mapwrites_SAFE003",
			relPkg:      "./tests/stress/testdata/src/rs_w_mapwrites",
			wantFinding: "SAFE003",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			targetArchive := filepath.Join(tmpDir, tc.name+".a")

			res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
				"go", "build", "-toolexec="+gateBin, "-a", "-o", targetArchive, tc.relPkg)

			if res.code == 0 {
				t.Fatalf("BREACH: expected non-zero exit code for %s, got 0", tc.name)
			}

			combined := res.stdout + res.stderr
			if !strings.Contains(combined, tc.wantFinding) {
				t.Errorf("expected finding %q in output, got:\n%s", tc.wantFinding, combined)
			}

			// FORENSIC VETO: Target archive MUST NOT exist on disk
			if fi, err := os.Stat(targetArchive); !os.IsNotExist(err) {
				t.Fatalf("CRITICAL BREACH: output archive %s was emitted despite findings! (size=%d)", targetArchive, fi.Size())
			}
		})
	}
}

// TestChallengerBuildGateCleanPassThrough verifies that clean packages build completely
// without obstruction and that the emitted binary executes successfully.
func TestChallengerBuildGateCleanPassThrough(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	// Clean library package
	t.Run("clean_library", func(t *testing.T) {
		tmpDir := t.TempDir()
		outArchive := filepath.Join(tmpDir, "libclean.a")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-o", outArchive, "./tests/e2e/testdata/src/t1_valid")
		if res.code != 0 {
			t.Fatalf("expected exit 0 for clean library, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
		if fi, err := os.Stat(outArchive); err != nil || fi.Size() == 0 {
			t.Fatalf("expected non-empty archive at %s", outArchive)
		}
	})

	// Clean executable with multi-file and imports
	t.Run("clean_executable_multifile", func(t *testing.T) {
		tmpDir := t.TempDir()
		file1 := filepath.Join(tmpDir, "main.go")
		file2 := filepath.Join(tmpDir, "helper.go")

		src1 := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(Message())\n}\n"
		src2 := "package main\n\nfunc Message() string {\n\treturn \"CHALLENGER_CLEAN_OK\"\n}\n"

		if err := os.WriteFile(file1, []byte(src1), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file2, []byte(src2), 0o644); err != nil {
			t.Fatal(err)
		}

		outBin := filepath.Join(tmpDir, "app")
		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-o", outBin, file1, file2)
		if res.code != 0 {
			t.Fatalf("expected exit 0 for clean executable, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}

		runRes := runCommand(t, tmpDir, nil, outBin)
		if runRes.code != 0 || !strings.Contains(runRes.stdout, "CHALLENGER_CLEAN_OK") {
			t.Fatalf("clean binary failed execution: code=%d, stdout=%s", runRes.code, runRes.stdout)
		}
	})
}

// TestChallengerInProcessFallback tests that when GOSREE_BIN is unset and no adjacent
// gosree binary is present, gosree-gate correctly falls back to in-process analysis
// and still halts compilation on vulnerable code.
func TestChallengerInProcessFallback(t *testing.T) {
	gateBin, _ := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	// Copy gosree-gate to an isolated directory without gosree adjacent
	isoDir := t.TempDir()
	isoGateBin := filepath.Join(isoDir, "gosree-gate-isolated")
	src, err := os.Open(gateBin)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(isoGateBin, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	dst.Close()

	// 1. In-process analysis must halt vulnerable package
	tmpDir := t.TempDir()
	targetArchive := filepath.Join(tmpDir, "vuln.a")
	// Clean environment with GOSREE_BIN stripped and PATH containing go toolchain but no gosree
	goBinDir := filepath.Dir(exec.Command("go").Path)
	if goBinDir == "" || goBinDir == "." {
		if p, err := exec.LookPath("go"); err == nil {
			goBinDir = filepath.Dir(p)
		}
	}
	env := []string{
		"GOSREE_BIN=",
		"PATH=" + goBinDir + ":/usr/bin:/bin:/usr/sbin:/sbin",
	}

	res := runCommand(t, repoRoot, env,
		"go", "build", "-toolexec="+isoGateBin, "-a", "-o", targetArchive, "./passes/unsafeban/testdata/src/u")

	if res.code == 0 {
		t.Fatalf("BREACH: in-process fallback failed to halt compilation on vulnerable package, exit=0")
	}
	if !strings.Contains(res.stdout+res.stderr, "SAFE001") {
		t.Errorf("expected SAFE001 in in-process fallback output, got:\n%s\n%s", res.stdout, res.stderr)
	}
	if _, err := os.Stat(targetArchive); !os.IsNotExist(err) {
		t.Fatalf("BREACH: archive emitted under in-process fallback mode!")
	}

	// 2. In-process analysis must pass clean package
	cleanArchive := filepath.Join(tmpDir, "clean.a")
	cleanRes := runCommand(t, repoRoot, env,
		"go", "build", "-toolexec="+isoGateBin, "-a", "-o", cleanArchive, "./tests/e2e/testdata/src/t1_valid")
	if cleanRes.code != 0 {
		t.Fatalf("expected in-process fallback to pass clean package, got %d\nstdout: %s\nstderr: %s", cleanRes.code, cleanRes.stdout, cleanRes.stderr)
	}
	if fi, err := os.Stat(cleanArchive); err != nil || fi.Size() == 0 {
		t.Fatalf("expected clean archive to be emitted under in-process fallback")
	}
}

// TestChallengerExitCodes_Comprehensive verifies all CLI exit code contracts:
// 0: Clean run
// 1: Failure / unparseable / error (outranks findings)
// 2: Refused flags / invalid args
// 3: Findings detected (both text and json)
func TestChallengerExitCodes_Comprehensive(t *testing.T) {
	_, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	// --- EXIT CODE 0 ---
	t.Run("exit_0_clean", func(t *testing.T) {
		res := runCommand(t, repoRoot, nil, gosreeBin, "./tests/e2e/testdata/src/t1_valid")
		if res.code != 0 {
			t.Fatalf("expected exit 0, got %d\nstderr: %s", res.code, res.stderr)
		}
	})

	t.Run("exit_0_meta_V", func(t *testing.T) {
		res := runCommand(t, repoRoot, nil, gosreeBin, "-V=full")
		if res.code != 0 || !strings.Contains(res.stdout, "buildID=") {
			t.Fatalf("expected exit 0 for -V=full, got %d\nstdout: %s", res.code, res.stdout)
		}
	})

	t.Run("exit_0_meta_flags", func(t *testing.T) {
		res := runCommand(t, repoRoot, nil, gosreeBin, "-flags")
		if res.code != 0 || !strings.Contains(res.stdout, "borrow") {
			t.Fatalf("expected exit 0 for -flags, got %d\nstdout: %s", res.code, res.stdout)
		}
	})

	// --- EXIT CODE 1 (Failure outranks findings) ---
	t.Run("exit_1_syntax_error", func(t *testing.T) {
		tmpDir := t.TempDir()
		badFile := filepath.Join(tmpDir, "syntax.go")
		if err := os.WriteFile(badFile, []byte("package bad\nfunc f() {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := runCommand(t, repoRoot, nil, gosreeBin, badFile)
		if res.code != 1 {
			t.Fatalf("expected exit 1 on syntax error, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
	})

	t.Run("exit_1_type_error", func(t *testing.T) {
		tmpDir := t.TempDir()
		typeBadFile := filepath.Join(tmpDir, "typeerr.go")
		if err := os.WriteFile(typeBadFile, []byte("package bad\nvar _ = undefinedSymbolXYZ\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res := runCommand(t, repoRoot, nil, gosreeBin, typeBadFile)
		if res.code != 1 {
			t.Fatalf("expected exit 1 on type error, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
	})

	t.Run("exit_1_outranks_exit_3", func(t *testing.T) {
		// When analyzing BOTH a package with safety findings AND a package with syntax error,
		// exit code 1 MUST outrank exit code 3.
		tmpDir := t.TempDir()
		badFile := filepath.Join(tmpDir, "bad.go")
		if err := os.WriteFile(badFile, []byte("package bad\nfunc broken( {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		vulnPkg := "./passes/unsafeban/testdata/src/u"

		res := runCommand(t, repoRoot, nil, gosreeBin, vulnPkg, badFile)
		if res.code != 1 {
			t.Fatalf("CONTRACT VIOLATION: exit 1 must outrank exit 3! Got exit %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
	})

	// --- EXIT CODE 2 (Usage / Refused flags) ---
	usageCases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"empty_args", nil, "no package arguments"},
		{"flag_only_borrow", []string{"-borrow"}, "no package arguments"},
		{"double_dash_only", []string{"--"}, "no package arguments"},
		{"refused_fix", []string{"-fix", "./tests/e2e/testdata/src/t1_valid"}, "-fix is not supported"},
		{"refused_diff", []string{"-diff", "./tests/e2e/testdata/src/t1_valid"}, "-diff is not supported"},
		{"refused_tags_space", []string{"-tags", "t1 t2", "./tests/e2e/testdata/src/t1_valid"}, "-tags takes a comma-separated list"},
		{"unknown_flag", []string{"-unknown-flag-adversarial", "./tests/e2e/testdata/src/t1_valid"}, "flag provided but not defined"},
	}

	for _, uc := range usageCases {
		t.Run("exit_2_"+uc.name, func(t *testing.T) {
			res := runCommand(t, repoRoot, nil, gosreeBin, uc.args...)
			if res.code != 2 {
				t.Fatalf("expected exit 2 for %s, got %d\nstdout: %s\nstderr: %s", uc.name, res.code, res.stdout, res.stderr)
			}
			if !strings.Contains(res.stderr, uc.wantStderr) {
				t.Errorf("expected %q in stderr, got:\n%s", uc.wantStderr, res.stderr)
			}
		})
	}

	// --- EXIT CODE 3 (Findings Detected) ---
	t.Run("exit_3_findings_text_and_json", func(t *testing.T) {
		vulnPkg := "./passes/unsafeban/testdata/src/u"

		// Text mode
		resText := runCommand(t, repoRoot, nil, gosreeBin, vulnPkg)
		if resText.code != 3 {
			t.Fatalf("expected exit 3 on findings, got %d\nstderr: %s", resText.code, resText.stderr)
		}
		if !strings.Contains(resText.stderr, "SAFE001") {
			t.Errorf("expected SAFE001 on stderr, got:\n%s", resText.stderr)
		}

		// JSON mode
		resJSON := runCommand(t, repoRoot, nil, gosreeBin, "-json", vulnPkg)
		if resJSON.code != 3 {
			t.Fatalf("expected exit 3 on findings with -json, got %d\nstdout: %s\nstderr: %s", resJSON.code, resJSON.stdout, resJSON.stderr)
		}
		var tree map[string]any
		if err := json.Unmarshal([]byte(resJSON.stdout), &tree); err != nil {
			t.Fatalf("expected valid json output with -json, err: %v\noutput: %s", err, resJSON.stdout)
		}
		if !strings.Contains(resJSON.stdout, "SAFE001") {
			t.Errorf("expected SAFE001 in json stdout, got:\n%s", resJSON.stdout)
		}
	})
}

// TestChallengerEvasionAttempts stress-tests potential evasion attacks.
func TestChallengerEvasionAttempts(t *testing.T) {
	gateBin, gosreeBin := getGateAndGosree(t)
	repoRoot := getRepoRoot(t)

	// Attack: Attempt to evade using .cfg directory confusion
	t.Run("evasion_cfg_directory_confusion", func(t *testing.T) {
		tmpDir := t.TempDir()
		goModFile := filepath.Join(tmpDir, "go.mod")
		if err := os.WriteFile(goModFile, []byte("module fakecfg\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfgDir := filepath.Join(tmpDir, "fake.cfg")
		if err := os.Mkdir(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		vulnFile := filepath.Join(cfgDir, "fake.go")
		// Write code that violates unsafeban SAFE001
		src := "package fake\n\nimport \"unsafe\"\n\nfunc Leak() {\n\t_ = unsafe.Pointer(uintptr(0))\n}\n"
		if err := os.WriteFile(vulnFile, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}

		// Run gosree from inside tmpDir targeting relative directory ./fake.cfg
		res := runCommand(t, tmpDir, nil, gosreeBin, "./fake.cfg")
		// Should NOT be treated as unitchecker .cfg; must detect SAFE001 and exit 3
		if res.code != 3 {
			t.Fatalf("evasion succeeded or failed incorrectly: expected exit 3 on directory named .cfg, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
		if !strings.Contains(res.stderr, "SAFE001") {
			t.Errorf("expected SAFE001 finding, got: %s", res.stderr)
		}
	})

	// Attack: Double-dash argument evasion
	t.Run("evasion_double_dash_separation", func(t *testing.T) {
		vulnPkg := "./passes/unsafeban/testdata/src/u"
		res := runCommand(t, repoRoot, nil, gosreeBin, "--", vulnPkg)
		if res.code != 3 {
			t.Fatalf("double-dash evasion bypassed findings! Expected exit 3, got %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
		}
	})

	// Attack: Disabling analysis passes warns user
	t.Run("evasion_pass_disabling_warning", func(t *testing.T) {
		vulnPkg := "./passes/unsafeban/testdata/src/u"
		res := runCommand(t, repoRoot, nil, gosreeBin, "-safeunsafe=false", vulnPkg)
		// Since safeunsafe was disabled, warning must be printed on stderr
		if !strings.Contains(res.stderr, "WARNING") || !strings.Contains(res.stderr, "switched off") {
			t.Errorf("expected warning on stderr when disabling pass, got:\n%s", res.stderr)
		}
	})

	// Attack: go build -toolexec with synthetic wrapper
	t.Run("synthetic_wrapper_diagnostics", func(t *testing.T) {
		tmpDir := t.TempDir()
		wrapSrc := filepath.Join(tmpDir, "wrap.go")
		// Wrapper embedding struct with method
		code := `package main

import "unsafe"

type Inner struct{}
func (Inner) Bad() {
	_ = unsafe.Pointer(uintptr(0))
}

type Outer struct {
	Inner
}

func main() {
	var o Outer
	o.Bad()
}
`
		if err := os.WriteFile(wrapSrc, []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
		targetBin := filepath.Join(tmpDir, "wrap_app")

		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-o", targetBin, wrapSrc)

		if res.code == 0 {
			t.Fatalf("gate breached: synthetic wrapper violation was not halted!")
		}
		if _, err := os.Stat(targetBin); !os.IsNotExist(err) {
			t.Fatalf("output binary emitted despite synthetic wrapper violation!")
		}
	})

	// Attack: -gcflags="-std" evasion attempt to bypass build gate
	t.Run("evasion_gcflags_std_bypass", func(t *testing.T) {
		tmpDir := t.TempDir()
		targetBin := filepath.Join(tmpDir, "evaded_app")
		vulnPkg := "./passes/unsafeban/testdata/src/u"

		res := runCommand(t, repoRoot, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetBin, vulnPkg)

		// The gate MUST NOT allow -gcflags="-std" on user packages to bypass safety enforcement!
		if res.code == 0 {
			t.Fatalf("CRITICAL EVASION VULNERABILITY: -gcflags=-std bypassed gosree-gate! Exit code was 0.")
		}
		if _, err := os.Stat(targetBin); !os.IsNotExist(err) {
			t.Fatalf("CRITICAL EVASION VULNERABILITY: binary %s was emitted despite SAFE001 violation via -gcflags=-std!", targetBin)
		}
	})

	// Attack: -gcflags="-std" mixed-file symlink evasion attempt for library package (SAFE001)
	t.Run("evasion_gcflags_std_symlink_archive_halt", func(t *testing.T) {
		gorootOut, err := exec.Command("go", "env", "GOROOT").CombinedOutput()
		if err != nil {
			t.Fatalf("failed to get GOROOT: %v", err)
		}
		goroot := strings.TrimSpace(string(gorootOut))
		stdCmpFile := filepath.Join(goroot, "src", "cmp", "cmp.go")
		if _, err := os.Stat(stdCmpFile); err != nil {
			t.Skipf("stdlib file %s not found: %v", stdCmpFile, err)
		}

		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module attackcmp\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		badSrc := `package cmp

import "unsafe"

func Exploit() {
	var x int
	_ = unsafe.Pointer(&x)
}
`
		if err := os.WriteFile(filepath.Join(tmpDir, "bad.go"), []byte(badSrc), 0o644); err != nil {
			t.Fatal(err)
		}

		// Symlink standard library cmp.go
		if err := os.Symlink(stdCmpFile, filepath.Join(tmpDir, "cmp.go")); err != nil {
			t.Fatal(err)
		}

		targetArchive := filepath.Join(tmpDir, "evaded_cmp.a")
		res := runCommand(t, tmpDir, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetArchive, ".")

		if res.code == 0 {
			t.Fatalf("CRITICAL EVASION VULNERABILITY: symlink stdlib evasion bypassed gosree-gate with exit code 0!")
		}
		combined := res.stdout + res.stderr
		if !strings.Contains(combined, "SAFE001") {
			t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", combined)
		}
		if _, err := os.Stat(targetArchive); !os.IsNotExist(err) {
			t.Fatalf("CRITICAL EVASION VULNERABILITY: archive %s was emitted despite SAFE001 violation via symlink evasion!", targetArchive)
		}
	})

	// Attack: -gcflags="-std" mixed-file symlink evasion attempt for executable binary (SAFE003)
	t.Run("evasion_gcflags_std_symlink_race_witness_binary_halt", func(t *testing.T) {
		gorootOut, err := exec.Command("go", "env", "GOROOT").CombinedOutput()
		if err != nil {
			t.Fatalf("failed to get GOROOT: %v", err)
		}
		goroot := strings.TrimSpace(string(gorootOut))
		stdCoverDoc := filepath.Join(goroot, "src", "cmd", "cover", "doc.go")
		if _, err := os.Stat(stdCoverDoc); err != nil {
			t.Skipf("stdlib file %s not found: %v", stdCoverDoc, err)
		}

		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module attackmain\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Copy concurrency race witness main.go
		mainSrcPath := filepath.Join(repoRoot, "tests", "stress", "testdata", "src", "sp_w_error", "main.go")
		mainSrc, err := os.ReadFile(mainSrcPath)
		if err != nil {
			t.Fatalf("failed to read witness main.go: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), mainSrc, 0o644); err != nil {
			t.Fatal(err)
		}

		// Symlink standard library cover/doc.go (package main, no func main)
		if err := os.Symlink(stdCoverDoc, filepath.Join(tmpDir, "doc.go")); err != nil {
			t.Fatal(err)
		}

		targetBin := filepath.Join(tmpDir, "evaded_race_bin")
		res := runCommand(t, tmpDir, []string{"GOSREE_BIN=" + gosreeBin},
			"go", "build", "-race", "-toolexec="+gateBin, "-a", "-gcflags=-std", "-o", targetBin, ".")

		if res.code == 0 {
			t.Fatalf("CRITICAL EVASION VULNERABILITY: symlink stdlib race witness bypassed gosree-gate with exit code 0!")
		}
		combined := res.stdout + res.stderr
		if !strings.Contains(combined, "SAFE003") {
			t.Errorf("expected SAFE003 diagnostic on stderr, got:\n%s", combined)
		}
		if _, err := os.Stat(targetBin); !os.IsNotExist(err) {
			t.Fatalf("CRITICAL EVASION VULNERABILITY: binary %s was emitted despite SAFE003 violation via symlink evasion!", targetBin)
		}
	})
}
