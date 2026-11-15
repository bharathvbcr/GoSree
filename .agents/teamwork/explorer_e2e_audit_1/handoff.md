# Comprehensive Technical Audit: GoSree (gocap) E2E Test Infrastructure & Acceptance Suite

**Agent**: `explorer_e2e_audit_1`  
**Working Directory**: `/Users/bharath/Code/devtools/gocap`  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_e2e_audit_1/`  
**Date**: 2026-11-11  

---

## 1. Observation

### 1.1 Acceptance Test Suite Fixture Audit (`tests/e2e/` & `tests/e2e/testdata/src/`)

An exhaustive audit of `tests/e2e/` and `tests/e2e/testdata/src/` was conducted.

#### Test Files in `tests/e2e/`:
- `tests/e2e/e2e_test.go`: 104 lines. Declares analyzers (`annotAnalyzer`, `unsafebanAnalyzer`, `ownershipAnalyzer`, `borrowAnalyzer`, `escapeAnalyzer`, `compositeAnalyzer`) and test helpers `VerifyFixtureSyntax` and `RunTierTest`.
- `tests/e2e/tier1_feature_test.go`: 109 lines. Implements 9 top-level tests for Tier 1:
  - `TestTier1_StorageConstraints` (line 18)
  - `TestTier1_ConflictingBorrows` (line 23)
  - `TestTier1_OwnerAndDangling` (line 28)
  - `TestTier1_ValidBorrowsNLL` (line 33)
  - `TestTier1_IsoMoveSemantics` (line 38)
  - `TestTier1_ForkJoinRecovery` (line 43)
  - `TestTier1_ImmDeepImmutability` (line 48)
  - `TestTier1_StackBorrowEscape` (line 53)
  - `TestTier1_DirectivesErasure` (line 58) containing subtest `ZeroCostAssemblyEquivalence` (line 61)
- `tests/e2e/tier2_boundary_test.go`: 17 lines. Implements 1 top-level test:
  - `TestTier2_BoundaryAndCornerCases` (line 14) running `compositeAnalyzer` against `t2_boundaries`.
- `tests/e2e/tier3_combination_test.go`: 22 lines. Implements 1 top-level test:
  - `TestTier3_CrossFeatureCombinations` (line 20) running `compositeAnalyzer` against `t3_combinations`.
- `tests/e2e/tier4_realworld_test.go`: 19 lines. Implements 1 top-level test:
  - `TestTier4_RealWorldScenarios` (line 17) running `compositeAnalyzer` against `t4_scenarios`.

#### Test Packages, Functions, and `// want` Counts per Tier:

| Tier | Package Directory | Fixture File | Test Function | Test Cases in Fixture | `// want` Lines |
|---|---|---|---|---|:---:|
| **Tier 1** | `t1_storage` | `storage.go` (51 lines) | `TestTier1_StorageConstraints` | TC-T1-1.1 to 1.6 (6 cases) | 5 |
| **Tier 1** | `t1_conflicts` | `conflicts.go` (71 lines) | `TestTier1_ConflictingBorrows` | TC-T1-2.1 to 2.6 (6 cases) | 6 |
| **Tier 1** | `t1_owner_dangling` | `owner.go` (87 lines) | `TestTier1_OwnerAndDangling` | TC-T1-3.1 to 3.9 (9 cases) | 9 |
| **Tier 1** | `t1_valid` | `valid.go` (59 lines) | `TestTier1_ValidBorrowsNLL` | TC-T1-4.1 to 4.5 (5 cases) | 0 (Clean Pass) |
| **Tier 1** | `t1_iso` | `iso.go` (50 lines) | `TestTier1_IsoMoveSemantics` | TC-T1-5.1 to 5.5 (5 cases) | 4 |
| **Tier 1** | `t1_forkjoin` | `forkjoin.go` (77 lines) | `TestTier1_ForkJoinRecovery` | TC-T1-6.1 to 6.5 (5 cases) | 1 |
| **Tier 1** | `t1_imm` | `imm.go` (63 lines) | `TestTier1_ImmDeepImmutability` | TC-T1-7.1 to 7.8 (8 cases) | 7 |
| **Tier 1** | `t1_escape` | `escape.go` (78 lines) | `TestTier1_StackBorrowEscape` | TC-T1-8.1 to 8.8 (8 cases) | 8 |
| **Tier 1** | `t1_erasure` | `erasure.go` (46 lines) | `TestTier1_DirectivesErasure` | TC-T1-9.1 to 9.5 (5 cases) | 0 (Parity) |
| **Tier 1 Subtotal** | **9 packages** | **9 files** | **9 functions (+1 subtest)** | **57 cases** | **40** |
| **Tier 2** | `t2_boundaries` | `boundaries.go` (315 lines) | `TestTier2_BoundaryAndCornerCases` | TC-T2-1.1 to 5.6 (26 cases across 5 categories) | 8 |
| **Tier 2 Subtotal** | **1 package** | **1 file** | **1 function** | **26 cases** | **8** |
| **Tier 3** | `t3_combinations` | `combinations.go` (195 lines) | `TestTier3_CrossFeatureCombinations` | Pairs 1 to 8 (8 pairwise interactions) | 5 |
| **Tier 3 Subtotal** | **1 package** | **1 file** | **1 function** | **8 cases** | **5** |
| **Tier 4** | `t4_scenarios` | `scenarios.go` (220 lines) | `TestTier4_RealWorldScenarios` | Scenarios 1 to 5 (incl. 10 negative attacks) | 11 |
| **Tier 4 Subtotal** | **1 package** | **1 file** | **1 function** | **5 scenarios (14 functions)** | **11** |
| **Auxiliary** | `github.com/.../cap`| `cap.go` (67 lines) | Auxiliary / Type definitions | `cap.Mut`, `cap.Ref`, `cap.Iso`, `cap.Imm` | 0 |
| **TOTAL** | **12 packages (+1 aux)** | **12 files (+1 aux)** | **12 functions (+1 subtest)** | **96 cases / scenarios** | **64** |

---

### 1.2 Mapping of Tiers to AC1–AC7 and `PROJECT.md` Feature Inventory

| Acceptance Criterion | Description | Primary Fixture Package & Test Function | Mapped Features (`PROJECT.md`) | Invariants Enforced |
|---|---|---|---|---|
| **AC1** | Struct Field & Package Global Borrow Ban | `t1_storage/storage.go`<br>`TestTier1_StorageConstraints` | Feature 6 (Borrow Fallback Positions), Feature 8 (Escaping Package Variables) | `BORROW001` rejected on struct fields and package variables |
| **AC2** | Conflicting Borrows, Aliasing, Owner Moves, & Dangling Stack Returns | `t1_conflicts/conflicts.go`<br>`TestTier1_ConflictingBorrows`<br>`t1_owner_dangling/owner.go`<br>`TestTier1_OwnerAndDangling` | Feature 6 (Borrow Fallback Positions), Feature 13 (Deterministic Map Iteration) | `BORROW002` (simultaneous `\mut` or `\mut`+`\rob`), `BORROW003` (owner write/move during borrow), `BORROW004` (dangling stack variable return) |
| **AC3** | Valid Second-Class Borrows Local to Scope & NLL Expiry | `t1_valid/valid.go`<br>`TestTier1_ValidBorrowsNLL` | Feature 2 (Adversarial Reproduction), Feature 12 (Closure Scope Variable Matching) | Non-Lexical Lifetime (NLL) reuse, `\rob` multiple readers, callee mutation clean return |
| **AC4** | `\iso` Reference Capability & Fork-Join Recovery | `t1_iso/iso.go`<br>`TestTier1_IsoMoveSemantics`<br>`t1_forkjoin/forkjoin.go`<br>`TestTier1_ForkJoinRecovery` | Feature 4 (Fact Decoder Bounds), Feature 11 (Stdlib Capability Contract Parameter Shifts) | `SAFE003` use-after-move across channel sends and goroutines; ownership restored after `sync.WaitGroup.Wait()` and channel receive |
| **AC5** | `\imm` Deep Immutability & Lock-Free Sharing | `t1_imm/imm.go`<br>`TestTier1_ImmDeepImmutability` | Feature 8 (Escaping Package Variable Mutation), Feature 9 (HTTP Handler Getter Chain Traversal) | `SAFE005` rejects field writes, pointer calls, and mutable fields (`[]string`, `chan *int`) on `//gosree:imm` types |
| **AC6** | Stack-Scoped Borrow Goroutine & Channel Escape Ban | `t1_escape/escape.go`<br>`TestTier1_StackBorrowEscape` | Feature 12 (Closure Scope Variable Matching) | `CAP003` prevents `\mub` and `\rob` from crossing goroutine boundaries or being sent over channels |
| **AC7** | Zero-Cost Erasure & Disassembly Parity | `t1_erasure/erasure.go`<br>`TestTier1_DirectivesErasure` | Feature 14 (Erasure Token Separation), Feature 15 (CRLF Directive Stripping), Feature 16 (CLI Incompatible Flag Refusal) | Stripping directives produces bit-for-bit compiled instruction opcode parity (`go tool compile -S`) |
| **Tier 2** | Boundary & Corner Cases | `t2_boundaries/boundaries.go`<br>`TestTier2_BoundaryAndCornerCases` | Feature 11 (Stdlib Capability Contracts), Feature 12 (Closure Scope Matching), Feature 13 (Deterministic Maps) | Empty structs/arrays, loop-carried borrows, defers/unwinding, branching/selects, interface boxing (`BORROW001`) |
| **Tier 3** | Cross-Feature Interactions | `t3_combinations/combinations.go`<br>`TestTier3_CrossFeatureCombinations` | Feature 8 (Package Variables), Feature 9 (HTTP Handler Getter Chain), Feature 10 (Synthetic Wrapper Positions) | 8 pairwise interactions (`\iso`+borrows, `\imm`+methods, fork-join+select, transient tuples, sync vs async closures) |
| **Tier 4** | Real-World Application Scenarios | `t4_scenarios/scenarios.go`<br>`TestTier4_RealWorldScenarios` | Feature 1 (Worktree Isolation), Feature 2 (Adversarial Suite), Feature 18 (Exit Code Gate), Feature 19 (Compiler Gate), Feature 20 (Acceptance Verification) | Worker pools, pipelines, event processors, key-value cache, 10 adversarial security attacks |

---

### 1.3 Execution and Verification of Test Runners

#### 1.3.1 Acceptance Runner (`tests/run_acceptance_tests.sh --json`)
Command executed:
```bash
bash /Users/bharath/Code/devtools/gocap/tests/run_acceptance_tests.sh --json
```
Exit code: `0`  
Standard output:
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

#### 1.3.2 Go Test Runner for Acceptance Suite (`go test -v ./tests/e2e`)
Command executed:
```bash
cd /Users/bharath/Code/devtools/gocap && go test -v ./tests/e2e
```
Exit code: `0`  
Execution time: `4.130s`  
Test statuses:
- `=== RUN   TestTier1_StorageConstraints` -> `--- PASS: TestTier1_StorageConstraints (0.42s)`
- `=== RUN   TestTier1_ConflictingBorrows` -> `--- PASS: TestTier1_ConflictingBorrows (0.05s)`
- `=== RUN   TestTier1_OwnerAndDangling` -> `--- PASS: TestTier1_OwnerAndDangling (0.05s)`
- `=== RUN   TestTier1_ValidBorrowsNLL` -> `--- PASS: TestTier1_ValidBorrowsNLL (0.05s)`
- `=== RUN   TestTier1_IsoMoveSemantics` -> `--- PASS: TestTier1_IsoMoveSemantics (0.04s)`
- `=== RUN   TestTier1_ForkJoinRecovery` -> `--- PASS: TestTier1_ForkJoinRecovery (1.02s)`
- `=== RUN   TestTier1_ImmDeepImmutability` -> `--- PASS: TestTier1_ImmDeepImmutability (0.10s)`
- `=== RUN   TestTier1_StackBorrowEscape` -> `--- PASS: TestTier1_StackBorrowEscape (0.54s)`
- `=== RUN   TestTier1_DirectivesErasure`
  - `=== RUN   TestTier1_DirectivesErasure/ZeroCostAssemblyEquivalence` -> `--- PASS: (0.11s)`
  -> `--- PASS: TestTier1_DirectivesErasure (0.23s)`
- `=== RUN   TestTier2_BoundaryAndCornerCases` -> `--- PASS: TestTier2_BoundaryAndCornerCases (0.38s)`
- `=== RUN   TestTier3_CrossFeatureCombinations` -> `--- PASS: TestTier3_CrossFeatureCombinations (0.66s)`
- `=== RUN   TestTier4_RealWorldScenarios` -> `--- PASS: TestTier4_RealWorldScenarios (0.45s)`
Summary: `PASS ok github.com/bharathvbcr/gosree/tests/e2e 4.130s` (12 passed, 0 failed).

#### 1.3.3 Stress Suite Runner (`go test -v -short ./tests/stress`) in Main Workspace
Command executed:
```bash
cd /Users/bharath/Code/devtools/gocap && go test -v -short ./tests/stress
```
Exit code: `0`  
Execution time: `2.699s`  
Summary:
- `143 fixture packages, 0 crash fixtures probed in child processes`
- `stress ledger: 610 hits locked, 187 KNOWN MISSES, 45 known false positives, 0 known crashes`
Rule ledger breakdown:
```
  rule         hit  miss    fp
  BORROW001     33     0     1
  BORROW002     30     1     3
  BORROW003     26     0     1
  BORROW004      1     0     6
  CAP003         4     6     0
  ERR001        11     0     0
  ERR002        12     0     0
  ERR003         8     0     0
  ERR004        11     1     0
  ERR005         5     0     0
  NORULE         0    63     0
  SAFE001       57     3     0
  SAFE002       33     0     9
  SAFE003      164    58    12
  SAFE004        3     1     1
  SAFE005       52     8     4
  SAFE006       22     7     1
  SAFE007       77     1     0
  SAFE008       20    13     0
  SAFE009       41    25     7
```

---

### 1.4 Git Worktree Status & Portability Investigation

#### 1.4.1 `git worktree list` output
```
/Users/bharath/Code/devtools/gocap                                                                                              7b739fc [main]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/8e79-adv          6a8ac00 [run/8e79-adv]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/8e79-cmd          6144167 [run/8e79-cmd]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/b6a2-adv          aa6d62f [run/b6a2-adv]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/b6a2-base2        9161d81 (detached HEAD)
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/b6a2-ownership    aa6d62f [run/b6a2-ownership]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/base              9161d81 (detached HEAD)
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/d323-frontend     fb3fb14 [run/d323-frontend]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/d323-frontend-adv 667c44e [run/d323-frontend-adv]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/d323-solver       3ca416b [run/d323-solver]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/d323-solver-adv   1dda5ef [run/d323-solver-adv]
/private/tmp/claude-501/-Users-bharath-Code-devtools-gocap/39229969-59d3-435b-9afe-c27dd610fe2c/scratchpad/wt/integ             6b401b9 [run/integ-1111]
/Users/bharath/Code/devtools/gocap-worktree                                                                                     6b401b9 [harden/gosree-gates]
```

- `/Users/bharath/Code/devtools/gocap-worktree` exists.
- Branch: `harden/gosree-gates` at commit `6b401b9`.
- Git status in worktree: `nothing to commit, working tree clean`.

#### 1.4.2 Runner Execution in `/Users/bharath/Code/devtools/gocap-worktree`
1. `tests/run_acceptance_tests.sh --json`:
   - Executed cleanly with exit code `0` in 6 seconds.
   - Result matches main workspace: 12 tests passed, 0 failed, 64 `// want` lines.
2. `go test -v ./tests/e2e`:
   - Executed cleanly with exit code `0` in 4.057 seconds.
   - All 12 tests (+1 subtest) passed.
3. `go test -v -short ./tests/stress`:
   - Initial execution FAILED with build link error:
     ```
     # github.com/bharathvbcr/gosree/tests/stress.test
     ld: warning: search path '/Users/bharath/Code/devtools/gocap-worktree/internal/gusseterr/../../rust/target/release' not found
     Undefined symbols for architecture arm64:
       "_gosree_gusset_init", referenced from:
           __cgo_ec2d956f784b_Cfunc_gosree_gusset_init in 000001.o
     ld: symbol(s) not found for architecture arm64
     clang: error: linker command failed with exit code 1
     FAIL	github.com/bharathvbcr/gosree/tests/stress [build failed]
     ```
   - Remediation: Executed `cd /Users/bharath/Code/devtools/gocap-worktree/rust && cargo build --release`.
     Finished in 6.58 seconds, generating `/Users/bharath/Code/devtools/gocap-worktree/rust/target/release/libgusset.a`.
   - Re-execution: `go test -v -short ./tests/stress` in worktree PASSED with exit code `0` in 5.314s.
   - Worktree stress ledger report:
     `141 fixture packages, 0 crash fixtures probed in child processes`
     `stress ledger: 600 hits locked, 186 KNOWN MISSES, 45 known false positives, 0 known crashes`
     Breakdown:
     ```
       rule         hit  miss    fp
       BORROW001     33     0     1
       BORROW002     30     1     3
       BORROW003     26     0     1
       BORROW004      1     0     6
       CAP003         4     6     0
       ERR001        11     0     0
       ERR002        12     0     0
       ERR003         8     0     0
       ERR004        11     1     0
       ERR005         5     0     0
       NORULE         0    63     0
       SAFE001       57     3     0
       SAFE002       33     0     9
       SAFE003      164    58    12
       SAFE004        3     1     1
       SAFE005       50     8     4
       SAFE006       22     7     1
       SAFE007       77     1     0
       SAFE008       12    12     0
       SAFE009       41    25     7
     ```

---

## 2. Logic Chain

1. **Premise 1 (E2E Suite Structure)**:
   - The E2E acceptance suite is partitioned across 4 tiers.
   - Tier 1 addresses AC1–AC7 across 9 distinct package directories (`t1_storage`, `t1_conflicts`, `t1_owner_dangling`, `t1_valid`, `t1_iso`, `t1_forkjoin`, `t1_imm`, `t1_escape`, `t1_erasure`), totaling 40 `// want` lines.
   - Tiers 2, 3, and 4 each use a single fixture package (`t2_boundaries`, `t3_combinations`, `t4_scenarios`), containing 8, 5, and 11 `// want` lines respectively.
   - While `t2_boundaries`, `t3_combinations`, and `t4_scenarios` contain 26, 8, and 14 test functions/scenarios, the majority test valid/clean code passes without diagnostics, leading to 64 total `// want` assertions across all tiers.

2. **Premise 2 (Test Infrastructure Plan vs Codebase Reality)**:
   - In `TEST_INFRA.md`, the planned minimum thresholds are:
     - Tier 1: ≥ 9 packages, ≥ 40 `// want` lines. (Actual: 9 packages, 40 want lines — 100% compliant).
     - Tier 2: ≥ 5 packages, ≥ 25 `// want` lines. (Actual: 1 package, 8 want lines — gap of 4 packages, 17 want lines).
     - Tier 3: ≥ 5 packages, ≥ 20 `// want` lines. (Actual: 1 package, 5 want lines — gap of 4 packages, 15 want lines).
     - Tier 4: ≥ 5 packages, ≥ 25 `// want` lines. (Actual: 1 package, 11 want lines — gap of 4 packages, 14 want lines).
     - Total: planned ≥ 24 packages and ≥ 110 want lines vs actual 12 packages and 64 want lines.
   - `tests/run_acceptance_tests.sh` dynamically scans for `t"$1"_*` directories and tallies actual `// want` lines. Because it checks whether the exit code is 0, zero tests failed, and total tests passed > 0, it outputs `"status": "PASSED"`.

3. **Premise 3 (Worktree Stress Baseline Discrepancy)**:
   - The main workspace `/Users/bharath/Code/devtools/gocap` contains uncommitted/untracked files:
     `tests/stress/testdata/src/lk_w_getter/` (with `main.go` and `dep/`).
   - These 2 extra packages add 10 hits (`SAFE008` +8, `SAFE005` +2) and 1 miss.
   - The isolated worktree `/Users/bharath/Code/devtools/gocap-worktree` at `6b401b9` does not have `lk_w_getter/`, so its locked ledger reflects the baseline of 141 packages, 600 hits, and 186 misses.
   - Both test runs pass `TestStress` because each directory's `FINDINGS.md` / `stress_test.go` ledger matches its respective package set.

4. **Premise 4 (Worktree CGO Build Dependency)**:
   - `internal/gusseterr/engine_unix.go` has `#cgo LDFLAGS: -L${SRCDIR}/../../rust/target/release` and calls `gosree_gusset_init()`.
   - Git ignores `rust/target/`. A newly initialized git worktree lacks `rust/target/release/libgusset.a`.
   - Any test importing `internal/gusseterr` (including `tests/stress`) will fail to link in a fresh worktree unless `cargo build --release` is invoked inside `rust/`.
   - Once built, `go test ./tests/stress` runs seamlessly.

---

## 3. Caveats

1. **Uncommitted Files on Main**:
   - The main workspace checkout `/Users/bharath/Code/devtools/gocap` has dirty changes in `passes/ownership` and untracked files in `tests/stress/testdata/src/lk_w_getter/`. As required by R4, these were NOT touched or committed.
2. **Acceptance Test Scope vs Unit Tests**:
   - Compiler-level build enforcement gates (`go build -toolexec="gosree-gate"` and `go vet -vettool=gosree`) are currently exercised by unit tests in `cmd/gosree/unitchecker_mode_test.go` and `passes/errcheck/adv_fixtures_test.go` rather than within `tests/e2e/testdata/src/t4_scenarios`.
3. **Rust Workspace Dependency**:
   - GoSree's error-discipline solver depends on `github.com/bharathvbcr/gusset` via the local directory `../gusset` as specified in `go.mod` (`replace github.com/bharathvbcr/gusset => ../gusset`). This relative path resolves identically from `/Users/bharath/Code/devtools/gocap` and `/Users/bharath/Code/devtools/gocap-worktree`, but would fail if a worktree were located in a different relative directory hierarchy.

---

## 4. Conclusion

1. **Acceptance Suite Status**:
   - The acceptance test infrastructure in `tests/e2e` is functional, healthy, and passes 100% across all 4 tiers in both the main workspace and the isolated worktree (`tests_passed`: 12, `tests_failed`: 0, `want_lines`: 64).
2. **Fixture Layout & Target Minimums**:
   - Tier 1 fully satisfies all AC1–AC7 criteria with 9 dedicated packages and 40 `// want` lines.
   - Tiers 2, 3, and 4 group their test cases into single monolithic packages (`t2_boundaries`, `t3_combinations`, `t4_scenarios`) containing 26, 8, and 14 test cases respectively, with 8, 5, and 11 `// want` lines.
   - While valid and green, expanding Tiers 2–4 into separate packages or adding dedicated fixtures for compiler build gates and error discipline would bring the suite up to the planned ≥ 24 packages and ≥ 110 want lines described in `TEST_INFRA.md`.
3. **Worktree Portability & Invariants**:
   - `/Users/bharath/Code/devtools/gocap-worktree` is verified, clean, on branch `harden/gosree-gates`.
   - **Critical Worktree Prerequisite**: In any fresh worktree, `cd rust && cargo build --release` MUST be run before running `go test ./tests/stress` or any package depending on `internal/gusseterr`.

---

## 5. Verification Method

To independently verify all findings:

1. **Verify Acceptance Test Runner (JSON output)**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap && bash tests/run_acceptance_tests.sh --json
   ```
   Expect: JSON output with `"status": "PASSED"`, `"totals": {"tests_passed": 12, "tests_failed": 0, "tests_skipped": 0, "want_lines": 64}`.

2. **Verify Go E2E Acceptance Test Suite**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap && go test -v ./tests/e2e
   ```
   Expect: 12 tests PASS in ~4.1s.

3. **Verify Stress Suite in Main Workspace**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap && go test -v -short ./tests/stress
   ```
   Expect: 143 fixture packages, 610 hits locked, 187 misses, 45 fp, 0 crashes.

4. **Verify Worktree Execution**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   bash tests/run_acceptance_tests.sh --json
   go test -v ./tests/e2e
   cd rust && cargo build --release
   cd .. && go test -v -short ./tests/stress
   ```
   Expect: Acceptance tests pass (12 passed, 64 want lines); stress suite passes (141 fixture packages, 600 hits locked, 186 misses, 45 fp, 0 crashes).
