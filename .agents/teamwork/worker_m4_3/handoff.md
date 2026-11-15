# Handoff Report — Worker M4-3 (Stdlib Symlink Mixed-File Evasion Remediation)

**Agent**: `worker_m4_3` (Teamwork Worker / Implementer & QA)  
**Parent Sub-Orchestrator ID**: `33d7e329-0a05-4f09-b12d-9123259f9fff`  
**Workspace**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_3/`  
**Date**: 2026-11-11T22:16:00Z  
**Verdict**: **REMEDIATION_COMPLETE_VERIFIED** (100% PASS ACROSS ALL SUITES)

---

## 1. Observation

### 1.1 Pre-Fix Empirical Reproduction of Challenger 3 Evasion Vulnerability
Prior to code modification, Challenger 3's empirical attack was reproduced verbatim in `/Users/bharath/Code/devtools/gocap-worktree`:
```bash
goroot=$(go env GOROOT)
tmpdir="/tmp/reproduce_symlink_evasion_test"
mkdir -p "$tmpdir"
cat <<'EOF' > "$tmpdir/go.mod"
module mainbinary
go 1.22
EOF
cp /Users/bharath/Code/devtools/gocap-worktree/tests/stress/testdata/src/sp_w_error/main.go "$tmpdir/main.go"
ln -s "$goroot/src/cmd/cover/doc.go" "$tmpdir/doc.go"

cd "$tmpdir"
go build -race -toolexec="gosree-gate" -a -gcflags="-std" -o "$tmpdir/evaded_bin" .
```
**Pre-Fix Verbatim Result**:
```
EXIT_CODE=0
-rwxr-xr-x@ 1 bharath  staff  2309458 Nov 11 16:03 /tmp/reproduce_symlink_evasion_test/evaded_bin
```
The unsafe binary with known data races (`SAFE003`) was successfully emitted to disk with exit status 0 because `isStandardLibrary` matched any file resolving to `GOROOT`, and `fileOriginatesInDir` followed symlinks into `GOROOT`.

### 1.2 Code Inspection & Defect Location
In `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/main.go`:
1. Lines 121–131 evaluated symlinks via `filepath.EvalSymlinks(abs)` without verifying that the literal path `abs` (prior to symlink resolution) resided inside `cleanDir` (or `evalDir`).
2. Lines 150–154 checked `if fileOriginatesInDir(f, cleanRoot)` inside a loop over `goFiles` and returned `true` if **ANY** single file resolved to `GOROOT`.

### 1.3 Post-Fix Verification Results
Following the implementation of the remediation in `cmd/gosree-gate/main.go`:
1. **Challenger 3 Repro Re-test**:
   ```
   # mainbinary
   ./main.go:21:4: SAFE003: composite literal is used after it was passed to a goroutine (line 17)
   ./main.go:17:2: 	the transfer that causes this finding
   EXIT_CODE=1
   ls: /tmp/reproduce_symlink_evasion_test/evaded_bin: No such file or directory
   ```
   Compilation was halted immediately, `SAFE003` diagnostics were printed on stderr, exit status was 1, and no binary was emitted.

2. **Cgo Standard Library Pass-Through**:
   Command: `go build -toolexec="gosree-gate" -a runtime/cgo`
   Result: `CGO_EXIT=0` (Clean pass-through, status 0).

3. **`cmd/gosree-gate/...` Test Suite**:
   Command: `go test -v -count=1 ./cmd/gosree-gate/...`
   Result: PASS (11/11 tests in 6.36s), including:
   - `TestFileOriginatesInDir_SymlinkOutsideDir` (PASS)
   - `TestIsStandardLibrary_PureGoUnitTests` (PASS)
   - `TestGateSymlinkMixedFileStdlibEvasionHalt` (PASS)

4. **`tests/gates/...` Test Suite**:
   Command: `go test -v -count=1 ./tests/gates/...`
   Result: PASS (all 11 test suites in 103.15s), including:
   - `TestChallengerEvasionAttempts/evasion_gcflags_std_symlink_archive_halt` (PASS)
   - `TestChallengerEvasionAttempts/evasion_gcflags_std_symlink_race_witness_binary_halt` (PASS)
   - `TestGoBuildGateEvasionResistance/evasion_symlink_mixed_file_halt` (PASS)

5. **E2E 4-Tier Acceptance Suite**:
   Command: `bash tests/run_acceptance_tests.sh --json`
   Result:
   ```json
   {
     "status": "PASSED",
     "tier": "all",
     "go_test_exit_code": 0,
     "duration_s": 3,
     "tiers": [
       {"tier": 1, "tests_passed": 9, "tests_failed": 0, "tests_skipped": 0, "want_lines": 40},
       {"tier": 2, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 8},
       {"tier": 3, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 5},
       {"tier": 4, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 11}
     ],
     "totals": {"tests_passed": 12, "tests_failed": 0, "tests_skipped": 0, "want_lines": 64}
   }
   ```

6. **All Go Packages Regression Suite**:
   Command: `go test -count=1 ./cmd/... ./passes/... ./internal/... ./tests/stress/...`
   Result: PASS across all 14 packages (0 failures):
   - `cmd/gosree` ok (50.4s)
   - `cmd/gosree-corpus` ok (32.2s)
   - `cmd/gosree-erase` ok (30.4s)
   - `cmd/gosree-gate` ok (16.5s)
   - `passes/borrow` ok (1.5s)
   - `passes/errcheck` ok (39.7s)
   - `passes/ownership` ok (142.2s)
   - `passes/unsafeban` ok (3.3s)
   - `internal/annot` ok (3.3s)
   - `internal/annot/stale` ok (7.3s)
   - `internal/formal` ok (0.2s)
   - `internal/gusseterr` ok (14.5s)
   - `internal/kinds` ok (0.6s)
   - `tests/stress` ok (99.2s)

7. **Rust Solver Test Suite**:
   Command: `cd rust && cargo test --workspace`
   Result: PASS across all crates (21 passed, 0 failed):
   - `gosree_errsolve` unit tests (10 passed)
   - `formal_lattice` tests (3 passed)
   - `solver_limits_and_joins` tests (6 passed)
   - `gusset` engine tests (2 passed)

8. **Strict Workspace Isolation**:
   Main repository `/Users/bharath/Code/devtools/gocap` status: Untouched by Worker M4-3 (only `.agents/teamwork/worker_m4_3/` contains agent briefing/progress metadata).

---

## 2. Logic Chain

1. **Vulnerability Root Cause**:
   - `fileOriginatesInDir(filePath, dir)` called `filepath.EvalSymlinks(abs)` and returned `true` if the evaluated target was within `dir`, without checking whether `abs` itself was under `dir`.
   - `isStandardLibrary(args, goFiles)` returned `true` if *any* file in `goFiles` matched `fileOriginatesInDir(f, cleanRoot)`.
   - Therefore, a malicious user package in `/tmp/...` containing safety violations (`SAFE001`, `SAFE003`) along with a symlink to any file in `$GOROOT` (e.g., `cmd/cover/doc.go` or `cmp/cmp.go`) combined with `-gcflags="-std"` caused `gosree-gate` to classify the compilation unit as standard library, bypassing GoSree analysis and emitting malicious binaries to disk.

2. **Remediation Invariants**:
   - **Literal Path Residency**: In `fileOriginatesInDir`:
     `literalUnderDir := isUnderDir(abs, cleanDir) || (errDir == nil && isUnderDir(abs, evalDir))`
     If `abs` (the literal file path before symlink evaluation) does not reside within `dir`, it is a user file or user symlink pointing into `dir` and cannot originate in `dir`.
   - **All-Source Provenance**: In `isStandardLibrary`:
     Pure Go standard library detection now mandates that **ALL** source files in `goFiles` originate in `GOROOT`:
     ```go
     allInGoroot := len(goFiles) > 0
     for _, f := range goFiles {
         if !fileOriginatesInDir(f, cleanRoot) {
             allInGoroot = false
             break
         }
     }
     if allInGoroot {
         return true
     }
     ```
   - **Cgo Separation**: Cgo standard library compilation units (where compiler arguments are intermediate files in `$WORK`) continue to be validated via package import path existence under `$GOROOT/src` and verifying that `//line` directives in `*.cgo1.go` originate within `cleanRoot`.

3. **Validation of Invariants**:
   - Adversarial packages with mixed files or user symlinks to GOROOT now return `fileOriginatesInDir == false` for the symlinks and user files, causing `allInGoroot == false`.
   - GoSree static analysis runs against the compilation unit, surfaces violations (`SAFE001`–`SAFE008`), exits with status 1 / 3, and terminates `compile` before `.a` or binary output is written.
   - Clean packages and genuine standard library packages (pure Go and Cgo) pass transparently.

---

## 3. Caveats

No caveats.
All changes are genuine, minimal, and fully covered by behavioral tests. Main workspace isolation is strictly preserved.

---

## 4. Conclusion

The standard library symlink evasion vulnerability identified by Challenger 3 has been completely remediated at the root cause level. `gosree-gate` now enforces strict literal path residency and all-source provenance before granting stdlib pass-through. Evasion attempts using mixed files, symlinks, and `-gcflags="-std"` are halted with non-zero exit codes, diagnostic emission, and total suppression of object files. All test suites across Go and Rust are passing 100%.

---

## 5. Verification Method

To independently verify this remediation in `/Users/bharath/Code/devtools/gocap-worktree`:

### 5.1 Run Gate Unit Tests
```bash
cd /Users/bharath/Code/devtools/gocap-worktree
go test -v -count=1 ./cmd/gosree-gate/...
```
*Expected: 11 tests pass.*

### 5.2 Run Gate E2E Regression Tests
```bash
cd /Users/bharath/Code/devtools/gocap-worktree
go test -v -count=1 ./tests/gates/...
```
*Expected: 11 test suites pass, including `evasion_gcflags_std_symlink_archive_halt`, `evasion_gcflags_std_symlink_race_witness_binary_halt`, and `evasion_symlink_mixed_file_halt`.*

### 5.3 Run Full Acceptance Suite
```bash
cd /Users/bharath/Code/devtools/gocap-worktree
bash tests/run_acceptance_tests.sh --json
```
*Expected: 100% pass across 12 packages (64 want lines).*

### 5.4 Run Cgo Stdlib Pass-Through
```bash
cd /Users/bharath/Code/devtools/gocap-worktree
go build -o /tmp/gate_check ./cmd/gosree-gate
go build -toolexec="/tmp/gate_check" -a runtime/cgo
echo "Exit: $?"
rm -f /tmp/gate_check
```
*Expected: Exit: 0.*

### 5.5 Invalidation Conditions
- A user package outside GOROOT containing a symlink to a stdlib file and `-gcflags="-std"` compiles with exit status 0.
- An archive (`.a`) or binary is emitted on disk for code with `SAFE001` or `SAFE003`.
- Any test in `cmd/gosree-gate`, `tests/gates`, or acceptance suites fails.
