# Worker Dispatch — Milestone 4: E2E Acceptance Verification & Final Forensic Audit

## Mandatory Inputs & References
- ORIGINAL_REQUEST.md: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md` (Read first)
- PROJECT.md: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md`
- SCOPE.md: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m4_acceptance/SCOPE.md`
- Working Directory: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)
- State Folder: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_1/`
- Parent Sub-Orchestrator ID: `33d7e329-0a05-4f09-b12d-9123259f9fff`

## MANDATORY INTEGRITY WARNING
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A teamwork_auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

## Objective & Tasks
1. Execute the 4-tier acceptance test suite in `/Users/bharath/Code/devtools/gocap-worktree`:
   - `bash tests/run_acceptance_tests.sh --json`
2. Execute all package test suites in `/Users/bharath/Code/devtools/gocap-worktree`:
   - `go test -v -count=1 ./cmd/...`
   - `go test -v -count=1 ./passes/...`
   - `go test -v -count=1 ./internal/...`
   - `go test -v -count=1 ./tests/gates/...`
   - `go test -v -count=1 ./tests/stress/...`
   - Rust solver suite: check `rust/` directory (e.g. `cd rust/gosree-solver && cargo test` or `cargo test --workspace` in `rust/` if present)
3. Stress Ledger Synchronization:
   - If any stress test outputs in `tests/stress/...` show differences because improved precision in Milestone 2 promoted previously missed findings, synchronize `tests/stress/testdata/ledger.json` (or equivalent) so all findings match expectations and tests pass 100%.
4. Document all verification commands, passing test outputs, coverage summaries, and statistics in `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_1/handoff.md`.
5. Send completion message with link to `handoff.md` to parent sub-orchestrator.
