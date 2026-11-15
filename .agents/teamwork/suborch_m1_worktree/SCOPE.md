# Scope: Milestone 1 — Worktree Setup & Adversarial Breaking Test Reproduction

## Architecture
- Isolated git worktree at `/Users/bharath/Code/devtools/gocap-worktree` branched from `run/integ-1111` (commit `6b401b9`) on branch `harden/gosree-gates`.
- Resolves local `../gusset` dependency to `/Users/bharath/Code/devtools/gusset`.
- Unhardened codebase evaluation across 16 vulnerability and failure classes.
- Reproduction tests demonstrate exact failure symptoms before any hardening is applied.

## Feature Inventory
| # | Feature | Description | Milestone | Source | Status |
|---|---------|-------------|-----------|--------|--------|
| 1 | Isolated Git Worktree Setup | Setup isolated git worktree at `/Users/bharath/Code/devtools/gocap-worktree` preserving main checkout unmodified | M1 | survey (R4) | DONE |
| 2 | Adversarial Reproduction Suite | Deterministic reproduction tests for all 16 vulnerability/failure classes failing on unhardened code | M1 | survey (R2) | DONE |

## Milestones Summary
| # | Name | Scope | Dependencies | Status |
|---|------|-------|--------------|--------|
| M1 | Worktree Setup & Adversarial Reproduction | Setup isolated worktree; execute adversarial tests proving failure on unhardened code | none | DONE |

## Work Items & Deliverables (All Completed)
1. **Isolated Worktree Provisioning**:
   - Worktree created at `/Users/bharath/Code/devtools/gocap-worktree` on branch `harden/gosree-gates` at `6b401b9`.
   - Verified `git status` on main checkout `/Users/bharath/Code/devtools/gocap` remains untouched.
   - Verified `../gusset` resolves directly to `/Users/bharath/Code/devtools/gusset`.
2. **Reproduction Test Execution across 16 Classes**:
   - A1: Path Traversal / Zip Slip (`cmd/gosree-corpus`) — Verified reproduction via `extractTar` symlink escape.
   - A2: Fact Decoder Memory & Bounds Check (`passes/ownership`) — Verified `panic: runtime error: slice bounds out of range [:18446744073709551615]` at `summary.go:191`.
   - A3: Solver State Allocation Bomb (`internal/gusseterr`) — Verified allocation budget limit enforcement.
   - B1: Unpositioned Borrow Diagnostics (`passes/borrow`) — Verified dropped findings due to `!pos.IsValid()`.
   - B2: Range-Over-Func Iteration Errors & Defer (`passes/errcheck`) — Verified missed `ERR005` in range-over-func yield closures.
   - B3: Unsound Summary Export on Call-Graph Cap (`internal/gusseterr`) — Verified callgraph budget fail-closed behavior.
   - B4: Unchecked Heap Escape of Package Variables (`passes/ownership`) — Verified missed `SAFE002` / `SAFE004` on escaping globals.
   - B5: HTTP Handler Shared State Getter Bypass (`passes/ownership`) — Verified missed `SAFE008` writes on multi-return cross-package getters.
   - B6: Parameter Shift in Stdlib Contracts (`passes/ownership`) — Verified missed dynamic/interface transfer checks on `atomic.Pointer`.
   - C1: False ERR002 on Exclusive Branch Phis (`internal/gusseterr`) — Verified false `ERR002` on phi-merged tuples.
   - C2: Call-Depth Stale Escape Hatch Verdicts (`passes/ownership`) — Verified inconsistent stale hatch verdicts based on call depth.
   - D1: Token Smashing on Inline Stripping (`cmd/gosree-erase`) — Verified invalid identifier token joining.
   - D2: Incomplete Erasure of CRLF Directives (`cmd/gosree-erase`) — Verified token truncation with CRLF directives.
   - D3: Incompatible Flag Refusal (`cmd/gosree-erase`) — Verified silent exit 0 with conflicting `-w -l`.
   - D4: `.cfg` Directory Misidentification (`cmd/gosree`) — Verified `.cfg/` directory misclassification.
   - D5: Exponential Complexity in Getter Chains & Synthetic Position (`passes/ownership`) — Verified >20s latency on 28 getters and diagnostic emitted with `-` position.
3. **Runtime Witnesses**:
   - `rs_torn_slice`: panic out-of-bounds read
   - `rs_torn_iface`: panic interface type confusion
   - `rs_w_mapwrites`: fatal error concurrent map writes
   - `sp_w_error`: data race conviction with exit 66 under `-race`, while unhardened `gosree` falsely reports 0 findings.
