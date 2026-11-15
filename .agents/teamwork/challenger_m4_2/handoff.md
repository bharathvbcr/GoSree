# Milestone 4 Challenge Report: Build Gate Interception, Exit Code Semantics & Evasion Resistance

**Agent**: `challenger_m4_2` (Empirical Challenger 2)  
**Parent Sub-Orchestrator ID**: `33d7e329-0a05-4f09-b12d-9123259f9fff`  
**Workspace**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m4_2/`  
**Date**: 2026-11-11  
**Verdict**: **CHALLENGE_FAILED** (Critical Evasion Vulnerability Discovered)

---

## 1. Observation

### 1.1 Acceptance & Standard Gate Baseline
- **Command**: `bash tests/run_acceptance_tests.sh --json`
- **Output**:
  ```json
  {
    "status": "PASSED",
    "tier": "all",
    "go_test_exit_code": 0,
    "duration_s": 5,
    "tiers": [
      {"tier": 1, "tests_passed": 9, "tests_failed": 0, "tests_skipped": 0, "want_lines": 40},
      {"tier": 2, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 8},
      {"tier": 3, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 5},
      {"tier": 4, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 11}
    ],
    "totals": {"tests_passed": 12, "tests_failed": 0, "tests_skipped": 0, "want_lines": 64}
  }
  ```
- **Existing Gate Tests**:
  - `go test -v -count=1 ./tests/gates/...`: PASS (53.496s)
  - `go test -v -count=1 ./cmd/gosree-gate/...`: PASS (8.136s)
  - `go test -v -count=1 -run "TestExit" ./cmd/gosree/...`: PASS (11.378s)

### 1.2 Adversarial Verification Harness
An empirical adversarial test suite was authored and executed in `/Users/bharath/Code/devtools/gocap-worktree/tests/gates/adversarial_challenger_test.go`:
- `TestChallengerBuildGateStrictHalt`: PASS (60.69s)
  - Verified across all 7 vulnerable rule and concurrency witness packages: `passes/unsafeban/testdata/src/u` (`SAFE001`), `passes/borrow/testdata/src/borrows` (`BORROW001`), `passes/errcheck/testdata/src/errbasic` (`ERR001`), `tests/stress/testdata/src/sp_w_error` (`SAFE003`), `tests/stress/testdata/src/rs_torn_slice` (`SAFE005`), `tests/stress/testdata/src/rs_torn_iface` (`SAFE005`), `tests/stress/testdata/src/rs_w_mapwrites` (`SAFE003`).
  - Verified under standard `go build -toolexec="gosree-gate"` invocation that build fails with non-zero exit code, error diagnostics appear on stderr, and target `.a` archives are strictly **not** emitted to disk.
- `TestChallengerBuildGateCleanPassThrough`: PASS (9.47s)
  - Clean library packages and multi-file executables build with exit code 0; resulting binaries execute and output expected values.
- `TestChallengerInProcessFallback`: PASS (4.83s)
  - When `GOSREE_BIN` is stripped and no adjacent `gosree` binary is present, `gosree-gate` falls back to `runInProcessAnalysis`, correctly halting vulnerable compilation units and passing clean ones.
- `TestChallengerExitCodes_Comprehensive`: PASS (2.10s)
  - **Exit 0**: Clean package, `-V=full` (reports buildID), `-flags`.
  - **Exit 1**: Syntax errors, type errors; confirmed that analysis failure **strictly outranks** findings (exit 1 returned when analyzing both vulnerable code and broken syntax).
  - **Exit 2**: Missing package arguments (`gosree`, `gosree -borrow`, `gosree --`), refused flags (`-fix`, `-diff`), refused space-separated `-tags`, unknown flags (`-unknown-flag-adversarial`).
  - **Exit 3**: Safety findings detected in both text mode (diagnostics to stderr) and `-json` mode (valid JSON with diagnostics to stdout, exit code 3).
- `TestChallengerEvasionAttempts`:
  - `evasion_cfg_directory_confusion`: PASS (package directory literally named `fake.cfg` correctly treated as package pattern, detects `SAFE001`, exits 3).
  - `evasion_double_dash_separation`: PASS (findings detected across `--`).
  - `evasion_pass_disabling_warning`: PASS (warning emitted on stderr when passes are switched off).
  - `synthetic_wrapper_diagnostics`: PASS (synthetic wrapper struct violations halted).

### 1.3 Critical Vulnerability Discovery: Gate Evasion via `-gcflags="-std"`
- **Vulnerability Location**: `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/main.go`, lines 60–63:
  ```go
  // 3. Skip standard library compilation units.
  if hasStdFlag(args) {
      execTool(tool, args)
  }
  ```
  And lines 100–107:
  ```go
  func hasStdFlag(args []string) bool {
      for _, a := range args {
          if a == "-std" {
              return true
          }
      }
      return false
  }
  ```
- **Adversarial Command Line Execution**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree
  rm -f /tmp/evaded.a
  go build -toolexec="$(pwd)/gosree-gate" -a -gcflags="-std" -o /tmp/evaded.a ./passes/unsafeban/testdata/src/u
  ls -la /tmp/evaded.a
  ```
- **Verbatim Output**:
  ```
  -rw-r--r--@ 1 bharath  staff  162578 Nov 11 14:59 /tmp/evaded.a
  (exit code: 0)
  ```
- **Concurreny Witness Evasion Execution**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree
  rm -f /tmp/evaded_sp.a
  go build -toolexec="$(pwd)/gosree-gate" -a -gcflags="-std" -o /tmp/evaded_sp.a ./tests/stress/testdata/src/sp_w_error
  ls -la /tmp/evaded_sp.a
  /tmp/evaded_sp.a
  ```
- **Verbatim Output**:
  ```
  -rwxr-xr-x@ 1 bharath  staff  1832098 Nov 11 14:59 /tmp/evaded_sp.a
  (exit code: 0, binary executes)
  ```
- **Test Failure in `adversarial_challenger_test.go`**:
  ```
  === RUN   TestChallengerEvasionAttempts
  === RUN   TestChallengerEvasionAttempts/evasion_gcflags_std_bypass
      adversarial_challenger_test.go:432: CRITICAL EVASION VULNERABILITY: -gcflags=-std bypassed gosree-gate! Exit code was 0.
  --- FAIL: TestChallengerEvasionAttempts (30.97s)
      --- FAIL: TestChallengerEvasionAttempts/evasion_gcflags_std_bypass (10.03s)
  FAIL
  FAIL	github.com/bharathvbcr/gosree/tests/gates	31.105s
  ```

---

## 2. Logic Chain

1. **Gate Architecture**:
   - `gosree-gate` acts as a compiler interception wrapper invoked by `go build -toolexec`.
   - Its primary security invariant is that compilation of user packages containing safety violations or concurrency witnesses must be strictly halted before object files (`.a` / `.o` / executables) are emitted.
2. **Defective Assumption in `cmd/gosree-gate/main.go`**:
   - The implementation assumes that if `-std` is present in the compiler arguments (`hasStdFlag(args)`), the compilation unit belongs to the Go standard library, and therefore static analysis can be skipped.
   - However, the `compile` tool accepts `-std` even when compiling user packages. A user or build script can inject compiler flags into user packages via `-gcflags="-std"`.
3. **Premature Delegation**:
   - In `cmd/gosree-gate/main.go`, `if hasStdFlag(args) { execTool(tool, args) }` is evaluated at Step 3, **before** Step 5 which checks whether the files reside in `runtime.GOROOT()` (`allFilesUnderGoroot(goFiles)`).
   - Because `-std` is passed to the compiler arguments by `-gcflags="-std"`, `hasStdFlag(args)` evaluates to `true` on user source files.
   - `gosree-gate` immediately invokes `execTool(tool, args)` without calling `runGoSreeAnalysis(goFiles)`.
4. **Empirical Breach**:
   - Compiling `passes/unsafeban/testdata/src/u` (which violates `SAFE001`) with `-gcflags="-std"` succeeded with exit code 0 and emitted a 162 KB `.a` archive `/tmp/evaded.a`.
   - Compiling `tests/stress/testdata/src/sp_w_error` (which contains a proven data race witness) with `-gcflags="-std"` succeeded with exit code 0 and emitted a 1.8 MB executable binary `/tmp/evaded_sp.a`.
   - The emitted binary executed without hindrance.
5. **Deductive Conclusion**:
   - `cmd/gosree-gate` fails the Evasion Resistance acceptance criterion (R3, Feature 19). The build gate can be completely bypassed by appending `-gcflags="-std"`.

---

## 3. Adversarial Review Challenge Report

### Challenge Summary
**Overall Risk Assessment**: **CRITICAL**

### Challenges

#### [Critical] Challenge 1: Gate Evasion via `-gcflags="-std"` Injection
- **Assumption Challenged**: That `-std` only appears on trusted standard library compilation units and can be used to bypass analysis.
- **Attack Scenario**: A malicious or unhardened build script compiles vulnerable Go packages using:
  ```bash
  go build -toolexec="gosree-gate" -gcflags="-std" ./...
  ```
  `gosree-gate` sees `-std`, assumes it is compiling GOROOT standard library files, skips `runGoSreeAnalysis`, and forwards the command to `compile`. The Go compiler compiles the user package, emits archives and executables, and exits with code 0.
- **Blast Radius**: 100% compromise of build gate enforcement. Any code containing memory corruption, torn reads, data races, or unhandled errors can be compiled and deployed without detection.
- **Root Cause**: Premature check of `hasStdFlag(args)` at Step 3 without checking `allFilesUnderGoroot(goFiles)`.
- **Recommended Mitigation**:
  In `cmd/gosree-gate/main.go`:
  1. Remove Step 3 (`if hasStdFlag(args) ...`), OR require that `-std` is accompanied by all files residing under GOROOT:
     ```go
     // Skip standard library compilation units only if files reside in GOROOT.
     if hasStdFlag(args) && allFilesUnderGoroot(goFiles) {
         execTool(tool, args)
     }
     ```
  2. Better yet, let Step 5 (`allFilesUnderGoroot(goFiles)`) be the single canonical owner of standard library detection. If source files are not under `GOROOT`, they are user code and must be analyzed, regardless of what compiler flags were passed.

---

## 4. Caveats

- **No Caveats**. The evasion vulnerability was reproduced deterministically via command-line invocation, shell scripts, and an automated Go test in `tests/gates/adversarial_challenger_test.go`.

---

## 5. Conclusion

**Definitive Verdict**: **CHALLENGE_FAILED**

While `cmd/gosree` CLI exit code contracts (0 clean, 1 failure outranking findings, 2 usage/refused flags, 3 findings) and standard `go build -toolexec` interception without extra flags are fully implemented and functional, **`cmd/gosree-gate` contains a critical evasion flaw** allowing user code with safety violations to bypass compilation halting via `-gcflags="-std"`.

Milestone 4 cannot be approved until this evasion flaw is remediated and verified by `tests/gates/adversarial_challenger_test.go`.

---

## 6. Verification Method

To independently reproduce the evasion vulnerability in `/Users/bharath/Code/devtools/gocap-worktree`:

1. **Direct Command Line Reproduction**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go build -o /tmp/gate ./cmd/gosree-gate
   rm -f /tmp/evaded.a
   go build -toolexec="/tmp/gate" -a -gcflags="-std" -o /tmp/evaded.a ./passes/unsafeban/testdata/src/u
   echo "Exit code: $?"
   ls -la /tmp/evaded.a
   ```
   *Expected behavior for hardened gate*: Exit code non-zero, `/tmp/evaded.a` does NOT exist.  
   *Actual behavior (VULNERABILITY)*: Exit code 0, `/tmp/evaded.a` exists with size ~162 KB.

2. **Automated Test Reproduction**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v -count=1 ./tests/gates -run "TestChallengerEvasionAttempts/evasion_gcflags_std_bypass"
   ```
   *Result*: Test fails with `CRITICAL EVASION VULNERABILITY: -gcflags=-std bypassed gosree-gate! Exit code was 0.`
