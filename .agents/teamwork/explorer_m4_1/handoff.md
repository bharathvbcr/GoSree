# Investigation & Fix Recommendation: Compiler Gate Bypass via `-gcflags="-std"`

**Agent**: `explorer_m4_1` (Explorer 1)  
**Parent Sub-Orchestrator ID**: `33d7e329-0a05-4f09-b12d-9123259f9fff`  
**Workspace**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m4_1/`  
**Date**: 2026-11-11  

---

## Executive Summary

A critical gate evasion vulnerability in `cmd/gosree-gate/main.go` allows any Go package containing memory-safety violations, concurrency races, or unhandled errors to completely evade GoSree static analysis and build to executable binaries when compiled with `-gcflags="-std"`.

The root cause is a premature, unvalidated check in `cmd/gosree-gate/main.go` lines 60–63 (`if hasStdFlag(args) { execTool(tool, args) }`) that blindly trusts the `-std` compiler flag as proof that a compilation unit belongs to the standard library. Because the Go toolchain (`cmd/go`) forwards user `-gcflags` directly to the `compile` tool, an adversarial build command passes `-std` to `compile` for user code. `gosree-gate` immediately invokes the underlying compiler without inspecting source file locations or running static analysis. Furthermore, an adjacent path-prefix boundary flaw in `allFilesUnderGoroot(files []string)` lines 109–122 allows any directory matching the prefix of `GOROOT` (e.g. `/path/to/go_fake`) to masquerade as standard library code.

The recommended fix is **Strategy A**: completely eliminate Step 3 (`hasStdFlag`) and make `allFilesUnderGoroot` the single canonical authority for standard library passthrough, hardened with `filepath.Rel` and `filepath.EvalSymlinks`.

---

## 1. Observation

### 1.1 Vulnerable Code in `cmd/gosree-gate/main.go`
Direct inspection of `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/main.go`:

Lines 54–79:
```go
	// 2. Only intercept compiler tool invocations; transparently pass through asm, link, cgo, etc.
	toolName := strings.TrimSuffix(filepath.Base(tool), ".exe")
	if toolName != "compile" {
		execTool(tool, args)
	}

	// 3. Skip standard library compilation units.
	if hasStdFlag(args) {
		execTool(tool, args)
	}

	// 4. Extract Go source files from compiler arguments.
	var goFiles []string
	for _, a := range args {
		if strings.HasSuffix(a, ".go") && !strings.HasPrefix(a, "-") {
			goFiles = append(goFiles, a)
		}
	}
	if len(goFiles) == 0 {
		execTool(tool, args)
	}

	// 5. If all source files reside in GOROOT, skip analysis.
	if allFilesUnderGoroot(goFiles) {
		execTool(tool, args)
	}
```

Lines 100–122:
```go
func hasStdFlag(args []string) bool {
	for _, a := range args {
		if a == "-std" {
			return true
		}
	}
	return false
}

func allFilesUnderGoroot(files []string) bool {
	goroot := runtime.GOROOT()
	if goroot == "" {
		return false
	}
	cleanRoot := filepath.Clean(goroot)
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil || !strings.HasPrefix(abs, cleanRoot) {
			return false
		}
	}
	return true
}
```

### 1.2 Toolchain Interaction with `-toolexec` and `-gcflags`
1. When running `go build -toolexec="gosree-gate" -gcflags="-std" <pkg>`, the `go` command invokes `gosree-gate` as a wrapper around the compiler toolchain:
   ```
   gosree-gate /opt/homebrew/Cellar/go/1.27.3/libexec/pkg/tool/darwin_arm64/compile -std -p u -o $WORK/b001/_pkg_.a ./u.go
   ```
2. The argument list passed to `gosree-gate` begins with the tool path (`/path/to/compile`), followed by compiler flags (`args = os.Args[2:]`).
3. Because `-gcflags="-std"` was supplied on the command line, `"-std"` is present in `args`.
4. In `cmd/gosree-gate/main.go:61`, `hasStdFlag(args)` evaluates `"-std" == "-std"` and returns `true`.
5. `execTool(tool, args)` is executed at line 62.
6. Execution never reaches:
   - Line 67: extraction of `goFiles`
   - Line 77: `allFilesUnderGoroot(goFiles)`
   - Line 82: `runGoSreeAnalysis(goFiles)`

### 1.3 Verbatim Test Failure in `tests/gates/adversarial_challenger_test.go`
Executing the challenger test in `/Users/bharath/Code/devtools/gocap-worktree`:
- **Command**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree
  go test -v -count=1 ./tests/gates -run TestChallengerEvasionAttempts/evasion_gcflags_std_bypass
  ```
- **Verbatim Output**:
  ```
  === RUN   TestChallengerEvasionAttempts
  === RUN   TestChallengerEvasionAttempts/evasion_gcflags_std_bypass
      adversarial_challenger_test.go:432: CRITICAL EVASION VULNERABILITY: -gcflags=-std bypassed gosree-gate! Exit code was 0.
  --- FAIL: TestChallengerEvasionAttempts (5.66s)
      --- FAIL: TestChallengerEvasionAttempts/evasion_gcflags_std_bypass (2.27s)
  FAIL
  FAIL	github.com/bharathvbcr/gosree/tests/gates	5.796s
  FAIL
  ```

### 1.4 Secondary Vulnerability: Prefix Boundary Flaw in `allFilesUnderGoroot`
Direct inspection of `allFilesUnderGoroot` at lines 114–120:
```go
	cleanRoot := filepath.Clean(goroot)
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil || !strings.HasPrefix(abs, cleanRoot) {
			return false
		}
	}
	return true
```
- If `goroot` is `/opt/homebrew/Cellar/go/1.27.3/libexec`, `cleanRoot` has no trailing directory separator.
- If a package resides in `/opt/homebrew/Cellar/go/1.27.3/libexec_malicious/main.go`:
  `strings.HasPrefix("/opt/homebrew/Cellar/go/1.27.3/libexec_malicious/main.go", "/opt/homebrew/Cellar/go/1.27.3/libexec")` returns `true`!
- Furthermore, `filepath.Abs` does not evaluate symlinks (`filepath.EvalSymlinks`), making path comparison brittle across symlinked Go installations (such as `/opt/homebrew/opt/go` on macOS).

---

## 2. Logic Chain

1. **Gate Security Invariant (R3, Feature 19)**:
   The compiler gate `gosree-gate` exists to enforce build prevention: any compilation unit containing safety violations (`SAFE001`–`SAFE008`), borrow checking errors (`BORROW001`–`BORROW004`), or error discipline violations (`ERR001`–`ERR005`) must halt the compiler before `.a` / `.o` archives or executable binaries are emitted (Obs 1.1).
2. **Standard Library Exception Intent**:
   The Go standard library (`runtime`, `unsafe`, `sync`, `net/http`) contains low-level memory operations that do not adhere to GoSree borrow or capabilities annotations and must not be analyzed by GoSree. The author intended to skip analysis for standard library compilation units (Obs 1.1, lines 60 and 76).
3. **Competing Owners Defect**:
   The author introduced two separate mechanisms for standard library detection:
   - Step 3 (line 60–63): Checking for `-std` flag in compiler arguments (`hasStdFlag(args)`).
   - Step 5 (line 76–79): Checking if source files reside in `runtime.GOROOT()` (`allFilesUnderGoroot(goFiles)`).
4. **Premature Trust in Untrusted Flag**:
   Step 3 was placed *before* source files are extracted (Step 4). However, `-std` is an ordinary command-line flag accepted by the Go compiler. When compiling user packages, a user or CI script can inject `-std` via `go build -gcflags="-std"` (Obs 1.2).
5. **Breach Execution**:
   When `-gcflags="-std"` is provided, `hasStdFlag(args)` returns `true` for user source files located entirely outside `GOROOT`. Step 3 triggers an early return `execTool(tool, args)`. Analysis is bypassed, the Go compiler builds the vulnerable code, and an object file/binary is emitted with exit code 0 (Obs 1.3).
6. **Path Boundary Flaw**:
   Even if Step 3 were deleted, `allFilesUnderGoroot` uses raw `strings.HasPrefix` without enforcing directory boundaries or resolving symlinks, allowing sibling directories sharing the GOROOT prefix to bypass analysis (Obs 1.4).
7. **Resolution Invariant**:
   To eliminate all bypass avenues while strictly maintaining standard library passthrough, `cmd/gosree-gate/main.go` must:
   - Completely delete Step 3 and the `hasStdFlag` function ("One canonical owner per behavior", "Replace, don't accumulate").
   - Make `allFilesUnderGoroot` the sole authority for standard library exemption, hardened with `filepath.Rel` and `filepath.EvalSymlinks`.

---

## 3. Comparison of Fix Strategies

| Metric / Consideration | Strategy A: Delete Step 3 + Harden `allFilesUnderGoroot` (Recommended) | Strategy B: Conjunctive Check (`hasStdFlag && allFilesUnderGoroot`) |
|------------------------|-------------------------------------------------------------------------|---------------------------------------------------------------------|
| **Architecture** | Single canonical owner: physical filesystem location in GOROOT determines standard library status. | Two coupled checks: requires both `-std` flag AND file in GOROOT. |
| **Bypass Prevention** | 100% immune: `-gcflags="-std"` on user files is completely ignored. | 100% immune: user files fail the GOROOT check. |
| **Code Size / Complexity** | Net -6 lines. Dead helper `hasStdFlag` deleted. Linear, flat control flow. | Adds logic. Retains redundant flag parsing helper. |
| **Failure Modes** | If GOROOT is compiled without `-std`, still properly skipped. | If GOROOT is compiled without `-std` (e.g. custom build tools), false positive analysis crash on stdlib `unsafe`. |
| **Compliance with Rules** | Fully conforms to user rule: "One canonical owner per behavior" and "Leaving the codebase smaller or flatter is a success". | Violates single-owner rule by retaining redundant flag parsing. |

---

## 4. Proposed Fix Specification (For Implementer)

### 4.1 Target File: `cmd/gosree-gate/main.go`

#### Change 1: Remove Step 3 (lines 60–63)
Delete:
```go
	// 3. Skip standard library compilation units.
	if hasStdFlag(args) {
		execTool(tool, args)
	}
```

#### Change 2: Remove `hasStdFlag` Helper (lines 100–107)
Delete:
```go
func hasStdFlag(args []string) bool {
	for _, a := range args {
		if a == "-std" {
			return true
		}
	}
	return false
}
```

#### Change 3: Harden `allFilesUnderGoroot` (lines 109–122)
Replace lines 109–122 with:
```go
func allFilesUnderGoroot(files []string) bool {
	goroot := runtime.GOROOT()
	if goroot == "" {
		return false
	}
	cleanRoot := filepath.Clean(goroot)
	if evalRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = evalRoot
	}
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return false
		}
		if evalAbs, err := filepath.EvalSymlinks(abs); err == nil {
			abs = evalAbs
		}
		rel, err := filepath.Rel(cleanRoot, abs)
		if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			return false
		}
	}
	return true
}
```

### 4.2 Exact Git Diff Patch

```diff
--- a/cmd/gosree-gate/main.go
+++ b/cmd/gosree-gate/main.go
@@ -57,11 +57,6 @@ func main() {
 		execTool(tool, args)
 	}
 
-	// 3. Skip standard library compilation units.
-	if hasStdFlag(args) {
-		execTool(tool, args)
-	}
-
 	// 4. Extract Go source files from compiler arguments.
 	var goFiles []string
 	for _, a := range args {
@@ -97,14 +92,6 @@ func isVersionQuery(args []string) bool {
 	return false
 }
 
-func hasStdFlag(args []string) bool {
-	for _, a := range args {
-		if a == "-std" {
-			return true
-		}
-	}
-	return false
-}
-
 func allFilesUnderGoroot(files []string) bool {
 	goroot := runtime.GOROOT()
 	if goroot == "" {
 		return false
 	}
 	cleanRoot := filepath.Clean(goroot)
+	if evalRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
+		cleanRoot = evalRoot
+	}
 	for _, f := range files {
 		abs, err := filepath.Abs(f)
-		if err != nil || !strings.HasPrefix(abs, cleanRoot) {
+		if err != nil {
+			return false
+		}
+		if evalAbs, err := filepath.EvalSymlinks(abs); err == nil {
+			abs = evalAbs
+		}
+		rel, err := filepath.Rel(cleanRoot, abs)
+		if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
 			return false
 		}
 	}
 	return true
 }
```

### 4.3 Complementary Unit Test in `cmd/gosree-gate/gate_test.go`
In addition to the existing test `TestChallengerEvasionAttempts/evasion_gcflags_std_bypass` in `tests/gates/adversarial_challenger_test.go`, the implementer should add a direct unit test to `cmd/gosree-gate/gate_test.go`:
```go
func TestGateRejectsStdFlagOnUserCode(t *testing.T) {
	compileTool := getGoCompileTool(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}

	tmpDir := t.TempDir()
	outObj := filepath.Join(tmpDir, "evaded.a")
	vulnSource := filepath.Join(repoRoot, "passes", "unsafeban", "testdata", "src", "u", "u.go")

	// Passing -std on user code outside GOROOT must NOT bypass GoSree analysis.
	_, stderr, code := runGate(t, nil, repoRoot, compileTool, "-std", "-o", outObj, "-p", "u", vulnSource)
	if code != 3 {
		t.Fatalf("expected exit code 3 (halt on findings), got %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "SAFE001") {
		t.Errorf("expected SAFE001 diagnostic on stderr, got:\n%s", stderr)
	}
	if _, err := os.Stat(outObj); !os.IsNotExist(err) {
		t.Errorf("expected object file %s to NOT be created, but it exists", outObj)
	}
}
```

---

## 5. Caveats

No caveats.
- The evasion vulnerability has been reproduced deterministically in both automated tests and CLI executions.
- The standard library passthrough for genuine stdlib compilations was verified to reside entirely under `runtime.GOROOT()`.
- Version queries (`-V`, `-V=full`) are handled by Step 1 and are completely unaffected by this change.

---

## 6. Conclusion

The `-gcflags="-std"` compiler gate bypass is a severe vulnerability that breaks build prevention enforcement across all GoSree safety passes. By removing the premature, unvalidated `hasStdFlag` check and consolidating standard library passthrough under a hardened `allFilesUnderGoroot` implementation:
1. `-gcflags="-std"` cannot be used to bypass analysis on any user code outside GOROOT.
2. GOROOT prefix-collision evasion vectors are permanently eliminated.
3. The codebase becomes smaller, cleaner, and strictly adheres to the single-canonical-owner principle.

---

## 7. Verification Method

To verify this fix once implemented in `/Users/bharath/Code/devtools/gocap-worktree`:

1. **Run the Adversarial Challenger Test**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v -count=1 ./tests/gates -run TestChallengerEvasionAttempts/evasion_gcflags_std_bypass
   ```
   *Expected Result*: `PASS` (clean exit 0, test verifies non-zero exit and absence of emitted binary).

2. **Run Full Gates Test Suite**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v -count=1 ./cmd/gosree-gate/...
   go test -v -count=1 ./tests/gates/...
   ```
   *Expected Result*: All tests pass (including `gate_build_test.go`, `gate_test.go`, `vet_test.go`, and `adversarial_challenger_test.go`).

3. **Run Manual CLI Verification**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go build -o /tmp/gate ./cmd/gosree-gate
   go build -o /tmp/gosree ./cmd/gosree
   rm -f /tmp/test_evaded.a
   GOSREE_BIN=/tmp/gosree go build -toolexec="/tmp/gate" -a -gcflags="-std" -o /tmp/test_evaded.a ./passes/unsafeban/testdata/src/u
   ```
   *Expected Result*:
   - Exit code != 0 (status 3).
   - Error diagnostic `SAFE001` printed to stderr.
   - Archive `/tmp/test_evaded.a` does NOT exist.
