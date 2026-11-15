# Scope: Milestone 4 — E2E Acceptance Verification & Final Forensic Audit

## Objective
Verify 100% test pass on the hardened codebase across all packages, test tiers, and stress suites in `/Users/bharath/Code/devtools/gocap-worktree`. Perform the final comprehensive forensic integrity audit verifying zero mocks, zero facades, zero hardcoded test strings, and strict workspace isolation.

## Target Workspace
- Worktree Directory: `/Users/bharath/Code/devtools/gocap-worktree`
- Branch: `harden/gosree-gates`
- Main Checkout: `/Users/bharath/Code/devtools/gocap` (Strictly read-only / untouched)

## Test & Verification Scope
1. **Full 4-Tier E2E Acceptance Suite**:
   - `bash tests/run_acceptance_tests.sh --json` in `/Users/bharath/Code/devtools/gocap-worktree`.
   - Expected: 100% pass across all 12 packages (64 want lines).
2. **Package Regression Test Suites**:
   - `go test -v -count=1 ./cmd/...` (including `cmd/gosree`, `cmd/gosree-gate`, `cmd/gosree-erase`, `cmd/gosree-corpus`)
   - `go test -v -count=1 ./passes/...` (including `passes/unsafeban`, `passes/borrow`, `passes/ownership`, `passes/errcheck`)
   - `go test -v -count=1 ./internal/...` (including `internal/annot`, `internal/formal`, `internal/gusseterr`)
   - `go test -v -count=1 ./tests/gates/...` (compiler gate interception & `go vet -vettool`)
   - `go test -v -count=1 ./tests/stress/...` (adversarial stress suite & runtime witnesses)
   - Rust solver tests: `cd rust/gosree-solver && cargo test` (if present)
3. **Stress Ledger Synchronization**:
   - If Milestone 2's precision improvements promoted previously marked `MISS` cases to `HIT`, synchronize the stress ledger `tests/stress/testdata/ledger.json` (or equivalent) so that the entire stress suite passes cleanly with zero unexpected regressions or discrepancies.
4. **Independent Verification Wave**:
   - Worker to run all suites, update stress ledger if needed, and confirm 100% PASS.
   - 2 Reviewers independently reviewing test completeness, regression avoidance, and interface contracts.
   - 2 Challengers executing empirical adversarial stress tests and runtime witness verification.
   - 1 Forensic Auditor (`teamwork_auditor`) performing repo-wide forensic integrity verification with binary veto.

## Mandatory Constraints
- DO NOT CHEAT. All implementations and tests must be genuine.
- Strict workspace isolation: `/Users/bharath/Code/devtools/gocap` must remain unmodified.
- Forensic Auditor verdict is an absolute binary veto.
