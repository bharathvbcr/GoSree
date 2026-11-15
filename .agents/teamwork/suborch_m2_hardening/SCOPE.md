# Scope: Milestone 2 — Codebase Hardening & Vulnerability Remediation

## Architecture & Boundaries
- Target Workspace: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)
- Protected Workspace: `/Users/bharath/Code/devtools/gocap` (Main checkout, strictly read-only)
- Components in Scope:
  - `passes/ownership/`: `summary.go`, `globals.go`, `handlerwrite.go`, `contracts.go`, `check.go`
  - `passes/borrow/`: `check.go`
  - `passes/errcheck/`: `astscan.go`, `lower.go`
  - `internal/gusseterr/` & `rust/gosree-errsolve`: solver state budgeting, lattice joins, fail-closed caps
  - `cmd/gosree/`: `.cfg` regular file check
  - `cmd/gosree-erase/`: whitespace insertion, CRLF calculation, flag validation
  - `cmd/gosree-corpus/`: path traversal & symlink validation
  - Concurrency Safety Models: `internal/kinds/kinds.go`, `passes/ownership/contracts.go` (runtime witnesses)

## Feature Inventory & Target Files
| # | Class | Description | Target Files | In-Tree Status |
|---|-------|-------------|--------------|----------------|
| 1 | A1 | Path traversal / Zip Slip via symlinks | `cmd/gosree-corpus/refservice.go`, `annotations.go` | Hardened / Verify |
| 2 | A2 | Fact decoder corrupt length / allocation bomb | `passes/ownership/summary.go` | Active defect -> Patch |
| 3 | A3 | Solver state allocation bomb (DoS) | `internal/gusseterr`, `rust/gosree-errsolve` | Hardened / Verify |
| 4 | B1 | Silent drop of unpositioned borrow findings | `passes/borrow/check.go` | Active defect -> Patch |
| 5 | B2 | Range-over-func iteration errors & defer drops | `passes/errcheck/astscan.go`, `lower.go` | Hardened / Verify |
| 6 | B3 | Unsound summary export on call-graph cap | `internal/gusseterr` | Hardened / Verify |
| 7 | B4 | Unchecked heap escape of package globals | `passes/ownership/globals.go` | Active defect -> Patch |
| 8 | B5 | HTTP handler shared state getter bypass | `passes/ownership/handlerwrite.go` | Active defect -> Patch |
| 9 | B6 | Parameter shift in stdlib capability contracts | `passes/ownership/contracts.go` | Active defect -> Patch |
| 10 | C1 | False ERR002 on exclusive branch phis | `internal/gusseterr` | Hardened / Verify |
| 11 | C2 | Call-depth dependent stale escape hatch verdicts | `passes/ownership/contracts.go` | Active defect -> Patch |
| 12 | D1 | Token smashing on inline stripping | `cmd/gosree-erase/main.go` | Hardened / Verify |
| 13 | D2 | CRLF directive stripping truncation | `cmd/gosree-erase/main.go` | Hardened / Verify |
| 14 | D3 | Silent success on incompatible flags | `cmd/gosree-erase/main.go` | Hardened / Verify |
| 15 | D4 | `.cfg` directory misidentification | `cmd/gosree/main.go` | Hardened / Verify |
| 16 | D5 | Exponential complexity in getter chains & synthetic pos | `passes/ownership/handlerwrite.go` | Active defect -> Patch |
| 17 | Ref | Closure variable scope matching | `passes/ownership/check.go` | Active defect -> Patch |
| 18 | Wit | Concurrency safety models (runtime witnesses) | `internal/kinds/kinds.go`, `passes/ownership/contracts.go` | Active defect -> Patch |

## Milestones & Iteration Plan
| Step | Action | Description | Status |
|------|--------|-------------|--------|
| Step 1 | Survey & Technical Plan | Spawn 3 Explorers to inspect target files, analyze exact root causes, and propose surgical patches | IN_PROGRESS |
| Step 2 | Worker Implementation | Spawn Worker to apply surgical fixes strictly in `/Users/bharath/Code/devtools/gocap-worktree` | PLANNED |
| Step 3 | Independent Review | Spawn 2 Reviewers independently to audit code quality, correctness, and interface conformance | PLANNED |
| Step 4 | Empirical Challenger Verification | Spawn 2 Challengers to verify all unit tests pass, breaking tests now pass, and no regressions exist | PLANNED |
| Step 5 | Forensic Integrity Audit | Spawn Forensic Auditor (`teamwork_auditor`) to verify zero cheating, genuine implementations, and isolation | PLANNED |
| Step 6 | Gate Decision & Handoff | Synthesize verdicts into `GATE_STATUS.md`; produce `handoff.md` and report to parent | PLANNED |
