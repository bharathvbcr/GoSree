## 2026-11-11T17:08:50Z
You are Worker (teamwork_worker) for Milestone 2: Codebase Hardening & Vulnerability Remediation in GoSree (gocap).

Working Directory: /Users/bharath/Code/devtools/gocap
Target Isolated Worktree: /Users/bharath/Code/devtools/gocap-worktree (Branch: harden/gosree-gates)
Your State Folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m2_1/

## MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A teamwork_auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

## Mandatory Reading:
You MUST read these documents before starting any work:
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m2_hardening/SCOPE.md
- Explorer 1 Report: /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_1/handoff.md (and analysis.md)
- Explorer 2 Report: /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_2/handoff.md (and analysis.md)
- Explorer 3 Report: /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_3/handoff.md (and analysis.md)

## Write Ownership & Scope:
You have EXCLUSIVE write ownership to modify files ONLY inside `/Users/bharath/Code/devtools/gocap-worktree`.
The main directory `/Users/bharath/Code/devtools/gocap` is STRICTLY READ-ONLY. Do not write or modify any files outside your state folder and the target worktree.

## Implementation Tasks (Based on Explorer Technical Reports):
Execute all fixes inside `/Users/bharath/Code/devtools/gocap-worktree`:

1. **A2 — Fact Decoder Slice Bounds & Allocation Bomb (`passes/ownership/summary.go`)**:
   - In `summary.go`, fix the integer wrap bug: advance `rest = rest[nr:]` and check `sLen >= uint64(len(rest))` before slicing `rest[:sLen]`.
   - Prevent allocation bomb DoS: validate `nLinks > uint64(len(rest))/4` and `nAcq > uint64(len(rest))/4` before allocating slices.
   - Verify test: `go test -v ./passes/ownership -run '^TestFuncFactDecodeRejectsCorruptLengths$'` passes!

2. **B4 — Unchecked Heap Escape of Package Globals (`passes/ownership/globals.go`)**:
   - In `globalRootSeen`, add cases for `*ssa.Alloc` (checking referrers) and `*ssa.MakeClosure` (checking bindings).
   - In `collectGlobalMutations`, check `sum.leaks(i)` in addition to `sum.writes(i)` to mark escaping globals as mutated.
   - Verify test: `go test -v ./passes/ownership -run '^TestInitOnlyGlobalEscapeShapesFailClosed$'` passes!

3. **Closure Scope Variable Matching (`passes/ownership/check.go`)**:
   - In `check.go` (around lines 818-853, 915-948), match closure variable captures by `*types.Var` pointer equality (`lv == vr`, `lv == fnVar`) rather than string name comparison (`.Name == .Name`).
   - Reject `*ast.SelectorExpr` (so field selectors like `h.f` are never treated as local variable captures).
   - Verify test: `go test -v ./passes/ownership -run '^TestShadowedClosureNameIsNotABorrowCapture$'` passes!

4. **B1 — Unpositioned Borrow Finding Fallback (`passes/borrow/check.go`)**:
   - In `bc.report(pos token.Pos, cat, msg string)`, if `!pos.IsValid()`, fall back to the enclosing function's position `bc.fn.Pos()`, or package file start position `bc.pass.Files[0].Pos()`.
   - Verify test: `go test -v ./passes/borrow -run '^TestReportWithoutPositionFallsBack$'` passes!

5. **B5 & D5 — Getter Traversal, Linear Memoization, Synthetic Attribution (`passes/ownership/handlerwrite.go`)**:
   - In `st.fieldFromGetter`:
     - Handle cross-package getters when `callee.Blocks == 0` using `st.summary(callee).links`.
     - Multi-return getters: pass `x.Index` from `*ssa.Extract` to match `Link.B == np + resIdx`.
     - Memoize visited/failed getter search paths so traversal is linear ($O(V+E)$), dropping runtime from >30s to <1s.
   - In `st.followable`: Disallow following external generic synthetic wrappers so diagnostics attribute to the AST call site rather than `-`.
   - Verify tests:
     - `go test -v ./passes/ownership -run '^TestHandlerWriteThroughGetterOfAnotherPackage$'` passes!
     - `go test -v ./passes/ownership -run '^TestHandlerWriteGetterChainTerminates$' -timeout 2m` passes in <1s!
     - `go test -v ./passes/ownership -run '^TestHandlerWriteInSyntheticWrapperHasPosition$'` passes!

6. **B6 & C2 — Stdlib Capability Contracts & Stale Hatch Independence (`passes/ownership/contracts.go` & `graph.go`)**:
   - Bound method values and interface invokes: shift parameter index by -1 (`idx - 1`) in `publishArgs` so method arguments align with parameters.
   - Unnest interface values and resolve methods via `directCallee`.
   - Separate `checkSharedLoadWrites` into two phases: fixed-point BFS propagation to convergence, followed by write checking, so verdicts are independent of hop depth.
   - Verify tests:
     - `go test -v ./passes/ownership -run '^TestStdlibCapabilityContractsDynamicAndInterface$'` passes!
     - `go test -v ./passes/ownership -run '^TestSharedLoadHatchVerdictIndependentOfCallDepth$'` passes!

7. **B2 — Range-Over-Func Yield Error Tracking (`passes/errcheck/lower.go`)**:
   - Restore/unstage `passes/errcheck/lower.go` (reverting the unhardened M1 test reproduction diff back to HEAD commit `26ed498` which contains `isYield` handling).
   - Verify test: `go test -v ./passes/errcheck -run '^TestAdvRangeFuncDeferFallOff$'` passes!

8. **Concurrency Witnesses & Interface Boxing (`passes/ownership/graph.go`)**:
   - In `passes/ownership/graph.go`, ensure that when an unshareable value is boxed into an `error` interface across goroutine boundaries, it is tracked and flagged if mutated (fixing `sp_w_error`).
   - Verify runtime witnesses:
     - `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites` must be flagged with exit status 3 (`SAFE003`, `SAFE005`).

9. **Full Build & Test Suite Verification**:
   - Run tests in `/Users/bharath/Code/devtools/gocap-worktree`:
     - `go test ./passes/ownership/...`
     - `go test ./passes/borrow/...`
     - `go test ./passes/errcheck/...`
     - `go test ./cmd/gosree/...`
     - `go test ./cmd/gosree-erase/...`
     - `go test ./cmd/gosree-corpus/...`
     - `go test ./internal/gusseterr/...`
     - `cd rust/gosree-errsolve && cargo test`
     - `bash tests/run_acceptance_tests.sh`
   - Document all test outcomes and commands in your `handoff.md`.

Send message back to parent when completed.

## 2026-11-11T17:23:32Z
**Context**: Milestone 2 Codebase Hardening implementation in `/Users/bharath/Code/devtools/gocap-worktree`.
**Content**: Checking in on your status. Please update your `progress.md` with completed tasks and current work item.
**Action**: Reply with your current progress and status.

## 2026-11-11T17:43:29Z
**Context**: Milestone 2 Codebase Hardening implementation.
**Content**: Checking in on Task 6 (`contracts.go` & `graph.go`) and subsequent tasks.
**Action**: Please send a brief status update on Task 6.

## 2026-11-11T17:53:34Z
**Context**: Milestone 2 Codebase Hardening implementation.
**Content**: Heartbeat check: it has been 20 minutes since your last progress.md update. Please update your progress.md and report current status on Task 6 and subsequent tasks.
**Action**: Reply immediately with status.
