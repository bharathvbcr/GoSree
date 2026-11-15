# Toolchain Interoperability and Blast Radius Investigation: `gosree-gate` `-std` Flag vs GOROOT Provenance

**Agent**: `explorer_m4_3` (Explorer 3)  
**Parent Sub-Orchestrator ID**: `33d7e329-0a05-4f09-b12d-9123259f9fff`  
**Workspace**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m4_3/`  
**Date**: 2026-11-11  

---

## 1. Observation

### 1.1 Gate Evasion Vulnerability via `-gcflags="-std"`
- **File**: `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/main.go`, lines 60–63 and 100–107:
  ```go
  // 3. Skip standard library compilation units.
  if hasStdFlag(args) {
      execTool(tool, args)
  }
  ...
  func hasStdFlag(args []string) bool {
      for _, a := range args {
          if a == "-std" {
              return true
          }
      }
      return false
  }
  ```
- **Reproduction Command (Rule Violation `SAFE001`)**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree
  rm -f /tmp/evaded.a
  go build -toolexec="$(pwd)/gosree-gate" -a -gcflags="-std" -o /tmp/evaded.a ./passes/unsafeban/testdata/src/u
  ```
  - **Result**: Exit code `0`; output archive `/tmp/evaded.a` (size: 162,578 bytes) emitted.
- **Reproduction Command (Concurrency Witness `SAFE003`)**:
  ```bash
  cd /Users/bharath/Code/devtools/gocap-worktree
  rm -f /tmp/evaded_sp.a
  go build -toolexec="$(pwd)/gosree-gate" -a -gcflags="-std" -o /tmp/evaded_sp.a ./tests/stress/testdata/src/sp_w_error
  ```
  - **Result**: Exit code `0`; output executable `/tmp/evaded_sp.a` (size: 1,832,098 bytes) emitted and executed without halting.
- **Automated Adversarial Test Failure**:
  - File: `tests/gates/adversarial_challenger_test.go:422-437`
  - Output:
    ```
    === RUN   TestChallengerEvasionAttempts/evasion_gcflags_std_bypass
        adversarial_challenger_test.go:432: CRITICAL EVASION VULNERABILITY: -gcflags=-std bypassed gosree-gate! Exit code was 0.
    --- FAIL: TestChallengerEvasionAttempts (30.97s)
        --- FAIL: TestChallengerEvasionAttempts/evasion_gcflags_std_bypass (10.03s)
    ```

### 1.2 Toolchain Standard Library Compilation Mechanics
- **Go Toolchain Source Inspection** (`$GOROOT/src/cmd/go`):
  - In `/opt/homebrew/Cellar/go/1.27.3/libexec/src/cmd/go/internal/load/pkg.go:407`:
    ```go
    p.Standard = p.Goroot && p.ImportPath != "" && search.IsStandardImportPath(p.ImportPath)
    ```
  - In `/opt/homebrew/Cellar/go/1.27.3/libexec/src/cmd/go/internal/work/gc.go:81-83`:
    ```go
    if p.Standard {
        defaultGcFlags = append(defaultGcFlags, "-std")
    }
    ```
  - In `/opt/homebrew/Cellar/go/1.27.3/libexec/src/cmd/go/internal/work/gc.go:125`:
    ```go
    gcflags := str.StringList(forcedGcflags, p.Internal.Gcflags)
    ```
- **Standard Library Invocations**:
  - Executed dry-run recompilation of standard library: `go build -n -a std`.
  - Exactly **375** `compile` tool invocations were emitted.
  - Exactly **375 out of 375 (100%)** standard library `compile` invocations contain `-std`.
  - Zero standard library `compile` invocations omit `-std`.
  - When a user supplies `-gcflags="-std"`, `cmd/go` appends `-std` to the `compile` invocation of user packages.

### 1.3 `allFilesUnderGoroot` Invalidation on CGO Packages
- **File**: `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree-gate/main.go`, lines 109–122:
  ```go
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
- **Standard Library Package Classification Breakdown across all 375 compilation units**:
  - **371 packages** (pure Go packages, e.g. `fmt`, `os`, `sync`, `runtime`):
    - 100% of `.go` files passed to `compile` reside in `/opt/homebrew/.../libexec/src/...`.
    - `allFilesUnderGoroot(goFiles)` evaluates to `true`.
  - **4 packages** (all standard library packages utilizing CGO: `runtime/cgo`, `plugin`, `internal/runtime/cgobench`, `net/internal/cgotest`):
    - Cgo processes source files and generates temporary intermediate files in `$WORK/bXXX/` (e.g. `_cgo_gotypes.go`, `*.cgo1.go`, `_cgo_import.go`).
    - In `runtime/cgo`: 6 files under GOROOT (`callbacks.go`, `handle.go`, etc.) + 3 files in `$WORK` (`_cgo_gotypes.go`, `cgo.cgo1.go`, `_cgo_import.go`).
      - Result: `allFilesUnderGoroot` evaluates to **`false`**.
    - In `plugin`: 1 file under GOROOT (`plugin.go`) + 3 files in `$WORK` (`_cgo_gotypes.go`, `plugin_dlopen.cgo1.go`, `_cgo_import.go`).
      - Result: `allFilesUnderGoroot` evaluates to **`false`**.
    - In `internal/runtime/cgobench`: **0 files under GOROOT** (all 3 files passed to `compile` are `$WORK/b316/_cgo_gotypes.go`, `funcs.cgo1.go`, `_cgo_import.go`).
      - Result: `allFilesUnderGoroot` evaluates to **`false`**.
    - In `net/internal/cgotest`: **0 files under GOROOT** (all 3 files passed to `compile` are `$WORK/b346/_cgo_gotypes.go`, `resstate.cgo1.go`, `_cgo_import.go`).
      - Result: `allFilesUnderGoroot` evaluates to **`false`**.

### 1.4 Blast Radius of Naive `hasStdFlag` Removal or Constraining
- **Test Command**: Executed `gosree` static analysis directly against `runtime/cgo`:
  ```bash
  go build -o /tmp/gosree ./cmd/gosree
  /tmp/gosree runtime/cgo
  ```
- **Output**:
  - Exit code: `3` (`exitFindings`).
  - Total findings emitted: **56 diagnostics**, including:
    - `callbacks.go:40:1: SAFE001: //go:linkname reaches unexported symbols and bypasses type safety`
    - `callbacks.go:41:1: SAFE001: function _runtime_cgo_panic_internal has no Go body: it is implemented in assembly or linked in, which bypasses Go's memory safety`
    - `callbacks.go:124:23: SAFE001: unsafe.Pointer bypasses Go's memory safety; wrap it in //gosree:unsafe <reason>`
    - `handle.go:123:15: SAFE002: parameter v is published via (*sync.Map).Store but is not owned here: the caller still holds it`
- **Resulting Impact**:
  If `hasStdFlag` is removed or naively paired as `if hasStdFlag(args) && allFilesUnderGoroot(goFiles)`:
  - `runtime/cgo` fails `allFilesUnderGoroot`.
  - `gosree-gate` executes `runGoSreeAnalysis` on `runtime/cgo`.
  - GoSree detects 56 violations and halts with exit 3.
  - **Fatal Build Regression**: Every `go build -a` or build of any Go application importing `net` (with cgo) or using `import "C"` will fail to build under `gosree-gate`.

### 1.5 Cgo Origin Traceability via `//line` Directives
- **Direct File Inspection of `*.cgo1.go` Files**:
  Inspected 96 cgo-generated `.cgo1.go` files in active toolchain caches.
  - In standard library packages (`runtime/cgo/cgo.cgo1.go`):
    Line 3: `//line /opt/homebrew/Cellar/go/1.27.3/libexec/src/runtime/cgo/cgo.go:1:1`
    Points directly into `$GOROOT`.
  - In user packages (`cgo_test_main.cgo1.go`):
    Line 3: `//line /tmp/cgo_test_main.go:1:1` (or user workspace `/Users/bharath/...`).
    Points into user directory, NOT `$GOROOT`.

---

## 2. Logic Chain

1. **Gate Invariant**:
   `gosree-gate` must halt compilation of user source packages containing safety violations or concurrency witnesses before object files (`.a` / `.o` / executables) are emitted. Simultaneously, it must transparently pass toolchain version queries, non-compiler tools, and genuine standard library packages.

2. **Root Cause of Evasion (Observation 1.1)**:
   In `cmd/gosree-gate/main.go`, Step 3 skips static analysis solely based on `hasStdFlag(args)`. Because `cmd/go` appends user-provided `-gcflags` to compiler invocations, any user package compiled with `-gcflags="-std"` receives `-std`. Step 3 returns `true` and bypasses GoSree analysis, allowing memory corruption, data races, and unhandled errors to compile without restriction.

3. **Inadequacy of `allFilesUnderGoroot` (Observation 1.3)**:
   The reviewer and challenger suggested removing `hasStdFlag` or checking `hasStdFlag(args) && allFilesUnderGoroot(goFiles)`.
   However, observation 1.3 demonstrates that for standard library packages that use Cgo (`runtime/cgo`, `plugin`, `internal/runtime/cgobench`, `net/internal/cgotest`), the source files passed to `compile` include Cgo-generated files located in temporary build directories (`$WORK/bXXX/`).
   Therefore, `allFilesUnderGoroot(goFiles)` returns `false` for these packages.

4. **Blast Radius Analysis (Observation 1.4)**:
   If `allFilesUnderGoroot` alone is used, or if `hasStdFlag` is gated on `allFilesUnderGoroot`, standard library packages like `runtime/cgo` will be classified as user code.
   Because standard library packages inherently use `//go:linkname`, `unsafe.Pointer`, and assembly functions, GoSree flags 56 safety violations on `runtime/cgo` and halts with exit code 3.
   This would cause catastrophic regressions on all Cgo-dependent applications.

5. **Toolchain Invariant Reconciliation (Observations 1.2 & 1.5)**:
   - Genuine standard library packages **always** have `-std` in compiler arguments (375/375 verified in Observation 1.2). Thus, `hasStdFlag(args)` is a mandatory **necessary condition** (`!hasStdFlag` immediately implies user code).
   - However, `-std` is not a **sufficient condition** because of `-gcflags="-std"` injection.
   - Genuine standard library packages can be definitively proven by verifying provenance:
     a. At least one source file in `goFiles` resides under `GOROOT` (`strings.HasPrefix(abs, cleanRoot)`), which holds for 373 out of 375 standard packages.
     b. For Cgo packages whose files are entirely within temporary work directories (e.g. `internal/runtime/cgobench`), the package path `-p <pkg>` is a standard import path (`search.IsStandardImportPath`) whose source directory exists on disk at `$GOROOT/src/<pkg>`, AND the `//line` directive embedded in `*.cgo1.go` points to an original file under `GOROOT` (Observation 1.5).
   - User packages with `-gcflags="-std"` fail both checks: zero files reside in `GOROOT`, their package path does not exist under `$GOROOT/src`, and any Cgo `//line` directives point to user workspace paths.

---

## 3. Caveats

- **No Caveats**.
- All 375 standard library compile invocations from `go build -n -a std` were individually inspected and categorized.
- The evasion exploit (`-gcflags="-std"`) and the Cgo failure modes (`runtime/cgo`) were empirically reproduced with verbatim command outputs and logs.
- Toolchain queries (`compile -V=full`) and non-compile tools (`asm`, `link`) remain 100% unaffected as they are intercepted earlier at Steps 1 and 2 of `gosree-gate`.

---

## 4. Conclusion

1. **`hasStdFlag` must NOT be removed**: Removing `hasStdFlag` or requiring `allFilesUnderGoroot(goFiles)` breaks compilation of `runtime/cgo` and other standard Cgo packages, causing 56 false GoSree findings to abort the build.
2. **`hasStdFlag` must be CONSTRAINED by GOROOT provenance**: A compilation unit should only be recognized as standard library if `hasStdFlag(args)` is true **AND** provenance verification confirms the package originates from `GOROOT`.
3. **Recommended Implementation for `cmd/gosree-gate/main.go`**:
   Replace Step 3 (`if hasStdFlag(args)`) and Step 5 (`if allFilesUnderGoroot(goFiles)`) with a single, robust standard library check:

   ```go
   // Extract Go source files first.
   var goFiles []string
   for _, a := range args {
       if strings.HasSuffix(a, ".go") && !strings.HasPrefix(a, "-") {
           goFiles = append(goFiles, a)
       }
   }
   if len(goFiles) == 0 {
       execTool(tool, args)
   }

   // Skip genuine standard library compilation units.
   if isStandardLibrary(args, goFiles) {
       execTool(tool, args)
   }
   ```

   Where `isStandardLibrary` is defined as:
   ```go
   func isStandardLibrary(args []string, goFiles []string) bool {
       if !hasStdFlag(args) {
           return false
       }
       goroot := runtime.GOROOT()
       if goroot == "" {
           return false
       }
       cleanRoot := filepath.Clean(goroot)

       // 1. If any source file resides directly under GOROOT, it is standard library.
       for _, f := range goFiles {
           abs, err := filepath.Abs(f)
           if err == nil && strings.HasPrefix(abs, cleanRoot) {
               return true
           }
       }

       // 2. For cgo-only standard library packages (internal/runtime/cgobench, net/internal/cgotest)
       // where all files passed to compile are in temporary directories ($WORK), verify package path
       // exists under GOROOT/src and inspect //line directives in *.cgo1.go files.
       pkgPath := getPkgPath(args)
       if pkgPath != "" && isStandardImportPath(pkgPath) {
           stdDir := filepath.Join(cleanRoot, "src", filepath.FromSlash(pkgPath))
           if fi, err := os.Stat(stdDir); err == nil && fi.IsDir() {
               for _, f := range goFiles {
                   if strings.HasSuffix(f, ".cgo1.go") {
                       return cgoFileOriginatesFromGoroot(f, cleanRoot)
                   }
               }
           }
       }

       return false
   }

   func getPkgPath(args []string) string {
       for i := 0; i < len(args); i++ {
           if args[i] == "-p" && i+1 < len(args) {
               return args[i+1]
           }
           if strings.HasPrefix(args[i], "-p=") {
               return strings.TrimPrefix(args[i], "-p=")
           }
       }
       return ""
   }

   func isStandardImportPath(path string) bool {
       i := strings.Index(path, "/")
       if i < 0 {
           i = len(path)
       }
       elem := path[:i]
       return !strings.Contains(elem, ".")
   }

   func cgoFileOriginatesFromGoroot(file, cleanRoot string) bool {
       data, err := os.ReadFile(file)
       if err != nil {
           return false
       }
       const prefix = "//line "
       idx := strings.Index(string(data), prefix)
       if idx == -1 {
           return false
       }
       line := string(data[idx+len(prefix):])
       if end := strings.IndexByte(line, '\n'); end != -1 {
           line = line[:end]
       }
       if colon := strings.IndexByte(line, ':'); colon != -1 {
           line = line[:colon]
       }
       abs, err := filepath.Abs(strings.TrimSpace(line))
       if err != nil {
           return false
       }
       return strings.HasPrefix(abs, cleanRoot)
   }
   ```

---

## 5. Verification Method

1. **Verify Evasion Resistance**:
   Run the adversarial test in `tests/gates`:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v -count=1 ./tests/gates -run "TestChallengerEvasionAttempts/evasion_gcflags_std_bypass"
   ```
   *Expected outcome*: Test passes; non-zero exit code on `-gcflags="-std"`; no binary emitted.

2. **Verify Clean Standard Library Cgo Builds**:
   Recompile `runtime/cgo` under `gosree-gate`:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go build -o /tmp/gate ./cmd/gosree-gate
   go build -toolexec="/tmp/gate" -a runtime/cgo
   ```
   *Expected outcome*: Exit code 0; clean delegation to compiler; no false GoSree findings.

3. **Verify Full Gate Regression Suite**:
   ```bash
   cd /Users/bharath/Code/devtools/gocap-worktree
   go test -v -count=1 ./cmd/gosree-gate/...
   go test -v -count=1 ./tests/gates/...
   ```
   *Expected outcome*: 100% test pass across all gate tests.
