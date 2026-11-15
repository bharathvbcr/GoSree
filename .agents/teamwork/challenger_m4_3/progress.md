# Progress — Challenger M4-3 (Milestone 4 Iteration 2)

Last visited: 2026-11-11T21:51:00Z
Status: CHALLENGE_FAILED

## Steps Completed
- [x] Read DISPATCH.md, ORIGINAL_REQUEST.md, PROJECT.md, SCOPE.md, and worker_m4_2/handoff.md.
- [x] Initialized BRIEFING.md and progress.md.
- [x] Re-ran exact baseline evasion commands:
  - `go build -toolexec="gosree-gate" -a -gcflags="-std" ./passes/unsafeban/testdata/src/u`: Halted with exit code 1, SAFE001 printed, no archive emitted.
  - `go build -toolexec="gosree-gate" -a -gcflags="-std" ./tests/stress/testdata/src/sp_w_error`: Halted with exit code 1, SAFE003 printed, no binary emitted.
- [x] Ran `go test -v -count=1 ./tests/gates/...`: All 11 test suites passed (113.9s), including `TestGoBuildGateEvasionResistance` and `TestChallengerEvasionAttempts`.
- [x] Ran `go test -v -count=1 ./cmd/gosree-gate/...`: All 8 unit tests passed (6.6s).
- [x] Ran stdlib cgo pass-through check: `go build -toolexec="gosree-gate" -a runtime/cgo`: clean exit 0.
- [x] Designed and empirically executed novel adversarial attack vectors:
  - **Vector A**: Symlink to stdlib library file (`cmp/cmp.go`) + user file with `SAFE001` (`bad.go`) + `-gcflags="-std"`:
    - **Result**: `gosree-gate` completely bypassed! Exit code 0, `/tmp/evaded_cmp.a` (3,660 bytes) emitted containing `attack.Exploit`.
  - **Vector B**: Symlink to stdlib library file (`cmp/cmp.go`) + concurrency data race witness (`sp_w_error`) + `-gcflags="-std"`:
    - **Result**: `gosree-gate` completely bypassed! Exit code 0, `/tmp/evaded_witness.a` (17,530 bytes) emitted containing `witness.ExploitWitness`.
  - **Vector C**: Symlink to stdlib executable doc file (`cmd/cover/doc.go`) + concurrency data race witness (`sp_w_error` with `func main()`) + `-gcflags="-std"`:
    - **Result**: `gosree-gate` completely bypassed! Exit code 0, runnable executable `/tmp/evaded_main_bin` (1,832,146 bytes) emitted on disk! Executing under `-race` triggered `WARNING: DATA RACE`!
- [x] Identified Root Cause:
  - In `cmd/gosree-gate/main.go` lines 148-154, `isStandardLibrary` returns `true` if **ANY** source file resides directly under GOROOT.
  - Furthermore, `fileOriginatesInDir` uses `filepath.EvalSymlinks`, resolving user symlinks outside GOROOT to their targets in GOROOT.
  - Consequently, any user package with safety violations that symlinks a stdlib file and passes `-gcflags="-std"` is treated as standard library and bypasses gate interception entirely.
- [ ] Next: Update `BRIEFING.md` and write `handoff.md` with verdict CHALLENGE_FAILED, then notify parent.
