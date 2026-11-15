# Scope: Milestone 3 — Build & Compilation Enforcement Gates

## Architecture
GoSree (`gosree`) is a static analysis suite for Go enforcing Rust-style memory safety, reference capabilities, second-class borrow checking, and error discipline (`ERR001`–`ERR005`).
Milestone 3 implements Requirement R3: Build & Compilation Enforcement Gates. This guarantees that code with unresolved errors or vulnerabilities halts compilation or build execution.

The enforcement architecture consists of:
1. **Preflight CLI Exit Code Gate (`cmd/gosree`)**:
   - `0`: Clean (no errors, no findings).
   - `1`: Analysis or compilation failure (fatal error, internal crash, unparseable code).
   - `2`: Refused/invalid CLI flags or bad arguments (including empty invocations without targets).
   - `3`: Vulnerabilities or safety findings detected (e.g. `SAFE001`–`SAFE008`, `BORROW001`–`BORROW004`, `ERR001`–`ERR005`).
2. **Compiler Interceptor Gate (`cmd/gosree-gate`)**:
   - Invocation via `go build -toolexec="gosree-gate" ...` or `go vet -vettool="gosree" ...`.
   - Intercepts compilation steps (`compile` tool invocations) before object file emission (`.a` / `.o`):
     - Executes GoSree analysis passes against the package files being compiled.
     - On safety/vulnerability findings or errors: prints formatted diagnostics to stderr and exits with status 3, immediately aborting the `go build` pipeline before compiler emits object files.
     - On clean analysis: transparently execs underlying compiler tool (`os.Args[1:]`) and propagates its return code.
3. **Empirical Gate Integration Tests (`tests/gates` and `cmd/gosree`)**:
   - Clean package build succeeds with `-toolexec` and produces executable binary / archive.
   - Vulnerable/error packages fail compilation under `-toolexec` with exit code != 0, emitting diagnostics and suppressing binary / object generation.
   - `go vet -vettool` integration verified across clean and vulnerable packages.
   - Preflight invalid flags and missing arguments verify exit code 2.

## Feature Inventory
| # | Feature | Description | Milestone | Source | Status |
|---|---------|-------------|-----------|--------|--------|
| 18 | Preflight Exit Code Gate | Strict exit codes (0 clean, 1 failure, 2 usage, 3 findings) in `cmd/gosree` | M3 | survey (R3) | DONE |
| 19 | Compilation & Build Gate Interception | Implement compiler gate (`go build -toolexec="gosree-gate"` / `go vet -vettool`) halting compilation on unresolved errors | M3 | survey (R3) | DONE |

## Iteration Steps
- **Iteration 1**:
  - Explorers (3): Mapped `cmd/gosree`, `toolexec` mechanics, test fixtures. [DONE]
  - Worker (1): Implemented `cmd/gosree` preflight exit codes, `cmd/gosree-gate`, and `tests/gates` in worktree. [DONE]
  - Verification Wave: 2 Reviewers (APPROVE), 2 Challengers (APPROVE), 1 Forensic Auditor (CLEAN). [DONE]
  - Gate Result: **PASS**.

## Interface Contracts
### Preflight Exit Code Contract (`cmd/gosree`)
- Command: `gosree [flags] [packages]`
- Status 0: Analysis ran to completion with zero findings and zero errors.
- Status 1: Analysis or tool execution failed unexpectedly (internal error, syntax error, failed to load package).
- Status 2: Invalid CLI flags, conflicting options (e.g., `-w` with `-l`), or missing package targets.
- Status 3: Analysis completed successfully and detected 1 or more safety errors, capability violations, borrow errors, or unhandled errors (`SAFE*`, `BORROW*`, `ERR*`).

### Compiler Interceptor Contract (`gosree-gate`)
- Invocation: `go build -toolexec="gosree-gate" [build-flags] [packages]`
- Tool routing:
  - If `filepath.Base(os.Args[1]) == "compile"`:
    - Pass through `-V=full` version query verbatim to preserve build IDs.
    - Pass through `-std` or `$GOROOT` source files.
    - Identify Go source files being compiled from `os.Args[2:]`.
    - Run GoSree analysis on target compilation unit.
    - If findings exist: emit diagnostics to `stderr` and exit with status 3, halting `go build` before compiler runs.
    - If clean: run `exec(os.Args[1], os.Args[2:]...)` to complete compilation.
  - If not `compile` (e.g., `asm`, `link`, `cgo`):
    - Pass through transparently to underlying tool `exec(os.Args[1], os.Args[2:]...)`.
- Vettool contract:
  - `go vet -vettool="gosree"` correctly prints checker flags on `-flags` and runs as singlechecker/multichecker.

## Code Layout (in Isolated Worktree `/Users/bharath/Code/devtools/gocap-worktree`)
- `cmd/gosree/main.go`: Preflight argument parsing, package filtering, and exit code 2 enforcement.
- `cmd/gosree/exit_test.go`: Unit test suite verifying exit codes 0, 1, 2, 3 across text and JSON modes.
- `cmd/gosree-gate/main.go`: Compiler gate interceptor binary.
- `cmd/gosree-gate/gate_test.go`: Unit tests for compiler interceptor command routing.
- `tests/gates/gate_build_test.go`: E2E integration tests for `go build -toolexec="gosree-gate"`.
- `tests/gates/vet_test.go`: E2E integration tests for `go vet -vettool="gosree"`.
- `tests/gates/gate_helpers_test.go`: Reusable test helpers for gate execution.
