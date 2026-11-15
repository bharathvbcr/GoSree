# Handoff Report: Test Suite Architecture & Empirical Test Harness Design for Milestone 3

**Author**: Explorer 3 (`teamwork_explorer`)  
**Milestone**: Milestone 3 (Build & Compilation Enforcement Gates)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m3_3/`  
**Working Directory**: `/Users/bharath/Code/devtools/gocap`  
**Target Isolated Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**Date**: 2026-11-11T19:25:00Z  
**Recipient**: Sub-Orchestrator Milestone 3 (`879a5ae1-3b1b-40a0-b0ee-5e17eebfbf4c`)

---

## 1. Observation

### 1.1 Existing Test Suite Inventory
An exhaustive inspection of `/Users/bharath/Code/devtools/gocap-worktree` revealed the following test structure:

1. **`cmd/gosree` Test Suite**:
   - `cmd/gosree/main_test.go`:
     - Builds `cmd/gosree` into a temporary directory via `getGosreeBin(t)` with `sync.Once`.
     - `TestGosreeFlags`: Validates `-flags` JSON output across all 5 analyzers (`safeannot`, `safeunsafe`, `safeownership`, `borrow`, `safeerr`).
     - `TestGosreeHelp`: Validates help text.
     - `TestGosreeCleanPackage`: Asserts exit status 0 and zero output on `tests/e2e/testdata/src/t1_valid`.
     - `TestGosreeDiagnosticsReport`: Asserts exit status 3 and diagnostic emission (`BORROW001`-`004`, `SAFE007`) on `passes/borrow/testdata/src/borrows`.
     - `TestGosreeSafeErrViolation`: Asserts exit status 3 on `passes/errcheck/testdata/src/errbasic` and emission of `ERR001`-`ERR005`.
     - `TestGosreeUnsafebanViolation`: Asserts exit status 3 on `passes/unsafeban/testdata/src/u` and emission of `SAFE001`.
     - `TestGosreeMultiPackageInvocation`: Asserts exit status 3 when any of multiple packages has violations.
     - `TestGoVetIntegration`: Runs `go vet -vettool=bin tests/e2e/testdata/src/t1_valid`.
     - `TestGosreeJSONDedupTestVariant`: Validates `-json` deduplication of base package vs `[pkg.test]`.
   - `cmd/gosree/preflight_test.go`:
     - `TestParseCommandLine`: Unit tests for CLI flag parsing, `-test=false`, `-NAME=false`, `-tags`, and warning emission.
     - `TestParseCommandLineRefusals`: Validates refusal of `-fix`, `-diff`, bad `-tags` (exiting with code 2).
     - `TestJSONExit`: Validates exit code resolution from driver JSON (0 clean, 1 error, 3 findings).
     - `TestDedupJSONUnit`: Validates JSON deduplication logic.
   - `cmd/gosree/unitchecker_mode_test.go`:
     - `TestUnitcheckerMode`: Validates unitchecker mode detection for `go vet` `.cfg` arguments.
     - `TestCfgDirAsPattern`: Validates directory `x.cfg/` handling.
   - `cmd/gosree/adv_1111_test.go`:
     - Adversarial tests for `-tags` wildcard resolution and `.cfg` package paths.

2. **`tests/stress` Adversarial Stress Suite**:
   - `tests/stress/vetparity_test.go`:
     - Implements comprehensive parity tests between standalone `gosree` and `go vet -vettool=gosree`.
     - Lines 549–687 (`TestVetCLIContract`): Rigorously defines the CLI exit code contract:
       - `gosree clean`: exit 0
       - `gosree findings`: exit 3
       - `gosree type error`: exit 1
       - `gosree syntax error`: exit 1
       - `gosree -fix`: exit 2 (refused)
       - `vet clean`: exit 0
       - `vet findings`: exit 1 (vet standard)
       - `vet type error`: exit 1
   - `tests/stress/stress_test.go`:
     - Validates ledger markers across 141 fixture packages in 132 roots.
     - `TestDeterministic`: Executes double-pass check ensuring diagnostic order is deterministic.
   - `tests/stress/testdata/src/`:
     - 57 runtime witnesses with `// WITNESS: race` or `// WITNESS: panic`.
     - Specific critical witnesses:
       - `sp_w_error/main.go`: Pointer boxed into `error` interface causes data race; GoSree emits `SAFE003: composite literal is used after it was passed to a goroutine` (line 21:4) and exits 3.
       - `rs_torn_slice/main.go`: Torn slice write through shared atomic pointer; GoSree emits `SAFE005: write through shared value loaded from (*sync/atomic.Pointer).Load` (line 58:5, 60:5) and exits 3.
       - `rs_torn_iface/main.go`: Torn interface write through shared atomic pointer; GoSree emits `SAFE005: write through shared value loaded from (*sync/atomic.Pointer).Load` (line 59:5, 61:5) and exits 3.
       - `rs_w_mapwrites/main.go`: Concurrent map writes via published atomic pointer; GoSree emits `SAFE003` (line 33:4) and `SAFE005` (line 20:3) and exits 3.

3. **`tests/e2e` Acceptance Suite**:
   - 4-tier acceptance suite run via `bash tests/run_acceptance_tests.sh --json`.
   - `tests/e2e/testdata/src/t1_valid`: Fully valid code demonstrating second-class borrows, NLL expiry, and read-only borrows. Compiles and analyzes with exit 0.

4. **Status of Build Enforcement Gates**:
   - Neither `cmd/gosree-gate/` nor `tests/gates/` currently exists in `/Users/bharath/Code/devtools/gocap-worktree`.
   - No tests currently exist in the worktree that verify `go build -toolexec="gosree-gate"` halting compilation.

---

### 1.2 Target Fixture Catalog for Empirical Gate Verification

Empirical verification requires clean packages that pass without findings and vulnerable packages spanning all diagnostic categories:

| Target Package | Source Path | Target Category / Diagnostic | Expected `gosree` Exit | Expected `go build -toolexec` Result | Expected `go vet -vettool` Result |
|---|---|---|---|---|---|
| **Clean Package (E2E)** | `tests/e2e/testdata/src/t1_valid` | Valid borrows, NLL expiry | `0` (Clean) | **Build PASS (0)**; object/binary produced | **Pass (0)**; no output |
| **Clean Package (Main)** | Synthetic `package main; func main() {}` | Valid standalone binary | `0` (Clean) | **Build PASS (0)**; executable produced | **Pass (0)**; no output |
| **Runtime Witness 1** | `tests/stress/testdata/src/sp_w_error` | `SAFE003` (race via error) | `3` (Findings) | **Build FAIL (!=0)**; diagnostics on stderr; binary NOT produced | **Fail (1)**; `SAFE003` on stderr |
| **Runtime Witness 2** | `tests/stress/testdata/src/rs_torn_slice` | `SAFE005` (torn slice write) | `3` (Findings) | **Build FAIL (!=0)**; diagnostics on stderr; binary NOT produced | **Fail (1)**; `SAFE005` on stderr |
| **Runtime Witness 3** | `tests/stress/testdata/src/rs_torn_iface` | `SAFE005` (torn iface write) | `3` (Findings) | **Build FAIL (!=0)**; diagnostics on stderr; binary NOT produced | **Fail (1)**; `SAFE005` on stderr |
| **Runtime Witness 4** | `tests/stress/testdata/src/rs_w_mapwrites` | `SAFE003`, `SAFE005` (map write) | `3` (Findings) | **Build FAIL (!=0)**; diagnostics on stderr; binary NOT produced | **Fail (1)**; `SAFE003`/`SAFE005` on stderr |
| **Unsafe Ban** | `passes/unsafeban/testdata/src/u` | `SAFE001` (unsafe.Pointer) | `3` (Findings) | **Build FAIL (!=0)**; `SAFE001` on stderr; object NOT emitted | **Fail (1)**; `SAFE001` on stderr |
| **Borrow Storage** | `tests/e2e/testdata/src/t1_storage` | `BORROW001` (struct storage) | `3` (Findings) | **Build FAIL (!=0)**; `BORROW001` on stderr; object NOT emitted | **Fail (1)**; `BORROW001` on stderr |
| **Borrow Conflicts** | `passes/borrow/testdata/src/borrows` | `BORROW001`-`004`, `SAFE007` | `3` (Findings) | **Build FAIL (!=0)**; `BORROW*` on stderr; object NOT emitted | **Fail (1)**; `BORROW*` on stderr |
| **Error Discipline** | `passes/errcheck/testdata/src/errbasic` | `ERR001`-`ERR005` | `3` (Findings) | **Build FAIL (!=0)**; `ERR*` on stderr; object NOT emitted | **Fail (1)**; `ERR*` on stderr |
| **Unanalyzable Code** | Synthetic syntax error or type error | Syntax / Typecheck failure | `1` (Failure) | **Build FAIL (!=0)**; compiler / type error on stderr | **Fail (1)**; compiler / type error |
| **Invalid CLI Flags** | Any package + `-fix` or `-diff` | Refused flags | `2` (Usage) | N/A (`gosree` direct) | N/A |

---

### 1.3 Empirical `toolexec` Interception Protocol

Tracing the Go toolchain execution under `go build -toolexec="<gate>"` revealed the exact mechanics:

1. **Toolchain Version Probe**:
   `go build` executes:
   `<toolexec> /path/to/go/pkg/tool/<os>_<arch>/compile -V=full`
   - *Requirement*: The gate interceptor MUST detect `-V=full` in `os.Args[2:]` and immediately exec the underlying tool, streaming its stdout/stderr verbatim. Failing to do so causes `go build` to fail immediately with `parsing buildID from go tool compile -V=full: unexpected output`.

2. **Standard Library Filtering**:
   When building user packages, `go build` may invoke `compile` for standard library dependencies with `-std`.
   - *Requirement*: If `-std` is present in `os.Args[2:]`, the interceptor MUST pass through transparently to the underlying compiler.

3. **Tool Dispatch**:
   `toolexec` receives all tool invocations:
   - `compile`: Compiles Go source files into `_pkg_.a` (specified by `-o <path>`).
   - `asm`: Assembles assembly files.
   - `link`: Links object files into the final executable.
   - `cgo`, `buildid`: Ancillary build tools.
   - *Requirement*: If `filepath.Base(os.Args[1]) != "compile"`, the interceptor MUST pass through transparently without running static analysis.

4. **Compilation Interception & Build Halting**:
   For user package `compile` invocations:
   - Extract `.go` files from arguments (`os.Args[2:]`).
   - Run GoSree static analysis passes.
   - If findings or errors are detected:
     - Print formatted diagnostics to `os.Stderr`.
     - Exit with status 3 (or non-zero) **WITHOUT** invoking the underlying compiler tool (`os.Args[1]`).
     - Result: `go build` halts immediately, the object file `_pkg_.a` is NOT generated, `link` is never invoked, and the target executable is NOT created.
   - If clean (zero findings, zero errors):
     - Execute the underlying compiler tool (`os.Args[1]`, `os.Args[2:]...`) and propagate its exit code.

We empirically verified this protocol in `/Users/bharath/Code/devtools/gocap-worktree` using a mock gate script paired with the real compiled `gosree` binary:
- **Clean Package (`t1_valid`)**: `go build -a -toolexec="mock_gate.sh" ...` exited with code `0`.
- **Vulnerable Package (`sp_w_error`)**: `go build -a -toolexec="mock_gate.sh" ...` exited with code `1`, printed:
  ```
  # github.com/bharathvbcr/gosree/tests/stress/testdata/src/sp_w_error
  tests/stress/testdata/src/sp_w_error/main.go:21:4: SAFE003: composite literal is used after it was passed to a goroutine (line 17)
  ```
  and the target binary was confirmed NOT to exist (`BIN_EXISTS=false`).

---

## 2. Logic Chain

1. **Isolation & Verification Principle**:
   - The original user requirement (R3) mandates: "enforce compilation or build prevention against errors and vulnerabilities... such that code with unresolved errors or vulnerabilities fails compilation or build checks."
   - To prove build enforcement, tests cannot merely assert that a static analyzer printed text; tests MUST execute real `go build` and `go vet` commands against the toolchain and assert that compilation is physically prevented and that output binaries/object files are not emitted.

2. **Decomposition into Test Modules**:
   To ensure clean separation of concerns and maintainable testing, the Milestone 3 test suite should be organized into three distinct locations:
   - **`cmd/gosree/exit_test.go`**: Unit and CLI contract testing for `cmd/gosree` exit codes (0, 1, 2, 3), verifying deterministic status codes in both text and `-json` modes.
   - **`cmd/gosree-gate/gate_test.go`**: Unit testing for the `gosree-gate` interceptor binary itself (flag routing, `-V=full` passthrough, non-compile passthrough, diagnostic output).
   - **`tests/gates/`**: End-to-end integration test suite:
     - `tests/gates/gate_build_test.go`: Tests `go build -toolexec="<gosree-gate>"`, verifying compilation success on clean packages, compilation abort on vulnerable packages, suppression of binary and object file creation, and diagnostics verification.
     - `tests/gates/vet_test.go`: Tests `go vet -vettool="<gosree>"`, verifying exit 0 on clean code and exit 1 on findings across all rule categories.

3. **Determinism and Hermeticity**:
   - All tests must use `t.TempDir()` for build outputs and temporary packages to avoid contaminating the workspace or depending on pre-existing artifacts.
   - Interceptor tests must pass `GOSREE_BIN` in the child environment so `gosree-gate` reliably locates the companion `gosree` binary regardless of system `$PATH`.
   - Tests must run cleanly under `-race` and `-count=1`.

---

## 3. Caveats

1. **Standard Library Packages**:
   `go build` under `-toolexec` may invoke `compile` on standard library packages when `-a` (rebuild all) or cross-compilation is used. `gosree-gate` must ignore packages with `-std` or files located inside `$GOROOT` to prevent attempting to analyze standard library internals.
2. **`go vet` Exit Code Transformation**:
   The Go toolchain's `go vet` command translates any non-zero exit code from a `vettool` into exit code `1`. Therefore:
   - `gosree` standalone returns exit code `3` on findings.
   - `go vet -vettool="gosree"` returns exit code `1` on findings.
   The test assertions must reflect this difference.
3. **CWD Sensitivity of Package Loading**:
   When invoking `gosree` or `go build`, the Go toolchain locates `go.mod` based on the process's current working directory. Test commands invoking `gosree` or `go build` must ensure `cmd.Dir` is explicitly set to the worktree root or a temporary module directory containing a valid `go.mod`.

---

## 4. Conclusion & Test Specification for Worker

The Worker should implement the Milestone 3 test harness across the following 3 files in `/Users/bharath/Code/devtools/gocap-worktree`:

### 4.1 Specification for `cmd/gosree/exit_test.go`
- **Location**: `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree/exit_test.go`
- **Package**: `package main_test`
- **Purpose**: Verify preflight exit codes (0, 1, 2, 3) across all modes (text and `-json`).
- **Required Test Functions**:
  1. `TestExitCode0_CleanPackage`:
     - Target: `tests/e2e/testdata/src/t1_valid`
     - Command: `gosree ./tests/e2e/testdata/src/t1_valid`
     - Assertions: `code == 0`, `stderr == ""`, `stdout == ""`
     - JSON mode: `gosree -json ./tests/e2e/testdata/src/t1_valid` -> `code == 0`, stdout is valid empty JSON structure `{}`.
  2. `TestExitCode1_AnalysisFailure`:
     - Target: Package with syntax error (e.g. `package bad\nfunc f( {\n`) created in `t.TempDir()`.
     - Command: `gosree <bad_pkg>`
     - Assertions: `code == 1`, stderr contains error message.
     - JSON mode: `gosree -json <bad_pkg>` -> `code == 1`.
  3. `TestExitCode2_RefusedFlags`:
     - Test cases:
       - `gosree -fix ./...` -> `code == 2`, stderr contains `"-fix is not supported"`
       - `gosree -diff ./...` -> `code == 2`, stderr contains `"-diff is not supported"`
       - `gosree -tags "a b" ./...` -> `code == 2`, stderr contains `"-tags takes a comma-separated list"`
  4. `TestExitCode3_VulnerabilitiesAndFindings`:
     - Test matrix covering all 16 rule categories:
       - `SAFE001`: `passes/unsafeban/testdata/src/u` -> `code == 3`, stderr contains `SAFE001`
       - `SAFE003`: `tests/stress/testdata/src/sp_w_error` -> `code == 3`, stderr contains `SAFE003`
       - `SAFE005`: `tests/stress/testdata/src/rs_torn_slice` -> `code == 3`, stderr contains `SAFE005`
       - `SAFE005`: `tests/stress/testdata/src/rs_torn_iface` -> `code == 3`, stderr contains `SAFE005`
       - `SAFE003` + `SAFE005`: `tests/stress/testdata/src/rs_w_mapwrites` -> `code == 3`, stderr contains `SAFE003` and `SAFE005`
       - `BORROW001`-`004`: `passes/borrow/testdata/src/borrows` -> `code == 3`, stderr contains `BORROW001`-`004`
       - `ERR001`-`005`: `passes/errcheck/testdata/src/errbasic` -> `code == 3`, stderr contains `ERR001`-`005`
     - JSON mode for each: asserts `code == 3`, stdout contains JSON array with `posn` and `message`.

---

### 4.2 Specification for `cmd/gosree-gate/gate_test.go`
- **Location**: `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/gate_test.go`
- **Package**: `package main_test`
- **Purpose**: Unit tests for the compiler interceptor binary `gosree-gate`.
- **Required Test Functions**:
  1. `TestGatePassthroughNonCompile`:
     - Runs `gosree-gate echo hello world`.
     - Asserts stdout equals `hello world\n`, exit code `0`.
  2. `TestGatePassthroughCompilerVersion`:
     - Runs `gosree-gate <path_to_go_compile> -V=full`.
     - Asserts exit code `0`, output matches `go tool compile -V=full`.
  3. `TestGatePassthroughStdlib`:
     - Runs `gosree-gate <path_to_go_compile> -std -o /dev/null ...`.
     - Asserts `gosree` analysis is skipped for standard library packages.
  4. `TestGateInterceptHaltOnFindings`:
     - Invokes `gosree-gate <path_to_go_compile> ... tests/stress/testdata/src/sp_w_error/main.go`.
     - Asserts exit code is `3` (or non-zero).
     - Asserts stderr contains `SAFE003`.
     - Asserts underlying `<path_to_go_compile>` is NOT called.

---

### 4.3 Specification for `tests/gates/gate_build_test.go`
- **Location**: `/Users/bharath/Code/devtools/gocap-worktree/tests/gates/gate_build_test.go`
- **Package**: `package gates_test`
- **Purpose**: End-to-end integration tests verifying that `go build -toolexec="gosree-gate"` prevents builds of vulnerable code while allowing clean code to build.
- **Required Test Functions**:
  1. `TestGoBuildGateCleanPackage`:
     - Sets up clean executable package in `t.TempDir()` or builds `tests/e2e/testdata/src/t1_valid`.
     - Executes:
       ```go
       cmd := exec.Command("go", "build", "-toolexec="+gateBin, "-o", outBin, "./...")
       cmd.Dir = workDir
       cmd.Env = append(os.Environ(), "GOSREE_BIN="+gosreeBin)
       ```
     - Asserts:
       - `err == nil` and exit code == `0`.
       - `outBin` exists on filesystem (`os.Stat(outBin)` succeeds).
       - Executing `outBin` runs cleanly.
  2. `TestGoBuildGateHaltsOnConcurrencyWitnesses`:
     - Iterates through the 4 runtime witnesses:
       - `tests/stress/testdata/src/sp_w_error`
       - `tests/stress/testdata/src/rs_torn_slice`
       - `tests/stress/testdata/src/rs_torn_iface`
       - `tests/stress/testdata/src/rs_w_mapwrites`
     - For each witness:
       - Executes `go build -toolexec="<gateBin>" -o <outBin> <witness_path>`.
       - Asserts:
         - Exit code != `0`.
         - Stderr contains `# <package>` and expected safety diagnostics (`SAFE003` or `SAFE005`).
         - `<outBin>` DOES NOT EXIST (`os.Stat` returns `os.ErrNotExist`).
  3. `TestGoBuildGateHaltsOnRuleViolations`:
     - Iterates through rule violation packages:
       - `passes/unsafeban/testdata/src/u` (`SAFE001`)
       - `passes/borrow/testdata/src/borrows` (`BORROW001`-`004`)
       - `passes/errcheck/testdata/src/errbasic` (`ERR001`-`005`)
     - Asserts:
       - Exit code != `0`.
       - Stderr contains respective diagnostic rule identifiers.
       - Object files are not created.
  4. `TestGoBuildGateTransitiveDependencyHalts`:
     - Sets up a clean `main` package that imports a package containing a safety violation (e.g. `sp_w_error` or `errbasic`).
     - Executes `go build -toolexec="<gateBin>" -o <outBin> <main_pkg>`.
     - Asserts:
       - Build fails at the dependency compilation step.
       - Binary `<outBin>` is NOT created.

---

### 4.4 Specification for `tests/gates/vet_test.go`
- **Location**: `/Users/bharath/Code/devtools/gocap-worktree/tests/gates/vet_test.go`
- **Package**: `package gates_test`
- **Purpose**: End-to-end integration tests verifying `go vet -vettool="gosree"` behavior.
- **Required Test Functions**:
  1. `TestGoVetCleanPackage`:
     - Executes: `go vet -vettool="<gosreeBin>" ./tests/e2e/testdata/src/t1_valid`.
     - Asserts exit code == `0`, stderr is empty.
  2. `TestGoVetVulnerablePackages`:
     - Runs against `sp_w_error`, `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`.
     - Asserts exit code == `1` (go vet convention).
     - Asserts stderr contains expected `SAFE003` / `SAFE005` diagnostics.
  3. `TestGoVetRuleViolations`:
     - Runs against `passes/unsafeban/testdata/src/u` (`SAFE001`), `borrows` (`BORROW*`), `errbasic` (`ERR*`).
     - Asserts exit code == `1` and expected diagnostic codes appear on stderr.

---

### 4.5 Test Harness Helper Routines (Recommended for Worker)
Worker can define common test helpers in `tests/gates/gate_helpers_test.go`:
```go
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
	builtOnce  sync.Once
	gosreeBin  string
	gateBin    string
	buildError error
)

func buildBinaries(t *testing.T) (string, string) {
	t.Helper()
	builtOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "gosree-gate-test-*")
		if err != nil {
			buildError = err
			return
		}
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			buildError = err
			return
		}
		gosreeBin = filepath.Join(tmpDir, "gosree")
		gateBin = filepath.Join(tmpDir, "gosree-gate")

		// Build gosree
		cmd1 := exec.Command("go", "build", "-o", gosreeBin, "./cmd/gosree")
		cmd1.Dir = repoRoot
		if out, err := cmd1.CombinedOutput(); err != nil {
			buildError = fmt.Errorf("failed to build gosree: %v\n%s", err, string(out))
			return
		}

		// Build gosree-gate
		cmd2 := exec.Command("go", "build", "-o", gateBin, "./cmd/gosree-gate")
		cmd2.Dir = repoRoot
		if out, err := cmd2.CombinedOutput(); err != nil {
			buildError = fmt.Errorf("failed to build gosree-gate: %v\n%s", err, string(out))
			return
		}
	})
	if buildError != nil {
		t.Fatalf("setup failed: %v", buildError)
	}
	return gosreeBin, gateBin
}

type runResult struct {
	stdout string
	stderr string
	code   int
}

func runCmd(t *testing.T, dir string, env []string, name string, args ...string) runResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
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
	return runResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   code,
	}
}
```

---

## 5. Verification Method

To verify the test fixtures, exit codes, and gate behaviors documented in this report inside `/Users/bharath/Code/devtools/gocap-worktree`:

```bash
cd /Users/bharath/Code/devtools/gocap-worktree

# 1. Verify clean package exits 0
go run ./cmd/gosree ./tests/e2e/testdata/src/t1_valid
echo "Clean Exit: $?" # Must be 0

# 2. Verify runtime witnesses exit 3
go run ./cmd/gosree ./tests/stress/testdata/src/sp_w_error; echo "Witness 1 Exit: $?"       # Must be 3 (SAFE003)
go run ./cmd/gosree ./tests/stress/testdata/src/rs_torn_slice; echo "Witness 2 Exit: $?"   # Must be 3 (SAFE005)
go run ./cmd/gosree ./tests/stress/testdata/src/rs_torn_iface; echo "Witness 3 Exit: $?"   # Must be 3 (SAFE005)
go run ./cmd/gosree ./tests/stress/testdata/src/rs_w_mapwrites; echo "Witness 4 Exit: $?" # Must be 3 (SAFE003/SAFE005)

# 3. Verify rule categories exit 3
go run ./cmd/gosree ./passes/unsafeban/testdata/src/u; echo "Unsafe Exit: $?"             # Must be 3 (SAFE001)
go run ./cmd/gosree ./passes/borrow/testdata/src/borrows; echo "Borrow Exit: $?"          # Must be 3 (BORROW001-004)
go run ./cmd/gosree ./passes/errcheck/testdata/src/errbasic; echo "Errbasic Exit: $?"      # Must be 3 (ERR001-005)

# 4. Verify go vet -vettool clean vs findings
TMPBIN=$(mktemp -d)/gosree
go build -o "$TMPBIN" ./cmd/gosree
go vet -vettool="$TMPBIN" ./tests/e2e/testdata/src/t1_valid; echo "Vet Clean Exit: $?"    # Must be 0
go vet -vettool="$TMPBIN" ./tests/stress/testdata/src/sp_w_error; echo "Vet Vuln Exit: $?" # Must be 1
rm -rf "$(dirname "$TMPBIN")"

# 5. Run existing cmd/gosree and stress vetparity tests
go test -v -count=1 ./cmd/gosree
go test -v -count=1 ./tests/stress -run '^TestVetCLIContract$'
```
