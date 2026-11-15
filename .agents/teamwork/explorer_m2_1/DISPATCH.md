## 2026-11-11T16:52:53Z
You are Explorer 1 (teamwork_explorer) for Milestone 2: Codebase Hardening & Vulnerability Remediation in GoSree (gocap).

Working Directory: /Users/bharath/Code/devtools/gocap
Target Isolated Worktree: /Users/bharath/Code/devtools/gocap-worktree (Branch: harden/gosree-gates)
Your State Folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_1/

## Mandatory Reading:
You MUST read this document before doing any work:
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m2_hardening/SCOPE.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/handoff.md

## Scope & Assigned Tasks:
Investigate in the target worktree `/Users/bharath/Code/devtools/gocap-worktree`:
1. **A2 (Fact Decoder Slice Bounds Panic)**:
   - Target file: `passes/ownership/summary.go`
   - Test: `TestFuncFactDecodeRejectsCorruptLengths` in `passes/ownership/`
   - Investigate why `sLen > uint64(len(rest)-nr)` check is missing or failing, and why casting `sLen` to signed `int` wraps negatively causing a slice bounds panic out-of-range (`panic: runtime error: slice bounds out of range [:18446744073709551615] with capacity 12`).
   - Detail the exact lines, type conversions, and boundary checks needed to safely reject corrupt lengths and prevent allocation bombs.
2. **B4 (Unchecked Heap Escape of Package Globals)**:
   - Target file: `passes/ownership/globals.go`
   - Test: `TestInitOnlyGlobalEscapeShapesFailClosed` in `passes/ownership/`
   - Investigate why escaping package globals fail to emit `SAFE002` / `SAFE004` when their addresses escape or undergo non-init mutations.
   - Detail the exact AST/SSA escape shapes and AST checks needed to ensure escaping package globals fail closed.
3. **Closure Variable Scope Matching**:
   - Target file: `passes/ownership/check.go`
   - Test: `TestShadowedClosureNameIsNotABorrowCapture` in `passes/ownership/`
   - Investigate why string-based variable name matching causes false `CAP003` diagnostics when closure variable names are shadowed.
   - Detail how to match variables by `*types.Var` instead of identifier strings.

## Constraints:
- Read-only investigation. DO NOT write or modify code in the repository.
- Inspect the codebase inside `/Users/bharath/Code/devtools/gocap-worktree`.
- Output: Write `analysis.md` and `handoff.md` in `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m2_1/`.
- Send message back to parent when complete.
