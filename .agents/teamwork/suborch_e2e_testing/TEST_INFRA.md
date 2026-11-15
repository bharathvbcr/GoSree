# E2E Test Infra: GoSree (gocap) Hardening & Build Gate Acceptance

## Test Philosophy
- **Requirement-Driven & Opaque-Box**: Acceptance tests are derived directly from `ORIGINAL_REQUEST.md` (R1–R4), architectural specifications in `PROJECT.md`, and formal safety invariants.
- **Progressive Testability**: Verification does not rely on features more complex than what is under test. Tiers progress from syntactic annotations and marker types up to multi-package concurrent interactions and compiler build gates.
- **Methodology**: Systematic 4-tier testing combining Category-Partition, Boundary Value Analysis (BVA), Pairwise Combinatorial Interaction, and Real-World Application Workloads.
- **Fail-Closed & Binary Veto**: A test failure or integrity violation causes immediate gate failure. No test may be skipped, commented out, or mocked.
- **Worktree Isolation**: All test executions, builds, and verification passes run against the isolated git worktree at `/Users/bharath/Code/devtools/gocap-worktree` (preserving the primary repository clean).

---

## 4-Tier Testing Methodology & Architecture

### Tier 1: Feature Coverage (Language Basics & Directives)
- **Objective**: Verify individual core language features, directives, marker types, borrow basics, and zero-cost erasure in isolation.
- **Scope & Key Checks**:
  - `internal/annot`: Parse `//gosree:iso`, `//gosree:imm`, `//gosree:mub`, `//gosree:rob`, `//gosree:guarded_by`, `//gosree:holds`, `//gosree:unsafe`.
  - Directive syntax validation and near-miss detection (`SAFE007`).
  - Zero-cost generic type aliases: `cap.Mut[T]`, `cap.Ref[T]`, `cap.Iso[T]`, `cap.Imm[T]` (`cap/cap.go`).
  - Basic second-class borrows: stack-scoped `\mub` / `\rob` bindings (`passes/borrow`).
  - Zero-cost AST erasure: `cmd/gosree-erase` strips directives and marker types without token concatenation, CRLF corruption, or altering Go syntax.
  - Bit-for-bit compiled instruction parity: `cmd/gosree-erase -verify` compares `.text` disassembly before and after erasure.
- **Acceptance Criteria (AC1–AC7)**:
  - AC1: All 7 directive types parse into AST indices with correct token spans.
  - AC2: Misspelled or misplaced directives emit `SAFE007` near-miss warnings or errors.
  - AC3: Marker types compile cleanly as standard Go type aliases (`*T`).
  - AC4: Second-class borrows prohibited from struct fields and package globals (`BORROW001`).
  - AC5: Local stack returns prohibited (`BORROW004`).
  - AC6: Erasure preserves formatting, handles CRLF, and leaves zero leftover directives.
  - AC7: Bit-for-bit binary disassembly match between unannotated and erased targets.
- **Baseline Verified Metrics**:
  - Packages: 9 packages (`t1_storage`, `t1_conflicts`, `t1_owner_dangling`, `t1_valid`, `t1_iso`, `t1_forkjoin`, `t1_imm`, `t1_escape`, `t1_erasure`).
  - Test functions: 9 top-level tests + 1 assembly parity subtest.
  - Test cases: 57 test cases.
  - Assertions: 40 `// want` lines.
  - Pass rate: 100% (9/9 pass in 0.4s).

### Tier 2: Boundary & Corner Cases (Concurrency Boundaries & Transfers)
- **Objective**: Verify system behavior at capability boundaries, concurrency transitions, and container interactions.
- **Scope & Key Checks**:
  - Boundary crossing invalidation: Sending `cap.Iso` or unowned values across `go` spawn or channel sends invalidates sender (`SAFE002`, `SAFE003`).
  - Fork-Join join point recovery: Restoration of unique ownership after `sync.WaitGroup` join or channel receive (`SAFE003-forkjoin`).
  - Standard library concurrent container contracts:
    - `atomic.Pointer[T].Store` / `Swap`: Transferred values must be unique (`\iso`) or deeply immutable (`\imm`).
    - `atomic.Value`: Enforce deep immutability on store; reject mutable pointers.
    - `sync.Map.Store` / `Load`: Stored values transferred; loaded values yield `\imm` read-only references.
    - `sync.Pool.Put` / `Get`: Ownership transfer on publish.
    - `context.WithValue`: Stored values deeply immutable; reject mutable pointers.
  - Concurrency spawner boundary coverage: `runtime.AddCleanup`, `sync.Pool.New`, `b.RunParallel`, slog `Handler.Handle`, `Timer.Reset`.
  - Escape checking: Moved values prohibited from escaping to package globals or outer returns (`SAFE004`).
  - Stack borrow escapes: Second-class borrows prohibited from crossing goroutine boundaries (`CAP003`).
- **Baseline Verified Metrics**:
  - Packages: 1 package (`t2_boundaries`).
  - Test cases: 26 test cases across 5 boundary categories (empty structs/arrays, loops, defers, selects, interface boxing).
  - Assertions: 8 `// want` lines (with remaining cases validating sound clean compilation).
  - Pass rate: 100% (1/1 pass in 0.38s).

### Tier 3: Cross-Feature Combinations (Immutability, Mutexes & Handler Writes)
- **Objective**: Verify multi-feature interactions, cross-package boundaries, and concurrent state protection.
- **Scope & Key Checks**:
  - Deep immutability verification (`SAFE005`):
    - Recursive struct inspection: all fields must be pointer-free, deeply immutable pointers, or protected.
    - Rejection of unmarshal leaks into unexported embedded structs (`json.Unmarshal`).
    - Rejection of mutable method calls or address-taking on immutable receivers.
  - Package variable mutation discipline (`SAFE006`):
    - Package variables only mutated inside `init()` or under explicit mutex protection.
    - Escaping package variables whose memory escapes declaring package fail closed.
  - HTTP handler self-racing write prevention (`SAFE008`):
    - Handlers registered with `http.HandleFunc` or `http.Handler` cannot write shared receiver fields or captured closure variables.
    - Multi-return getter chain traversal across packages terminates linearly without exponential blowup.
    - Diagnostics in synthetic wrappers attributed to calling source lines.
  - Mutex lockset must-analysis (`SAFE009`):
    - Every access to memory annotated with `//gosree:guarded_by <mu>` requires `<mu>` held in caller lockset.
    - Inter-procedural contracts with `//gosree:holds <mu>` enforced at call sites.
- **Baseline Verified Metrics**:
  - Packages: 1 package (`t3_combinations`).
  - Test cases: 8 pairwise interaction cases (`\iso`+borrows, `\imm`+methods, fork-join+select, transient tuples, sync vs async closures).
  - Assertions: 5 `// want` lines.
  - Pass rate: 100% (1/1 pass in 0.66s).

### Tier 4: Real-World Scenarios & Compiler Build Gates
- **Objective**: Full end-to-end integration across real-world workloads, runtime witnesses, error discipline, and compiler gate enforcement.
- **Scope & Key Checks**:
  - Error discipline (`passes/errcheck` & Rust solver):
    - `ERR001`: Discarded error return.
    - `ERR002`: Unchecked companion result use before `err == nil` check.
    - `ERR003`: Lost or shadowed error assignment.
    - `ERR004`: Typed-nil concrete pointer boxed into `error` interface.
    - `ERR005`: Discarded `Close()` on writable resource.
    - Range-over-func error and defer tracking through yield functions.
  - Runtime witness validation (`tests/stress`):
    - 58 runtime witness programs convicted of data races (`// WITNESS: race`) or panics (`// WITNESS: panic`).
    - GoSree static analyzer must flag the exact defect line on hardened code.
  - Compiler-level build enforcement gates:
    - Preflight multichecker `cmd/gosree`:
      - Exit `0`: Clean (0 findings, 0 errors).
      - Exit `1`: Syntax/type failure or internal crash.
      - Exit `2`: Refused/invalid flags (`-fix`, `-diff`).
      - Exit `3`: Vulnerabilities or safety findings detected.
    - Compiler interceptor (`gosree-gate` via `go build -toolexec="gosree-gate"`):
      - Halts compiler before object file generation whenever errors or findings are detected.
    - `go vet` toolchain integration (`go vet -vettool=$(which gosree)`).
- **Baseline Verified Metrics**:
  - Packages: 1 package (`t4_scenarios`).
  - Scenarios: 5 realistic application scenarios (Worker Pools, Event Processors, Pipelines, Key-Value Cache, 10 Negative Attack Vectors across 14 functions).
  - Assertions: 11 `// want` lines.
  - Pass rate: 100% (1/1 pass in 0.45s).

---

## Feature Inventory & Test Mapping

| # | Feature | Requirement | Tier 1 | Tier 2 | Tier 3 | Tier 4 | Primary Test Path |
|---|---------|-------------|:------:|:------:|:------:|:------:|-------------------|
| 1 | Worktree Isolation Setup | R4 | - | - | - | ✓ | `git worktree list`, harness runner |
| 2 | Adversarial Reproduction Suite | R2 | ✓ | ✓ | ✓ | ✓ | `tests/stress`, `passes/*/*_test.go` |
| 3 | Path Traversal / Zip Slip Hardening | R1, R3 | - | - | - | ✓ | `cmd/gosree-corpus/archive_paths_test.go` |
| 4 | Fact Decoder Bounds & Fuzz Check | R1, R3 | - | ✓ | - | - | `passes/ownership/summary_fuzz_1111_test.go` |
| 5 | Gusset Solver State Bounding | R1, R3 | - | - | - | ✓ | `internal/gusseterr/solver_limits_test.go` |
| 6 | Borrow Fallback Positions | R1, R3 | ✓ | - | - | - | `passes/borrow/b6a2_fallbackpos_1111_test.go` |
| 7 | Range-Over-Func Error & Defer Tracking | R1, R3 | - | - | - | ✓ | `passes/errcheck/adv_fixtures_test.go` |
| 8 | Escaping Package Variable Mutation | R1, R3 | - | - | ✓ | - | `passes/ownership/b6a2_fixes_1111_test.go` |
| 9 | HTTP Handler Getter Chain Traversal | R1, R3 | - | - | ✓ | - | `passes/ownership/handler_getter_test.go` |
| 10| Synthetic Wrapper Position Attribution| R1, R3 | - | - | ✓ | - | `passes/ownership/analysistest.go` |
| 11| Stdlib Capability Contract Parameter Shifts | R1, R3 | - | ✓ | - | - | `passes/ownership/contracts_test.go` |
| 12| Closure Scope Variable Matching | R1, R3 | - | ✓ | - | - | `passes/ownership/check.go` |
| 13| Deterministic Map Iterations | R1, R3 | ✓ | ✓ | - | - | `passes/borrow`, `passes/ownership` |
| 14| Erasure Token Separation | R1, R3 | ✓ | - | - | - | `cmd/gosree-erase/fixes_1111_test.go` |
| 15| CRLF Directive Stripping | R1, R3 | ✓ | - | - | - | `cmd/gosree-erase/fixes_1111_test.go` |
| 16| CLI Incompatible Flag Refusal | R1, R3 | ✓ | - | - | - | `cmd/gosree-erase/fixes_1111_test.go` |
| 17| `.cfg` Directory Validation | R1, R3 | ✓ | - | - | - | `cmd/gosree/unitchecker_mode_test.go` |
| 18| Preflight Exit Code Gate | R3 | - | - | - | ✓ | `cmd/gosree/preflight.go`, `cmd/gosree` |
| 19| Compilation & Build Gate Interception | R3 | - | - | - | ✓ | `cmd/gosree/gosree-gate`, `go build -toolexec` |
| 20| Dual-Track Acceptance Verification | R1–R4 | ✓ | ✓ | ✓ | ✓ | `tests/run_acceptance_tests.sh` |
| 21| Forensic Audit Verification | Zero-Cheat | ✓ | ✓ | ✓ | ✓ | `teamwork_auditor` verification |

---

## Test Architecture & Validated Runners

### 1. Acceptance Suite Runner: `tests/run_acceptance_tests.sh`
- **Location**: `tests/run_acceptance_tests.sh`
- **Invocation**:
  - Run all tiers: `bash tests/run_acceptance_tests.sh`
  - Run specific tier: `bash tests/run_acceptance_tests.sh --tier <1|2|3|4>`
  - Machine-readable JSON: `bash tests/run_acceptance_tests.sh --json`
  - Verbose output: `bash tests/run_acceptance_tests.sh -v`
- **Verified Output**:
  ```json
  {
    "status": "PASSED",
    "tier": "all",
    "go_test_exit_code": 0,
    "duration_s": 4,
    "tiers": [
      {"tier": 1, "tests_passed": 9, "tests_failed": 0, "tests_skipped": 0, "want_lines": 40},
      {"tier": 2, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 8},
      {"tier": 3, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 5},
      {"tier": 4, "tests_passed": 1, "tests_failed": 0, "tests_skipped": 0, "want_lines": 11}
    ],
    "totals": {"tests_passed": 12, "tests_failed": 0, "tests_skipped": 0, "want_lines": 64}
  }
  ```

### 2. Go E2E Package: `go test ./tests/e2e`
- **Location**: `tests/e2e/e2e_test.go`
- **Framework**: `golang.org/x/tools/go/analysis/analysistest`
- **Invocation**: `go test -v ./tests/e2e`
- **Verified Output**: 12 top-level tests (+1 subtest) PASS in 4.1s.

### 3. Stress Suite & Ledger Runner: `go test -short ./tests/stress`
- **Location**: `tests/stress/stress_test.go`
- **Invocation**: `go test -v -short ./tests/stress`
- **Verified Output (Main)**: 143 fixture packages, 610 hits locked, 187 misses, 45 fp, 0 crashes.
- **Verified Output (Worktree)**: 141 fixture packages, 600 hits locked, 186 misses, 45 fp, 0 crashes.

### 4. Worktree Execution Environment
- **Isolated Directory**: `/Users/bharath/Code/devtools/gocap-worktree`
- **Branch**: `harden/gosree-gates` at `6b401b9`
- **Local Dependency Override**: `replace github.com/bharathvbcr/gusset => ../gusset` resolves identically.
- **Critical Build Prerequisite**: `cd rust && cargo build --release` produces `/Users/bharath/Code/devtools/gocap-worktree/rust/target/release/libgusset.a` for CGO linking.
- **Portability Verification**: All test runners (`tests/run_acceptance_tests.sh`, `go test ./tests/e2e`, `go test -short ./tests/stress`) execute identically and cleanly in the worktree.
