# Dispatch — Reviewer M4-6

## Task
Review interface contracts, stress test ledger stability, and overall regression test suite health across Go and Rust packages.

## Mandatory Inputs
- Original Request: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md` (Read first)
- Scope Document: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m4_acceptance_gen2/SCOPE.md`
- Worker 3 Handoff: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_3/handoff.md`
- Gate Status: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m4_acceptance_gen2/GATE_STATUS.md`
- Workspace: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)
- Main Checkout: `/Users/bharath/Code/devtools/gocap` (Strictly read-only / untouched)

## Instructions
1. Read `ORIGINAL_REQUEST.md` and Worker 3 handoff report.
2. Review `tests/stress/testdata/ledger.json` (623 hits, 15 promoted findings) for integrity and stability.
3. Verify the 4-tier acceptance test suite (`bash tests/run_acceptance_tests.sh --json` -> 12 packages, 64 want lines).
4. Verify package test suites across `cmd/...`, `passes/...`, `internal/...`, and Rust crates (`cargo test --workspace` under `rust/`).
5. Confirm strict workspace isolation: main repo `/Users/bharath/Code/devtools/gocap` has no uncommitted source edits.
6. Deliver handoff at `/Users/bharath/Code/devtools/gocap/.agents/teamwork/reviewer_m4_6/handoff.md` with explicit verdict `APPROVE` or `REQUEST_CHANGES`.


## 2026-11-11T22:20:46Z
You are Reviewer M4-6.
Your state folder is: /Users/bharath/Code/devtools/gocap/.agents/teamwork/reviewer_m4_6/
Your working directory is: /Users/bharath/Code/devtools/gocap-worktree
Your dispatch assignment is in: /Users/bharath/Code/devtools/gocap/.agents/teamwork/reviewer_m4_6/DISPATCH.md
Read /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md first.

Review interface contracts, stress test ledger stability (tests/stress/testdata/ledger.json with 623 hits, 15 promoted findings), and overall regression test health across Go and Rust packages.
Verify 4-tier acceptance test suite (bash tests/run_acceptance_tests.sh --json -> 12 packages, 64 want lines).
Verify package regression test suites across cmd/..., passes/..., internal/..., and rust/ crates.
Confirm strict workspace isolation: main checkout /Users/bharath/Code/devtools/gocap is completely clean and untouched.
Write your handoff report to /Users/bharath/Code/devtools/gocap/.agents/teamwork/reviewer_m4_6/handoff.md with explicit verdict APPROVE or REQUEST_CHANGES.
When complete, notify parent via send_message.
