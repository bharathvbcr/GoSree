# Dispatch Log

## 2026-11-11T16:50:00Z
You are the Sub-Orchestrator for Milestone 2: Codebase Hardening & Vulnerability Remediation in GoSree (gocap).

Working Directory: /Users/bharath/Code/devtools/gocap
Target Isolated Worktree: /Users/bharath/Code/devtools/gocap-worktree (Branch: harden/gosree-gates)
Your State Folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m2_hardening/
Original Parent: Project Orchestrator
Original Parent Conversation ID: 8388fa01-83cf-4def-9fc2-7a950c9b25bd

## Mandatory Document Readings (MUST read before any work):
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/TEST_READY.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/handoff.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m1_1/handoff.md

## Objective & Scope: Milestone 2 (Codebase Hardening & Vulnerability Remediation)
You are responsible for fixing and hardening the gocap codebase against all 16 vulnerability and failure classes identified during the survey and empirically confirmed in Milestone 1, strictly inside `/Users/bharath/Code/devtools/gocap-worktree`.

### Vulnerability Remediation Scope (16 Classes):
1. **A1 (Zip Slip / Path Traversal)**: In `cmd/gosree-corpus/refservice.go` and `annotations.go`, validate symlink and tar header destinations so files cannot be extracted outside target directory.
2. **A2 (Fact Decoder Slice Bounds Panic)**: In `passes/ownership/summary.go`, validate `sLen > uint64(len(rest)-nr)` before casting `sLen` to signed `int` to eliminate negative wrap panic on corrupt lengths (`TestFuncFactDecodeRejectsCorruptLengths`).
3. **A3 (Solver State Allocation Bomb DoS)**: In `internal/gusseterr` and `rust/gosree-errsolve`, bound `blocks * slots <= 2^23`.
4. **B1 (Silent Drop of Unpositioned Borrow Findings)**: In `passes/borrow/check.go`, fall back to enclosing function or package file start position when `!pos.IsValid()` (`TestReportWithoutPositionFallsBack`).
5. **B2 (Range-Over-Func Iteration Errors & Defer Drops)**: In `passes/errcheck/astscan.go` and `lower.go`, model yield closures and ensure deferred cleanups in range-over-func bodies are not dropped (`ERR005`).
6. **B3 (Unsound Summary Export on Call-Graph Cap)**: In `internal/gusseterr`, ensure cap truncation fails closed.
7. **B4 (Unchecked Heap Escape of Package Globals)**: In `passes/ownership/globals.go`, emit `SAFE002`/`SAFE004` on package globals escaping to heap (`TestInitOnlyGlobalEscapeShapesFailClosed`).
8. **B5 (HTTP Handler Shared State Getter Bypass)**: In `passes/ownership/handlerwrite.go`, support cross-package and multi-return getter paths (`TestHandlerWriteThroughGetterOfAnotherPackage`).
9. **B6 (Parameter Shift in Stdlib Capability Contracts)**: In `passes/ownership/contracts.go`, fix dynamic invoke and bound method wrappers (`sync.Pool.Put`, `atomic.Pointer.Store`) without parameter index shifts (`TestStdlibCapabilityContractsDynamicAndInterface`).
10. **C1 (False ERR002 on Exclusive Branch Phis)**: In `internal/gusseterr`, correct lattice join logic for disjoint branch errors.
11. **C2 (Call-Depth Dependent Stale Escape Hatch Verdicts)**: In `passes/ownership/contracts.go`, ensure BFS search order is independent of call hop depth (`TestSharedLoadHatchVerdictIndependentOfCallDepth`).
12. **D1 (Token Smashing on Inline Stripping)**: In `cmd/gosree-erase/main.go`, preserve separating whitespace when stripping inline directives.
13. **D2 (CRLF Directive Stripping Truncation)**: In `cmd/gosree-erase/main.go`, handle CRLF comments without undercounting carriage returns (`5c139cdeef10152b`).
14. **D3 (Silent Success on Incompatible Flags)**: In `cmd/gosree-erase/main.go`, validate flag combinations (reject `-w` with `-l`, etc.).
15. **D4 (.cfg Directory Misidentification)**: In `cmd/gosree/main.go`, only treat `.cfg` as config file if it's a regular file, not a directory (`TestCfgDirAsPattern`).
16. **D5 (Exponential Complexity in Getter Chains & Synthetic Position Loss)**: In `passes/ownership/handlerwrite.go`:
    - Memoize visited call edges in getter chain traversal to bound search linearly (`TestHandlerWriteGetterChainTerminates` down from >30s to <1s).
    - Attribute synthetic wrapper call diagnostics to the calling AST source position rather than `-` (`TestHandlerWriteInSyntheticWrapperHasPosition`).
17. **Closure Variable Scope Matching**: In `passes/ownership/check.go`, match closure variable captures by `*types.Var` instead of identifier string names (`TestShadowedClosureNameIsNotABorrowCapture`) to fix false `CAP003` masking true safety errors.
18. **Runtime Witnesses & Concurrency Safety**: Ensure static analyzer flags the concurrency safety violations demonstrated in `tests/stress/testdata/src/` (`rs_torn_slice`, `rs_torn_iface`, `rs_w_mapwrites`, `sp_w_error`).
