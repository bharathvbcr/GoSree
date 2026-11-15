# Investigation Report: Root Cause & History Analysis of GoSree (gocap) Failure

## 1. Observation

### 1.1 Repository Structure & Subsystem Layout
From `/Users/bharath/Code/devtools/gocap/PROJECT.md` (lines 1-72, 137-170) and `go list ./...`:
The repository contains 15 Go packages plus Rust engines:
- `cap/`: Zero-cost generic type aliases (`cap.Mut[T]`, `cap.Ref[T]`, `cap.Iso[T]`, `cap.Imm[T]`).
- `cmd/gosree/`: Multichecker CLI driver (`go vet -vettool`) with preflight checks (`preflight.go`).
- `cmd/gosree-erase/`: AST zero-cost erasure tool and verification (`verify_equiv.go`).
- `cmd/gosree-corpus/`: Race corpus and real-world evaluation harness (`census.go`).
- `internal/annot/`: Directive parser for `//gosree:` line comments and `/*gosree:*/` block comments; `stale/` tracks unused escape hatches.
- `internal/formal/`: Reference model for container hook rules (`featherweight_gosree.go`).
- `internal/gusseterr/`: Error-discipline wire format and Gusset solver bridge.
- `internal/kinds/`: Type classification and shareability definitions (`kinds.go`).
- `passes/unsafeban/`: `SAFE001` ban on unsafe operations and cross-package `unsafe.Pointer` leaks.
- `passes/borrow/`: Second-class borrow checker pass (`BORROW001`–`BORROW004`).
- `passes/ownership/`: Reference capabilities and concurrency pass (`SAFE002`–`SAFE009`, `CAP003`).
- `passes/errcheck/`: Error-discipline pass `safeerr` (`ERR001`–`ERR005`).
- `rust/`: Cargo workspace holding `gosree-errsolve` (Rust solver engine) and `gusset-engine` (`libgusset.a`).
- `specs/`: Formal specification in Lean 4 (`specs/FeatherweightGoSree.lean`).
- `tests/e2e/`: 4-tier E2E acceptance test suite.
- `tests/stress/`: Adversarial stress suite with 143 test packages in 133 roots, 58 runtime witness programs.

### 1.2 Git Commit History & Branch Landscape
Inspection of `git log` and `git branch -a` shows:
- **HEAD of main**: Commit `7b739fc427413fe77857ba2f9968d960b3b02b2e` ("Correct docs for the preflight flags, container contracts and item 4 cleanup", 2026-11-11 07:24:38 -0600).
- **Previous key commits on main**:
  - `9161d81`: "Record a first look at the re-measure's new SAFE006 findings and transfers" (2026-11-11 07:11:33)
  - `6a854f8`: "Point FINDINGS.md at the checker it now describes" (2026-11-10 21:32:04)
  - `d87a17d`: "Drop -h=false and -help=false instead of handing them to the driver" (2026-11-10 21:28:45)
  - `d05f337`: "Prefer an unhatched origin where shared Loads merge" (2026-11-10 21:28:45)
  - `b279e66`: "Solve recursive imm return summaries as a bounded fixpoint" (2026-11-10 21:28:45)
  - `241973c`: "Cache an imm return summary only when no traced caller fed it" (2026-11-10 21:14:25)
  - `f6f3809`: "Record the consolidated tree's measurements; gofmt the erase test" (2026-11-10 21:04:35)
  - `6b12630`: "Fail closed on escaping init-only globals; verify erasure per directory set" (2026-11-10 20:39:10)
  - `87e48f5`: "Close stress-suite gaps: container contracts, bounded lockset, NLL back edges" (2026-11-10 19:57:25)
  - `404eee4`: "Rename gocap to gosree throughout the code" (2026-11-10 15:00:44)
- **Active branches**:
  - `run/integ-1111` (commit `6b401b9`): Merged `run/8e79-cmd`, `run/d323-frontend`, and `run/d323-solver`.
  - `run/8e79-cmd` (commit `6144167`): Fixes for CLI flags, erase CRLF, -verify deadlines, wildcard check with `-tags`.
  - `run/8e79-adv` (commit `6a8ac00`): Adversarial tests for erase CRLF, stdin, wildcard `-tags`.
  - `run/d323-frontend` (commit `fb3fb14`): Errcheck AST scan linearity, range-over-func yield handling, concrete error pointers.
  - `run/d323-frontend-adv` (commit `667c44e`): Adversarial tests for errcheck front-end.
  - `run/d323-solver` (commit `3ca416b`): Errcheck call-graph sweep cap bounded by function count, cancellation polling.
  - `run/d323-solver-adv` (commit `1dda5ef`): Soundness oracle and boundary tests for solver.
  - `run/b6a2-ownership` (commit `aa6d62f`): Fixed closure lookup by variable (not name), map nondeterminism, storage checker unpositioned SSA findings, fact decoder bounds check.
  - `run/b6a2-adv` (commit `aa6d62f`): Adversarial tests for ownership/borrow.
- **Git tags**: None (`git tag -l` returns empty).

### 1.3 State of the Main Working Tree
`git status` on `/Users/bharath/Code/devtools/gocap` shows dirty uncommitted state with 15 files changed (+2214 insertions, -162 deletions) and untracked files:
- Modified:
  - `passes/ownership/contracts.go` (+1507 lines)
  - `passes/ownership/contracts_test.go` (+435 lines)
  - `passes/ownership/globals.go` (+5 lines)
  - `passes/ownership/graph.go` (+80 lines)
  - `passes/ownership/handlerwrite.go` (+227 lines)
  - `passes/ownership/ownership.go` (+2 lines)
  - `passes/ownership/summary.go` (+31 lines)
  - `specs/FeatherweightGoSree.lean` (+38 lines)
  - `tests/stress/FINDINGS.md` (+34 lines)
  - `tests/stress/testdata/src/sp_launder/launder.go` (+2 lines)
  - `tests/stress/testdata/src/sp_w_syncmap/main.go` (+2 lines)
  - `.gitignore`, `PROJECT.md`, `README.md`, `docs/ARCHITECTURE.md`
- Untracked:
  - `passes/ownership/handler_getter_test.go`
  - `passes/ownership/handler_nopos_test.go`
  - `specs/lakefile.toml`, `specs/lean-toolchain`
  - `tests/stress/testdata/src/lk_w_getter/`

### 1.4 Verbatim Errors & Test Failures
Running `go test ./...` in the main checkout fails with:
1. `passes/ownership`:
   ```text
   --- FAIL: TestReleaseSummariesRecursiveAndClosureDiamondFast (8.61s)
       gap_fixes_test.go:287: recursive releaseSummaries + closure phi diamond took 6.791273666s (want < 2s)
   --- FAIL: TestHandlerWriteGetterChainTerminates (55.53s)
       --- FAIL: TestHandlerWriteGetterChainTerminates/leads_to_the_shared_loop (31.42s)
           handler_getter_test.go:257: analysing a chain of 28 two-return getters took 31.418856959s (want < 20s)
       --- FAIL: TestHandlerWriteGetterChainTerminates/leads_to_a_new_loop (24.11s)
           handler_getter_test.go:257: analysing a chain of 28 two-return getters took 24.110016375s (want < 20s)
   --- FAIL: TestHandlerWriteInSyntheticWrapperHasPosition (15.93s)
       analysistest.go:654: -: unexpected diagnostic: SAFE008: the method value's receiver is passed to (*nopos/dep.Cache[V]).Put, which may write it, by a handler registered with (*net/http.ServeMux).HandleFunc (line 24), which shares it with every concurrent request
       analysistest.go:713: nopos/nopos.go:15: no diagnostic was reported matching `which may write it`
       analysistest.go:713: nopos/nopos.go:19: no diagnostic was reported matching `which may write it`
       analysistest.go:713: nopos/nopos.go:20: no diagnostic was reported matching `which may write it`
   FAIL
   FAIL	github.com/bharathvbcr/gosree/passes/ownership	337.247s
   ```
2. `tests/stress`:
   ```text
   --- FAIL: TestWitnesses (0.17s)
       --- FAIL: TestWitnesses/lk_wit_maplookup/main.go (1.83s)
           stress_test.go:200: the runtime did not convict this program of a race: it witnesses nothing
   FAIL
   FAIL	github.com/bharathvbcr/gosree/tests/stress	253.843s
   ```

### 1.5 Worktree Layout
`git worktree list` shows 12 worktrees:
- 1 main worktree at `/Users/bharath/Code/devtools/gocap` on `main` (commit `7b739fc`).
- 11 worktrees in `/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/`:
  - `8e79-adv` (`run/8e79-adv`)
  - `8e79-cmd` (`run/8e79-cmd`)
  - `b6a2-adv` (`run/b6a2-adv`)
  - `b6a2-base2` (detached `9161d81`)
  - `b6a2-ownership` (`run/b6a2-ownership`)
  - `base` (detached `9161d81`)
  - `d323-frontend` (`run/d323-frontend`)
  - `d323-frontend-adv` (`run/d323-frontend-adv`)
  - `d323-solver` (`run/d323-solver`)
  - `d323-solver-adv` (`run/d323-solver-adv`)
  - `integ` (`run/integ-1111`)

---

## 2. Logic Chain

### Step 1: Why GoSree Failed to Guarantee Concurrency & Memory Safety
- **Observation**: `tests/stress/FINDINGS.md` lines 28–65, 87–111 documents that 3 safe Go programs corrupt memory on every execution without unsafe/cgo/reflection (`rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`), and `dx_syscall_witness` forges pointers causing segfaults (`unexpected fault address 0x4141414141414141`). Out of 58 runtime witness programs convicted by Go runtime race detection / panics, GoSree's static checker accepted 37 of them (63.8% false negative rate).
- **Inference**: GoSree failed its foundational promise: enforcing Rust-style memory safety and data-race prevention in safe Go.

### Step 2: Identification of Analysis Engine Vulnerabilities (Causes A through H)
- **Observation**: `tests/stress/FINDINGS.md` section 2 details the root causes:
  - *Cause A (Storing without spawning)*: Escapes were only checked during explicit channel send or `go` spawn. Storing values into concurrent containers (`atomic.Pointer`, `atomic.Value`, `sync.Map`, `sync.Pool`, `context.WithValue`) was not treated as a transfer until commit `87e48f5`, and custom registries remain unmodelled.
  - *Cause B (Assumed shareable pointer laundering)*: `internal/kinds/kinds.go` treated `error`, `context.Context`, `context.CancelFunc`, `net.Listener`, `testing.TB` as shareable. Converting any function literal to `CancelFunc` or wrapping a pointer into an `error` laundered the reference past ownership checks.
  - *Cause C (Dynamic calls & interfaces unmodelled)*: Dynamic dispatch (`cc.IsInvoke()`, method values, func variables) and reflection (`reflect.Value.Send`) bypassed capability tracking.
  - *Cause D (Incomplete concurrency spawner table)*: Callbacks like `runtime.AddCleanup`, `sync.Pool.New`, `b.RunParallel`, slog `Handler.Handle`, `Timer.Reset`, `runtime.SetFinalizer`, and `http.Server{Handler: h}` were omitted from spawn tracking.
  - *Cause E (Immutability breaks)*: `//gosree:imm` values could be mutated via `json.Unmarshal` into embedded unexported structs, variable capture reassignment, array container overwriting, and address-taking method accessors.
  - *Cause F (Cross-package global writes)*: Package variable writes across package boundaries were invisible to analysis (`SAFE006`).
  - *Cause G (Lock discipline bypass)*: Memory guarded by `//gosree:guarded_by` could be written without locks via method addresses (`buf.WriteString`), map reads after Unlock, and closures unlocking captured mutexes.
  - *Cause H (Escape hatch unsoundness & silent suppression)*: `//gosree:unsafe` muted every diagnostic in its entire syntactic span; misspelled directives were reported as near-miss warnings (`SAFE007`) but left dangerous code unchecked; stale hatches were never reported by `gosree ./...`.
- **Inference**: The analysis engine lacked complete flow tracking for standard library containers, dynamic dispatch, and interface wrappers, creating wide loopholes where concurrency bugs survived static analysis.

### Step 3: Analysis of Recent Fix Regressions and Opus Review Discoveries
- **Observation**: `scratchpad/BUILD_REPORT.draft.md`, `scratchpad/tasks.json`, and commit logs of branches `run/8e79-cmd`, `run/d323-*`, and `run/b6a2-*` show that fixing these gaps introduced severe regressions:
  - *False Positives in Borrow/Ownership (`ft-b6a2`)*: `passes/ownership/check.go` looked up closure bindings by identifier name (`id.Name`) across scopes. In `TestShadowedClosureNameIsNotABorrowCapture`, an unrelated loop variable `f` matched `f := func() { b.n++ }` in a separate block, emitting false `CAP003` errors and masking true `SAFE002`/`SAFE003`/`SAFE004` violations.
  - *Nondeterminism*: Iterating over Go maps in `passes/borrow` and `passes/ownership` produced nondeterministic diagnostic output across runs (`b6a2-maprange.txt`).
  - *Solver Scaling Limits (`ft-d323`)*: Hardcoded 4096 sweep cap in the Rust error solver caused failures on deep call graphs; quadratic shadow scans slowed AST passes.
  - *Tooling Vulnerabilities (`ft-8e79`)*: CRLF directive handling corrupted source files in `gosree-erase`; directory traversal vulnerabilities in `cmd/gosree-erase` (`-annotate`).
- **Inference**: Rapid patching without comprehensive architectural regression testing caused secondary defects, nondeterministic diagnostics, and solver scaling failures.

### Step 4: Root Cause of In-Tree Failures on Main
- **Observation**: Uncommitted edits on `main` attempted to solve Cause C (dynamic/interface calls and synthetic wrapper positions in `contracts.go` and `handlerwrite.go`). Running `go test ./passes/ownership` fails with:
  1. `TestHandlerWriteInSyntheticWrapperHasPosition`: Synthetic wrapper methods generated by `go/ssa` have no position (`-`), producing unpositioned diagnostics (`-: unexpected diagnostic: SAFE008...`) instead of attributing findings to the calling source lines (15, 19, 20).
  2. `TestHandlerWriteGetterChainTerminates`: The root walk in `handlerwrite.go` explores paths through 28 two-return getters exponentially (taking 31.4s / 24.1s, failing the 20s timeout limit).
  3. `TestReleaseSummariesRecursiveAndClosureDiamondFast`: Wall-clock regression (6.79s vs < 2s bound) due to recursive release summaries.
  4. `TestWitnesses/lk_wit_maplookup/main.go`: Flaky runtime witness race detection.
- **Inference**: The uncommitted attempt on `main` to handle synthetic wrappers and interface getters introduced exponential search paths in `handlerwrite.go` and unpositioned diagnostics, breaking the test suite.

### Step 5: Root Cause of Workspace & Worktree Breakdown
- **Observation**: `BUILD_REPORT.draft.md` line 9 recorded: *"main checkout has ~1981 lines of uncommitted edits in passes/ownership/... Likely Claude Desktop pid 20696; not messageable. Overlaps run/b6a2-ownership."*
- **Observation**: Branch `run/integ-1111` merged `run/8e79-cmd`, `run/d323-frontend`, and `run/d323-solver`, but could not merge `run/b6a2-ownership` because `b6a2-ownership` heavily touched `passes/ownership/`, colliding with the dirty main checkout.
- **Inference**: Failure to enforce strict worktree isolation allowed uncommitted, failing code to sit in the main checkout, creating merge conflicts, blocking integration of `b6a2-ownership`, and leaving the repository in a broken state.

---

## 3. Caveats

1. **Active Concurrent Sessions**: Background Claude Desktop processes (`pid 84772`, `pid 1507`, `pid 92128`) were observed running shell scripts and test loops against scratchpad directories and main checkout test files during the survey.
2. **Machine Load & Wall-Clock Assertions**: Current system load average is high (~80–90). Wall-clock assertions such as `TestScale` and `TestReleaseSummariesRecursiveAndClosureDiamondFast` are sensitive to CPU contention and may fail due to machine load rather than algorithmic regression.
3. **Flaky Race Detector Witness**: `TestWitnesses/lk_wit_maplookup/main.go` passed 40 consecutive runs earlier today but failed in a single whole-package test run, demonstrating nondeterminism in the Go runtime race detector under heavy thread contention.

---

## 4. Conclusion

GoSree (gocap) failed due to a combination of:
1. **Unsound Safety Bounds**: Fundamental omissions in transfer tracking across standard library concurrent containers (`atomic.Pointer`, `sync.Map`, `sync.Pool`, `context.WithValue`), pointer laundering via `internal/kinds/kinds.go` (`error`, `CancelFunc`), unmodelled dynamic/interface dispatch, and broad escape hatch suppression.
2. **Checker Regressions**: Scope-blind name-based closure matching (`passes/ownership/check.go`), map iteration nondeterminism, and solver sweep caps.
3. **Current In-Tree Breakages**: Incomplete and uncommitted handler write walk changes on `main` that exhibit exponential latency (`TestHandlerWriteGetterChainTerminates`), unpositioned synthetic diagnostics (`TestHandlerWriteInSyntheticWrapperHasPosition`), and release summary timeouts.
4. **Worktree Isolation Failure**: Uncommitted changes in the main working tree collided with `run/b6a2-ownership`, preventing complete integration of bug fixes into `run/integ-1111`.

To proceed with hardening (Requirements R2–R4):
- All development and testing must strictly occur in an isolated git worktree created from a clean base.
- The stranded fixes from `run/b6a2-ownership` must be reconciled with `run/integ-1111` and clean container contracts.
- Adversarial tests must be written to reproduce the synthetic wrapper position bug, getter-chain exponential explosion, FreeVar cell tracking (`ERR005`), and census wrapper edge coverage.
- Hardening gates must enforce that unpositioned diagnostics, unbounded AST walks, and unresolved safety errors fail the build.

---

## 5. Verification Method

To independently verify all observations and conclusions:

1. **Verify In-Tree Test Failures on Main**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test ./passes/ownership -run '^(TestHandlerWriteInSyntheticWrapperHasPosition|TestHandlerWriteGetterChainTerminates|TestReleaseSummariesRecursiveAndClosureDiamondFast)$' -v
   ```
   *Expected result*: `TestHandlerWriteInSyntheticWrapperHasPosition` fails with diagnostic at `-` position; `TestHandlerWriteGetterChainTerminates` fails with runtime > 20s.

2. **Verify Main Working Tree Status & Collisions**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   git status
   git diff --stat
   ```
   *Expected result*: 15 modified files (+2214, -162), untracked `handler_getter_test.go`, `handler_nopos_test.go`.

3. **Verify Git Branches & Merged Worktrees**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   git log --graph --oneline main run/integ-1111 run/b6a2-ownership -n 20
   ```
   *Expected result*: `run/integ-1111` contains merges of `8e79-cmd`, `d323-frontend`, and `d323-solver`; `run/b6a2-ownership` branched from `9161d81` and remains unmerged.

4. **Verify Stress Suite Findings & Runtime Witnesses**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   grep -E '^(SAFE|BORROW|ERR|CAP|Total)' tests/stress/FINDINGS.md
   ```
   *Expected result*: Confirms 610 hits locked, 187 known misses, 45 false positives, 58 runtime witnesses.
