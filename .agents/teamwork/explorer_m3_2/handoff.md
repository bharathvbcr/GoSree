# Explorer 2 Handoff Report: Compiler Gate Interception Architecture (`go build -toolexec` & `gosree-gate`)

**Milestone**: Milestone 3: Build & Compilation Enforcement Gates  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m3_2/`  
**Working Directory**: `/Users/bharath/Code/devtools/gocap` (Main checkout, strictly untouched)  
**Target Isolated Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**Date**: 2026-11-11T19:27:00Z  
**Parent Agent ID**: `879a5ae1-3b1b-40a0-b0ee-5e17eebfbf4c`  
**Status**: INVESTIGATION COMPLETE  

---

## 1. Observation

### 1.1 Existing Tooling in Worktree
- Inspected `/Users/bharath/Code/devtools/gocap-worktree/cmd/`:
  - `cmd/gosree`: Multichecker CLI and preflight gates (`main.go`, `preflight.go`, `main_test.go`, `preflight_test.go`, `unitchecker_mode_test.go`).
  - `cmd/gosree-erase`: AST zero-cost erasure tool.
  - `cmd/gosree-corpus`: Corpus extraction and annotation tool.
  - **Result**: No `cmd/gosree-gate` or compiler interception utilities currently exist in the worktree (`ls -la /Users/bharath/Code/devtools/gocap-worktree/cmd` shows only `gosree`, `gosree-corpus`, and `gosree-erase`).

### 1.2 Existing Preflight Exit Code Semantics in `cmd/gosree`
- Inspected `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree/preflight.go:12-17`:
  ```go
  const (
      exitClean    = 0 // every package loaded, type-checked and analysed, and nothing was reported
      exitFailed   = 1 // a package could not be loaded or type-checked, or an analyzer failed: no verdict
      exitUsage    = 2 // a flag gosree does not accept, or one it refuses because it would change the verdict
      exitFindings = 3 // every package was analysed and at least one finding was reported
  )
  ```
- Tested exit codes:
  - Clean package (`./cap` with `-test=false`): exited with status `0`.
  - Package with safety errors (`./tests/stress/testdata/src/sp_w_error`): exited with status `3`:
    ```
    /Users/bharath/Code/devtools/gocap-worktree/tests/stress/testdata/src/sp_w_error/main.go:21:4: SAFE003: composite literal is used after it was passed to a goroutine (line 17)
    exit: 3
    ```
  - Refused flags (`gosree -fix ./...`): exited with status `2` (`cmd/gosree/preflight_test.go:58-68`).
  - Unparseable / load error: exited with status `1` (`cmd/gosree/main_test.go:94-114`).

### 1.3 Mechanics of `go build -toolexec`
Empirical testing via command line runs revealed the exact toolchain invocation protocol:
1. **Initial Tool Query (`-V=full`)**:
   When `go build -toolexec="cmd"` runs, `go` first executes:
   ```
   <toolexec-cmd> /path/to/compile -V=full
   ```
   If `<toolexec-cmd>` emits unexpected output to `stdout` before running the tool, `go build` fails immediately:
   `go: parsing buildID from go tool compile -V=full: unexpected output: ...`
   *Crucial Invariant*: Whenever `-V=full` or `-V` is in the arguments, the tool must transparently execute the underlying compiler and stream stdout unmodified.
2. **Compiler Invocation Arguments**:
   When compiling a package (e.g. `cap`), `toolexec` receives:
   - `os.Args[0]`: `<toolexec-cmd>` (e.g. `gosree-gate`)
   - `os.Args[1]`: Tool executable path: `/opt/homebrew/Cellar/go/1.27.3/libexec/pkg/tool/darwin_arm64/compile`
   - `os.Args[2:]`: Compiler flags and files:
     ```
     -o $WORK/b001/_pkg_.a
     -trimpath "$WORK/b001=>"
     -p github.com/bharathvbcr/gosree/cap
     -lang=go1.27
     -complete
     -buildid 4N6vBCE2uDmszCPDlmBB/4N6vBCE2uDmszCPDlmBB
     -goversion go1.27.3
     -c=18
     -shared
     -nolocalimports
     -importcfg $WORK/b001/importcfg
     -pack
     cap/cap.go
     ```
3. **Non-Compiler Invocations**:
   During linking or assembly, `toolexec` receives:
   - Tool path: `.../link`, `.../asm`, `.../cgo`, `.../buildid`.
   - Tool arguments: e.g. `-o <binary> -importcfg ...`.
4. **Halting Compilation via Exit Code**:
   Empirical test: When a wrapper script exited with status `3` (or non-zero) on the `compile` invocation:
   - Output emitted to `stderr` was printed by `go build`:
     ```
     # github.com/bharathvbcr/gosree/cap
     gosree-gate: error: SAFE001 unsafe ban in ./cap
     ```
   - `go build` immediately aborted with non-zero exit code (`1`).
   - The target archive `.a` or binary was **never created** (halted *before* object emission).

### 1.4 Standard Library vs User Packages
- Standard library packages (`internal/...`, `cmp`, `sync`, `runtime`, `fmt`, etc.) are compiled if not cached.
- Source files for standard library packages reside under `runtime.GOROOT()` (`/opt/homebrew/Cellar/go/1.27.3/libexec/src/...`).
- User packages have source files outside `runtime.GOROOT()`.

---

## 2. Logic Chain

1. **Gate Purpose & Enforcement Point**:
   - Requirement R3 requires compilation or build prevention against errors and vulnerabilities.
   - In Go, `go build -toolexec="gosree-gate"` intercepts each compiler toolchain command immediately before it executes.
   - If `gosree-gate` executes GoSree's static analysis passes against the target package *before* invoking the real `compile` tool:
     - On finding detection: `gosree-gate` writes formatted diagnostic messages to `stderr` and exits with status `3` (or non-zero).
     - Because `gosree-gate` exits without invoking `compile`, no object file (`.a` / `.o`) is created.
     - `go build` receives the non-zero status from `compile`, aborts the entire build DAG, and returns exit code 1 to the shell.
     - On clean analysis: `gosree-gate` executes the real compiler (`exec.Command(os.Args[1], os.Args[2:]...)`) and returns its exit code, allowing the build to proceed normally.

2. **Tool Identification & Transparent Routing**:
   - `toolName := strings.TrimSuffix(filepath.Base(os.Args[1]), ".exe")`.
   - If `toolName != "compile"`: `gosree-gate` must not intercept; it must transparently execute `os.Args[1:]`. This guarantees transparent pass-through for `asm`, `link`, `cgo`, `pack`, and other build tools.
   - If `toolName == "compile"`:
     - If `os.Args[2:]` contains `-V=full` or starts with `-V`: `gosree-gate` must pass through directly to avoid corrupting Go's build ID computation.
     - If all source files reside under `runtime.GOROOT()`: standard library code should not be analyzed by GoSree; pass through directly.
     - If no `.go` source files are present: pass through directly.

3. **Analysis Execution Architecture (Option A vs Option B)**:
   - *Option A (Spawn `gosree` CLI)*:
     - Spawns `gosree` child process per package.
     - Issues: Requires `gosree` binary to be pre-built and placed in `$PATH`; runs redundant `go list` queries; risks cache contention during parallel builds.
   - *Option B (In-Process Execution via `checker.Analyze`)*:
     - Recommended and standard approach: `cmd/gosree-gate` directly imports GoSree's analyzers (`annot.Analyzer`, `unsafeban.Analyzer`, `ownership.Analyzer`, `borrow.Analyzer`, `errcheck.Analyzer`).
     - Extracts the package directory `dir := filepath.Dir(goFiles[0])`.
     - Detects whether test files are included (`hasTests := strings.HasSuffix(f, "_test.go")`).
     - Loads the package in-process using `packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Tests: hasTests, Dir: dir}, ".")`.
     - Runs analyzers in-process via `checker.Analyze(analyzers, pkgs, nil)` (the same driver already proven in `cmd/gosree-corpus` and `tests/stress/harness_test.go`).
     - Collects `Diagnostics` from `graph.Roots`.
     - If `len(diags) > 0`: formats each diagnostic to `os.Stderr` (`%s: %s\n`) and exits `3`.
     - If `len(diags) == 0`: transparently executes the real compiler (`execTool(tool, args...)`).

4. **Exit Code Alignment (Requirement R3)**:
   - Multichecker CLI `cmd/gosree`:
     - `0`: Clean (no findings, no errors).
     - `1`: Failure (unparseable code, missing package, internal panic).
     - `2`: Refused flags (e.g. `-fix`, `-diff`).
     - `3`: Vulnerability or safety findings (`SAFE001`–`SAFE008`, `BORROW001`–`BORROW004`, `ERR001`–`ERR005`).
   - Compiler Gate `gosree-gate`:
     - Exits `3` when findings are detected, halting `go build`.
     - Exits `0` on clean build (propagating compiler exit code).
     - Exits `2` on bad invocation (e.g. `len(os.Args) < 2`).
     - Exits `1` on analysis execution failure.

---

## 3. Caveats

1. **`go build` Exit Code Mapping**:
   When `gosree-gate` exits `3` on findings, `go build` wraps the tool's failure and itself exits with exit code `1`. The `3` exit code is seen by `go build` to trigger build abortion; users/CI scripts checking `go build -toolexec="gosree-gate"` will observe non-zero exit code (`1`) and diagnostics on `stderr`. In standalone `gosree`, exit code `3` is emitted directly.
2. **Build Cache Interaction**:
   `go build` caches successful package compilation in `GOCACHE`. If a clean package was already compiled, `go build` may skip re-running `compile` unless `-a` is provided. If a file is modified to introduce an error, `GOCACHE` cache misses, triggering `gosree-gate` and halting compilation.
3. **Cross-Package Fact Sharing**:
   Using `packages.Load` with `packages.LoadAllSyntax` in `gosree-gate` loads package syntax and resolves dependencies with full type information.

---

## 4. Conclusion

1. **Architecture Formulation**:
   - Create `cmd/gosree-gate/main.go` inside the worktree repository.
   - Implement transparent routing for non-compile tools and `-V=full`.
   - Implement in-process analysis of user Go compilation units using `packages.Load` + `checker.Analyze(analyzers, pkgs, nil)`.
   - Intercept compilation: halt on findings with status `3` (preventing `.a` output) or transparently exec real compiler on clean packages.
2. **Verification Suite**:
   - Implement `cmd/gosree/exit_test.go` verifying the strict 4-state exit code contract for `cmd/gosree` (0, 1, 2, 3).
   - Implement `cmd/gosree-gate/gate_test.go` and `tests/gates/` verifying:
     - Clean packages compile successfully under `go build -toolexec="gosree-gate"`.
     - Vulnerable packages (`sp_w_error`, `borrows`, `errbasic`) fail compilation under `go build -toolexec="gosree-gate"` with non-zero exit, emitting diagnostics to stderr, with zero `.a` / binary produced.
     - `go vet -vettool="gosree"` functions identically.

---

## 5. Verification Method

To verify the gate interception mechanics independently:

```bash
cd /Users/bharath/Code/devtools/gocap-worktree

# 1. Verify existing cmd/gosree exit code semantics
go test -v ./cmd/gosree -run 'TestGosree'

# 2. Verify compilation halting behavior with an inline gate interceptor
# Test A: Clean package succeeds
go build -toolexec="/bin/sh -c 't=\$(basename \"\$1\"); if [ \"\$t\" = \"compile\" ]; then for a in \"\$@\"; do if [ \"\$a\" = \"-V=full\" ]; then exec \"\$@\"; fi; done; fi; exec \"\$@\"' --" -o /dev/null ./cap; echo "exit: $?"
# Expected: exit 0

# Test B: Interception halts compilation with non-zero exit
go build -toolexec="/bin/sh -c 't=\$(basename \"\$1\"); if [ \"\$t\" = \"compile\" ]; then for a in \"\$@\"; do if [ \"\$a\" = \"-V=full\" ]; then exec \"\$@\"; fi; done; echo \"gosree-gate: error: SAFE003 detected\" >&2; exit 3; fi; exec \"\$@\"' --" -a -o /dev/null ./cap; echo "exit: $?"
# Expected: output contains "gosree-gate: error: SAFE003 detected", exit 1
```
