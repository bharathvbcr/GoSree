# Dispatch for worker_m1_1

## Task Assignment
**Role**: teamwork_worker
**Assigned Milestone**: Milestone 1 (Worktree Setup & Adversarial Breaking Test Reproduction)
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/`
**Target Worktree**: `/Users/bharath/Code/devtools/gocap-worktree`
**Main Checkout (READ-ONLY, MUST NOT MODIFY)**: `/Users/bharath/Code/devtools/gocap`

## Mandatory Documents to Read First
1. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md`
2. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md`
3. `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/SCOPE.md`
4. Survey reports:
   - `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_survey_1/handoff.md`
   - `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_survey_2/handoff.md`
   - `/Users/bharath/Code/devtools/gocap/.agents/teamwork/specminer_survey_3/handoff.md`

## Instructions
1. Worktree Isolation:
   - Check `git worktree list`.
   - Create isolated worktree at `/Users/bharath/Code/devtools/gocap-worktree` branching from `run/integ-1111` (or current HEAD if appropriate):
     `git worktree add -b harden/gosree-gates /Users/bharath/Code/devtools/gocap-worktree run/integ-1111`
   - Verify `../gusset` resolves from `/Users/bharath/Code/devtools/gocap-worktree` to `/Users/bharath/Code/devtools/gusset`.
   - Verify `/Users/bharath/Code/devtools/gocap` remains completely unmodified.
2. Adversarial Breaking Test Execution:
   - Run tests demonstrating failures across the 16 vulnerability and failure classes on unhardened code in the worktree.
   - Capture exact reproduction outputs showing failures (e.g. `TestHandlerWriteInSyntheticWrapperHasPosition` unpositioned diagnostic `-`, `TestHandlerWriteGetterChainTerminates` exponential explosion > 20s, `rs_torn_slice` slice bounds panic, `sp_w_error` pointer laundering bypass, `cmd/gosree-corpus` path traversal, `passes/ownership` fact decoder corrupt length panic).
3. Produce handoff report at `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/handoff.md` detailing:
   - Worktree setup verification commands and status
   - Reproduction test commands, exit codes, and exact error outputs for each of the 16 vulnerability classes
   - Affirmation of worktree isolation and zero modification of main checkout.


## 2026-11-11T16:04:31Z
You are worker_m1_1 (Worktree Setup and Breaking Test Runner) for Milestone 1.

MANDATORY INTEGRITY WARNING:
DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A teamwork_auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

MANDATORY FIRST STEP:
Read the following files before doing anything else:
1. /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
2. /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
3. /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/SCOPE.md
4. /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/DISPATCH.md
5. Prior survey reports:
   - /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_survey_1/handoff.md
   - /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_survey_2/handoff.md
   - /Users/bharath/Code/devtools/gocap/.agents/teamwork/specminer_survey_3/handoff.md

Your state folder is: /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/
Working directory: /Users/bharath/Code/devtools/gocap
Isolated worktree to create: /Users/bharath/Code/devtools/gocap-worktree

WRITE OWNERSHIP:
- You own the creation and execution inside /Users/bharath/Code/devtools/gocap-worktree.
- You own writing your state and report files in /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/.
- YOU MUST NOT modify any source files in the main checkout /Users/bharath/Code/devtools/gocap.

TASK REQUIREMENTS:
1. Isolated Git Worktree Setup (Requirement R4)
2. Adversarial Breaking Test Execution & Output Capture (Requirement R2)
3. Deliverables & Handoff
