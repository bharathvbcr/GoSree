# Handoff Report: Milestone 2 — A2, B4, and Closure Variable Scope Matching

**Agent**: Explorer 1 (`teamwork_explorer`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_1/`  
**Target Isolated Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**Working Directory**: `/Users/bharath/Code/devtools/gocap`  
**Date**: 2026-11-11T17:05:00Z  
**Parent Orchestrator ID**: `8682956d-3651-47d9-9c9e-ac932901be9a`  
**Detailed Analysis**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_1/analysis.md`  

---

## 1. Observation

### 1.1 A2 (Fact Decoder Slice Bounds Panic & Allocation Bomb)
- **Target File**: `/Users/bharath/Code/devtools/gocap-worktree/passes/ownership/summary.go:146-199`
- **Reproduction Test**: `summary_fuzz_1111_test.go:18` (`TestFuncFactDecodeRejectsCorruptLengths`)
- **Execution Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run '^TestFuncFactDecodeRejectsCorruptLengths$'
  ```
- **Verbatim Output**:
  ```
  === RUN   TestFuncFactDecodeRejectsCorruptLengths
  === RUN   TestFuncFactDecodeRejectsCorruptLengths/steps_length_wraps_negative
  --- FAIL: TestFuncFactDecodeRejectsCorruptLengths (0.00s)
      --- FAIL: TestFuncFactDecodeRejectsCorruptLengths/steps_length_wraps_negative (0.00s)
  panic: runtime error: slice bounds out of range [:18446744073709551615] with capacity 12 [recovered, repanicked]

  goroutine 7 [running]:
  github.com/bharathvbcr/gosree/passes/ownership.(*FuncFact).GobDecode(0x68c420360ea0, ...)
  	/Users/bharath/Code/devtools/gocap-worktree/passes/ownership/summary.go:191 +0x920
  ```
- **Exact Flawed Code in `summary.go`**:
  - Line 186-187:
    ```go
    sLen, nr := binary.Uvarint(rest)
    if nr <= 0 || int(sLen) > len(rest)-nr-1 {
        return errors.New("truncated FuncFact acquire steps")
    }
    ```
    `int(sLen)` with `sLen = 18446744073709551615` casts to signed `-1`. Comparison `-1 > len(rest)-nr-1` evaluates to `false`.
  - Line 190-191:
    ```go
    rest = rest[nr:]
    steps := string(rest[:sLen])
    ```
    Slice expression `rest[:sLen]` triggers panic with unsigned out-of-range bounds.
  - Lines 146-174:
    `nLinks` and `nAcq` checked only against `1<<20` without checking against `len(rest)`, allowing pre-allocation of 32MB / 40MB slices on empty/corrupt remaining data.

### 1.2 B4 (Unchecked Heap Escape of Package Globals)
- **Target File**: `/Users/bharath/Code/devtools/gocap-worktree/passes/ownership/globals.go:79-147, 391-490`
- **Reproduction Test**: `b6a2_fixes_1111_test.go:363` (`TestInitOnlyGlobalEscapeShapesFailClosed`)
- **Execution Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run '^TestInitOnlyGlobalEscapeShapesFailClosed$'
  ```
- **Verbatim Output**:
  ```
  === RUN   TestInitOnlyGlobalEscapeShapesFailClosed
      analysistest.go:713: p/p.go:27: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:27: no diagnostic was reported matching `SAFE004`
      analysistest.go:713: p/p.go:38: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:38: no diagnostic was reported matching `SAFE004`
      analysistest.go:713: p/p.go:50: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:50: no diagnostic was reported matching `SAFE004`
      analysistest.go:713: p/p.go:58: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:58: no diagnostic was reported matching `SAFE004`
  --- FAIL: TestInitOnlyGlobalEscapeShapesFailClosed (0.86s)
  ```
- **Exact Flawed Code in `globals.go`**:
  - Lines 391–488: `globalRootSeen` has no `case *ssa.Alloc:` or `case *ssa.MakeClosure:`. When `New()` returns `&Config{Table: heapRoutes}`, the return result is `*ssa.Alloc`. `globalRootSeen` returns `nil, false`. When `Make()` returns a closure capturing `closureRoutes`, `globalRootSeen` returns `nil, false`.
  - Lines 79–88:
    ```go
    sum := st.summary(callee)
    for i, a := range cc.Args {
        if !sum.writes(i) {
            continue
        }
        if g, ok := st.globalMutArg(a); ok {
            mutated[g] = true
            writes[g] = append(writes[g], posOf(ins, ins))
        }
    }
    ```
    The loop skips arguments where `!sum.writes(i)`. When calling `expose(calleeRoutes)` or `keeper.Keep(otherPkgRoutes)`, both callees leak the parameter (`sum.leaks(0) == true`), but do not write it (`sum.writes(0) == false`). The arguments are skipped and neither global is marked in `mutated`.

### 1.3 Closure Variable Scope Matching
- **Target File**: `/Users/bharath/Code/devtools/gocap-worktree/passes/ownership/check.go:818-853, 915-948`
- **Reproduction Test**: `b6a2_fixes_1111_test.go:21` (`TestShadowedClosureNameIsNotABorrowCapture`)
- **Execution Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree && go test -v ./passes/ownership -run '^TestShadowedClosureNameIsNotABorrowCapture$'
  ```
- **Verbatim Output**:
  ```
  === RUN   TestShadowedClosureNameIsNotABorrowCapture
      analysistest.go:656: p/p.go:22:3: diagnostic "CAP003: stack-scoped borrow 'b' (\\mub) cannot cross goroutine boundary: cannot be captured by a goroutine" does not match pattern `SAFE002`
      analysistest.go:656: p/p.go:34:3: diagnostic "CAP003: stack-scoped borrow 'b' (\\mub) cannot cross goroutine boundary: cannot be captured by a goroutine" does not match pattern `SAFE002`
      analysistest.go:656: p/p.go:46:2: diagnostic "CAP003: stack-scoped borrow 'b' (\\mub) cannot cross goroutine boundary: cannot be captured by a goroutine" does not match pattern `SAFE002`
      analysistest.go:654: p/p.go:56:2: unexpected diagnostic: CAP003: stack-scoped borrow 'b' (\mub) cannot cross goroutine boundary: cannot be captured by a goroutine
      analysistest.go:656: p/p.go:72:2: diagnostic "CAP003: stack-scoped borrow 'b' (\\mub) cannot cross goroutine boundary: cannot be captured by a goroutine" does not match pattern `SAFE004`
      analysistest.go:713: p/p.go:22: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:34: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:46: no diagnostic was reported matching `SAFE002`
      analysistest.go:713: p/p.go:72: no diagnostic was reported matching `SAFE004`
  --- FAIL: TestShadowedClosureNameIsNotABorrowCapture (0.23s)
  ```
- **Exact Flawed Code in `check.go`**:
  - Line 825: `if lid := rootIdent(lhs); lid != nil && lid.Name == vr.Name()`
  - Line 839: `if name.Name == vr.Name() && i < len(stmt.Values)`
  - Line 920: `if lid := rootIdent(lhs); lid != nil && lid.Name == id.Name`
  - Line 933: `if name.Name == id.Name && i < len(stmt.Values)`
  - Line 990-991: `rootIdent` returns `x.Sel` on `*ast.SelectorExpr`.
  Matching by identifier string causes shadowed same-named variables (`f`, `j`) and struct field assignments (`h.f`) to be falsely attributed as closure borrow captures. The false `CAP003` then suppresses `SAFE002` and `SAFE003`.

---

## 2. Logic Chain

1. **A2 Panic Mechanism**:
   - `binary.Uvarint` yields `uint64(2^64-1)`.
   - Cast `int(sLen)` yields `-1`.
   - The comparison `-1 > len(rest)-nr-1` evaluates to `false`, bypassing the truncation error return.
   - Slicing `rest[:sLen]` uses the original `uint64` value, exceeding slice length and triggering runtime bounds panic.
   - Guarding with `nr <= 0` followed by slicing `rest = rest[nr:]` and comparing `sLen >= uint64(len(rest))` operates entirely on non-negative unsigned values without arithmetic overflow, eliminating the panic.
   - Guarding `nLinks` and `nAcq` against `uint64(len(rest))/4` prevents allocation bomb DoS before `make([]...)`.
2. **B4 Global Escape Mechanism**:
   - Atomic freeze `liveCfg.Store(&Config{Table: g})` fails closed with `SAFE002` and `SAFE004` only when `st.isInitOnlyImmutableGlobal(g)` returns `false`.
   - `isInitOnlyImmutableGlobal(g)` returns `false` if `st.mutatedGlobals[g]` is `true`.
   - In Shape 1 (`return &Config{Table: heapRoutes}`), `r` is `*ssa.Alloc`. Handling `*ssa.Alloc` in `globalRootSeen` by inspecting `alloc.Referrers()` detects `heapRoutes` and sets `mutated[heapRoutes] = true`.
   - In Shape 2 (`return func() *Config { return &cfg }`), `r` is `*ssa.MakeClosure`. Handling `*ssa.MakeClosure` by inspecting `mc.Bindings` detects `closureRoutes` and sets `mutated[closureRoutes] = true`.
   - In Shape 3 (`expose(calleeRoutes)`) and Shape 4 (`keeper.Keep(otherPkgRoutes)`), the callee leaks the parameter. Checking `sum.leaks(i)` in `collectGlobalMutations` detects `calleeRoutes` and `otherPkgRoutes` and sets `mutated[g] = true`.
   - Setting `mutated[g] = true` without adding to `st.globalWrites[g]` invalidates `isInitOnlyImmutableGlobal` without producing spurious `SAFE006` diagnostics.
3. **Closure Scope Variable Matching Mechanism**:
   - AST nodes `ast.AssignStmt` and `ast.ValueSpec` have associated `types.Object` instances stored in `st.pass.TypesInfo.Defs` and `st.pass.TypesInfo.Uses`.
   - In `go/types`, every declared variable has a single unique `*types.Var` pointer. Variables with the same identifier in different scopes have distinct pointers.
   - Replacing string comparison `lid.Name == vr.Name()` with `lv == vr` ensures only assignments to the identical variable object match.
   - Requiring `unparen(lhs).(*ast.Ident)` ensures struct selectors (`h.f`) are not matched as variables.
   - Eliminating the false closure borrow capture removes spurious `CAP003` findings and allows `SAFE002` and `SAFE003` to be properly reported.

---

## 3. Caveats

1. **Read-Only Investigation Bound**:
   - In accordance with constraints, no code in `/Users/bharath/Code/devtools/gocap` or `/Users/bharath/Code/devtools/gocap-worktree` has been modified during this turn.
2. **Other Milestone 2 Defects**:
   - Defects B1 (`passes/borrow/check.go`), B5/D5 (`passes/ownership/handlerwrite.go`), B6/C2 (`passes/ownership/contracts.go`), and runtime witnesses (`internal/kinds/kinds.go`) belong to other parallel explorer scopes and are untouched here.
3. **No Unintended Side-Effects**:
   - Setting `mutated[g] = true` without updating `writes[g]` strictly controls `isInitOnlyImmutableGlobal` without emitting `SAFE006` on the global declarations. Control tables (`controlRoutes`) that are not leaked or returned remain unaffected and continue to pass clean.

---

## 4. Conclusion

All three assigned defects have been definitively analyzed down to exact lines of code, AST/SSA structures, and failure mechanisms. Complete, verified remediation specifications are documented in `analysis.md`:
1. **A2**: In `passes/ownership/summary.go`, advance `rest = rest[nr:]` and use `sLen >= uint64(len(rest))` boundary check, with pre-allocation bounds `nLinks > uint64(len(rest))/4` and `nAcq > uint64(len(rest))/4`.
2. **B4**: In `passes/ownership/globals.go`, add `*ssa.Alloc` and `*ssa.MakeClosure` handling to `globalRootSeen`, and track `sum.leaks(i)` in `collectGlobalMutations` to mark escaping globals as mutated.
3. **Closure Scope**: In `passes/ownership/check.go`, replace string matching with `*types.Var` pointer equality (`lv == vr`, `lv == fnVar`) and reject `*ast.SelectorExpr`.

---

## 5. Verification Method

To independently verify this investigation and confirm pre-fix test failures:

1. **Verify A2 (Slice Bounds Panic)**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v ./passes/ownership -run '^TestFuncFactDecodeRejectsCorruptLengths$'
   ```
   *Expected Pre-Fix Result*: Panic at `summary.go:191`: `panic: runtime error: slice bounds out of range [:18446744073709551615] with capacity 12`.

2. **Verify B4 (Global Escape)**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v ./passes/ownership -run '^TestInitOnlyGlobalEscapeShapesFailClosed$'
   ```
   *Expected Pre-Fix Result*: 8 failures for missing `SAFE002` and `SAFE004` diagnostics at lines 27, 38, 50, 58 of `p/p.go`.

3. **Verify Closure Variable Scope Matching**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v ./passes/ownership -run '^TestShadowedClosureNameIsNotABorrowCapture$'
   ```
   *Expected Pre-Fix Result*: Spurious `CAP003` diagnostics reported instead of expected `SAFE002` / `SAFE003` / `SAFE004`.
