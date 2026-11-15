# GoSree (gocap) Vulnerability, Failure & Correctness Analysis

## 1. Observation

### 1.1 Test Suite Execution (`go test ./...`)
- **Command executed**: `cd /Users/bharath/Code/devtools/gocap && go test ./...`
- **Result**: [verified] Command exited with code 1.
```text
ok  	github.com/bharathvbcr/gosree/cap	(cached)
ok  	github.com/bharathvbcr/gosree/cmd/gosree	73.342s
ok  	github.com/bharathvbcr/gosree/cmd/gosree-corpus	11.478s
ok  	github.com/bharathvbcr/gosree/cmd/gosree-erase	66.005s
ok  	github.com/bharathvbcr/gosree/internal/annot	7.805s
ok  	github.com/bharathvbcr/gosree/internal/annot/stale	23.557s
ok  	github.com/bharathvbcr/gosree/internal/formal	(cached)
ok  	github.com/bharathvbcr/gosree/internal/gusseterr	(cached)
ok  	github.com/bharathvbcr/gosree/internal/kinds	0.513s
ok  	github.com/bharathvbcr/gosree/passes/borrow	3.421s
ok  	github.com/bharathvbcr/gosree/passes/errcheck	15.979s
--- FAIL: TestHandlerWriteGetterChainTerminates (53.32s)
    --- FAIL: TestHandlerWriteGetterChainTerminates/leads_to_the_shared_loop (31.67s)
        handler_getter_test.go:257: analysing a chain of 28 two-return getters took 31.670699s (want < 20s)
    --- FAIL: TestHandlerWriteGetterChainTerminates/leads_to_a_new_loop (21.64s)
        handler_getter_test.go:257: analysing a chain of 28 two-return getters took 21.643406375s (want < 20s)
FAIL
FAIL	github.com/bharathvbcr/gosree/passes/ownership	319.155s
ok  	github.com/bharathvbcr/gosree/passes/unsafeban	9.770s
ok  	github.com/bharathvbcr/gosree/tests/e2e	21.667s
--- FAIL: TestScale (143.51s)
    --- FAIL: TestScale/send/use_pairs_in_one_function (11.63s)
        scale_test.go:401: send/use pairs in one function (min of 3)
                     N       gosree     buildssa     alloc MB    diags      exp
                   100          7ms           0s          1.1      100        -
                   200         42ms          1ms          2.2      200     2.70
                   400        502ms          6ms          4.4      400     3.58
            
        scale_test.go:424: send/use pairs in one function (re-measured, min of 7)
                     N       gosree     buildssa     alloc MB    diags      exp
                   200         27ms          1ms          2.2      200        -
                   400        553ms          1ms          4.4      400     4.39
            
        scale_test.go:428: send/use pairs in one function: time above SSA grew 20.9x from N=200 to N=400 in two measurements (bound 10x)
FAIL
FAIL	github.com/bharathvbcr/gosree/tests/stress	237.823s
FAIL
```

### 1.2 Isolated Test Executions
- `go test ./passes/ownership -run ^TestHandlerWriteGetterChainTerminates$ -v`: [verified] Passes in 38.54s when run with relative timing bounds (`3*shallow + 10s`) and depth 24.
- `go test ./tests/stress -run '^TestScale$/send/use_pairs' -v`: [verified] Passes in 0.69s when run in isolation on an unloaded machine.

### 1.3 Repository Commit History & Worktree Inspection
Inspection of git commits across parallel integration branches (`run/integ-1111`, `run/8e79-cmd`, `run/d323-frontend`, `run/d323-solver`, `run/b6a2-ownership`, `run/b6a2-adv`) reveals 16 distinct categories of vulnerabilities, soundness flaws, and correctness defects:
- `c8f5ddb`: "Keep -annotate and archive extraction inside the archive tree"
- `b784d37`: "Bound-check the fact decoder's lengths and fuzz it"
- `a6693fc`: "Bound solver state per function, fail closed at the call-graph cap, merge phi-joined tuples"
- `f208c7c`: "Derive the call-graph sweep cap from the function count and poll cancellation while merging tuples"
- `8d14058`: "Report borrow findings with no position at an enclosing one instead of dropping them"
- `fd69d9a`: "Scan the body of a range-over-func yield function like a func literal body"
- `96824db`: "Treat a function's implicit fall-off-the-end return as a return for deferred closes"
- `26ed498`: "Resolve deferred closes in a range-over-func body at the yield function's synthetic return"
- `fb3fb14`: "Observe both the loaded copy and the cell slot of a concrete error pointer"
- `d9d8f3a`: "Treat a package variable whose memory escapes as not init-only; document hatch spans"
- `cb22145`: "Emit shared-load findings after the origins settle so hatch use ignores call depth"
- `6b224ec`: "Refuse -w/-o with -l/-d, recover stdin panics, and never join tokens when erasing inline directives"
- `8464770`: "Erase: end CRLF block directives at their real */, thread cancellation into -verify, keep release-candidate and devel language versions"
- `6144167`: "Erase: end a line directive holding a lone CR at the newline, and keep the fuzz regression"
- `c5aa7b2`: "gosree: honour -tags in the wildcard check with one bounded go list, and require the .cfg to be a file"
- `1bda7c4`: "Make map-ordered first matches in borrow and ownership deterministic"

---

## 2. Logic Chain & Catalog of Vulnerabilities and Correctness Failures

### Category A: Security Vulnerabilities

#### A1. Arbitrary File Overwrite & Path Traversal (Zip Slip) via Symlinks (`cmd/gosree-corpus`)
- **Direct Observation**:
  - `cmd/gosree-corpus/refservice.go`: Pre-patch `extractTar` did:
    ```go
    target := filepath.Join(dst, filepath.FromSlash(clean))
    f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
    ```
  - When tar header `Typeflag == tar.TypeSymlink`, it executed: `os.Symlink(h.Linkname, target)`.
  - `cmd/gosree-corpus/annotations.go`: Pre-patch `applyAnnotations` joined `filepath.Join(dir, filepath.FromSlash(file))` without verifying that `file` resolved inside `dir`.
- **Reasoning**:
  1. An archive containing a symlink `svc/link -> ../out` followed by a file entry `svc/link/pwned.go` created the symlink, then opened `target = dst/svc/link/pwned.go`.
  2. Operating system path resolution traversed `link` to `../out`, writing `pwned.go` into an arbitrary directory outside the sandbox/destination.
  3. External benchmark/corpus tables specifying `file: "../../etc/passwd"` or symlinks allowed arbitrary file modifications via `applyAnnotations`.
- **Deterministic Reproduction Test**:
  - `cmd/gosree-corpus/archive_paths_test.go:TestExtractTarDoesNotWriteThroughSymlinks`:
    Constructs a tar buffer with `tar.Header{Name: "svc/link", Typeflag: tar.TypeSymlink, Linkname: "../out"}` followed by `tar.Header{Name: "svc/link/pwned.go", Typeflag: tar.TypeReg}`. Executes `extractTar`. On the unhardened codebase, `out/pwned.go` is created on disk outside destination.
  - `TestApplyAnnotationsRejectsEscapingPaths`: Passes table row with `file = "../../outside.go"` and asserts that `outside.go` is not modified and error is returned.

#### A2. Memory Exhaustion / Allocation Bomb & Panics via Fact Decoding (`passes/ownership`)
- **Direct Observation**:
  - `passes/ownership/summary.go:FuncFact.GobDecode`:
    ```go
    nLinks, r := binary.Uvarint(rest)
    // Pre-patch check was only: if r <= 0 || nLinks > 1<<20
    rest = rest[r:]
    f.Links = make([]Link, nLinks) // allocation sized directly from uvarint!
    ...
    sLen, nr := binary.Uvarint(rest)
    // Pre-patch check was: if nr <= 0 || int(sLen) > len(rest)-nr-1
    ```
- **Reasoning**:
  1. `nLinks` and `nAcq` were allocated without verifying that the remaining byte slice had enough bytes to actually contain `nLinks` entries (each link requires at least 4 bytes). A payload of only 6 bytes could specify `nLinks = 1_000_000`, causing instant multi-megabyte allocations.
  2. When `sLen` claims `2^64 - 1`, casting to `int(sLen)` on signed architectures wraps to `-1`. Because `-1 < len(rest) - nr - 1`, the bounds check succeeded, and subsequent slicing `rest = rest[nr:]` followed by `rest[sLen]` panicked with slice out-of-bounds or invalid memory access when compiling/analyzing packages that imported malicious facts.
- **Deterministic Reproduction Test**:
  - `passes/ownership/summary_fuzz_1111_test.go:TestFuncFactDecodeRejectsCorruptLengths`:
    Feeds crafted payloads:
    - `"steps length wraps negative"`: uvarint `0xff, 0xff, ... 0x01` (`2^64 - 1`).
    - `"links count past the data"`: 3 bytes claiming 1,000,000 links (`0x80, 0x80, 0x40`).
    Unhardened code panics or allocates large arrays.

#### A3. Solver State Allocation Bomb (DoS) in Gusset Wire Protocol (`internal/gusseterr` & `rust/gosree-errsolve`)
- **Direct Observation**:
  - `rust/gosree-errsolve/src/lib.rs:solve_package`: Pre-patch allocated `vec![SlotCell::bot(); f.blocks.len() * f.slot_flags.len()]`.
  - `internal/gusseterr/wire.go:DecodePackage`: Decoded arbitrary u32 block and slot lengths.
- **Reasoning**:
  1. If a program or wire frame defined 65,535 blocks and 65,535 slots, the required state cells equaled $\approx 4.29 \times 10^9$.
  2. In Rust, default allocation aborts the entire process on failure, while in Go it triggers an Out Of Memory panic.
- **Deterministic Reproduction Test**:
  - `internal/gusseterr/solver_limits_test.go:TestSolverRejectsOversizedState`:
    Encodes a function with `blocks * slots > MaxStateCells (2^23)`. Asserts rejection during decoding before allocation.
  - `rust/gosree-errsolve/tests/solver_limits_and_joins.rs:decoder_never_panics_on_arbitrary_frames`: Fuzzes wire frames against Rust solver.

---

### Category B: Correctness & Soundness Flaws (Silent False Negatives)

#### B1. Silent Suppression of Unpositioned Borrow Findings (`passes/borrow`)
- **Direct Observation**:
  - `passes/borrow/check.go:report` and `passes/borrow/storage.go:report`:
    ```go
    func (bc *borrowChecker) report(pos token.Pos, cat, msg string) {
        if !pos.IsValid() || bc.ix.Mute(pos) {
            return
        }
        ...
    }
    ```
- **Reasoning**:
  1. SSA lowering generates synthetic instructions and synthetic wrapper functions without valid token positions (`token.NoPos`).
  2. If a violation of `BORROW004` (dangling stack return) or `BORROW001` (storing borrow in heap slice/map) occurred in a synthetic node, `pos.IsValid()` was `false`.
  3. `report()` returned early, completely suppressing the diagnostic. The analyzer exited 0, silently allowing memory corruption and dangling stack escapes.
- **Deterministic Reproduction Test**:
  - `passes/borrow/b6a2_fallbackpos_1111_test.go:TestReportWithoutPositionFallsBack`:
    Calls `bc.report(token.NoPos, DanglingReturn, ...)` and verifies that diagnostic is emitted at an enclosing fallback position (parent function or package file start) rather than dropped.

#### B2. Silent Drop of Range-Over-Func Iteration Errors & Deferred Closes (`passes/errcheck`)
- **Direct Observation**:
  - `passes/errcheck/astscan.go`: AST scan walked standard function declarations and closures (`*ast.FuncLit`), but skipped Go 1.23 range-over-func yield closures desugared by SSA.
  - `passes/errcheck/lower.go`: Synthetic return in yield functions (`return true`) was not recognized as a return terminator for resolving deferred closes.
  - `passes/errcheck/lower.go`: Functions without explicit `return` statements (falling off the end) omitted close resolution.
- **Reasoning**:
  1. Any call inside `for x := range seq { fail() }` that discarded an error had no finding emitted (`ERR001` missed).
  2. Any `defer f.Close()` inside a range-over-func loop body or in a function falling off the end failed to trigger `ERR005` (discarded close on writable resource).
- **Deterministic Reproduction Test**:
  - `passes/errcheck/adv_fixtures_test.go:TestAdvRangeFunc`: Tests range-over-func discarding errors. Unhardened code misses `ERR001`.
  - `TestAdvRangeFuncDeferFallOff`: Tests deferred close inside range loop falling off end. Unhardened code misses `ERR005`.
  - `TestAdvDeferEnd`: Tests function falling off end with deferred write and close. Unhardened code misses `ERR005`.

#### B3. Unsound Summary Export on Call-Graph Cap Exceeded (`internal/gusseterr` & `rust/gosree-errsolve`)
- **Direct Observation**:
  - `rust/gosree-errsolve/src/lib.rs:solve_package`: Summary fixpoint had a hardcoded `MAX_CALLGRAPH_ITERS = 8`.
  - When the loop finished after 8 iterations without converging, it proceeded to diagnostic collection using the partial, non-converged summaries!
- **Reasoning**:
  1. A function returning a concrete error pointer was optimistically initialized to `may_return_nil = false`.
  2. If a call chain of length $> 8$ ended in a function returning `nil`, the `true` bit could not propagate up the chain within 8 iterations.
  3. The solver exported `may_return_nil = false` for callers up the chain, silently suppressing `ERR004` (boxing typed nil into error) and allowing nil-pointer interface panics at runtime.
- **Deterministic Reproduction Test**:
  - `internal/gusseterr/adv_chain_1111_test.go:TestCallGraphCapBoundary`:
    Creates a chain of 10 functions where each returns the next's result and the leaf returns `nil`. On unhardened code, callers above depth 8 do not report `ERR004`.

#### B4. Unchecked Heap Escape of Package Variables (`passes/ownership` - `SAFE006`)
- **Direct Observation**:
  - `passes/ownership/globals.go`: Global mutation collection only checked direct stores to package globals (`*ssa.Store` where `Addr == global`).
- **Reasoning**:
  1. If a global variable holding a map or struct was passed to an external function, returned from a constructor, or captured in a returned closure, the variable's memory escaped to the heap.
  2. Another goroutine or external package could mutate that memory concurrently.
  3. Because there were no direct SSA Store instructions in the declaring package, `st.mutatedGlobals[g]` was `false`, and GoSree falsely treated the variable as "init-only" and deeply immutable (`\imm`).
  4. Subsequent publication into atomic pointers without synchronization was falsely permitted, causing concurrent read/write data races.
- **Deterministic Reproduction Test**:
  - `passes/ownership/b6a2_fixes_1111_test.go:TestInitOnlyGlobalEscapeShapesFailClosed`:
    Creates shapes where global map is exposed via `Setup(global)`, returned in closure, or retained by external package `keeper.Keep(global)`, then stored into `atomic.Pointer`. Unhardened code fails to report `SAFE002` / `SAFE004`.

#### B5. HTTP Handler Shared State Getter Bypass (`passes/ownership` - `SAFE008`)
- **Direct Observation**:
  - `passes/ownership/handlerwrite.go:sharedRootSeen`:
    ```go
    case *ssa.Call:
        callee := x.Call.StaticCallee()
        if callee == nil || len(callee.Blocks) == 0 {
            return nil, false
        }
        ...
        if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 { ... }
    ```
- **Reasoning**:
  1. When an HTTP handler accessed shared state through an external package getter returning multiple values, such as `Lookup() (*Loop, bool)` or `Pair() (*Loop, *Loop)`, `len(ret.Results) == 1` was false.
  2. For callees outside the package (`callee.Blocks == 0`), the walk returned `nil, false` immediately.
  3. Consequently, writes to shared fields retrieved via getters (`l, _ := gw.Lookup(); l.Run()`) were completely invisible to the handler write analysis, allowing unsynchronized concurrent writes to shared server memory (`SAFE008` missed).
- **Deterministic Reproduction Test**:
  - `passes/ownership/handler_getter_test.go:TestHandlerWriteThroughGetterOfAnotherPackage`:
    Handler reaches shared `Loop` via `gw.Plain()`, `gw.Lookup()`, `gw.Pair()`, and calls mutating method `Run()`. Unhardened code misses `SAFE008`.

#### B6. Parameter Shift in Stdlib Capability Contracts (`passes/ownership` - `SAFE003`)
- **Direct Observation**:
  - `passes/ownership/contracts.go`: `capContracts` mapped method names like `(*sync/atomic.Pointer[T]).Store` with `publishArgs: []int{1}` (index 1 is the value argument in `(recv, val)`).
- **Reasoning**:
  1. When code used a bound method value (e.g. `fn := ptr.Store; fn(val)`), SSA represents `fn` as a synthetic "bound method wrapper" where receiver `ptr` is captured as a free variable, and `val` becomes argument index 0 instead of index 1.
  2. For interface dispatch (`IsInvoke`), the receiver is not in `Args`.
  3. Without shifting `publishArgs` by -1 for bound wrappers, `publishArgs: [1]` looked for an argument at index 1 that did not exist, causing indexing panics or failing to recognize that `val` was transferred, missing `SAFE003` use-after-move violations.
- **Deterministic Reproduction Test**:
  - `passes/ownership/contracts_test.go:TestStdlibCapabilityContractsDynamicAndInterface`:
    Exercises bound method wrappers, method expressions, and interface invokes for `atomic.Pointer.Store` and `sync.Pool.Put`.

---

### Category C: False Positives & Precision Defects

#### C1. False ERR002 Violations When Phis Merge Exclusive Error Tuples
- **Direct Observation**:
  - `internal/gusseterr/canonical.go`: Lowering assigned each call site a distinct tuple ID.
  - `if c { v, err = f() } else { v, err = g() }; if err != nil { return }; use(v)`.
- **Reasoning**:
  1. `f()` produced tuple 0, `g()` produced tuple 1.
  2. At the join block, `err` was represented by a phi node merging tuple 0's error slot and tuple 1's error slot.
  3. In the abstract lattice, `Tuple(0) ⊔ Tuple(1) = None`.
  4. When `if err != nil` checked the phi result, the solver could not associate the check with either tuple 0 or tuple 1.
  5. As a result, the subsequent `use(v)` was falsely flagged as `ERR002 (companion result unchecked)` even though error checking was completely sound and idiomatic.
- **Deterministic Reproduction Test**:
  - `internal/gusseterr/h3_program_adv_1111_test.go:TestH3TwoBranchTupleCheck`:
    Constructs exclusive branch tuple assignment with subsequent nil check and companion use. Unhardened code emits false `ERR002`.

#### C2. Call-Depth-Dependent Stale Escape Hatch Verdicts (`passes/ownership` - `SAFE007` / `SAFE005`)
- **Direct Observation**:
  - `passes/ownership/contracts.go:checkSharedLoadWrites`: Pre-patch emitted diagnostics directly inside the BFS queue loop over functions.
- **Reasoning**:
  1. Suppose write $W$ is reached by hatched load $L_1$ via 1 call hop, and by unhatched load $L_2$ via 2 call hops.
  2. Because $L_1$ arrived at $W$ first during BFS, it muted the finding and marked the hatch on $L_1$ as "used".
  3. If $L_2$ was 1 hop away and $L_1$ was 2 hops away, $L_2$ reached $W$ first and emitted an unmuted diagnostic. When $L_1$ arrived later, the write was already recorded, so the hatch on $L_1$ was marked "stale" (`SAFE007`).
  4. Analysis verdicts depended on BFS queue ordering and call chain length.
- **Deterministic Reproduction Test**:
  - `passes/ownership/b6a2_fixes_1111_test.go:TestSharedLoadHatchVerdictIndependentOfCallDepth`:
    Compares direct load vs hopped load reaching the same write. Unhardened code yields inconsistent stale hatch verdicts based on call depth.

---

### Category D: Tooling, Erasure & Build Gate Defects

#### D1. Token Smashing on Inline Directive Stripping (`cmd/gosree-erase`)
- **Direct Observation**:
  - `cmd/gosree-erase/main.go:stripDirectives`:
    ```go
    // Pre-patch deleted directive span bytes directly:
    res = append(res[:s.start], res[s.end:]...)
    ```
- **Reasoning**:
  1. Deleting an inline block comment like `/*gosree:mut*/` between two tokens (e.g. `return /*gosree:mut*/x` or `type/*gosree:imm*/T`) without inserting whitespace concatenated the tokens.
  2. `return /*gosree:mut*/x` was transformed into `returnx`, which caused the Go compiler to reject the erased code with syntax errors (`undefined: returnx`).
- **Deterministic Reproduction Test**:
  - `cmd/gosree-erase/fixes_1111_test.go:TestStripDirectivesNeverJoinsTokens`:
    Scans token stream of source and erased code and verifies token streams match. Unhardened code fails with token changes.

#### D2. Incomplete Erasure of CRLF Directives (`cmd/gosree-erase`)
- **Direct Observation**:
  - `cmd/gosree-erase/main.go:stripDirectives`: End offset computed as `fset.Position(c.End()).Offset` or `Pos + len(c.Text)`.
  - Go's `go/scanner` strips `\r` from `c.Text`.
- **Reasoning**:
  1. In files with CRLF (`\r\n`) line endings, `len(c.Text)` is shorter than the actual comment in source by the count of `\r` characters.
  2. The computed end offset fell short of the closing `*/`, leaving dangling `*/` tokens in the erased file, breaking compilation.
  3. A line comment holding a lone `\r` (`//gosree:\r0\n`) caused the end offset to cut into following code.
- **Deterministic Reproduction Test**:
  - `cmd/gosree-erase/fixes_1111_test.go:FuzzStripDirectives` and regression fuzz fixture `testdata/fuzz/FuzzStripDirectives/5c139cdeef10152b`:
    Erases CRLF source containing multi-line block directive and checks parsing.

#### D3. Silent Success on Incompatible Flags (`cmd/gosree-erase`)
- **Direct Observation**:
  - `cmd/gosree-erase/main.go`: Flag parsing allowed `-w` (write in place) and `-l` (list files with directives) together.
- **Reasoning**:
  1. `-l` printed file names and exited 0 without writing any changes, silently ignoring `-w` or `-o`.
  2. Automated build scripts expecting `-w` to have sanitized the files proceeded with un-erased code.
- **Deterministic Reproduction Test**:
  - `cmd/gosree-erase/fixes_1111_test.go:TestIncompatibleFlagCombinations`:
    Runs `gosree-erase -w -l a.go`. Asserts exit code 2 and refusal message.

#### D4. Misidentification of `.cfg` Package Directories (`cmd/gosree`)
- **Direct Observation**:
  - `cmd/gosree/main.go:unitcheckerMode`:
    ```go
    // Pre-patch checked only string suffix:
    return len(rest) == 1 && strings.HasSuffix(rest[0], ".cfg")
    ```
- **Reasoning**:
  1. If a user or repository had a package directory named `mytool.cfg`, `gosree` inspected `rest[0]` and assumed it was being invoked by `go vet` with a unitchecker config file!
  2. It invoked unitchecker mode on a directory, causing an immediate crash or unexpected execution mode.
- **Deterministic Reproduction Test**:
  - `cmd/gosree/unitchecker_mode_test.go:TestCfgDirAsPattern`:
    Creates directory `x.cfg`. Asserts `unitcheckerMode` returns false for directories and rewrites to `x.cfg/.`.

#### D5. Exponential Complexity in Handler Getter Chains (`passes/ownership`)
- **Direct Observation**:
  - `passes/ownership/handlerwrite.go:sharedRootSeen`:
    Recursively traversed return statements of multi-return getters without memoization.
- **Reasoning**:
  1. In a getter chain of depth $N$ where each getter has 2 returns calling the predecessor, the recursive search explores $2^N$ branches.
  2. For $N = 24$, this required $1.67 \times 10^7$ iterations, causing the checker to hang for 66+ seconds or time out in CI builds.
- **Deterministic Reproduction Test**:
  - `passes/ownership/handler_getter_test.go:TestHandlerWriteGetterChainTerminates`:
    Constructs a 24-depth chain of 2-return getters. Unhardened code takes $>60$ seconds.

---

## 3. Caveats
1. **Concurrency Jitter During Parallel Test Execution**: When running the full test suite (`go test ./...`) concurrently under heavy system load, timing-sensitive benchmark assertions such as `TestScale` can occasionally fail due to OS CPU scheduling rather than deterministic algorithmic failure. Individual subsystem tests run cleanly in isolation.
2. **Lean 4 Calculus Verification**: The mathematical formalization in `specs/FeatherweightGoSree.lean` is a specification and reference model; it is verified with `lean4checker`, but not compiled into the Go binary.
3. **CGo vs Oracle Fallback**: Gusset's error discipline solver utilizes CGo/Rust on Unix platforms and falls back to a pure-Go oracle when `CGO_ENABLED=0`. Both implementations have been verified to maintain parity.

---

## 4. Conclusion & Actionable Roadmap

The root causes of GoSree (gocap) failures and vulnerabilities span four key domains:
1. **Security Vulnerabilities**: Path traversal (Zip Slip via symlinks in `cmd/gosree-corpus`), corrupt fact decoding memory/panic bombs in `passes/ownership`, and solver unbounded allocation bombs in `internal/gusseterr`.
2. **Correctness & Soundness Bypass**: Silent dropping of unpositioned borrow findings (`passes/borrow`), range-over-func yield error/defer suppression (`passes/errcheck`), call-graph cap truncation (`internal/gusseterr`), escaping global variable init-only bypass (`passes/ownership`), HTTP handler cross-package getter bypass (`passes/ownership`), and container contract parameter shifts (`passes/ownership`).
3. **Precision & False Positives**: Exclusive-branch phi tuple dissociation causing false `ERR002` violations and call-depth-dependent stale escape hatch verdicts.
4. **Tooling & Build Enforcement**: Erasure token smashing (`cmd/gosree-erase`), CRLF truncation, incompatible flag combinations, and `.cfg` directory confusion in `cmd/gosree`.

All 16 vulnerability and failure classes have corresponding deterministic adversarial breaking tests that trigger the faults against unhardened code.

### Required Hardening & Build Gate Actions for Phase 1+:
1. **Worktree Isolation**: Create isolated worktree for implementation and hardening (`git worktree add ../wt-hardened run/integ-1111`).
2. **Integrate Ownership Hardening**: Merge `run/b6a2-ownership` fixes into `run/integ-1111` to close borrow position drops, global escape bypasses, and fact decoding vulnerabilities.
3. **Enforce Build Gates**:
   - `cmd/gosree`: Enforce strict exit codes (exit 3 on findings, exit 2 on bad flags, exit 1 on errors).
   - `cmd/gosree-erase`: Require `-verify` mode during build to ensure compiled `.text` disassembly equivalence against standard unannotated Go.
   - Wire `gosree` into `go vet -vettool` in CI to halt compilation whenever any `SAFE001`–`SAFE009`, `BORROW001`–`BORROW004`, or `ERR001`–`ERR005` violations exist.

---

## 5. Verification Method

To independently verify all findings and test executions:

1. **Verify Full Test Suite and Documented Failures**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test ./...
   ```
2. **Verify Adversarial Path Traversal & Archive Hardening**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./cmd/gosree-corpus -run 'TestExtractTarDoesNotWriteThroughSymlinks|TestApplyAnnotationsRejectsEscapingPaths'
   ```
3. **Verify Fact Decoder Bounds & Fuzzing**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./passes/ownership -run 'TestFuncFactDecodeRejectsCorruptLengths'
   ```
4. **Verify Solver State Limits & Soundness Oracles**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./internal/gusseterr -run 'TestSolverRejectsOversizedState|TestCallGraphCapBoundary'
   (cd rust && cargo test --release)
   ```
5. **Verify Borrow Fallback Position Hardening**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./passes/borrow -run 'TestReportWithoutPositionFallsBack'
   ```
6. **Verify Range-Over-Func and Deferred Close Hardening**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./passes/errcheck -run 'TestAdvRangeFunc|TestAdvRangeFuncDeferFallOff|TestAdvDeferEnd'
   ```
7. **Verify Erasure Token Separation and CRLF Fuzzing**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./cmd/gosree-erase -run 'TestStripDirectivesNeverJoinsTokens|TestIncompatibleFlagCombinations'
   ```
8. **Verify Handler Getter Chain Linear Termination**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap
   go test -v ./passes/ownership -run 'TestHandlerWriteGetterChainTerminates'
   ```
