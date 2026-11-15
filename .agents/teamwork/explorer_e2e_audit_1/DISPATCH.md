# Task Assignment: E2E Test Suite & Infrastructure Audit

Working directory: /Users/bharath/Code/devtools/gocap
State folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_e2e_audit_1/
Parent conversation ID: ecd86881-5de7-43d6-bfc3-e9af5c06f1df

MANDATORY FIRST STEPS:
1. Read /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md.
2. Read /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md.

YOUR MISSION:
Perform a comprehensive technical audit of the E2E test infrastructure and acceptance suite:
1. Inspect all files in `tests/e2e/`, including `tests/e2e/testdata/src/`:
   - Categorize all fixtures under Tier 1 (`t1_*`), Tier 2 (`t2_*`), Tier 3 (`t3_*`), and Tier 4 (`t4_*`).
   - Count the number of test files, test functions, and `// want` lines for each tier.
   - Trace how each tier maps to AC1–AC7 and the Feature Inventory in `PROJECT.md`.
2. Inspect `tests/run_acceptance_tests.sh` and verify its behavior:
   - Run `bash tests/run_acceptance_tests.sh --json` and capture the exact JSON output.
   - Run `go test -v ./tests/e2e` and check individual test statuses.
   - Run `go test -v -short ./tests/stress` and report the ledger breakdown (hits locked, misses, false positives, crashes).
3. Check the status of git worktrees:
   - Run `git worktree list`.
   - Check if `/Users/bharath/Code/devtools/gocap-worktree` exists, what branch it is on, and whether test runners execute seamlessly from that worktree directory.
4. Identify any gaps, missing test cases, or test runner portability issues between the main workspace and the worktree.
5. Write your comprehensive audit report to `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_e2e_audit_1/handoff.md`.
6. Send a message to parent (`ecd86881-5de7-43d6-bfc3-e9af5c06f1df`) when done with summary.
