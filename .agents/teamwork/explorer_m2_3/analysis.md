# Analysis: Milestone 2 Hardening Investigation (Explorer 3)

**Author**: Explorer 3 (`teamwork_explorer`)  
**Target Worktree**: `/Users/bharath/Code/devtools/gocap-worktree`  
**Branch**: `harden/gosree-gates` (Base Commit: `6b401b94ea9c2128ceba90adcb809f626d7f878a`)  
**Date**: 2026-11-11T17:05:00Z  

---

## Executive Summary

This investigation analyzed 5 critical areas across tooling, security, precision, error analysis, and concurrency safety within the isolated git worktree `/Users/bharath/Code/devtools/gocap-worktree`.

1. **Tooling & CLI Hardening (D1–D4)**: Verified that fixes for D1 (token separation on inline stripping), D2 (CRLF comment parsing), D3 (incompatible flag refusal), and D4 (`.cfg` directory vs regular file check) were merged into branch `harden/gosree-gates` via commits `6b224ec`, `8464770`, `6144167`, and `c5aa7b2` under merge commit `6b401b9`. All unit tests (`TestStripDirectivesNeverJoinsTokens`, `TestIncompatibleFlagCombinations`, `TestCfgDirAsPattern`) and fuzz testing (`FuzzStripDirectives` with 48k+ executions) pass cleanly with zero failures. **Status: No code patches required.**
2. **Corpus Archive Security (A1)**: Verified that `cmd/gosree-corpus/refservice.go` implements Go 1.24 `os.OpenRoot(dst)` directory sandboxing to prevent path traversal and symlink writing (Zip Slip). The full test suite (`TestExtractTarDoesNotWriteThroughSymlinks`, `TestAdvExtractTarAdversarial`, `TestApplyAnnotationsStaysInsideArchive`) passes 100%. **Status: No code patches required.**
3. **Solver Bounds & Precision (A3, B3, C1)**: Verified that `internal/gusseterr` and `rust/gosree-errsolve` enforce `MaxStateCells = 8_388_608` (A3), fail-closed callgraph fixpoint termination on cap (B3), and sound join-semilattice properties for phi-merged disjoint branch error tuples (C1). Both cargo tests (19 tests) and go tests (17 tests) pass. **Status: No code patches required.**
4. **Errcheck Pass Range-over-Func Modeling (B2)**: Analyzed `passes/errcheck/astscan.go` and `lower.go`. Identified that commit `26ed498` introduced the proper yield return modeling (`_, isYield := fl.fn.Syntax().(*ast.RangeStmt)`). However, `passes/errcheck/lower.go` currently has a staged modification in the worktree that reverts this fix to unhardened state (as part of M1 adversarial reproduction). Re-applying the fix in HEAD restores PASS for `TestAdvRangeFuncDeferFallOff` and `TestRangeFuncBodyDeferredClose`. **Status: Re-apply/unstage HEAD fix in Worker step.**
5. **Concurrency Safety & Runtime Witnesses**:
   - `sp_w_error`: Missed by static analysis because `internal/kinds/kinds.go:43` explicitly includes `{"error", false, ...}` in `overrides`, leading `Classifier.Shareable(error)` to return `true`. Consequently, `graph.val(v)` returns `nil` for values typed `error`, causing `checkTransfer` in `passes/ownership/check.go` to silently abort without tracking aliased memory or emitting `SAFE003`.
   - `rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`: Correctly modeled in `passes/ownership/contracts.go`. `(*sync/atomic.Pointer).Load` enforces `sharedReadOnlyLoad` (`\imm`), so subsequent writes trigger `SAFE005`. Stores (`atomic.Pointer.Store`) record transfers, so sender reuse triggers `SAFE003`. Confirmed that `gosree` halts on all three with exit code 3.
   - Proposed surgical hardening strategies to catch `sp_w_error` without regressing standard error handling.

---

## Detailed Investigation by Area

### 1. Tooling & CLI Hardening (D1, D2, D3, D4)

#### 1.1 In-Tree Status & Commit Verification
- **D1 (Token Smashing on Inline Stripping)**:
  - **Location**: `cmd/gosree-erase/main.go:610–642` (`span.resolveInline`)
  - **Mechanism**: When erasing an inline directive comment (e.g., `return /*gosree:mut*/x`), `resolveInline` examines surrounding bytes. If code precedes or follows without whitespace (`!isBlank`), it assigns `sp.repl = " "` instead of empty string:
    ```go
    case sp.start > 0 && !isBlank(src[sp.start-1]):
        sp.repl = " "
    ```
    This prevents `returnx` syntax errors.
  - **Commit**: `6b224ec` ("Refuse -w/-o with -l/-d, recover stdin panics, and never join tokens when erasing inline directives").
- **D2 (CRLF Comment Stripping Truncation)**:
  - **Location**: `cmd/gosree-erase/main.go:665–682, 701–707`
  - **Mechanism**: In Go's AST parser, comment text has carriage returns stripped (`\r`), so computing comment bounds as `start + len(c.Text)` falls short on Windows CRLF files. The implementation explicitly searches for the source bytes:
    ```go
    if strings.HasPrefix(c.Text, "/*") {
        if k := bytes.Index(src[start+2:], []byte("*/")); k >= 0 {
            end = start + 2 + k + 2
        }
    }
    if strings.HasPrefix(c.Text, "//") {
        end = len(src)
        if k := bytes.IndexByte(src[start:], '\n'); k >= 0 {
            end = start + k
        }
        if end > start && src[end-1] == '\r' {
            end--
        }
    }
    ```
  - **Commit**: `8464770` and `6144167`.
- **D3 (Incompatible Flag Refusal)**:
  - **Location**: `cmd/gosree-erase/main.go:115–130`
  - **Mechanism**: Refuses combinations of reporting flags (`-l`, `-d`) with destination flags (`-w`, `-o`), printing an error and exiting with code 2:
    ```go
    for _, report := range []struct { on bool; name string }{{o.list, "-l"}, {o.diff, "-d"}} {
        for _, dest := range []struct { on bool; name string }{{o.write, "-w"}, {o.outPath != "", "-o"}} {
            if report.on && dest.on {
                o.errorf("error: cannot use %s with %s: %s only reports and writes nothing", dest.name, report.name, report.name)
                return 2
            }
        }
    }
    ```
  - **Commit**: `6b224ec`.
- **D4 (`.cfg` Directory Misidentification)**:
  - **Location**: `cmd/gosree/main.go:77–103` (`unitcheckerMode`, `cfgDirAsPattern`)
  - **Mechanism**: Multichecker previously mistook package directory arguments ending in `.cfg` (e.g. `x.cfg`) as `go vet` configuration files. `unitcheckerMode` now checks `fi.Mode().IsRegular()`:
    ```go
    func unitcheckerMode(args []string) bool {
        rest := args[flagEnd(args):]
        if len(rest) != 1 || !strings.HasSuffix(rest[0], ".cfg") {
            return false
        }
        fi, err := os.Stat(rest[0])
        return err != nil || fi.Mode().IsRegular()
    }
    ```
    If `rest[0]` is a directory, `cfgDirAsPattern` converts it to `x.cfg/.` to force directory interpretation by the package loader.
  - **Commit**: `c5aa7b2`.

#### 1.2 Test Execution Results
- `go test -v ./cmd/gosree-erase -run '^(TestStripDirectivesNeverJoinsTokens|TestIncompatibleFlagCombinations)$'`: **PASS** (0.289s)
- `go test -v ./cmd/gosree-erase -run '^$' -fuzz 'FuzzStripDirectives' -fuzztime 2s`: **PASS** (3.23s, 48,456 executions, 0 crashes)
- `go test -v ./cmd/gosree -run '^TestCfgDirAsPattern$'`: **PASS** (0.00s)
- Entire `cmd/gosree-erase` and `cmd/gosree` suites: **PASS** (100% pass across 45 tests).

---

### 2. Corpus Archive Security (A1)

#### 2.1 In-Tree Status & Commit Verification
- **A1 (Path Traversal / Zip Slip via Symlinks)**:
  - **Location**: `cmd/gosree-corpus/refservice.go:68–138` (`extractTar`)
  - **Mechanism**: Extracts tar archives using Go 1.24's directory capability root `os.OpenRoot(dst)`:
    ```go
    root, err := os.OpenRoot(dst)
    if err != nil {
        return 0, err
    }
    defer root.Close()
    ```
    For every entry:
    - Verifies path is not absolute and does not contain `..` or escape prefixes.
    - All directory creation uses `root.MkdirAll(target, 0o755)`.
    - All file creation uses `root.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)`.
    - Symlinks are planted via `root.Symlink(h.Linkname, target)`. If a subsequent entry attempts to write through a planted symlink pointing outside `dst`, `root.OpenFile` rejects it at OS boundary with an error.
  - **Commit**: `c8f5ddb` ("Keep -annotate and archive extraction inside the archive tree") merged in `6b401b9`.

#### 2.2 Test Execution Results
- `go test -v ./cmd/gosree-corpus -run '^TestExtractTarDoesNotWriteThroughSymlinks$'`: **PASS** (0.00s)
- Entire `cmd/gosree-corpus` test suite: **PASS** (11.243s across 25 tests, including `TestAdvExtractTarAdversarial` with 8 subcases).

---

### 3. Solver Bounds & Precision (A3, B3, C1)

#### 3.1 In-Tree Status & Implementation
- **A3 (State Allocation Bomb / Bounds Check)**:
  - **Go Interface**: `internal/gusseterr/wire.go:35, 40–45`:
    ```go
    MaxStateCells = 8_388_608 // 2^23 cells, bounding state memory to <= 896 MiB in Rust
    func checkStateCells(nBlocks, nSlots uint64) error {
        if nBlocks*nSlots > MaxStateCells {
            return fmt.Errorf("gusseterr: GSE1 limit exceeded: state cells (blocks x slots) %d x %d exceeds %d", nBlocks, nSlots, MaxStateCells)
        }
        return nil
    }
    ```
  - **Rust Engine**: `rust/gosree-errsolve/src/lib.rs:43, 360–362, 1547–1550`:
    ```rust
    pub const MAX_STATE_CELLS_PER_FUNC: u64 = 8_388_608;
    ...
    if (num_blocks as u64) * (num_slots as u64) > MAX_STATE_CELLS_PER_FUNC {
        return Err(SolveError::LimitExceeded("state cells (blocks x slots)"));
    }
    ...
    let mut in_states: Vec<State> = try_vec(n_blocks, "block entry states")?;
    ```
    Allocations use fallible `try_vec`, returning an error rather than aborting on OOM.
- **B3 (Fail-Closed Callgraph Cap)**:
  - **Go Oracle**: `internal/gusseterr/oracle.go:959–961`:
    ```go
    if !converged {
        return PackageResult{}, fmt.Errorf("gusseterr: call-graph summary fixpoint did not converge within %d iterations", maxCallgraphIters)
    }
    ```
    Fails closed with a non-convergence error rather than exporting unsound optimistic summaries (`may_return_nil = false`).
  - **Rust Engine**: Tested in `tests/solver_limits_and_joins.rs` (`callgraph_cap_fails_closed_instead_of_exporting_optimistic_summaries`).
- **C1 (Lattice Join for Disjoint Branch Errors)**:
  - **Rust Semilattice**: `rust/gosree-errsolve/src/lib.rs:622–770`:
    Implements formal join-semilattice for `Origin`, `NilVal`, `ErrVar`, `WriteMode`, `ErrLink`, and `SlotCell`.
    Phi-merging tuples preserves error identity:
    `tuples_merged_by_a_phi_share_identity_so_one_check_promotes_the_companion` verifies that when exclusive branches merge error tuples at a phi node, checking the merged error promotes the companion without emitting false `ERR002`.

#### 3.2 Test Execution Results
- `cd rust/gosree-errsolve && cargo test`: **PASS** (19 tests: 10 unittests, 3 formal lattice tests, 6 solver limits/joins tests).
- `go test -v ./internal/gusseterr`: **PASS** (7.941s across 17 unit and differential tests, including `FuzzDecoders`).

---

### 4. Errcheck Pass: Range-Over-Func Yield Modeling (B2)

#### 4.1 Root Cause & Mechanism
- In Go 1.23+ range-over-func loops:
  ```go
  for x := range seq(3) {
      w := ws[x]
      defer w.Close()
      _, _ = w.Write([]byte("x"))
  }
  ```
- The Go SSA builder lowers the loop body into an anonymous synthetic yield function with signature `func(yieldArgs...) bool`.
- The body falls off its end through `return true` (or `continue`), which the SSA builder represents as an `*ssa.Return` with `!t.Pos().IsValid()`.
- In `passes/errcheck/lower.go:1353`:
  ```go
  case *ssa.Return:
      if !t.Pos().IsValid() {
          if len(t.Results) == 0 {
              return gusseterr.Terminator{
                  Kind:    gusseterr.TermReturn,
                  SiteID:  fl.siteForVal(token.NoPos, "return"),
                  RetSlot: gusseterr.NoneU32,
              }
          }
          return gusseterr.Terminator{Kind: gusseterr.TermStop}
      }
  ```
- Because the yield function returns `bool` (`return true`), `len(t.Results) == 1`.
- Without checking if the function is a yield closure, `lowerTerminator` fell into `TermStop`.
- `TermStop` is an abrupt termination (panic/exit), NOT a normal return. Consequently, deferred function calls registered in the body were never resolved against the exit, causing `ERR005` (deferred call preceded by write) to be completely missed!

#### 4.2 In-Tree Status & Discrepancy
- Commit `26ed498` in branch `run/d323-frontend` properly fixed this at line 1364:
  ```go
  _, isYield := fl.fn.Syntax().(*ast.RangeStmt)
  if len(t.Results) == 0 || isYield {
      return gusseterr.Terminator{
          Kind:    gusseterr.TermReturn,
          SiteID:  fl.siteForVal(token.NoPos, "return"),
          RetSlot: gusseterr.NoneU32,
      }
  }
  ```
- **Discrepancy in Worktree**:
  - `git diff --staged -- passes/errcheck/lower.go` reveals that during Milestone 1 adversarial reproduction setup, `lower.go` was staged with a diff that removed `isYield` and restored the unhardened defect so that `TestAdvRangeFuncDeferFallOff` would break!
  - As a result, running `go test ./passes/errcheck -run '^TestAdvRangeFuncDeferFallOff$'` currently fails:
    `analysistest.go:713: advrangefuncdefer/adv.go:24: no diagnostic was reported matching ERR005`
  - Re-applying the fix from `HEAD:passes/errcheck/lower.go` (or unstaging/checking out HEAD) immediately resolves this failure.

---

### 5. Concurrency Safety & Runtime Witnesses

#### 5.1 Root Cause of `sp_w_error` Miss
- **Test Case**: `tests/stress/testdata/src/sp_w_error/main.go`
  ```go
  type Job struct{ ID int }
  type jobErr struct{ j *Job }
  func (e *jobErr) Error() string { return "job" }

  func main() {
      j := &Job{}
      done := make(chan struct{})
      go func(e error) {
          e.(*jobErr).j.ID = 2
          close(done)
      }(&jobErr{j: j})
      j.ID = 1 // MISS(SAFE003): j crossed inside an error value
      <-done
  }
  ```
- Under `-race`, running this produces:
  `WARNING: DATA RACE Write at 0x... by goroutine 6 ... Previous write at 0x... by main goroutine (exit status 66)`.
- However, `gosree` exits 0 with 0 findings.
- **Trace through Codebase**:
  1. `internal/kinds/kinds.go:43`:
     ```go
     {"error", false, "assumed: error values are not mutated after creation (convention, not checked)"},
     ```
  2. `Classifier.Shareable(t)`:
     ```go
     if name := QualifiedName(t); name != "" {
         if valueOverride[name] {
             return true
         }
     ```
     For the interface type `error`, `QualifiedName(t)` is `"error"`. `valueOverride["error"]` is `true`.
  3. `passes/ownership/graph.go:176–180` (`val` method):
     ```go
     default:
         if !g.st.cls.Shareable(v.Type()) {
             n = g.newNode()
         }
     }
     g.nodes[v] = n
     return n
     ```
     When `t1 := make error <- &jobErr{j: j}` is created, `v.Type()` is `error`.
     Because `Shareable(error)` is `true`, `g.nodes[v] = nil`!
  4. In `passes/ownership/check.go:41–56` (`checkTransfer`):
     ```go
     func (st *state) checkTransfer(g *graph, t transfer) {
         ...
         n := g.val(t.v)
         if n == nil {
             return
         }
         reg := g.region(n)
         ...
     ```
     Because `n == nil`, `checkTransfer` exits on line 51!
     It never performs escape analysis, never discovers that `j` is reachable, and never reports `SAFE003` (use after transfer) when `j.ID = 1` executes.

#### 5.2 Atomic Publications & Map Writes
- **`rs_torn_slice` & `rs_torn_iface`**:
  - In `tests/stress/testdata/src/rs_torn_slice/main.go` and `rs_torn_iface/main.go`, a struct containing a slice or interface is published via `atomic.Pointer`. A goroutine loads `r := live.Load()` and mutates `r.routes = maint` or `r.codec = gzip` concurrently with readers, causing torn reads and panics.
  - **Modeling**: In `passes/ownership/contracts.go`:
    - `(*sync/atomic.Pointer[T]).Load` has `sharedReadOnlyLoad: true`.
    - `checkSharedLoadWrites` (lines 176–361) tracks values returned by `Load()`. Any field store through `r` (e.g. `r.routes = ...` or `r.codec = ...`) is detected as a mutation of shared memory and flagged with `SAFE005`:
      `SAFE005: write through shared value loaded from (*sync/atomic.Pointer).Load... published atomic values are read-only once shared`.
  - **Empirical Verification**:
    `go run ./cmd/gosree ./tests/stress/testdata/src/rs_torn_slice` -> Exit 3 (`SAFE005` reported on lines 58 and 60).
    `go run ./cmd/gosree ./tests/stress/testdata/src/rs_torn_iface` -> Exit 3 (`SAFE005` reported on lines 59 and 61).
- **`rs_w_mapwrites`**:
  - In `tests/stress/testdata/src/rs_w_mapwrites/main.go`, a map is published via `sessions.Store(&m)`. Both the main goroutine and worker goroutines write to `m` concurrently, triggering `fatal error: concurrent map writes`.
  - **Modeling**:
    - `sessions.Store(&m)` publishes `m` (`capContracts["(*sync/atomic.Pointer[T]).Store"]`).
    - The sender continues writing `m[...] = -i`, triggering `SAFE003` ("variable m is used after it was published").
    - The worker loads `sessions.Load()` and writes `m[...] = id`, triggering `SAFE005`.
  - **Empirical Verification**:
    `go run ./cmd/gosree ./tests/stress/testdata/src/rs_w_mapwrites` -> Exit 3 (`SAFE003` on line 33, `SAFE005` on line 20).

---

## Hardening Strategies for Concurrency Safety Rules

### Recommendation for `sp_w_error`
The core tension is:
- **Good Go convention**: Pure error types (`errors.New`, `fmt.Errorf`, custom sentinel errors) are immutable and must be freely returnable across functions and goroutines without onerous capability annotations.
- **Vulnerability**: Any arbitrary mutable struct can implement `Error() string`. Wrapping it in `error` allows smuggling mutable, aliased pointers across goroutine boundaries without `gosree` detecting the ownership transfer.

**Surgical Remediation Strategy**:
1. **Interface Boxing Validation at Goroutine Spawns (`graph.go` & `check.go`)**:
   - In `internal/kinds/kinds.go`, keep `error` as shareable by default for return values and sequential code.
   - HOWEVER, in `passes/ownership/graph.go:goStmt` (and whenever an interface value is passed to a goroutine or channel):
     If the static value is an `*ssa.MakeInterface`, inspect the concrete underlying type `ins.X.Type()`.
     If `!st.cls.Shareable(ins.X.Type())`, the interface value MUST NOT be treated as shareable!
     Instead, `g.join(ins, ins.X)` should bind the interface node to the unshareable payload node, and `g.val(ins)` must return a non-nil node representing that payload.
   - In `passes/ownership/check.go`:
     When an interface value containing an unshareable payload is passed to a goroutine, `checkTransfer` will now receive a valid node `n`, will compute its region `reg`, and will detect that `j` was transferred. When `j.ID = 1` is subsequently executed, `checkUseAfter` will correctly emit:
     `SAFE003: variable j is used after it was passed to a goroutine` (or `SAFE002` if not uniquely owned).
2. **Impact**:
   - Standard errors (`errors.New`, `fmt.Errorf`) contain strings / unexported immutable fields and remain classified as `Shareable` (or pointer-free).
   - Smuggled mutable error pointers (`*jobErr` containing `*Job`) are intercepted at the goroutine boundary, eliminating the data race while preserving ergonomics.

---

## Action Plan for Milestone 2 Workers

| # | Task | Target File | Action Required |
|---|------|-------------|-----------------|
| 1 | Tooling & CLI (D1–D4) | `cmd/gosree-erase/main.go`, `cmd/gosree/main.go` | **No change needed**; verified fully in-tree and passing all tests. |
| 2 | Corpus Security (A1) | `cmd/gosree-corpus/refservice.go` | **No change needed**; verified `os.OpenRoot` sandboxing passing all tests. |
| 3 | Solver Bounds (A3, B3, C1) | `internal/gusseterr`, `rust/gosree-errsolve` | **No change needed**; verified cell limits, caps, and semilattice passing all tests. |
| 4 | Errcheck Yield (B2) | `passes/errcheck/lower.go` | **Unstage/restore HEAD fix**: restore `_, isYield := fl.fn.Syntax().(*ast.RangeStmt)` at line 1364. Verify `TestAdvRangeFuncDeferFallOff` and `TestRangeFuncBodyDeferredClose` pass. |
| 5 | Concurrency Safety (`sp_w_error`) | `passes/ownership/graph.go`, `passes/ownership/check.go` | **Implement interface boxing inspection**: when `*ssa.MakeInterface` boxes a non-shareable value into an `error` interface passed to a goroutine, preserve its tracking node so `SAFE003` is emitted. |
| 6 | Active Defect Remediation | `passes/ownership` (A2, B1, B4, B5, B6, C2, D5) | Implement surgical fixes as investigated by Explorers 1 & 2. |
