# Handoff Report: Explorer 2 (Milestone 2 Hardening)

**From**: Explorer 2 (`teamwork_explorer`)  
**To**: Sub-Orchestrator Milestone 2 (`suborch_m2_hardening`) / Parent Orchestrator (`8682956d-3651-47d9-9c9e-ac932901be9a`)  
**Working Directory**: `/Users/bharath/Code/devtools/gocap` (strictly unmodified)  
**Target Isolated Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_2/`  
**Date**: 2026-11-11  

---

## 1. Observation

Direct empirical observations were gathered inside `/Users/bharath/Code/devtools/gocap-worktree` across all assigned tasks:

### 1.1 B1: Silent Drop of Unpositioned Borrow Findings
- **Target File**: `passes/borrow/check.go:185-199`
- **Executed Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/borrow -run '^TestReportWithoutPositionFallsBack$'
  ```
- **Verbatim Output**:
  ```
  === RUN   TestReportWithoutPositionFallsBack
      b6a2_fallbackpos_1111_test.go:32: got 0 diagnostics, want 1: a finding without a position was dropped
  --- FAIL: TestReportWithoutPositionFallsBack (0.00s)
  ```
- **Code Observation (`passes/borrow/check.go:185-188`)**:
  ```go
  func (bc *borrowChecker) report(pos token.Pos, cat, msg string) {
      if !pos.IsValid() || bc.ix.Mute(pos) {
          return
      }
  ```
  Any finding where `!pos.IsValid()` returns immediately without invoking `bc.pass.Report(...)`.

### 1.2 B5 & D5: HTTP Handler Getter Traversal, Search Complexity & Synthetic Position Loss
- **Target File**: `passes/ownership/handlerwrite.go`
- **Executed Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run 'TestHandlerWriteThroughGetterOfAnotherPackage|TestHandlerWriteGetterChainTerminates|TestHandlerWriteInSyntheticWrapperHasPosition'
  ```
- **Verbatim Output**:
  ```
  === RUN   TestHandlerWriteThroughGetterOfAnotherPackage
      analysistest.go:713: getter/getter.go:18: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
      analysistest.go:713: getter/getter.go:26: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
      analysistest.go:713: getter/getter.go:36: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
      analysistest.go:713: getter/getter.go:41: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
  --- FAIL: TestHandlerWriteThroughGetterOfAnotherPackage (7.54s)
  === RUN   TestHandlerWriteGetterChainTerminates
  === RUN   TestHandlerWriteGetterChainTerminates/leads_to_the_shared_loop
      analysistest.go:713: getterchain/getterchain.go:37: no diagnostic was reported matching `is written here by a handler`
      analysistest.go:713: getterchain/getterchain.go:221: no diagnostic was reported matching `is written here by a handler`
  === RUN   TestHandlerWriteGetterChainTerminates/leads_to_a_new_loop
  --- FAIL: TestHandlerWriteGetterChainTerminates (37.04s)
  === RUN   TestHandlerWriteInSyntheticWrapperHasPosition
      analysistest.go:654: -: unexpected diagnostic: SAFE008: the method value's receiver is passed to (*nopos/dep.Cache[V]).Put, which may write it, by a handler registered with (*net/http.ServeMux).HandleFunc (line 31), which shares it with every concurrent request
      analysistest.go:713: nopos/nopos.go:15: no diagnostic was reported matching `which may write it`
      analysistest.go:713: nopos/nopos.go:19: no diagnostic was reported matching `which may write it`
      analysistest.go:713: nopos/nopos.go:20: no diagnostic was reported matching `which may write it`
      analysistest.go:713: nopos/nopos.go:21: no diagnostic was reported matching `which may write it`
  --- FAIL: TestHandlerWriteInSyntheticWrapperHasPosition (13.72s)
  ```
- **Code Observations**:
  - `handlerwrite.go:330`: `case *ssa.Extract: v = x.Tuple` discards `x.Index`.
  - `handlerwrite.go:381-384`: `callee := x.Call.StaticCallee(); if callee == nil || len(callee.Blocks) == 0 { return nil, false }` drops cross-package getters.
  - `handlerwrite.go:400`: `len(ret.Results) == 1` ignores multi-return getters.
  - `handlerwrite.go:401-404`: `subSeen := make(map[ssa.Value]bool, len(seen))` clones seen set on each branch, causing $O(2^N)$ search taking 37.04s.
  - `handlerwrite.go:166`: `return fn.Pkg == nil && fn.Synthetic != ""` follows calls into external generic instantiation wrappers where instructions have `token.NoPos`, collapsing all findings in the package to `-`.

### 1.3 B6 & C2: Capability Contracts Parameter Shifts & Stale Hatch Hop Independence
- **Target Files**: `passes/ownership/contracts.go` & `passes/ownership/graph.go`
- **Executed Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run 'TestStdlibCapabilityContractsDynamicAndInterface|TestSharedLoadHatchVerdictIndependentOfCallDepth'
  ```
- **Verbatim Output**:
  ```
  === RUN   TestSharedLoadHatchVerdictIndependentOfCallDepth
      b6a2_fixes_1111_test.go:355: stale hatches at lines [10], want [10 27]: the verdict must not depend on call depth
  --- FAIL: TestSharedLoadHatchVerdictIndependentOfCallDepth (0.12s)
  === RUN   TestStdlibCapabilityContractsDynamicAndInterface
      analysistest.go:654: capdyn/capdyn.go:109:18: unexpected diagnostic: SAFE002: this value is returned to (*sync.Pool).Put but is not owned here: it may alias result of (*sync.Pool).Get$thunk (not known to return a fresh value)
      analysistest.go:654: capdyn/capdyn.go:18:2: unexpected diagnostic: SAFE006: package variable liveCfg is written outside init
      analysistest.go:713: capdyn/capdyn.go:158: no diagnostic was reported matching `SAFE003`
      analysistest.go:713: capdyn/capdyn.go:160: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: capdyn/capdyn.go:166: no diagnostic was reported matching `CAP003`
      analysistest.go:713: capdyn/capdyn.go:171: no diagnostic was reported matching `SAFE003`
      analysistest.go:713: capdyn/capdyn.go:311: no diagnostic was reported matching `SAFE003`
      analysistest.go:713: capdyn/capdyn.go:318: no diagnostic was reported matching `CAP003`
  --- FAIL: TestStdlibCapabilityContractsDynamicAndInterface (1.73s)
  ```
- **Code Observations**:
  - `graph.go:645`: Dynamic calls and bound method values did not resolve through `directCallee` / `resolveClosureVal`.
  - `graph.go:695`: In bound method wrappers and interface invokes, `publishArgs` was not shifted by -1, skipping all argument validation (`1 >= len(cc.Args)`).
  - `graph.go:627`: Interface invokes unconditionally treated receivers as written outside init and results as `evForeign`.
  - `contracts.go:263-360`: In `checkSharedLoadWrites`, writes were inspected and `emit` called *during* the BFS worklist loop, so shorter hops (`aHop`, 1 hop) called `st.ix.Mute(orig.pos)` before longer unhatched paths (`bHop -> hop`, 2 hops) arrived.

---

## 2. Logic Chain

1. **B1 Inference**:
   - `bc.report` receives `token.NoPos` on unpositioned borrow findings.
   - Guard `if !pos.IsValid() { return }` terminates execution before reporting.
   - Resolving a valid fallback position to the enclosing function (`bc.fn.Pos()`) or the first valid file token (`bc.pass.Files[0].Pos()`) ensures diagnostics are delivered to `bc.pass.Report` without dropping.

2. **B5 & D5 Inference**:
   - For cross-package getters, callee blocks are empty (`len(callee.Blocks) == 0`). Reading `st.summary(callee).links` connects arguments to returned results.
   - For multi-return getters, passing `resIdx = x.Index` from `*ssa.Extract` maps to `Link.B == np + resIdx`.
   - Global memoization (`visited` / `failed` maps) eliminates re-traversal of identical subtrees, bounding graph traversal to $O(V + E)$ linear time (<1s).
   - Rejecting external synthetic wrappers in `st.followable` redirects calls to `st.checkCalleeWrites` at the AST call site, preserving accurate source line numbers and enabling `//gosree:unsafe` suppression.

3. **B6 & C2 Inference**:
   - Resolving bound methods via `directCallee` and unnesting interface values allows capability contract lookup on `cc.Method.Name()`.
   - Shifting `publishArgs` by -1 (`argIdx := idx - 1`) aligns method parameters with call site arguments when receiver is captured in closure or is `cc.Value`.
   - Decoupling `checkSharedLoadWrites` into Phase 1 (fixed-point propagation until queue empty) and Phase 2 (converged write checking) guarantees that any write reached by an unhatched load evaluates to `muted: false`, rendering stale escape hatch determinations independent of BFS call depth.

---

## 3. Caveats

- **Scope Boundary**: Analysis was conducted strictly in read-only mode inside `/Users/bharath/Code/devtools/gocap-worktree`. No production code or tests were modified during this investigation.
- **External Dependencies**: Local Go module replacement (`github.com/bharathvbcr/gusset => ../gusset`) was verified and operates normally.
- **Parallel Tasks**: Tasks A1, A2, A3, B2, B3, B4, C1, D1, D2, D3, D4 belong to Explorers 1 and 3.

---

## 4. Conclusion

All root causes for B1, B5/D5, and B6/C2 have been identified and traced to specific functions and lines. Surgical remediation specifications are complete and documented in `analysis.md`:
1. `passes/borrow/check.go`: Implement `fallbackPos()` in `bc.report`.
2. `passes/ownership/handlerwrite.go`: Leverage `st.summary(callee).links`, memoize getter paths linearly, and disallow following external synthetic wrappers.
3. `passes/ownership/graph.go` & `contracts.go`: Resolve bound methods / interface invokes with `idx - 1` parameter shifts, and separate `checkSharedLoadWrites` into two distinct phases (fixed point propagation then write emission).

---

## 5. Verification Method

To verify the diagnoses and subsequent fixes:

```bash
# 1. Verify B1 Borrow Fallback
cd /Users/bharath/Code/devtools/gocap-worktree
go test -v ./passes/borrow -run '^TestReportWithoutPositionFallsBack$'

# 2. Verify B5 & D5 Getter Traversal, Memoization, and Synthetic Attribution
cd /Users/bharath/Code/devtools/gocap-worktree
go test -v ./passes/ownership -run 'TestHandlerWriteThroughGetterOfAnotherPackage|TestHandlerWriteGetterChainTerminates|TestHandlerWriteInSyntheticWrapperHasPosition'

# 3. Verify B6 & C2 Capability Contracts and Call-Depth Independent Stale Hatches
cd /Users/bharath/Code/devtools/gocap-worktree
go test -v ./passes/ownership -run 'TestStdlibCapabilityContractsDynamicAndInterface|TestSharedLoadHatchVerdictIndependentOfCallDepth'

# 4. Verify Full Regression Suite
cd /Users/bharath/Code/devtools/gocap-worktree
go test ./passes/borrow/... ./passes/ownership/...
```
