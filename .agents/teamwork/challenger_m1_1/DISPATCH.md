# Dispatch for challenger_m1_1

## Task Assignment
**Role**: teamwork_challenger
**Assigned Milestone**: Milestone 1 (Worktree Setup & Adversarial Breaking Test Reproduction)
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m1_1/`
**Target Worktree**: `/Users/bharath/Code/devtools/gocap-worktree`
**Main Checkout (READ-ONLY)**: `/Users/bharath/Code/devtools/gocap`

## Mandatory Documents to Read First
1. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md`
2. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md`
3. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/SCOPE.md`
4. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/handoff.md`

## Challenge Objective
Empirically challenge and confirm reproduction of breaking failures:
1. Empirically verify in `/Users/bharath/Code/devtools/gocap-worktree` that adversarial tests and runtime witnesses genuinely fail on the unhardened codebase.
   - Run sample witnesses: `rs_torn_slice` (panic), `rs_torn_iface` (panic), `rs_w_mapwrites` (fatal crash), `sp_w_error` (race detector conviction).
   - Run sample breaking tests: `TestFuncFactDecodeRejectsCorruptLengths` (slice bounds panic), `TestHandlerWriteInSyntheticWrapperHasPosition` (unpositioned diagnostic at `-`), `TestReportWithoutPositionFallsBack` (dropped diagnostic).
2. Confirm whether failures are genuine and deterministic.
3. Write your findings and verdict (`APPROVE` or `REQUEST_CHANGES`) to `/Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m1_1/handoff.md`.


## 2026-11-11T16:29:25Z
From: 498c572c-0282-4cf2-9e29-2645358b4cdc
Priority: MESSAGE_PRIORITY_HIGH
Content:
You are challenger_m1_1 for Milestone 1.

MANDATORY FIRST STEP:
Read these documents:
1. /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
2. /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
3. /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/SCOPE.md
4. /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/handoff.md
5. /Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m1_1/DISPATCH.md

Your state folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m1_1/
Working directory: /Users/bharath/Code/devtools/gocap
Target worktree: /Users/bharath/Code/devtools/gocap-worktree

CHALLENGE TASKS:
1. Empirically verify in `/Users/bharath/Code/devtools/gocap-worktree` that adversarial tests and runtime witnesses genuinely fail on the unhardened codebase.
   - Run sample witnesses: `rs_torn_slice` (panic), `rs_torn_iface` (panic), `rs_w_mapwrites` (fatal crash), `sp_w_error` (race detector conviction).
   - Run sample breaking tests: `TestFuncFactDecodeRejectsCorruptLengths` (slice bounds panic), `TestHandlerWriteInSyntheticWrapperHasPosition` (unpositioned diagnostic at `-`), `TestReportWithoutPositionFallsBack` (dropped diagnostic).
2. Confirm whether failures are genuine and deterministic.
3. Write your report to `/Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m1_1/handoff.md` with explicit verdict `APPROVE` or `REQUEST_CHANGES`. Send completion message when done.
