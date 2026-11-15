# Milestone 2 Technical Analysis: A2, B4, and Closure Variable Scope Matching

**Target Isolated Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**Package Under Investigation**: `github.com/bharathvbcr/gosree/passes/ownership`  
**Investigator**: Explorer 1 (`teamwork_explorer`)  
**Date**: 2026-11-11  

---

## Executive Summary

This report documents the deep-dive root cause analysis and surgical remediation specifications for three critical defects in GoSree's ownership analysis pass (`passes/ownership`):
1. **A2 (Fact Decoder Slice Bounds Panic & Allocation Bomb)** in `summary.go` causing panic `runtime error: slice bounds out of range [:18446744073709551615] with capacity 12` on corrupt payload lengths and potential DoS via unbounded memory allocation.
2. **B4 (Unchecked Heap Escape of Package Globals)** in `globals.go` causing four distinct escape shapes (allocated struct return, closure capture, unexported callee propagation, external package callee retention) to bypass `SAFE002` / `SAFE004` fail-closed enforcement.
3. **Closure Variable Scope Matching Defect** in `check.go` causing false `CAP003` diagnostics and suppression of genuine `SAFE002` / `SAFE003` diagnostics due to string-based variable matching (`.Name == .Name`) and selector peeling across shadowed scopes.

---

## 1. Task 1: A2 — Fact Decoder Slice Bounds Panic & Allocation Bomb

### 1.1 Target File & Test
- **File**: `passes/ownership/summary.go` (lines 146–199)
- **Reproduction Test**: `passes/ownership/summary_fuzz_1111_test.go:18` (`TestFuncFactDecodeRejectsCorruptLengths`)

### 1.2 Observed Failure
Running `go test -v ./passes/ownership -run '^TestFuncFactDecodeRejectsCorruptLengths$'`:
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

### 1.3 Root Cause Analysis

#### A. Integer Wrap in `int(sLen)` and Slice Out-of-Bounds Panic
In `passes/ownership/summary.go:186-193`:
```go
186: 			sLen, nr := binary.Uvarint(rest)
187: 			if nr <= 0 || int(sLen) > len(rest)-nr-1 {
188: 				return errors.New("truncated FuncFact acquire steps")
189: 			}
190: 			rest = rest[nr:]
191: 			steps := string(rest[:sLen])
192: 			rest = rest[sLen:]
193: 			mode := lockMode(rest[0])
```

1. **Unsigned to Signed Wrap**:
   - `sLen` is decoded as `uint64`. In `TestFuncFactDecodeRejectsCorruptLengths`, `huge` is the 10-byte uvarint for $2^{64}-1$ (`0xff, 0xff, ..., 0x01`).
   - `binary.Uvarint(rest)` decodes `sLen = 18446744073709551615` ($2^{64}-1$) with `nr = 10`.
   - On 64-bit architectures, `int(sLen)` casts `uint64(0xffffffffffffffff)` to signed `int`, which equals `-1`.
2. **Bypassing the Guard**:
   - The boundary check on line 187 evaluates `int(sLen) > len(rest)-nr-1`.
   - Here `int(sLen)` is `-1`. Since `len(rest) - nr - 1` is $\ge -1$, the condition `-1 > ...` evaluates to `false`!
   - The validation check fails to trigger.
3. **Slice Bounds Panic**:
   - On line 190, `rest = rest[nr:]` advances past the uvarint bytes.
   - On line 191, `rest[:sLen]` slices `rest` using the original unsigned `sLen` ($18446744073709551615$).
   - Go's runtime slice bounds check detects that the slice index exceeds capacity, immediately triggering a fatal panic.

#### B. Allocation Bomb Vulnerability in `nLinks` and `nAcq`
In `passes/ownership/summary.go:146-174`:
```go
146: 	nLinks, r := binary.Uvarint(rest)
147: 	if r <= 0 || nLinks > 1<<20 {
148: 		return errors.New("corrupt FuncFact links length")
149: 	}
150: 	rest = rest[r:]
151: 	if nLinks > 0 {
152: 		f.Links = make([]Link, int(nLinks))
```
1. `nLinks` and `nAcq` are validated only against an arbitrary upper bound of $1 \ll 20$ (`1,048,576`).
2. An attacker can craft a 3-byte uvarint payload (`0x80, 0x80, 0x40`) claiming $1,048,576$ links with 0 bytes of remaining data.
3. Line 152 immediately executes `make([]Link, 1048576)`. Since `sizeof(Link) = 32 bytes` (4 $\times$ 8-byte ints), this allocates **33.5 MB** of memory before any link data is read.
4. For acquires (`sizeof(acquire) = 40 bytes`), `make([]acquire, 1048576)` allocates **41.9 MB**.
5. Repeated requests with this tiny 9-byte payload cause extreme heap pressure and GC thrashing (allocation bomb DoS).
6. **Physical Wire-Length Invariant**:
   - Each `Link` encodes 4 varints (`A`, `DepthA`, `B`, `DepthB`). Each varint takes at least 1 byte, so a valid `Link` requires at least 4 bytes in `rest`.
   - Each `acquire` encodes 2 varints (`Param`, `Free`), 1 uvarint (`sLen`), 0+ bytes (`Steps`), and 1 byte (`Mode`). An `acquire` requires at least 4 bytes in `rest`.
   - Therefore, `nLinks > uint64(len(rest))/4` and `nAcq > uint64(len(rest))/4` are physically impossible in well-formed data and must be rejected before any allocation occurs.

### 1.4 Detailed Remediation Specification

1. **Safe Slicing & Boundary Check for `sLen`**:
   - First validate `nr > 0`.
   - Advance `rest = rest[nr:]` immediately so `len(rest)` represents the exact remaining bytes available for steps and mode.
   - For an acquire step, `rest` must hold `sLen` bytes of string plus at least 1 byte of `mode` (`rest[0]`).
   - Check `sLen >= uint64(len(rest))`. If true, there are insufficient bytes for steps + mode, so return `errors.New("truncated FuncFact acquire steps")`.
   - Because `sLen` and `uint64(len(rest))` are both unsigned, and no arithmetic (`+ 1` or `- 1`) is performed on `sLen`, this check is completely immune to both signed wrap and unsigned integer overflow.
2. **Pre-Allocation Size Guards for `nLinks` and `nAcq`**:
   - After reading `nLinks, r := binary.Uvarint(rest)` and slicing `rest = rest[r:]`, verify:
     ```go
     if nLinks > uint64(len(rest))/4 {
         return errors.New("corrupt FuncFact links length")
     }
     ```
   - After reading `nAcq, r := binary.Uvarint(rest)` and slicing `rest = rest[r:]`, verify:
     ```go
     if nAcq > uint64(len(rest))/4 {
         return errors.New("corrupt FuncFact acquires length")
     }
     ```

---

## 2. Task 2: B4 — Unchecked Heap Escape of Package Globals

### 2.1 Target File & Test
- **File**: `passes/ownership/globals.go` (lines 21–147, 199–257, 372–490)
- **Reproduction Test**: `passes/ownership/b6a2_fixes_1111_test.go:363` (`TestInitOnlyGlobalEscapeShapesFailClosed`)

### 2.2 Observed Failure
Running `go test -v ./passes/ownership -run '^TestInitOnlyGlobalEscapeShapesFailClosed$'`:
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

### 2.3 Root Cause Analysis

In GoSree, when a global variable is frozen into an atomic pointer (`liveCfg.Store(&Config{Table: g})`), `checkTransfer` in `passes/ownership/check.go:60` suppresses `SAFE002` (NotOwned) and `SAFE004` (Escapes) if and only if `st.isInitOnlyImmutableGlobal(g)` returns `true`:
```go
if t.freezesToImm && e.global != nil && st.isInitOnlyImmutableGlobal(e.global) {
    continue
}
```
`isInitOnlyImmutableGlobal(g)` in `globals.go:201` checks:
```go
if st.mutatedGlobals[g] || len(st.globalWrites[g]) > 0 {
    return false
}
```
If a package global's backing state escapes to callers or other packages, it can be mutated concurrently outside package initialisation. Therefore, such globals MUST be marked in `st.mutatedGlobals[g] = true` so `isInitOnlyImmutableGlobal` returns `false`, causing the atomic freeze to fail closed with `SAFE002` and `SAFE004`.

The test `TestInitOnlyGlobalEscapeShapesFailClosed` defines four escaping global shapes, all four of which currently fail to be marked in `st.mutatedGlobals`:

#### Shape 1: Global Returned in Allocated Composite Literal (`heapRoutes`)
```go
var heapRoutes = map[string]int{"a": 1}
func New() *Config { return &Config{Table: heapRoutes} }
func freezeHeap() { liveCfg.Store(&Config{Table: heapRoutes}) } // want "SAFE002" "SAFE004"
```
- **SSA representation**:
  - `New()` allocates `t0 = new Config` (`*ssa.Alloc`).
  - It computes `t1 = &t0.Table` (`*ssa.FieldAddr`).
  - It stores `heapRoutes` into `t1` (`*ssa.Store`).
  - It returns `t0` (`*ssa.Return`).
- **Defect in `globals.go`**:
  - Lines 104–112 scan `*ssa.Return` for exported functions and call `st.globalMutArg(r)`.
  - `globalMutArg` calls `globalRootSeen(v, seen)`.
  - In `globalRootSeen` (lines 391–488), the logic to inspect `alloc.Referrers()` for stores of globals was placed exclusively under `case *ssa.UnOp` when `x.Op == token.MUL` (dereference `*p`).
  - There is NO `case *ssa.Alloc:` in `globalRootSeen`!
  - When a function returns a pointer to a struct (`return &Config{...}`), the return value is `*ssa.Alloc`, NOT `*ssa.UnOp`.
  - `globalRootSeen(*ssa.Alloc)` hits the default case and returns `nil, false`.
  - Result: `heapRoutes` is never marked in `st.mutatedGlobals`.

#### Shape 2: Global Captured by a Returned Closure (`closureRoutes`)
```go
var closureRoutes = map[string]int{"b": 2}
func Make() func() *Config {
    cfg := Config{Table: closureRoutes}
    return func() *Config { return &cfg }
}
func freezeClosure() { liveCfg.Store(&Config{Table: closureRoutes}) } // want "SAFE002" "SAFE004"
```
- **SSA representation**:
  - `Make()` allocates `cfg`, stores `closureRoutes` into `cfg.Table`, and builds `t_closure = make closure [cfg]`.
  - It returns `t_closure` (`*ssa.MakeClosure`).
- **Defect in `globals.go`**:
  - In `globalRootSeen`, there is NO `case *ssa.MakeClosure:`.
  - Ssa return value `r` is `*ssa.MakeClosure`. Its bindings (`mc.Bindings`) hold the captured variables (`&cfg`).
  - Because `*ssa.MakeClosure` is not handled, `globalRootSeen` returns `nil, false`.
  - Furthermore, if a closure's body (`mc.Fn`) returns a global directly, `globalRootSeen` does not inspect the closure's returns.
  - Result: `closureRoutes` is never marked in `st.mutatedGlobals`.

#### Shape 3: Global Escaping via Unexported Callee (`calleeRoutes`)
```go
var Exposed map[string]int // want "SAFE006"
var calleeRoutes = map[string]int{"c": 3}
func Setup() { expose(calleeRoutes) }
func expose(m map[string]int) { Exposed = m }
func freezeCallee() { liveCfg.Store(&Config{Table: calleeRoutes}) } // want "SAFE002" "SAFE004"
```
- **SSA representation**:
  - `Setup()` calls `expose(calleeRoutes)`.
  - `expose(m)` stores parameter `m` into exported package global `Exposed`.
- **Defect in `globals.go`**:
  - In `collectGlobalMutations` (lines 79–88):
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
  - The loop checks ONLY `if !sum.writes(i) { continue }`. It completely ignores `sum.leaks(i)`!
  - In `expose(m)`, storing `m` into package variable `Exposed` creates an `evEscape` event in the graph, so `sum.leaks(0)` is `true`. But `sum.writes(0)` is `false` (no memory reachable from `m` is mutated).
  - Because `!sum.writes(0)` is true, line 81 skips the argument!
  - Result: `calleeRoutes` is never marked in `st.mutatedGlobals`.

#### Shape 4: Global Leaked to External Package Callee (`otherPkgRoutes`)
```go
var otherPkgRoutes = map[string]int{"d": 4}
func SetupOther() { keeper.Keep(otherPkgRoutes) }
func freezeOther() { liveCfg.Store(&Config{Table: otherPkgRoutes}) } // want "SAFE002" "SAFE004"
```
- **SSA representation**:
  - `SetupOther()` calls `keeper.Keep(otherPkgRoutes)`.
  - `keeper.Keep` has summary `Leaks[0] = true`, `Writes[0] = false` (`kept = append(kept, m)`).
- **Defect in `globals.go`**:
  - Identical to Shape 3: lines 79–88 only check `sum.writes(i)`. Since `keeper.Keep` does not write `m`, `sum.writes(0)` is false.
  - The call is skipped, and `otherPkgRoutes` is never marked in `st.mutatedGlobals`.

### 2.4 Detailed Remediation Specification

1. **Add `*ssa.Alloc` Handling to `globalRootSeen`**:
   Extract a helper `allocRootsAtGlobal(alloc *ssa.Alloc, seen map[ssa.Value]bool) (*ssa.Global, bool)`:
   - Check `alloc.Referrers()`.
   - For direct store: `st, ok := ref.(*ssa.Store)` where `st.Addr == alloc`, recurse on `globalRootSeen(st.Val, seen)`.
   - For field store: `fa, ok := ref.(*ssa.FieldAddr)`, for `r2 := range *fa.Referrers()` where `st.Addr == fa`, recurse on `globalRootSeen(st.Val, seen)`.
   - For index store: `ia, ok := ref.(*ssa.IndexAddr)`, for `r2 := range *ia.Referrers()` where `st.Addr == ia`, recurse on `globalRootSeen(st.Val, seen)`.
   - In `globalRootSeen`, dispatch both `case *ssa.Alloc:` and `case *ssa.UnOp:` (when `x.Op == token.MUL`) to `allocRootsAtGlobal`.
2. **Add `*ssa.MakeClosure` Handling to `globalRootSeen`**:
   - For `case *ssa.MakeClosure:`:
     - Iterate `x.Bindings`: if `g, ok := globalRootSeen(b, seen); ok { return g, true }`.
     - Iterate instructions of `x.Fn`: if `ret, ok := ins.(*ssa.Return)`, check all `ret.Results` with `globalRootSeen(r, seen)`.
3. **Track Leaking Function Arguments in `collectGlobalMutations`**:
   In `globals.go:79-88`:
   ```go
   sum := st.summary(callee)
   for i, a := range cc.Args {
       if sum.writes(i) {
           if g, ok := st.globalMutArg(a); ok {
               mutated[g] = true
               writes[g] = append(writes[g], posOf(ins, ins))
           }
       } else if sum.leaks(i) {
           if g, ok := st.globalMutArg(a); ok {
               mutated[g] = true
           }
       }
   }
   ```
   Note: Do not append to `writes[g]` for leaks; `writes[g]` is reserved for `SAFE006` diagnostics. Setting `mutated[g] = true` correctly invalidates `isInitOnlyImmutableGlobal(g)` while preventing spurious `SAFE006` errors.

---

## 3. Task 3: Closure Variable Scope Matching

### 3.1 Target File & Test
- **File**: `passes/ownership/check.go` (lines 818–853, 915–948)
- **Reproduction Test**: `passes/ownership/b6a2_fixes_1111_test.go:21` (`TestShadowedClosureNameIsNotABorrowCapture`)

### 3.2 Observed Failure
Running `go test -v ./passes/ownership -run '^TestShadowedClosureNameIsNotABorrowCapture$'`:
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

### 3.3 Root Cause Analysis

In `passes/ownership/check.go:42-47`:
```go
borrowEscaped := st.checkBorrowEscape(g, t)
if borrowEscaped && t.what != "captured by a goroutine" {
    return
}
```
When `checkBorrowEscape` returns `true`, it reports a `CAP003` finding and aborts the remaining ownership transfer checks (lines 87–99), which suppresses `SAFE002` (NotOwned) and `SAFE004` (Escapes).

The root cause of the false `CAP003` diagnostics is located in `check.go` at:
- Lines 825 and 839 in `findBorrowsInLitInSyntax`
- Lines 920 and 933 in `identifyCapturedBorrowsSpawn`

```go
820: 				ast.Inspect(outerSyntax, func(sub ast.Node) bool {
821: 					switch stmt := sub.(type) {
822: 					case *ast.AssignStmt:
823: 						for i, lhs := range stmt.Lhs {
824: 							if i < len(stmt.Rhs) {
825: 								if lid := rootIdent(lhs); lid != nil && lid.Name == vr.Name() {
826: 									if innerFl, ok := unparen(stmt.Rhs[i]).(*ast.FuncLit); ok && innerFl != fl {
...
837: 					case *ast.ValueSpec:
838: 						for i, name := range stmt.Names {
839: 							if name.Name == vr.Name() && i < len(stmt.Values) {
```

#### Flaw 1: Identifier String Matching (`lid.Name == vr.Name()`)
- In `shadowed(b *B, files []*File)`:
  - An inner block defines `f := func() { b.n++ }` (where `f` is a closure capturing borrow `b`).
  - Later, a loop defines `for _, f := range files { go func() { f.Close() }() }`.
  - Inside the goroutine closure, `f.Close()` references the loop variable `f` (`*File`).
  - `findBorrowsInLitInSyntax` inspects `outerSyntax` looking for nested closure assignments to `vr`.
  - Line 825 checks `lid.Name == vr.Name()`. Both are named `"f"`.
  - Even though the inner `f` has already gone out of scope and is a completely distinct variable, the string equality check succeeds!
  - It falsely concludes that `go func() { f.Close() }()` captures borrow `b`, emitting a spurious `CAP003` and suppressing `SAFE002`.

#### Flaw 2: Selector Peeling in `rootIdent`
- In `selectorLHS(b *B, files []*File)`:
  - `var h struct{ f func() }`
  - `h.f = func() { b.n++ }`
  - In `check.go:990-991`:
    ```go
    case *ast.SelectorExpr:
        return x.Sel
    ```
  - `rootIdent(h.f)` peels the struct selector and returns identifier `f`.
  - `lid.Name == vr.Name()` compares field name `"f"` with variable name `"f"`, matching struct field assignment `h.f = ...` as if it were a declaration of variable `f`!

### 3.4 Detailed Remediation Specification

Replace string matching with canonical `*types.Var` pointer comparison using `st.pass.TypesInfo.Defs` and `st.pass.TypesInfo.Uses`.

In `go/types`, every declared variable has a unique `*types.Var` pointer. Two variables with the same identifier in different scopes have distinct `*types.Var` pointers, guaranteeing 100% precision.

1. **In `findBorrowsInLitInSyntax` (lines 820–852)**:
   ```go
   ast.Inspect(outerSyntax, func(sub ast.Node) bool {
       switch stmt := sub.(type) {
       case *ast.AssignStmt:
           for i, lhs := range stmt.Lhs {
               if i < len(stmt.Rhs) {
                   if lid, ok := unparen(lhs).(*ast.Ident); ok {
                       var lv *types.Var
                       if v, ok := st.pass.TypesInfo.Defs[lid].(*types.Var); ok {
                           lv = v
                       } else if v, ok := st.pass.TypesInfo.Uses[lid].(*types.Var); ok {
                           lv = v
                       }
                       if lv != nil && lv == vr {
                           if innerFl, ok := unparen(stmt.Rhs[i]).(*ast.FuncLit); ok && innerFl != fl {
                               for _, b := range st.findBorrowsInLitInSyntax(innerFl, outerSyntax, depth+1) {
                                   if !seen[b.vr] {
                                       seen[b.vr] = true
                                       res = append(res, b)
                                   }
                               }
                           }
                       }
                   }
               }
           }
       case *ast.ValueSpec:
           for i, name := range stmt.Names {
               if i < len(stmt.Values) {
                   if lv, ok := st.pass.TypesInfo.Defs[name].(*types.Var); ok && lv == vr {
                       if innerFl, ok := unparen(stmt.Values[i]).(*ast.FuncLit); ok && innerFl != fl {
                           for _, b := range st.findBorrowsInLitInSyntax(innerFl, outerSyntax, depth+1) {
                               if !seen[b.vr] {
                                   seen[b.vr] = true
                                   res = append(res, b)
                               }
                           }
                       }
                   }
               }
           }
       }
       return true
   })
   ```

2. **In `identifyCapturedBorrowsSpawn` (lines 915–948)**:
   ```go
   if id, ok := unparen(node.Call.Fun).(*ast.Ident); ok {
       fnVar, _ := st.pass.TypesInfo.Uses[id].(*types.Var)
       if fnVar != nil {
           ast.Inspect(syntax, func(sub ast.Node) bool {
               switch stmt := sub.(type) {
               case *ast.AssignStmt:
                   for i, lhs := range stmt.Lhs {
                       if i < len(stmt.Rhs) {
                           if lid, ok := unparen(lhs).(*ast.Ident); ok {
                               var lv *types.Var
                               if v, ok := st.pass.TypesInfo.Defs[lid].(*types.Var); ok {
                                   lv = v
                               } else if v, ok := st.pass.TypesInfo.Uses[lid].(*types.Var); ok {
                                   lv = v
                               }
                               if lv != nil && lv == fnVar {
                                   if fl, ok := unparen(stmt.Rhs[i]).(*ast.FuncLit); ok {
                                       if borrows := st.findBorrowsInLitInSyntax(fl, syntax, 0); len(borrows) > 0 {
                                           addBorrows(borrows)
                                           return false
                                       }
                                   }
                               }
                           }
                       }
                   }
               case *ast.ValueSpec:
                   for i, name := range stmt.Names {
                       if i < len(stmt.Values) {
                           if lv, ok := st.pass.TypesInfo.Defs[name].(*types.Var); ok && lv == fnVar {
                               if fl, ok := unparen(stmt.Values[i]).(*ast.FuncLit); ok {
                                   if borrows := st.findBorrowsInLitInSyntax(fl, syntax, 0); len(borrows) > 0 {
                                       addBorrows(borrows)
                                       return false
                                   }
                               }
                           }
                       }
                   }
               }
               return true
           })
           if len(foundBorrows) > 0 {
               return false
           }
       }
   }
   ```
   Note that checking `unparen(lhs).(*ast.Ident)` automatically rejects `*ast.SelectorExpr` (`h.f`), resolving Flaw 2 without changing global helper behavior.
