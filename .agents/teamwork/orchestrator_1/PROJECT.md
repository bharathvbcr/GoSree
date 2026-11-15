# Project: GoSree (gocap) — Hardening & Build Gate Enforcement

## Architecture
GoSree (`gosree`) is a static analysis suite for Go enforcing Rust-style memory safety, reference capabilities, borrow checking, and error discipline (`ERR001`–`ERR005`) with zero-cost erasure.
The hardening project addresses 16 identified failure and vulnerability classes across security (path traversal, fact decoder bomb, solver memory DoS), correctness (unpositioned drops, range-over-func yield suppression, escaping globals, getter bypasses, contract shifts), precision (false ERR002, stale hatches), and tooling/gates (token smashing, CRLF truncation, incompatible flags, .cfg confusion, exponential complexity, synthetic wrapper diagnostics).

## Feature Inventory
| # | Feature | Description | Milestone | Source |
|---|---------|-------------|-----------|--------|
| 1 | Isolated Git Worktree Setup | Setup isolated git worktree preserving main checkout unmodified (R4) | M1 | survey (R4) |
| 2 | Adversarial Reproduction Suite | Deterministic reproduction tests for all 16 vulnerability/failure classes failing on unhardened code | M1 | survey (R2) |
| 3 | Path Traversal / Zip Slip Hardening | Bound archive path traversal and symlink expansion in `cmd/gosree-corpus` | M2 | survey (A1) |
| 4 | Fact Decoder Memory & Bounds Check | Safe decoding of `FuncFact` lengths without integer wrap or over-allocation | M2 | survey (A2) |
| 5 | Gusset Solver State Bounding | Bound solver state cells per function and fail closed at call-graph cap | M2 | survey (A3, B3) |
| 6 | Borrow Fallback Positions | Report unpositioned SSA borrow findings at parent function/file rather than dropping | M2 | survey (B1) |
| 7 | Range-Over-Func Error & Defer Tracking | Full error and deferred close tracking through yield function synthetic returns | M2 | survey (B2) |
| 8 | Escaping Package Variable Mutation | Fail closed on package variables whose memory escapes declaring package (`SAFE006`) | M2 | survey (B4) |
| 9 | HTTP Handler Getter Chain Traversal | Memoized linear traversal of multi-return getter chains across packages (`SAFE008`) | M2 | survey (B5, D5) |
| 10 | Synthetic Wrapper Method Position Attribution | Attribute diagnostics in synthetic wrappers to calling lines rather than `-` | M2 | survey (D5) |
| 11 | Stdlib Capability Contract Parameter Shifts | Account for bound method wrappers and interface invokes in `publishArgs` | M2 | survey (B6) |
| 12 | Closure Scope Variable Matching | Match closure variables by `*types.Var` instead of name string, eliminating false CAP003 | M2 | survey (C1) |
| 13 | Deterministic Map Iterations | Sort map keys in borrow and ownership passes to guarantee deterministic diagnostics | M2 | survey (C1) |
| 14 | Erasure Token Separation | Insert whitespace when stripping inline block directives to prevent token concatenation | M2 | survey (D1) |
| 15 | CRLF Directive Stripping | Account for `\r\n` in comment length calculations in `cmd/gosree-erase` | M2 | survey (D2) |
| 16 | CLI Incompatible Flag Refusal | Refuse `-w`/`-o` with `-l`/`-d` and exit 2 in `cmd/gosree-erase` | M2 | survey (D3) |
| 17 | `.cfg` Directory Validation | Require `.cfg` argument in `cmd/gosree` to be regular file, not directory | M2 | survey (D4) |
| 18 | Preflight Exit Code Gate | Enforce strict exit codes (0 clean, 1 failure, 2 usage, 3 findings) in `cmd/gosree` | M3 | survey (R3) |
| 19 | Compilation & Build Gate Interception | Implement compiler gate (`go build -toolexec="gosree-gate"` / `go vet -vettool`) halting compilation on unresolved errors | M3 | survey (R3) |
| 20 | Dual-Track Acceptance Verification | 100% pass of 4-tier acceptance test suite (`tests/e2e`), stress ledger validation, and zero regressions | M4 | survey (Acceptance) |
| 21 | Forensic Audit Verification | Binary veto verification of genuine implementation, zero test mocking/stubs, worktree isolation | M4 | survey (Audit) |

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| M1 | Worktree Setup & Adversarial Reproduction | Setup isolated worktree; execute adversarial tests proving failure on unhardened code | none | DONE (Verified in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`; all 16 vulnerability classes and 4 runtime witnesses reproduced; Gate PASS) |
| M2 | Codebase Hardening & Vulnerability Remediation | Implement fixes for all 16 vulnerability classes in worktree; verify unit tests pass | M1 | DONE (Verified in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`; all 16 vulnerability classes & 4 runtime witnesses remediated; 100% unit, package, solver, acceptance, and stress tests pass; Gate PASS) |
| M3 | Build & Compilation Enforcement Gates | Wire strict exit codes and build gates (`go build -toolexec`, `go vet`) halting compilation | M2 | DONE (Verified in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`; cmd/gosree exit codes 0/1/2/3 enforced; cmd/gosree-gate compiler interceptor halts compilation before .a emission on violations; 100% tests pass; Gate PASS) |
| M4 | E2E Acceptance & Forensic Audit | Verify 100% test pass on hardened codebase; pass forensic audit veto check | M3 | DONE (Verified in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`; 100% test pass across 4-tier acceptance suite, all 14 Go packages, 4 Rust crates, and 58 concurrency witnesses; compiler evasion vectors defeated with zero emission before gate abortion; multi-phase forensic audit CLEAN with zero facades and strict workspace isolation; Gate PASS) |

## Interface Contracts
### Worktree Isolation ↔ Main Workspace
- Isolated worktree path: `/Users/bharath/Code/devtools/gocap-worktree`
- Branch: `harden/gosree-gates` created from current commit / `run/integ-1111`
- Dependency `../gusset` resolves identically from worktree directory.
- Main tree `/Users/bharath/Code/devtools/gocap` remains strictly untouched.

### Adversarial Tests ↔ Analysis Passes
- Tests located in respective package test suites (`passes/ownership`, `passes/borrow`, `passes/errcheck`, `cmd/gosree-erase`, `cmd/gosree-corpus`, `tests/stress`).
- Pre-fix: Tests FAIL with specific error message, race detector conviction (`WITNESS: race`), panic (`WITNESS: panic`), or timeout.
- Post-fix: Tests PASS with exit code 0.

### Build Enforcement Gates ↔ Compilation Pipeline
- Multichecker CLI `cmd/gosree`:
  - 0: Clean (no errors, no findings)
  - 1: Analysis or compilation failure
  - 2: Refused/invalid flags
  - 3: Findings detected (vulnerabilities or safety errors)
- `gosree-gate` compiler interceptor:
  - Invoked via `go build -toolexec="gosree-gate"` or `go vet -vettool=gosree`.
  - Halts compiler before object file generation on exit code != 0.

## Code Layout
- Worktree Root: `/Users/bharath/Code/devtools/gocap-worktree`
- `cap/`: Zero-cost generic type aliases
- `cmd/gosree/`: Multichecker CLI and preflight gates
- `cmd/gosree-erase/`: AST zero-cost erasure and bit-for-bit disassembly verification
- `cmd/gosree-corpus/`: Corpus archive extraction and annotation
- `internal/annot/`: Directive parser and stale hatch detection
- `internal/formal/`: Reference operational model
- `internal/gusseterr/`: Error discipline solver wire protocol and Gusset bridge
- `passes/unsafeban/`: SAFE001 ban on unsafe operations
- `passes/borrow/`: Second-class borrow checking (BORROW001–BORROW004)
- `passes/ownership/`: Reference capabilities, container contracts, handler writes, locksets
- `passes/errcheck/`: Error discipline pass safeerr (ERR001–ERR005)
- `rust/`: Cargo workspace (gosree-errsolve, gusset-engine)
- `tests/e2e/`: 4-tier acceptance test suite
- `tests/stress/`: Adversarial stress suite and runtime witnesses
