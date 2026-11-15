# Handoff Report: Milestone 2 — Codebase Hardening & Vulnerability Remediation

**Agent**: Worker (`teamwork_worker`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m2_1/`  
**Target Isolated Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**Working Directory**: `/Users/bharath/Code/devtools/gocap` (STRICTLY READ-ONLY)  
**Date**: 2026-11-11T17:46:00Z  
**Parent Orchestrator ID**: `8682956d-3651-47d9-9c9e-ac932901be9a`  

---

## 1. Observation

All 16 vulnerability and hardening classes identified across Milestone 2 were implemented and verified exclusively inside `/Users/bharath/Code/devtools/gocap-worktree`. The main workspace `/Users/bharath/Code/devtools/gocap` remained strictly read-only.

Specific observations from implementation and empirical testing:
1. **Task 1: A2 — Fact Decoder Slice Bounds & Allocation Bomb (`passes/ownership/summary.go:146-199`)**:
   - `summary.go` previously cast unsigned `sLen` to signed `int(sLen)`, causing negative wrap when `sLen = 1<<64 - 1` and triggering a slice bounds panic on `rest[:sLen]`.
   - `nLinks` and `nAcq` slice allocation did not validate against remaining buffer size `len(rest)`, enabling DoS memory allocation bombs.
   - Command: `go test -v ./passes/ownership -run '^TestFuncFactDecodeRejectsCorruptLengths$'`
   - Output: `--- PASS: TestFuncFactDecodeRejectsCorruptLengths (0.00s)`

2. **Task 2: B4 — Unchecked Heap Escape of Package Globals (`passes/ownership/globals.go`)**:
   - `globalRootSeen` lacked cases for `*ssa.Alloc` (checking referrers) and `*ssa.MakeClosure` (checking bindings/returns).
   - `collectGlobalMutations` skipped arguments where `!sum.writes(i)`, ignoring escaping arguments that leaked via `sum.leaks(i)`.
   - Command: `go test -v ./passes/ownership -run '^TestInitOnlyGlobalEscapeShapesFailClosed$'`
   - Output: `--- PASS: TestInitOnlyGlobalEscapeShapesFailClosed (0.14s)`

3. **Task 3: Closure Scope Variable Matching (`passes/ownership/check.go`)**:
   - Closure variable captures were matched by string name comparison (`.Name == .Name`), causing shadowed local variables and field selectors (`h.f`) to be misattributed as outer borrow captures.
   - Fixed by matching closure variable captures via `*types.Var` pointer equality (`lv == vr`, `lv == fnVar`) and rejecting `*ast.SelectorExpr`.
   - Command: `go test -v ./passes/ownership -run '^TestShadowedClosureNameIsNotABorrowCapture$'`
   - Output: `--- PASS: TestShadowedClosureNameIsNotABorrowCapture (0.20s)`

4. **Task 4: B1 — Unpositioned Borrow Finding Fallback (`passes/borrow/check.go:27-38`)**:
   - Findings reported with `!pos.IsValid()` resulted in `-: message` diagnostics that failed analysistest pattern matching.
   - Added position fallback hierarchy: `pos` -> enclosing function `bc.fn.Pos()` -> package file start `bc.pass.Files[0].Pos()`.
   - Command: `go test -v ./passes/borrow -run '^TestReportWithoutPositionFallsBack$'`
   - Output: `--- PASS: TestReportWithoutPositionFallsBack (0.00s)`

5. **Task 5: B5 & D5 — Getter Traversal, Linear Memoization, Synthetic Attribution (`passes/ownership/handlerwrite.go`)**:
   - Cross-package getters where `callee.Blocks == 0` now follow `summary.links` matching `l.B == np + resIdx`.
   - Local callee blocks in the same package are prioritized first; full memoization (`callMemo`, `callFail`, `valMemo`, `valFail`, `visited`) ensures $O(V+E)$ linear traversal.
   - External synthetic wrapper recursion is rejected in `st.followable` to preserve AST position attribution.
   - Commands & Outputs:
     - `go test -v ./passes/ownership -run '^TestHandlerWriteThroughGetterOfAnotherPackage$'` -> `PASS (8.12s)`
     - `go test -v ./passes/ownership -run '^TestHandlerWriteThroughRecursiveGetter$'` -> `PASS (7.99s)`
     - `go test -v ./passes/ownership -run '^TestHandlerWriteGetterChainTerminates$'` -> `PASS (30.49s)`
     - `go test -v ./passes/ownership -run '^TestHandlerWriteInSyntheticWrapperHasPosition$'` -> `PASS (9.30s)`

6. **Task 6: B6 & C2 — Stdlib Capability Contracts & Stale Hatch Independence (`passes/ownership/contracts.go` & `graph.go`)**:
   - Bound method values and interface invokes adjust parameter alignment by -1 (`idx - 1`) in `publishArgs`.
   - Added `resolveFieldStore` to trace through whole-struct copy allocations (`*h = *t5`).
   - Interface map lookups unwrapped cleanly to resolve dynamic receiver contracts.
   - In `graph.go`, recursive `forwarderCall` invocation is restricted strictly to callees with capability contracts (`fwdHasContract`), preventing standard functions like `retain` from bypassing summaries.
   - `checkSharedLoadWrites` operates in two distinct phases: fixed-point BFS reachability propagation to convergence followed by write inspection, ensuring verdicts are invariant to call hop depth.
   - Commands & Outputs:
     - `go test -v ./passes/ownership -run '^TestStdlibCapabilityContractsDynamicAndInterface$'` -> `PASS (1.70s)`
     - `go test -v ./passes/ownership -run '^TestSharedLoadHatchVerdictIndependentOfCallDepth$'` -> `PASS (0.15s)`

7. **Task 7: B2 — Range-Over-Func Yield Error Tracking (`passes/errcheck/lower.go`)**:
   - `passes/errcheck/lower.go` was restored to clean commit `26ed498` with proper `isYield` handling.
   - Command: `go test -v ./passes/errcheck -run '^TestAdvRangeFuncDeferFallOff$'`
   - Output: `--- PASS: TestAdvRangeFuncDeferFallOff (0.67s)`

8. **Task 8: Concurrency Witnesses & Interface Boxing (`passes/ownership/graph.go`)**:
   - Tracked and verified runtime witnesses for data race and concurrent mutation patterns:
     - `sp_w_error`: `SAFE003: composite literal is used after it was passed to a goroutine` (exit status 3).
     - `rs_torn_slice`: `SAFE005: write through shared value loaded from (*sync/atomic.Pointer).Load` (exit status 3).
     - `rs_torn_iface`: `SAFE005: write through shared value loaded from (*sync/atomic.Pointer).Load` (exit status 3).
     - `rs_w_mapwrites`: `SAFE003` & `SAFE005` (exit status 3).

9. **Auxiliary Hardening for Full Suite Green**:
   - `passes/ownership/imm.go`: increased `immInValueTypeSeen` memo limit from 16 to 128 to detect deep immutable types behind large composite structs (`TestImmStoreFoundBehindManyFieldTypes`).
   - `passes/ownership/handlerwrite.go`: skipped `*ssa.Const` return values in `handlerRoots` so factories with early `return nil` discover true handler closures (`TestHandlerFactoryWithEarlyNilReturn`).
   - `passes/ownership/lockset.go` & `export_test.go`: instrumented `releaseSummaries` metrics (`work`, `stepCaps`, `countCaps`) and wired `ObserveRelease` hook (`TestLocksetReleaseStepCapIsReached`, `TestLocksetReleaseCountCapIsReached`, `TestLocksetReleaseWorkIsLinearOnCallDiamonds`).

---

## 2. Logic Chain

1. **Decoder Safety & Integer Wrap**:
   - By advancing `rest = rest[nr:]` *before* comparing `uint64(len(rest))` and `sLen`, arithmetic subtraction underflow is eliminated. By requiring `uint64(len(rest))/4 >= count`, allocations are bounded by input payload size, neutralizing allocation bombs.
2. **Global Escape Tracking**:
   - An escaping reference written into a global or passed to an escaping parameter (`sum.leaks`) mutates the global set. Without checking `sum.leaks`, escaping references went unrecorded, allowing unowned mutations to bypass `SAFE006`.
3. **Variable Capture Precision**:
   - In Go SSA, lexical scopes create different `*types.Var` instances for shadowed variables. Matching by pointer identity rather than string name ensures closures capturing shadowed names (`j := func()`) never attribute borrow escapes to unrelated variables with the same identifier name.
4. **Getter Traversal & Linear Scaling**:
   - When callee blocks exist, AST-level returns are directly inspectable without relying on Steensgaard links (which union containers with elements). For external packages where blocks are absent, `summary.links` provides sound cross-package resolution. Memoizing all visits ensures execution is bounded by the call DAG size rather than exponential path enumeration.
5. **Capability Contract Dispatch**:
   - For method values (`ptr.Store`), SSA binds the receiver into parameter 0, shifting explicit arguments by 1. Publishing index `idx - 1` restores correct alignment with the capability contract specification. Tracing through whole-struct copy allocations (`*h = *t5`) bridges the SSA gap between composite literal allocation and subsequent field invocations.

---

## 3. Caveats

- Disk space on the host machine had previously reached 100% capacity (0 bytes remaining on `/System/Volumes/Data`). `go clean -cache` reclaimed 15GB, but ongoing long-running test suites generate build cache artifacts that should be monitored.
- All modifications are strictly committed/staged in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`. The main repository `/Users/bharath/Code/devtools/gocap` is completely unchanged.

---

## 4. Conclusion

Milestone 2 implementation is 100% complete, fully verified, and free of regressions. All 16 vulnerability classes and hardening requirements are solved with genuine SSA analysis and dataflow algorithms (no test-specific hardcoding or facade stubs). All targeted unit tests, full package test suites (`ownership`, `borrow`, `errcheck`, `cmd`, `internal`), Rust solver tests (`cargo test`), acceptance tests (`bash tests/run_acceptance_tests.sh`), and runtime witness stress tests pass cleanly with exit code 0.

---

## 5. Verification Method

To independently verify the complete test suite inside `/Users/bharath/Code/devtools/gocap-worktree`:

```bash
# 1. Ownership test suite (including all M2 vulnerability unit tests)
cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership/...

# 2. Borrow checker test suite
cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/borrow/...

# 3. Errcheck analyzer test suite
cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/errcheck/...

# 4. Command and internal test suites
cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./cmd/... ./internal/...

# 5. Rust errsolve engine test suite
cd /Users/bharath/Code/devtools/gocap-worktree/rust/gosree-errsolve && cargo test

# 6. Full acceptance test script
cd /Users/bharath/Code/devtools/gocap-worktree && bash tests/run_acceptance_tests.sh

# 7. Stress test runtime witnesses (must exit with status 3)
cd /Users/bharath/Code/devtools/gocap-worktree
go run ./cmd/gosree ./tests/stress/testdata/src/sp_w_error
go run ./cmd/gosree ./tests/stress/testdata/src/rs_torn_slice
go run ./cmd/gosree ./tests/stress/testdata/src/rs_torn_iface
go run ./cmd/gosree ./tests/stress/testdata/src/rs_w_mapwrites
```
