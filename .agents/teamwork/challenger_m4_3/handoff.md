# Challenger M4-3 Report: Milestone 4 Iteration 2 Gate Hardening Empirical Challenge

**Agent**: `challenger_m4_3` (Teamwork Challenger / Critic & Specialist)  
**Parent Sub-Orchestrator ID**: `33d7e329-0a05-4f09-b12d-9123259f9fff`  
**Workspace**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/challenger_m4_3/`  
**Date**: 2026-11-11  
**Verdict**: **`CHALLENGE_FAILED`** (CRITICAL EVASION VULNERABILITY CONFIRMED)

---

## 1. Observation

### 1.1 Baseline Evasion Command Re-tests (Unmodified Packages)
Both previously failing evasion commands were executed in `/Users/bharath/Code/devtools/gocap-worktree` with freshly built binaries:

#### 1.1.1 `passes/unsafeban/testdata/src/u` (`SAFE001`)
Command:
```bash
PATH="/tmp/challenger_test_bin:$PATH" go build -toolexec="gosree-gate" -a -gcflags="-std" -o /tmp/evaded_u.a ./passes/unsafeban/testdata/src/u
```
Result:
- Process terminated with exit code `1` (compiler gate exit `3`).
- 18 `SAFE001` diagnostics emitted on stderr.
- Verbatim filesystem verification: `ls: /tmp/evaded_u.a: No such file or directory`.
- Compilation strictly halted; no archive emitted.

#### 1.1.2 `tests/stress/testdata/src/sp_w_error` (`SAFE003`)
Command:
```bash
PATH="/tmp/challenger_test_bin:$PATH" go build -toolexec="gosree-gate" -a -gcflags="-std" -o /tmp/evaded_sp ./tests/stress/testdata/src/sp_w_error
```
Result:
- Process terminated with exit code `1` (compiler gate exit `3`).
- `SAFE003: composite literal is used after it was passed to a goroutine (line 17)` emitted on stderr.
- Verbatim filesystem verification: `ls: /tmp/evaded_sp: No such file or directory`.
- Compilation strictly halted; no binary emitted.

### 1.2 Test Suite Verification
- `go test -v -count=1 ./tests/gates/...`: All 11 test suites passed in 113.9s (including `TestChallengerBuildGateStrictHalt`, `TestChallengerEvasionAttempts`, and `TestGoBuildGateEvasionResistance`).
- `go test -v -count=1 ./cmd/gosree-gate/...`: All 8 unit tests passed in 6.6s.
- Real stdlib cgo pass-through: `go build -toolexec="gosree-gate" -a runtime/cgo` exited with status `0` cleanly.

---

### 1.3 Critical Vulnerability Discovery: Stdlib Symlink Evasion
Despite passing baseline tests, `cmd/gosree-gate/main.go` contains an insecure provenance condition at lines 148–154:
```go
	// 1. If any source file resides directly under GOROOT, it is standard library.
	// (Handles 371/375 stdlib packages in standard go toolchain).
	for _, f := range goFiles {
		if fileOriginatesInDir(f, cleanRoot) {
			return true
		}
	}
```
And `fileOriginatesInDir` at lines 121–131 evaluates symlinks with `filepath.EvalSymlinks`:
```go
	evalDir, errDir := filepath.EvalSymlinks(cleanDir)
	evalAbs, errAbs := filepath.EvalSymlinks(abs)
	if errDir == nil && isUnderDir(abs, evalDir) {
		return true
	}
	if errAbs == nil && isUnderDir(evalAbs, cleanDir) {
		return true
	}
	if errDir == nil && errAbs == nil && isUnderDir(evalAbs, evalDir) {
		return true
	}
```

Because `isStandardLibrary` checks if **ANY** source file originates under `GOROOT`, and `fileOriginatesInDir` resolves symlinks to their destination inside `GOROOT`, an adversary compiling arbitrary malicious user code outside `GOROOT` can bypass `gosree-gate` completely by including a single symlink to any standard library file sharing the package name.

#### 1.3.1 Empirical Reproduction: Exploiting `SAFE001` Unsafe Pointer
1. Created `/tmp/challenger_attack_symlink` containing:
   - `bad.go` (`package cmp`) containing `_ = unsafe.Pointer(&x)` (`SAFE001`)
   - `cmp.go` -> symlinked to `$GOROOT/src/cmp/cmp.go`
2. Executed:
   ```bash
   cd /tmp/challenger_attack_symlink
   rm -f /tmp/evaded_cmp.a
   PATH="/tmp/challenger_test_bin:$PATH" go build -toolexec="gosree-gate" -a -gcflags="-std" -o /tmp/evaded_cmp.a .
   ```
3. Verbatim Output:
   ```
   EXIT_CODE=0
   -rw-r--r--@ 1 bharath  staff  3660 Nov 11 15:47 /tmp/evaded_cmp.a
   ```
4. Verbatim Symbol Extraction:
   ```bash
   go tool nm /tmp/evaded_cmp.a | grep -i Exploit
   # Output: 3ad T attack.Exploit
   ```
   The archive was emitted on disk and contains the compiled unsafe user code.

#### 1.3.2 Empirical Reproduction: Exploiting `SAFE003` Executable Binary & Runtime Data Race Witness
1. Created `/tmp/challenger_attack_main_bin` containing:
   - `main.go` (`package main`, `func main()`) from `tests/stress/testdata/src/sp_w_error`
   - `doc.go` -> symlinked to `$GOROOT/src/cmd/cover/doc.go` (a stdlib file with `package main` and no `func main()`)
2. Executed build with race detector:
   ```bash
   cd /tmp/challenger_attack_main_bin
   PATH="/tmp/challenger_test_bin:$PATH" go build -race -toolexec="gosree-gate" -a -gcflags="-std" -o /tmp/evaded_main_race .
   ```
3. Verbatim Output:
   ```
   EXIT_CODE=0
   -rwxr-xr-x@ 1 bharath  staff  ... /tmp/evaded_main_race
   ```
4. Executed `/tmp/evaded_main_race`:
   ```
   ==================
   WARNING: DATA RACE
   Write at 0x00c000114008 by goroutine 5:
     main.main.func1()
         /tmp/challenger_attack_main_bin/main.go:18 +0x60
     main.main.gowrap1()
         /tmp/challenger_attack_main_bin/main.go:20 +0x48

   Previous write at 0x00c000114008 by main goroutine:
     main.main()
         /tmp/challenger_attack_main_bin/main.go:21 +0x194

   Goroutine 5 (running) created at:
     main.main()
         /tmp/challenger_attack_main_bin/main.go:17 +0x18c
   ==================
   Found 1 data race(s)
   ```
   Exit code: `66`.
   A runnable executable binary exhibiting a known concurrency data race was successfully compiled and linked directly through `gosree-gate`.

---

## 2. Logic Chain

1. **Gate Security Invariant**: The build gate interceptor `gosree-gate` must guarantee that NO binary or archive is emitted whenever user compilation units contain safety violations (`SAFE001`–`SAFE008`) or concurrency witnesses, regardless of flags or project configuration.
2. **Defective Premise**: In `cmd/gosree-gate/main.go:148-154`, the interceptor assumed that if *any* file passed to the compiler resides in `GOROOT`, the entire compilation unit belongs to the standard library.
3. **Flawed Implementation**:
   - `for _, f := range goFiles { if fileOriginatesInDir(f, cleanRoot) { return true } }` returns `true` on the first matching file, ignoring whether other source files reside outside `GOROOT`.
   - `fileOriginatesInDir` invokes `filepath.EvalSymlinks`, resolving symlinks placed in arbitrary user workspaces into `GOROOT`.
4. **Attack Exploitation**:
   - An attacker creates a user package containing malicious code (`bad.go`) alongside a symlink to any stdlib file (`cmp/cmp.go`, `cmd/cover/doc.go`).
   - The attacker invokes `go build -toolexec="gosree-gate" -a -gcflags="-std"`.
   - `gosree-gate` evaluates the symlink, resolves it to GOROOT, concludes the entire package is standard library, and bypasses analysis.
5. **Empirical Conviction**:
   - An archive containing `SAFE001` (`/tmp/evaded_cmp.a`) and a runnable executable binary containing `SAFE003` (`/tmp/evaded_main_race`) were emitted on disk with exit code `0`.
   - Running the emitted binary produced `WARNING: DATA RACE` (`Found 1 data race(s)`).
6. **Conclusion**: The gate fails closed against adversarial evasion using symlinks and mixed file provenance.

---

## 3. Caveats

No caveats.
The evasion has been demonstrated end-to-end with multiple packages:
- Library package with `SAFE001` unsafe memory bypass.
- Standalone executable binary with `SAFE003` runtime data race witness.
Main workspace `/Users/bharath/Code/devtools/gocap` remains strictly untouched.

---

## 4. Conclusion & Verdict

**Verdict**: **`CHALLENGE_FAILED`**

The `gosree-gate` compiler gate hardening does not fail closed against mixed-file provenance or symlinks. Any user package containing safety violations or race witnesses can bypass gate interception and emit compiled binaries simply by including a symlink to a Go standard library file and specifying `-gcflags="-std"`.

### Recommended Actionable Remediation:
1. In `cmd/gosree-gate/main.go`, pure Go standard library detection must require that **ALL** source files in `goFiles` reside directly within `GOROOT`, not just *any* file:
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
2. In `fileOriginatesInDir(filePath, dir string)`:
   Reject symlinks originating outside `cleanRoot`:
   The literal path `abs` (prior to symlink resolution) must reside within `cleanDir`. If `abs` is `/tmp/...` or in the user's workspace, it is a user file and does not originate in `GOROOT`.
3. Add regression tests for stdlib symlink evasion in `cmd/gosree-gate/gate_test.go` and `tests/gates/adversarial_challenger_test.go`.

---

## 5. Verification Method

To independently verify the vulnerability in `/Users/bharath/Code/devtools/gocap-worktree`:

### 5.1 Build the Gate Interceptor
```bash
cd /Users/bharath/Code/devtools/gocap-worktree
mkdir -p /tmp/challenger_test_bin
go build -o /tmp/challenger_test_bin/gosree ./cmd/gosree
go build -o /tmp/challenger_test_bin/gosree-gate ./cmd/gosree-gate
```

### 5.2 Reproduce Executable Binary Evasion (`SAFE003` Data Race)
```bash
goroot=$(go env GOROOT)
tmpdir="/tmp/reproduce_symlink_evasion"
rm -rf "$tmpdir"
mkdir -p "$tmpdir"

cat <<'EOF' > "$tmpdir/go.mod"
module mainbinary
go 1.22
EOF

# Copy concurrency race witness main.go
cp /Users/bharath/Code/devtools/gocap-worktree/tests/stress/testdata/src/sp_w_error/main.go "$tmpdir/main.go"

# Symlink a standard library package main file
ln -s "$goroot/src/cmd/cover/doc.go" "$tmpdir/doc.go"

# Build through gosree-gate
cd "$tmpdir"
PATH="/tmp/challenger_test_bin:$PATH" go build -race -toolexec="gosree-gate" -a -gcflags="-std" -o "$tmpdir/evaded_bin" .

# Observe exit code 0 and emitted binary
ls -la "$tmpdir/evaded_bin"

# Run the emitted binary to observe data race
"$tmpdir/evaded_bin"
```

### 5.3 Invalidation Conditions
- Compilation of `$tmpdir` halts with exit code 1 / 3, diagnostics are printed to stderr, and `$tmpdir/evaded_bin` is not emitted.
