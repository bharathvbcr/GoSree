# Investigation Report: CLI Architecture & Exit Code Gating in `cmd/gosree`

**Agent**: Explorer 1 (`teamwork_explorer`)  
**Milestone**: Milestone 3: Build & Compilation Enforcement Gates in GoSree  
**Worktree**: `/Users/bharath/Code/devtools/gocap-worktree` (Branch: `harden/gosree-gates`)  
**State Folder**: `/Users/bharath/Code/devtools/gocap/.agents/teamwork/explorer_m3_1/`  
**Date**: 2026-11-11T19:24:00Z  

---

## 1. Observation

### 1.1 Existing Architecture in `cmd/gosree`
The `cmd/gosree` package inside `/Users/bharath/Code/devtools/gocap-worktree/cmd/gosree` consists of:
- `main.go`: Entry point `main()`, `runJSON()`, `dedupJSON()`, `jsonExit()`, `unitcheckerMode()`, and `cfgDirAsPattern()`.
- `preflight.go`: Exit constants, flag parsing and refusal logic (`parseCommandLine`), and analyzer warning mappings (`covers`).
- `unmatched.go`: Pattern matching checks (`unmatchedWildcards`).
- Test suite: `main_test.go`, `preflight_test.go`, `unitchecker_mode_test.go`, `unmatched_test.go`, `adv_1111_test.go`, and `acceptance_script_test.go`.

In `cmd/gosree/main.go:42`:
```go
var analyzers = []*analysis.Analyzer{annot.Analyzer, unsafeban.Analyzer, ownership.Analyzer, borrow.Analyzer, errcheck.Analyzer}
```

In `cmd/gosree/preflight.go:12-17`:
```go
const (
	exitClean    = 0 // every package loaded, type-checked and analysed, and nothing was reported
	exitFailed   = 1 // a package could not be loaded or type-checked, or an analyzer failed: no verdict
	exitUsage    = 2 // a flag gosree does not accept, or one it refuses because it would change the verdict
	exitFindings = 3 // every package was analysed and at least one finding was reported
)
```

In `cmd/gosree/main.go:44-75`:
```go
func main() {
	if os.Getenv(childEnv) == "" {
		var names []string
		for _, a := range analyzers {
			names = append(names, a.Name)
		}
		p, err := parseCommandLine(os.Args[1:], names)
		if err != nil {
			fmt.Fprintln(os.Stderr, usage(err))
			os.Exit(exitUsage)
		}
		if !vetxOnly(p.args) {
			for _, w := range p.warnings {
				fmt.Fprintln(os.Stderr, "gosree: WARNING: "+w)
			}
		}
		os.Args = append(os.Args[:1:1], cfgDirAsPattern(p.args)...)
		if !unitcheckerMode(p.args) && p.goflags != "" {
			os.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+p.goflags))
		}
		if !unitcheckerMode(p.args) {
			for _, pat := range unmatchedWildcards(p.args, goListMatches) {
				fmt.Fprintf(os.Stderr, "gosree: WARNING: %s matched no packages, so nothing was checked for it\n", pat)
			}
		}
		if p.json && !unitcheckerMode(p.args) {
			os.Exit(runJSON(p.args))
		}
	}
	multichecker.Main(analyzers...)
}
```

### 1.2 Inspection of `multichecker.Main` & Underlying Drivers
From `golang.org/x/tools/go/analysis/multichecker` (`multichecker.go:24-60`):
1. Analyzers are registered and validated via `analysis.Validate(analyzers)` (exits 1 on error).
2. Flags are registered via `checker.RegisterFlags()` and parsed via `analysisflags.Parse(analyzers, true)`:
   - Analyzer flags are created (`-borrow`, `-safeownership`, etc.).
   - Standard flags are created (`-flags`, `-V`, `-json`, `-c`, `-fix`, `-diff`).
   - `flag.Parse()` is called under `flag.ExitOnError` mode:
     - On undefined or malformed flags: `flag.CommandLine` prints usage and exits `2`.
     - On `-flags`: outputs JSON flags and calls `os.Exit(0)`.
     - On `-V=full`: outputs version string and calls `os.Exit(0)`.
3. Positional argument checks (`multichecker.go:37-47`):
   ```go
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, `%[1]s is a tool for static analysis of Go programs...`, progname)
		os.Exit(1)
	}
   ```
   **Defect/Gap Observed**: When `len(args) == 0`, `multichecker.Main` exits with status `1`.
4. Help handling:
   ```go
	if args[0] == "help" {
		analysisflags.Help(progname, analyzers, args[1:])
		os.Exit(0)
	}
   ```
5. `unitchecker` delegation:
   ```go
	if len(args) == 1 && strings.HasSuffix(args[0], ".cfg") {
		unitchecker.Run(args[0], analyzers)
		panic("unreachable")
	}
   ```
6. Standalone driver execution:
   ```go
	os.Exit(checker.Run(args, analyzers))
   ```

### 1.3 Inspection of `checker.Run` & Exit Code Behavior
From `golang.org/x/tools/go/analysis/internal/checker/checker.go:234-265`:
```go
func printDiagnostics(graph *checker.Graph) (exitcode int) {
	if analysisflags.JSON {
		if err := graph.PrintJSON(os.Stdout); err != nil {
			return 1
		}
	} else {
		if err := graph.PrintText(os.Stderr, analysisflags.Context); err != nil {
			return 1
		}
		var numErrors, rootDiags int
		for act := range graph.All() {
			if act.Err != nil {
				numErrors++
			} else if act.IsRoot {
				rootDiags += len(act.Diagnostics)
			}
		}
		if numErrors > 0 {
			exitcode = 1 // analysis failed, at least partially
		} else if rootDiags > 0 {
			exitcode = 3 // successfully produced diagnostics
		}
	}
	return exitcode
}
```

In standalone text mode:
- Clean code: `checker.Run` returns `0` -> `os.Exit(0)`.
- Compilation / parse error / internal error: `checker.Run` returns `1` -> `os.Exit(1)`.
- Vulnerabilities / safety diagnostics: `checker.Run` returns `3` -> `os.Exit(3)`.

In `-json` mode:
- Standard `checker.Run` emits JSON and returns `0` even when findings exist.
- `cmd/gosree` solves this via `runJSON`: invokes child process with `GOSREE_JSON_CHILD=1`, intercepts the JSON stream, and calls `jsonExit(cleaned)`:
  - If `error` object present: returns `exitFailed` (`1`).
  - If finding list non-empty: returns `exitFindings` (`3`).
  - If empty: returns `exitClean` (`0`).

### 1.4 Empirical Command Probing
Empirical testing on `/Users/bharath/Code/devtools/gocap-worktree`:
1. **Clean package**:
   `gosree ./tests/e2e/testdata/src/t1_valid` -> exit code `0`. (Verified)
2. **Safety & Borrow Findings**:
   `gosree ./passes/borrow/testdata/src/borrows` -> exit code `3`. (Verified: emits BORROW001–004, SAFE006, SAFE007)
3. **Compilation / Syntax Failure**:
   `gosree <file_with_syntax_error>` -> exit code `1`. (Verified: syntax error, failed prerequisites)
4. **Invalid Flag**:
   `gosree -invalidflag` -> exit code `2`. (Verified: flag provided but not defined)
5. **Refused Flag**:
   `gosree -fix ./...` -> exit code `2`. (Verified: `gosree: -fix is not supported`)
6. **No Arguments (GAP)**:
   `gosree` (with no arguments) -> exit code `1`.
   `gosree -borrow` (flag with no package arguments) -> exit code `1`.
   Contract requires `2` (usage / missing required parameters).
7. **`go vet -vettool="gosree"` Compatibility**:
   - `go vet -vettool=... ./tests/e2e/testdata/src/t1_valid` -> exit code `0`. (Verified)
   - `go vet -vettool=... ./passes/borrow/testdata/src/borrows` -> exit code `1`. (Verified)

---

## 2. Logic Chain

1. **Exit Code Contract Alignment**:
   - The contract specified in `SCOPE.md` requires:
     - `0`: Clean (no errors, no findings).
     - `1`: Analysis or compilation failure (fatal error, unparseable code, crash).
     - `2`: Refused/invalid CLI flags or bad arguments (including missing required parameters).
     - `3`: Vulnerabilities or safety findings detected (`SAFE001`–`SAFE008`, `BORROW001`–`BORROW004`, `ERR001`–`ERR005`).
2. **Current Alignment**:
   - Exit `0`: Matches across text and `-json` modes.
   - Exit `1`: Matches on compilation errors, package loading errors, and analysis crashes.
   - Exit `3`: Matches on safety, borrow, and error discipline findings across text and `-json` modes.
   - Exit `2`: Matches on undefined flags (`flag.Parse`) and refused flags (`parseCommandLine`).
3. **The Zero-Argument / Missing Package Parameter Defect**:
   - When a user runs `gosree` or `gosree -borrow` without passing package patterns, execution passes `parseCommandLine` and reaches `multichecker.Main`.
   - `multichecker.Main` checks `if len(args) == 0` and unconditionally invokes `os.Exit(1)`.
   - This conflates bad CLI invocation / missing required parameters (which should be exit 2) with compilation/analysis failure (exit 1).
   - Because `main()` in `cmd/gosree` already wraps `multichecker.Main` and runs preflight parsing, `main()` can easily inspect positional package arguments before invoking `multichecker.Main`:
     - If non-flag arguments are empty, and neither `-flags`, `-V`, `-h`, `-help`, nor `help` was requested, `main()` prints usage and exits with `exitUsage` (`2`).
4. **`go vet -vettool` Contract Compatibility**:
   - When `go vet` drives `gosree`, it passes:
     - `-flags` (discovery phase -> exits `0`).
     - `-V=full` (cache versioning phase -> exits `0`).
     - `/path/to/unit.cfg` (compilation unit analysis phase).
   - In `cmd/gosree`, `unitcheckerMode(p.args)` detects `<unit>.cfg`.
   - In `multichecker.Main`, `<unit>.cfg` routes directly to `unitchecker.Run`.
   - `unitchecker.Run` exits `0` on clean, and `1` on findings or errors.
   - `go vet` interprets exit code `0` as pass, and any non-zero exit as failure, exiting with `1`.
   - Therefore, full compatibility with `go vet -vettool="gosree"` is already completely maintained.

---

## 3. Caveats

1. **`go vet` Process Exit Code vs Vettool Exit Code**:
   - `go vet` is a standard Go command tool: its own process exit code is binary (`0` for success, `1` for diagnostics or error). It does not pass through exit code `3`. This is normal and documented in `cmd/gosree/main.go:9-10`.
2. **Worktree Isolation**:
   - All tests and binaries must continue to be built and executed strictly inside `/Users/bharath/Code/devtools/gocap-worktree`. The main workspace `/Users/bharath/Code/devtools/gocap` must not be modified.
3. **`gosree-gate` Compiler Interceptor Scope**:
   - While `cmd/gosree` enforces preflight exit codes `0`, `1`, `2`, `3`, the companion binary `cmd/gosree-gate` (to be created in M3 Feature 19) will intercept `go build -toolexec="gosree-gate"` and invoke GoSree checks before compilation, halting the build if exit code != 0.

---

## 4. Conclusion & Recommended Implementation Plan for Worker

### 4.1 Root Cause of Existing Exit Code Gap
In `cmd/gosree/main.go`, `parseCommandLine` only checks flag validity and does not check for the presence of positional package arguments. When no packages are specified, execution falls through to `multichecker.Main`, which hardcodes `os.Exit(1)`.

### 4.2 Proposed Implementation in `cmd/gosree`
In `cmd/gosree/main.go`:
Add a preflight check before calling `multichecker.Main(analyzers...)`:

```go
// isMetaCommand reports whether the command is a query that requires no package arguments:
// -flags, -V=full, -help, -h, or the "help" subcommand.
func isMetaCommand(args []string) bool {
	for _, arg := range args {
		name := strings.TrimLeft(arg, "-")
		if k := strings.IndexByte(name, '='); k >= 0 {
			name = name[:k]
		}
		if name == "flags" || name == "V" || name == "help" || name == "h" {
			return true
		}
	}
	end := flagEnd(args)
	rest := args[end:]
	return len(rest) > 0 && rest[0] == "help"
}
```

In `main()`:
```go
		if !unitcheckerMode(p.args) && !isMetaCommand(p.args) {
			rest := p.args[flagEnd(p.args):]
			if len(rest) == 0 {
				fmt.Fprintln(os.Stderr, "gosree: no package arguments provided")
				fmt.Fprintln(os.Stderr, "Usage: gosree [flags] [packages...]")
				fmt.Fprintln(os.Stderr, "Run 'gosree help' for more detail.")
				os.Exit(exitUsage)
			}
		}
```

### 4.3 Proposed Empirical Exit Code Test Suite (`cmd/gosree/exit_test.go`)
Worker should add a dedicated `exit_test.go` in `cmd/gosree` testing all exit code branches:
1. `TestExitCode0_Clean`: Run `gosree ./tests/e2e/testdata/src/t1_valid`, assert exit code `0`.
2. `TestExitCode1_AnalysisFailure`: Run `gosree ./nonexistent_dir`, assert exit code `1`.
3. `TestExitCode1_SyntaxError`: Run `gosree <temp_syntax_error.go>`, assert exit code `1`.
4. `TestExitCode2_NoArguments`: Run `gosree` with empty args, assert exit code `2`.
5. `TestExitCode2_FlagsNoPackage`: Run `gosree -borrow`, assert exit code `2`.
6. `TestExitCode2_InvalidFlag`: Run `gosree -nonexistentflag ./...`, assert exit code `2`.
7. `TestExitCode2_RefusedFlag`: Run `gosree -fix ./...`, assert exit code `2`.
8. `TestExitCode3_SafetyFindings`: Run `gosree ./passes/borrow/testdata/src/borrows`, assert exit code `3`.
9. `TestExitCode3_JSONFindings`: Run `gosree -json ./passes/borrow/testdata/src/borrows`, assert exit code `3`.
10. `TestGoVetCompatibility`: Run `go vet -vettool=gosree ./tests/e2e/testdata/src/t1_valid` (exit 0) and `go vet -vettool=gosree ./passes/borrow/testdata/src/borrows` (exit 1).

---

## 5. Verification Method

To independently verify all findings and test current exit code behaviors in `/Users/bharath/Code/devtools/gocap-worktree`:

```bash
cd /Users/bharath/Code/devtools/gocap-worktree

# 1. Compile gosree binary for inspection
go build -o /tmp/gosree-test ./cmd/gosree

# 2. Verify Exit Code 0 (Clean)
/tmp/gosree-test ./tests/e2e/testdata/src/t1_valid
echo "Clean exit: $?" # Expect: 0

# 3. Verify Exit Code 3 (Findings Detected)
/tmp/gosree-test ./passes/borrow/testdata/src/borrows
echo "Findings exit: $?" # Expect: 3

# 4. Verify Exit Code 1 (Compilation / Syntax Failure)
/tmp/gosree-test ./nonexistent_package
echo "Failure exit: $?" # Expect: 1

# 5. Verify Exit Code 2 (Refused Flag)
/tmp/gosree-test -fix ./tests/e2e/testdata/src/t1_valid
echo "Refused flag exit: $?" # Expect: 2

# 6. Verify Exit Code 2 (Invalid Flag)
/tmp/gosree-test -invalidflag
echo "Invalid flag exit: $?" # Expect: 2

# 7. Observe Exit Code 1 Gap on No Arguments (Must be hardened to 2)
/tmp/gosree-test
echo "No args exit: $?" # Currently: 1, Target: 2

# 8. Verify go vet -vettool Compatibility
go vet -vettool=/tmp/gosree-test ./tests/e2e/testdata/src/t1_valid
echo "go vet clean exit: $?" # Expect: 0

go vet -vettool=/tmp/gosree-test ./passes/borrow/testdata/src/borrows
echo "go vet findings exit: $?" # Expect: 1

# 9. Run Full cmd/gosree Test Suite
go test -v ./cmd/gosree/...
```
