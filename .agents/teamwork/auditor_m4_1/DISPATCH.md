# Forensic Auditor Dispatch — Milestone 4: Final Repository-Wide Integrity Audit

## Mandatory Inputs & References
- ORIGINAL_REQUEST.md: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md` (Read first)
- PROJECT.md: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md`
- SCOPE.md: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m4_acceptance/SCOPE.md`
- Worker Handoff: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_1/handoff.md`
- Working Directory: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)
- Main Checkout: `/Users/bharath/Code/devtools/gocap`
- State Folder: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/auditor_m4_1/`
- Parent Sub-Orchestrator ID: `33d7e329-0a05-4f09-b12d-9123259f9fff`

## MANDATORY AUDIT RULES & BINARY VETO
⚠️ **HARD VETO — NON-NEGOTIABLE**: If you detect any cheat, hardcoded test expectation in production code, dummy/facade implementations, test bypasses, or isolation violations, you MUST report `INTEGRITY VIOLATION`. Your verdict is a non-negotiable binary veto that stops the project immediately.

## Forensic Integrity Verification Scope (Phases 1-5)
1. **Phase 1: Static AST & Code Inspection**:
   - Inspect all changes in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`.
   - Check for hardcoded test results, expected outputs, fake solvers, bypass flags, or dummy stubs.
   - Verify genuine implementation logic across `cmd/`, `passes/`, `internal/`, and `rust/`.
2. **Phase 2: Execution Validation**:
   - Verify that test assertions in `tests/e2e/`, `tests/gates/`, and `tests/stress/` genuinely execute analyzers, solvers, and compiler gates.
   - Verify that tests do not contain tautological assertions (e.g. `assert.True(true)`), swallowed errors, or mock bypasses.
3. **Phase 3: Stress Ledger & Diagnostic Parity**:
   - Audit the changes to `tests/stress/testdata/...` and `vetparity_test.go` in the worktree.
   - Verify that promoted `HIT` markers correspond to genuine detection improvements and not artificial ledger suppression.
4. **Phase 4: Commit Diff & Git History Inspection**:
   - Inspect `git diff run/integ-1111..HEAD` or `git status`/`git log` in `/Users/bharath/Code/devtools/gocap-worktree`.
   - Ensure every modification serves hardening, gate enforcement, or legitimate test validation without extraneous code.
5. **Phase 5: Workspace Isolation**:
   - Inspect the main repository `/Users/bharath/Code/devtools/gocap`.
   - Verify that NO source code files, tests, build scripts, or git commits were modified in `/Users/bharath/Code/devtools/gocap`. (Only `.agents/teamwork/` metadata allowed).
6. **Report Delivery**:
   - In `/Users/bharath/Code/devtools/gocap/.agents/teamwork/auditor_m4_1/handoff.md`, document all findings, evidence, verification commands run, and deliver a definitive verdict: `CLEAN` or `INTEGRITY VIOLATION`.
   - Message parent with your verdict and link to handoff.md.


## 2026-11-11T20:46:47Z
You are the Forensic Integrity Auditor for Milestone 4 (E2E Acceptance Verification & Final Forensic Audit) of GoSree (gocap).

Working Directory: /Users/bharath/Code/devtools/gocap-worktree (Branch: harden/gosree-gates)
Main Checkout: /Users/bharath/Code/devtools/gocap
State Folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/auditor_m4_1/
Parent Conversation ID: 33d7e329-0a05-4f09-b12d-9123259f9fff

MANDATORY FIRST STEP:
Read /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md before starting work.
Also read /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md, /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m4_acceptance/SCOPE.md, and the worker handoff at /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_1/handoff.md.

MANDATORY AUDIT RULES & HARD BINARY VETO:
If you detect any cheat, hardcoded test expectation in production code, dummy/facade implementations, test bypasses, or workspace isolation violations, you MUST report INTEGRITY VIOLATION. Your verdict is an absolute binary veto.

Tasks (Phases 1-5):
1. Initialize your state files in /Users/bharath/Code/devtools/gocap/.agents/teamwork/auditor_m4_1/ (BRIEFING.md, progress.md).
2. Phase 1: Static AST & Code Inspection:
   - Inspect all changes in `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates`.
   - Check for hardcoded test results, expected outputs, fake solvers, bypass flags, or dummy stubs.
   - Verify genuine implementation logic across `cmd/`, `passes/`, `internal/`, and `rust/`.
3. Phase 2: Execution Validation:
   - Verify that test assertions in `tests/e2e/`, `tests/gates/`, and `tests/stress/` genuinely execute analyzers, solvers, and compiler gates.
   - Verify that tests do not contain tautological assertions (e.g. `assert.True(true)`), swallowed errors, or mock bypasses.
4. Phase 3: Stress Ledger & Diagnostic Parity:
   - Audit the changes to `tests/stress/testdata/...` and `vetparity_test.go` in the worktree.
   - Verify that promoted `HIT` markers correspond to genuine detection improvements and not artificial ledger suppression.
5. Phase 4: Commit Diff & Git History Inspection:
   - Inspect git diff and status in `/Users/bharath/Code/devtools/gocap-worktree`.
   - Ensure every modification serves hardening, gate enforcement, or legitimate test validation without extraneous code.
6. Phase 5: Workspace Isolation:
   - Inspect the main repository `/Users/bharath/Code/devtools/gocap`.
   - Verify that NO source code files, tests, build scripts, or git commits were modified in `/Users/bharath/Code/devtools/gocap` (only `.agents/teamwork/` metadata allowed).
7. In your state folder, write a complete forensic report in /Users/bharath/Code/devtools/gocap/.agents/teamwork/auditor_m4_1/handoff.md with a definitive verdict: CLEAN or INTEGRITY VIOLATION.
8. Send a message to your parent (Recipient: 33d7e329-0a05-4f09-b12d-9123259f9fff) with your verdict and a link to handoff.md.
