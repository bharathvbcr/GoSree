## 2026-11-11T19:28:24Z
You are Worker 1 (teamwork_worker) for Milestone 3: Build & Compilation Enforcement Gates in GoSree (gocap).

Working Directory: /Users/bharath/Code/devtools/gocap
Target Isolated Worktree: /Users/bharath/Code/devtools/gocap-worktree (Branch: harden/gosree-gates)
Your State Folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m3_1/

## Mandatory Reading:
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m3_gates/SCOPE.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m3_1/handoff.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m3_2/handoff.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m3_3/handoff.md

## MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A teamwork_auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

## Write Ownership:
You have exclusive write ownership of the following files inside `/Users/bharath/Code/devtools/gocap-worktree`:
1. `cmd/gosree/main.go`
2. `cmd/gosree/exit_test.go`
3. `cmd/gosree-gate/main.go`
4. `cmd/gosree-gate/gate_test.go`
5. `tests/gates/gate_build_test.go`
6. `tests/gates/vet_test.go`
7. `tests/gates/gate_helpers_test.go` (if needed for shared helpers)
Do NOT modify any files in the main checkout `/Users/bharath/Code/devtools/gocap`!

## Implementation Tasks:
1. **Preflight Exit Code Hardening in `cmd/gosree`**:
   - In `cmd/gosree/main.go`: Ensure that when invoked with no package arguments (e.g. `gosree` or `gosree -borrow`), `gosree` prints usage and exits with status `2` (`exitUsage`), rather than status 1 from `multichecker.Main`.
   - Preserve meta-commands (`-flags`, `-V`, `-help`, `-h`, `help`) and `unitcheckerMode` (which handles `.cfg` arguments from `go vet`).
   - Implement `cmd/gosree/exit_test.go` covering all exit codes (0 clean, 1 syntax/failure, 2 usage/refused flags/no args, 3 findings) in text and `-json` modes.
2. **Compiler Gate Interceptor (`cmd/gosree-gate`)**:
   - Create `cmd/gosree-gate/main.go`:
     - Inspect `os.Args`: requires at least 2 arguments (tool path + flags).
     - Tool routing:
       - If `-V=full` or `-V` is in `os.Args[2:]`, immediately exec `os.Args[1]` with `os.Args[2:]` and stream stdout/stderr without modification.
       - If tool basename != `compile` (e.g. `asm`, `link`, `cgo`, `pack`, `buildid`), transparently exec `os.Args[1]` with `os.Args[2:]`.
       - If tool basename == `compile`:
         - If `-std` or files reside in `$GOROOT`: pass through to underlying compiler.
         - Extract Go source files from `os.Args[2:]`.
         - Execute GoSree analysis passes against the package compilation unit.
         - If findings or safety errors exist: print diagnostics to `stderr` and exit with non-zero status (status 3), halting the build before compiler emits object files.
         - If clean: transparently exec underlying compiler (`os.Args[1:]`) and exit with its status.
   - Implement `cmd/gosree-gate/gate_test.go` verifying passthrough, version check, and interception.
3. **Empirical Gate Integration Tests (`tests/gates`)**:
   - Create `tests/gates/gate_build_test.go`:
     - Verify `go build -toolexec="gosree-gate"` succeeds on clean packages (e.g. `tests/e2e/testdata/src/t1_valid`), exits 0, and produces output binary/object.
     - Verify `go build -toolexec="gosree-gate"` fails on vulnerable packages (runtime witnesses `tests/stress/testdata/src/sp_w_error`, `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites` and rule packages `passes/unsafeban/testdata/src/u`, `passes/borrow/testdata/src/borrows`, `passes/errcheck/testdata/src/errbasic`), exits != 0, prints diagnostics to stderr, and SUPPRESSES binary/object creation.
   - Create `tests/gates/vet_test.go`:
     - Verify `go vet -vettool="gosree"` succeeds on clean code (exit 0) and fails with exit 1 on vulnerable packages.
4. **Verification**:
   - Run all new unit and integration tests inside `/Users/bharath/Code/devtools/gocap-worktree`:
     - `go test -v -count=1 ./cmd/gosree/...`
     - `go test -v -count=1 ./cmd/gosree-gate/...`
     - `go test -v -count=1 ./tests/gates/...`
   - Run full regression suites:
     - `go test -v -count=1 ./passes/borrow/...`
     - `go test -v -count=1 ./passes/errcheck/...`
     - `go test -v -count=1 ./passes/ownership/...`
     - `go test -v -short ./tests/stress/...`
     - `bash tests/run_acceptance_tests.sh --json`
