# Dispatch Task: Worker M4-3 (Remediate Stdlib Symlink Mixed-File Evasion)

## Objective
Remediate the critical gate evasion vulnerability discovered by Challenger 3 in `cmd/gosree-gate/main.go`, add automated regression tests verifying that mixed-file symlink evasion is halted before object code emission, and verify 100% clean test execution across all suites.

## Inputs
- Scope Document: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/suborch_m4_acceptance/SCOPE.md`
- Challenger 3 Handoff Report: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m4_3/handoff.md`
- Target Worktree: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)
- State Folder: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_3/`
- Target Source Files:
  - `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/main.go`
  - `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/gate_test.go`
  - `/Users/bharath/Code/devtools/gocap-worktree/tests/gates/adversarial_challenger_test.go` (or `tests/gates/gate_build_test.go`)

## Vulnerability Details
In `cmd/gosree-gate/main.go`:
```go
// 1. If any source file resides directly under GOROOT, it is standard library.
for _, f := range goFiles {
    if fileOriginatesInDir(f, cleanRoot) {
        return true
    }
}
```
And `fileOriginatesInDir(filePath, dir string)` evaluates symlinks with `filepath.EvalSymlinks`.
When an attacker creates a package outside GOROOT with malicious/unsafe code (`bad.go`) and includes a symlink to any stdlib file (`doc.go -> $GOROOT/src/cmd/cover/doc.go`) and passes `-gcflags="-std"`, `gosree-gate` sees that one file resolves to GOROOT, assumes the whole package is stdlib, skips analysis, and produces an executable binary containing data races or unsafe pointer dereferences with exit code 0.

## Required Implementation
1. In `cmd/gosree-gate/main.go`:
   - Pure Go standard library detection must require that **ALL** source files in `goFiles` reside directly within `cleanRoot`:
     ```go
     allInGoroot := len(goFiles) > 0
     for _, f := range goFiles {
         if !fileOriginatesInDir(f, cleanRoot) {
             allInGoroot = false
             break
         }
     }
     if allInGoroot {
         return true
     }
     ```
   - In `fileOriginatesInDir(filePath, dir string)`:
     Ensure that the literal path `abs` (prior to symlink evaluation) must also be under `cleanDir`. If `abs` is outside `cleanDir` (e.g. in `/tmp/...` or user workspace), it is a user symlink pointing into GOROOT and does NOT originate in `GOROOT`!
2. In `cmd/gosree-gate/gate_test.go` and/or `tests/gates/adversarial_challenger_test.go`:
   - Add unit and end-to-end regression tests verifying that a package containing a symlink to a stdlib file combined with `-gcflags="-std"` and safety violations (`SAFE001`, `SAFE003`) is strictly intercepted and halted with non-zero exit code (1 or 3), and no binary or archive is emitted.
3. Verification in `/Users/bharath/Code/devtools/gocap-worktree`:
   - `go test -v -count=1 ./cmd/gosree-gate/...`
   - `go test -v -count=1 ./tests/gates/...`
   - `bash tests/run_acceptance_tests.sh --json`
   - `go test -count=1 ./cmd/... ./passes/... ./internal/... ./tests/stress/...`
   - `cd rust && cargo test --workspace`
   - Verify Cgo stdlib: `go build -toolexec="gosree-gate" -a runtime/cgo` exits 0 cleanly.
4. Document all changes and test outputs in `/Users/bharath/Code/devtools/gocap/.agents/teamwork/worker_m4_3/handoff.md`.


## 2026-11-11T22:00:59Z
Remediate the critical gate evasion vulnerability discovered by Challenger 3 in cmd/gosree-gate/main.go, add automated regression tests verifying that mixed-file symlink evasion is halted before object code emission, and verify 100% clean test execution across all suites.
