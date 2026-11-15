# Milestone 2 Technical Analysis: Hardening GoSree Passes

**Author**: Explorer 2 (`teamwork_explorer`)  
**Target Repository**: `/Users/bharath/Code/devtools/gocap-worktree`  
**Branch**: `harden/gosree-gates`  
**Date**: 2026-11-11  

---

## 1. Executive Summary

This investigation analyzes three critical failure areas in GoSree's safety and verification passes:
1. **B1**: Silent drop of unpositioned borrow findings in `passes/borrow/check.go`
2. **B5 & D5**: Cross-package & multi-return getter bypasses (`SAFE008`), $O(2^N)$ exponential getter chain search, and synthetic SSA wrapper position attribution loss (`-`) in `passes/ownership/handlerwrite.go`
3. **B6 & C2**: Parameter index shifts in bound method wrappers and interface invokes (`sync.Pool.Put`, `atomic.Pointer.Store`), and BFS hop-ordering flaws causing call-depth dependent stale escape hatch verdicts in `passes/ownership/contracts.go`

Each failure was reproduced in `/Users/bharath/Code/devtools/gocap-worktree` against unit tests (`TestReportWithoutPositionFallsBack`, `TestHandlerWriteThroughGetterOfAnotherPackage`, `TestHandlerWriteGetterChainTerminates`, `TestHandlerWriteInSyntheticWrapperHasPosition`, `TestStdlibCapabilityContractsDynamicAndInterface`, `TestSharedLoadHatchVerdictIndependentOfCallDepth`). Root causes were traced to concrete lines of code, and surgical patch specifications are provided below.

---

## 2. Investigation Findings

### 2.1 Task 1: B1 — Silent Drop of Unpositioned Borrow Findings

#### 2.1.1 Target & Test
- **Target File**: `passes/borrow/check.go:185-199`
- **Reproduction Test**: `passes/borrow/b6a2_fallbackpos_1111_test.go:TestReportWithoutPositionFallsBack`
- **Observed Test Failure**:
  ```
  === RUN   TestReportWithoutPositionFallsBack
      b6a2_fallbackpos_1111_test.go:32: got 0 diagnostics, want 1: a finding without a position was dropped
  --- FAIL: TestReportWithoutPositionFallsBack (0.00s)
  ```

#### 2.1.2 Root Cause Analysis
In `passes/borrow/check.go`:
```go
func (bc *borrowChecker) report(pos token.Pos, cat, msg string) {
	if !pos.IsValid() || bc.ix.Mute(pos) {
		return
	}
	k := reportKey{pos, cat}
	if bc.reports[k] {
		return
	}
	bc.reports[k] = true
	bc.pass.Report(analysis.Diagnostic{
		Pos:      pos,
		Category: cat,
		Message:  cat + ": " + msg,
	})
}
```
- `bc.report` is the centralized reporting sink for all borrow checking diagnostics (`BORROW001`–`BORROW004`).
- When an analysis finding is generated with `pos == token.NoPos` (or `!pos.IsValid()`), the early return `if !pos.IsValid()` immediately discards the finding without reporting anything to `bc.pass.Report`.
- Unlike `passes/storage` (which runs an AST pre-pass that reports syntax-positioned findings and uses SSA only for verification where unpositioned duplicates can be safely dropped), `passes/borrow` operates *exclusively* over SSA and CFGs.
- When an SSA instruction (e.g. synthetic wrapper instruction or unpositioned return) violates a borrow invariant, discarding the finding creates a silent false negative where stack memory corruption or dangling pointer returns pass unnoticed.

#### 2.1.3 Proposed Surgical Patch
Introduce a `fallbackPos()` helper on `borrowChecker` and invoke it before validity and mute checks in `bc.report`:
```go
func (bc *borrowChecker) fallbackPos() token.Pos {
	if bc.fn != nil && bc.fn.Pos().IsValid() {
		return bc.fn.Pos()
	}
	if bc.pass != nil {
		for _, f := range bc.pass.Files {
			if f.Pos().IsValid() {
				return f.Pos()
			}
			if f.Package.IsValid() {
				return f.Package
			}
		}
	}
	return token.NoPos
}

func (bc *borrowChecker) report(pos token.Pos, cat, msg string) {
	if !pos.IsValid() {
		pos = bc.fallbackPos()
	}
	if !pos.IsValid() || (bc.ix != nil && bc.ix.Mute(pos)) {
		return
	}
	k := reportKey{pos, cat}
	if bc.reports[k] {
		return
	}
	bc.reports[k] = true
	bc.pass.Report(analysis.Diagnostic{
		Pos:      pos,
		Category: cat,
		Message:  cat + ": " + msg,
	})
}
```

---

### 2.2 Task 2: B5 & D5 — HTTP Handler Getter Traversal & Synthetic Positions

#### 2.2.1 Targets & Tests
- **Target File**: `passes/ownership/handlerwrite.go`
- **Reproduction Tests**:
  - `TestHandlerWriteThroughGetterOfAnotherPackage` (B5)
  - `TestHandlerWriteGetterChainTerminates` (D5 complexity)
  - `TestHandlerWriteInSyntheticWrapperHasPosition` (D5 position)
- **Observed Test Failures**:
  - `TestHandlerWriteThroughGetterOfAnotherPackage`:
    ```
    getter/getter.go:18: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
    getter/getter.go:26: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
    getter/getter.go:36: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
    getter/getter.go:41: no diagnostic was reported matching `is passed to .*Loop..Run, which may write it`
    --- FAIL: TestHandlerWriteThroughGetterOfAnotherPackage (7.54s)
    ```
  - `TestHandlerWriteGetterChainTerminates`:
    ```
    getterchain/getterchain.go:37: no diagnostic was reported matching `is written here by a handler`
    getterchain/getterchain.go:221: no diagnostic was reported matching `is written here by a handler`
    --- FAIL: TestHandlerWriteGetterChainTerminates (37.04s)
    ```
  - `TestHandlerWriteInSyntheticWrapperHasPosition`:
    ```
    -: unexpected diagnostic: SAFE008: the method value's receiver is passed to (*nopos/dep.Cache[V]).Put, which may write it, by a handler registered with (*net/http.ServeMux).HandleFunc (line 31), which shares it with every concurrent request
    nopos/nopos.go:15: no diagnostic was reported matching `which may write it`
    nopos/nopos.go:19: no diagnostic was reported matching `which may write it`
    nopos/nopos.go:20: no diagnostic was reported matching `which may write it`
    nopos/nopos.go:21: no diagnostic was reported matching `which may write it`
    --- FAIL: TestHandlerWriteInSyntheticWrapperHasPosition (13.72s)
    ```

#### 2.2.2 Root Cause Analysis

1. **B5 (Cross-Package and Multi-Return Getter Bypasses)**:
   - In `handlerwrite.go:380-413`:
     ```go
     case *ssa.Call:
         callee := x.Call.StaticCallee()
         if callee == nil || len(callee.Blocks) == 0 {
             return nil, false
         }
     ```
     For external / other-package functions (`dep.Gateway`), `len(callee.Blocks) == 0`. The function immediately returned `nil, false` without reading `st.summary(callee)` or `FuncFact.Links`.
   - In `handlerwrite.go:330`:
     ```go
     case *ssa.Extract:
         v = x.Tuple
     ```
     When a getter returns a tuple (e.g. `Lookup() (*Loop, bool)` or `Pair() (*Loop, *Loop)`), `v` is an `*ssa.Extract`. Setting `v = x.Tuple` completely discarded the result index `x.Index`.
   - In `handlerwrite.go:400`:
     ```go
     if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
     ```
     It explicitly required `len(ret.Results) == 1`. Any function returning multiple values (like `(val, ok)` or `(loop, err)`) was ignored.

2. **D5 (Exponential Search in Getter Chains)**:
   - In `handlerwrite.go:401-404`:
     ```go
     subSeen := make(map[ssa.Value]bool, len(seen))
     for k, val := range seen {
         subSeen[k] = val
     }
     if pRoot, ok := sharedRootSeen(ret.Results[0], paramRoots, subSeen); ok {
     ```
     For every return instruction in `callee`, `seen` was cloned into a fresh `subSeen` map.
     When a getter chain of depth 24 has 2 returns calling predecessor getters, this creates a complete binary tree of paths ($2^{24} = 16,777,216$ branches). Because failed explorations were not memoized globally across branches, each branch re-computed the entire predecessor chain, causing execution times >37s to 118s.

3. **D5 (Synthetic SSA Wrapper Position Attribution Loss)**:
   - In `handlerwrite.go:162-167`:
     ```go
     func (st *state) followable(fn *ssa.Function) bool {
         if fn.Pkg == st.ssa.Pkg {
             return true
         }
         return fn.Pkg == nil && fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information")
     }
     ```
     When calling a method on a generic type from another package (e.g. `(*dep.Cache[int]).Put`), `go/ssa` synthesizes an instantiation wrapper where `fn.Pkg == nil` and `fn.Synthetic == "instantiation wrapper"`.
     Because `followable` returned true, `checkHandlerWrites` traversed *into* the synthetic wrapper.
     Inside synthetic wrappers, instructions have no source position (`ins.Pos() == token.NoPos`).
     When reporting writes inside the wrapper, `posOf(ins, ins)` returned `token.NoPos` (`-`).
     Because deduplication uses `lineKey{file, line}`, all diagnostics across all 4 calls in the package collapsed into a single diagnostic at `-`. Furthermore, line-specific directives (`//gosree:unsafe`) on the call site could not mute findings emitted at `-`.

#### 2.2.3 Proposed Surgical Patch
1. **Pass `*state` to `sharedRootSeen` and utilize `st.summary(callee)`**:
   - For any call (single-result `*ssa.Call` or tuple `*ssa.Extract`), determine `resIdx`.
   - Inspect `sum := st.summary(callee)`. For each link in `sum.links`:
     If `link.B == np + resIdx && link.A < np`, parameter `link.A` flows into result `resIdx`. Check `sharedRootSeen(call.Call.Args[link.A], roots, memo)`.
2. **Memoize visited values across branches**:
   - Replace the cloning of `subSeen` with a single shared memo table (`visited map[ssa.Value]ssa.Value` and `failed map[ssa.Value]bool`).
   - If `failed[v]`, return `nil, false` immediately in $O(1)$.
   - This bounds the walk to $O(V + E)$ linear time (<1s for depth 24).
3. **Restrict `st.followable` to local synthetic wrappers**:
   ```go
   func (st *state) followable(fn *ssa.Function) bool {
       if fn.Pkg == st.ssa.Pkg {
           return true
       }
       if fn.Pkg == nil && fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information") {
           if o := fn.Origin(); o != nil && o.Pkg != nil {
               return o.Pkg == st.ssa.Pkg
           }
           if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
               return obj.Pkg() == st.pass.Pkg
           }
           return false
       }
       return false
   }
   ```
   When `followable` returns false for external synthetic wrappers, `checkHandlerWrites` invokes `st.checkCalleeWrites(ins, cc, callee, ...)` at the call instruction in the handler source. Diagnostics are reported at the AST call line (`nopos.go:45`, `49`, `50`, `51`), and `//gosree:unsafe` directives properly mute them.

---

### 2.3 Task 3: B6 & C2 — Stdlib Capability Contracts & Stale Hatch Hop Independence

#### 2.3.1 Targets & Tests
- **Target File**: `passes/ownership/contracts.go` & `passes/ownership/graph.go`
- **Reproduction Tests**:
  - `TestStdlibCapabilityContractsDynamicAndInterface` (B6)
  - `TestSharedLoadHatchVerdictIndependentOfCallDepth` (C2)
- **Observed Test Failures**:
  - `TestSharedLoadHatchVerdictIndependentOfCallDepth`:
    ```
    b6a2_fixes_1111_test.go:355: stale hatches at lines [10], want [10 27]: the verdict must not depend on call depth
    --- FAIL: TestSharedLoadHatchVerdictIndependentOfCallDepth (0.12s)
    ```
  - `TestStdlibCapabilityContractsDynamicAndInterface`:
    ```
    analysistest.go:713: capdyn/capdyn.go:158: no diagnostic was reported matching `SAFE003`
    analysistest.go:713: capdyn/capdyn.go:160: no diagnostic was reported matching `SAFE002`
    analysistest.go:713: capdyn/capdyn.go:166: no diagnostic was reported matching `CAP003`
    ...
    analysistest.go:654: capdyn/capdyn.go:109:18: unexpected diagnostic: SAFE002: this value is returned to (*sync.Pool).Put but is not owned here: it may alias result of (*sync.Pool).Get$thunk (not known to return a fresh value)
    analysistest.go:654: capdyn/capdyn.go:18:2: unexpected diagnostic: SAFE006: package variable liveCfg is written outside init
    ```

#### 2.3.2 Root Cause Analysis

1. **B6 (Parameter Index Shifts in Bound Method Values and Interface Invokes)**:
   - In `graph.go:645`:
     ```go
     fn := cc.StaticCallee()
     if fn == nil { ... return }
     ```
     When calling a method value (e.g. `store := liveCfg.Store; store(c1)`), `cc.Value` is a `*ssa.MakeClosure` or closure variable, and `cc.StaticCallee()` returns `nil`. It was treated as an opaque dynamic call, failing to resolve the underlying method wrapper.
   - For bound method wrappers (`strings.HasPrefix(fn.Synthetic, "bound method wrapper")`), the receiver is captured in `FreeVars[0]` rather than being passed in `cc.Args`.
     In `capContracts`, `publishArgs` lists method parameter indices where receiver is `0` (e.g. `publishArgs: []int{1}` for `Store`).
     At the call site of a bound method, `cc.Args` contains only `[c1]`.
     Because `idx = 1 >= len(cc.Args) = 1`, `graph.go:695` skipped the argument entirely!
   - In interface invokes (`cc.IsInvoke()`), `cc.Value` is the receiver interface and `cc.Args` contains only non-receiver arguments.
     Currently, `graph.go:627` treats all invokes as generic escaping calls, and `contracts.go:306` returns immediately on `cc.IsInvoke()`.
   - In `graph.go:637`, interface invokes unconditionally marked results as `evForeign ("result of interface method ...")` and marked the receiver as written (`g.written(ins, cc.Value)`). This emitted false `SAFE002` on `sync.Pool.Get` and false `SAFE006` on package globals holding containers.

2. **C2 (BFS Hop Ordering in Escape Hatch Analysis)**:
   - In `contracts.go:263-360`, write checking (`rootOf(w.addr)` -> `emit`) occurred *inside* the BFS queue draining loop.
   - Consider two calls to `mutateHop(c)`:
     - `aHop` (1 hop away) loads `c` under `//gosree:unsafe` (`muted: true`).
     - `bHop -> hop -> mutateHop` (2 hops away) loads `c` unhatched (`muted: false`).
   - Because `aHop` is closer in BFS distance, `mutateHop` is popped first with `muted: true`.
     `emit` runs, marks the finding as muted, and calls `st.ix.Mute(orig.pos)` on line 27.
   - When `bHop -> hop` arrives later, it updates `mutateHop`'s origin to `muted: false` and emits the unmuted finding.
   - However, `st.ix.Mute(orig.pos)` was already called on line 27! Consequently, `stale.Analyzer` believes the escape hatch at line 27 was used, dropping it from the stale hatch report.
   - In contrast, in `aDirect` vs `bDirect` (both 1 hop away), `bDirect` can enqueue before `aDirect` is processed, so `mutateDirect` is initialized with `muted: false`, preventing `st.ix.Mute` from firing and leaving line 10 correctly reported as stale. The stale hatch verdict thus improperly depended on call depth!

#### 2.3.3 Proposed Surgical Patch
1. **Resolve Callables and Shift Parameter Indices in `graph.go`**:
   - In `graph.go:call`:
     - If `fn == nil`, resolve via `directCallee(cc.Value)`.
     - Detect bound method wrappers (`isBound := fn != nil && strings.HasPrefix(fn.Synthetic, "bound method wrapper")`).
     - Detect interface invokes matching container contracts via `unwrapInterface(cc.Value)`.
     - For bound methods and interface invokes, shift parameter index: `argIdx := idx - 1`.
     - For `contract.freshResult` (e.g. `sync.Pool.Get`), transfer unique `Iso` ownership without marking `result` as `evForeign`.
2. **Two-Phase Architecture in `checkSharedLoadWrites`**:
   - **Phase 1: Fixed-Point Propagation**:
     Drain `queue` completely to propagate `sharedLoadOrigin`. Unmuted origins supersede muted origins (`!prev.muted || orig.muted`).
   - **Phase 2: Converged Write Checking**:
     Only after `queue` is completely empty, iterate through all instructions to detect writes.
     At this point, if any unhatched path reached a write, `shared[fn][v].muted` is guaranteed to be `false`, regardless of BFS hop distance. `st.ix.Mute(orig.pos)` will fire if and only if all paths reaching the write were hatched.

---

## 3. Verification Plan

| Area | Reproduction Test Command | Success Criteria |
|---|---|---|
| **B1** | `cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/borrow -run '^TestReportWithoutPositionFallsBack$'` | PASS (1 diagnostic reported at fallback file position) |
| **B5 / D5** | `cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run 'TestHandlerWriteThroughGetterOfAnotherPackage\|TestHandlerWriteGetterChainTerminates\|TestHandlerWriteInSyntheticWrapperHasPosition'` | PASS (Cross-package getters caught; chain test finishes <1s; synthetic wrapper findings reported at calling lines) |
| **B6 / C2** | `cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run 'TestStdlibCapabilityContractsDynamicAndInterface\|TestSharedLoadHatchVerdictIndependentOfCallDepth'` | PASS (Lines [10 27] reported stale regardless of hop depth; all SAFE002/003/005/CAP003 caught) |
| **Regression** | `cd /Users/bharath/Code/devtools/gocap-worktree && go test ./passes/borrow/... ./passes/ownership/...` | 100% PASS across all existing and adversarial tests |
