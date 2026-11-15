# Dispatch Log: suborch_m3_gates

## 2026-11-11T19:12:00Z
You are Sub-Orchestrator for Milestone 3: Build & Compilation Enforcement Gates (Requirement R3) in GoSree (gocap).

Working directory: /Users/bharath/Code/devtools/gocap
Target Isolated Worktree: /Users/bharath/Code/devtools/gocap-worktree (Branch: harden/gosree-gates)
Your State Folder: /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m3_gates/

## Mandatory Reading:
You MUST read these documents before starting any work:
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/ORIGINAL_REQUEST.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/PROJECT.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/orchestrator_1/TEST_READY.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m2_hardening/handoff.md
- /Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m1_worktree/handoff.md

## Scope & Objectives (Milestone 3):
Implement and verify the build and compilation enforcement mechanisms (Requirement R3) in `/Users/bharath/Code/devtools/gocap-worktree` such that code with unresolved errors or vulnerabilities halts compilation or build execution.

1. **Preflight Exit Code Gates in `cmd/gosree`**:
   - 0: Clean (no errors, no findings).
   - 1: Analysis or compilation failure.
   - 2: Refused/invalid CLI flags or bad arguments.
   - 3: Vulnerabilities or safety findings detected (e.g. `SAFE001`–`SAFE008`, `BORROW001`–`BORROW004`, `ERR001`–`ERR005`).
   - Verify that all existing and adversarial packages adhere strictly to this contract.

2. **Compiler Interceptor Gate (`gosree-gate`)**:
   - Implement/verify `gosree-gate` (in `cmd/gosree-gate` or integrated into `cmd/gosree`) for invocation via:
     - `go build -toolexec="gosree-gate" ...`
     - `go vet -vettool="gosree" ...`
   - Intercept compilation steps (`compile` tool invocations) before object file generation:
     - Runs GoSree analysis on the package files being compiled.
     - If findings/errors exist: prints findings to stderr and exits with non-zero status (e.g., status 3), halting `go build` immediately before any `.a` or `.o` object is emitted.
     - If clean: transparently execs the underlying compiler tool (`os.Args[1:]`) and exits with its return code.

3. **Empirical Gate Tests**:
   - Write comprehensive unit and integration tests (e.g., in `cmd/gosree` or `tests/e2e` / `tests/gates`):
     - Test clean build: a clean Go package compiles successfully under `-toolexec="gosree-gate"`.
     - Test vulnerable build: a package containing any safety violation or error (e.g., mutable pointer sharing in goroutine `sp_w_error`, atomic pointer load race `rs_torn_slice`, unchecked error `ERR001`) fails compilation under `go build -toolexec="gosree-gate"` with exit code != 0 and halts object generation.
     - Test vettool mode: `go vet -vettool` correctly reports findings.
     - Test preflight flag checks: invalid flag combinations exit 2.

4. **Integrity & Isolation Rules**:
   - All modifications, builds, and tests must take place strictly inside `/Users/bharath/Code/devtools/gocap-worktree`.
   - The main repo `/Users/bharath/Code/devtools/gocap` MUST remain completely unmodified.
   - DO NOT CHEAT: zero facade logic, zero mocks, zero hardcoded test strings.
   - Binary veto: Forensic Auditor (`teamwork_auditor`) CLEAN verdict required.

## Workflow:
- Initialize `BRIEFING.md`, `SCOPE.md`, `GATE_STATUS.md`, `progress.md`.
- Dispatch Explorers (`teamwork_explorer`) to inspect existing gate implementation and compiler toolchain integration points.
- Dispatch Worker (`teamwork_worker`) to implement the enforcement gate and tests in `/Users/bharath/Code/devtools/gocap-worktree`. Include mandatory integrity warning.
- Dispatch Verification Wave:
  - 2 Reviewers (`teamwork_reviewer`) independently.
  - 2 Challengers (`teamwork_challenger`) independently to stress-test compilation halting and clean building.
  - 1 Forensic Auditor (`teamwork_auditor`) for binary veto integrity review.
- Synthesize `GATE_STATUS.md` and write `handoff.md`.
- Send message back to parent orchestrator (`8388fa01-83cf-4def-9fc2-7a950c9b25bd`).
