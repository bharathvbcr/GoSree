// Command gosree-gate is a compiler gate interceptor for Go builds (go build -toolexec="gosree-gate").
//
// It intercepts compilation tool invocations before object files (.a / .o) are emitted.
// If the tool being executed is compile on user source files, GoSree static analysis passes
// (annot, unsafeban, ownership, borrow, errcheck) are executed against the compilation unit.
// If safety findings or errors are detected, diagnostics are written to stderr and the process exits
// with status 3, aborting the build pipeline before object files can be generated.
//
// For non-compile tools (asm, link, cgo, buildid), version queries (-V, -V=full), standard library
// files, or clean compilation units, it transparently delegates execution to the underlying tool.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/passes/borrow"
	"github.com/bharathvbcr/gosree/passes/errcheck"
	"github.com/bharathvbcr/gosree/passes/ownership"
	"github.com/bharathvbcr/gosree/passes/unsafeban"
)

const (
	exitClean    = 0
	exitFailed   = 1
	exitUsage    = 2
	exitFindings = 3
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: gosree-gate <tool> [args...]")
		os.Exit(exitUsage)
	}

	tool := os.Args[1]
	args := os.Args[2:]

	// 1. Toolchain version queries must be forwarded verbatim without modifying stdout.
	if isVersionQuery(args) {
		execTool(tool, args)
	}

	// 2. Only intercept compiler tool invocations; transparently pass through asm, link, cgo, etc.
	toolName := strings.TrimSuffix(filepath.Base(tool), ".exe")
	if toolName != "compile" {
		execTool(tool, args)
	}

	// 3. Extract Go source files from compiler arguments.
	var goFiles []string
	for _, a := range args {
		if strings.HasSuffix(a, ".go") && !strings.HasPrefix(a, "-") {
			goFiles = append(goFiles, a)
		}
	}
	if len(goFiles) == 0 {
		execTool(tool, args)
	}

	// 4. Skip genuine standard library compilation units.
	if isStandardLibrary(args, goFiles) {
		execTool(tool, args)
	}

	// 5. Execute GoSree analysis passes against the package compilation unit.
	if err := runGoSreeAnalysis(goFiles); err != nil {
		// Compilation halted: suppress object emission and abort the build.
		os.Exit(exitFindings)
	}

	// 6. Clean analysis: transparently execute the underlying compiler.
	execTool(tool, args)
}

func isVersionQuery(args []string) bool {
	for _, a := range args {
		if a == "-V=full" || a == "-V" || strings.HasPrefix(a, "-V=") {
			return true
		}
	}
	return false
}

func hasStdFlag(args []string) bool {
	for _, a := range args {
		if a == "-std" {
			return true
		}
	}
	return false
}

func isUnderDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fileOriginatesInDir(filePath, dir string) bool {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return false
	}
	cleanDir := filepath.Clean(dir)
	evalDir, errDir := filepath.EvalSymlinks(cleanDir)

	// The literal path abs (prior to symlink evaluation) must reside within dir
	// (cleanDir or its canonical symlink-evaluated path evalDir).
	// If abs is outside dir (e.g. in /tmp or user workspace), it is a user file or
	// user symlink pointing into dir and does NOT originate in dir.
	literalUnderDir := isUnderDir(abs, cleanDir) || (errDir == nil && isUnderDir(abs, evalDir))
	if !literalUnderDir {
		return false
	}

	// Furthermore, the resolved target of the file must also remain within dir.
	evalAbs, errAbs := filepath.EvalSymlinks(abs)
	if errAbs != nil {
		return false
	}
	return isUnderDir(evalAbs, cleanDir) || (errDir == nil && isUnderDir(evalAbs, evalDir))
}

func isStandardLibrary(args []string, goFiles []string) bool {
	// All genuine standard library compiles have -std set by cmd/go.
	// If -std is missing, it is definitely not standard library.
	if !hasStdFlag(args) {
		return false
	}

	goroot := runtime.GOROOT()
	if goroot == "" {
		return false
	}
	cleanRoot := filepath.Clean(goroot)

	// 1. Pure Go standard library: ALL source files in goFiles must reside in GOROOT.
	// (Handles 371/375 stdlib packages in standard go toolchain).
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

	// 2. For cgo standard library packages where files passed to compile reside in temporary
	// directories ($WORK/bXXX/*.cgo1.go, _cgo_gotypes.go), verify package path (-p) is a standard
	// import path, exists in $GOROOT/src, and //line directives in *.cgo1.go originate in GOROOT.
	pkgPath := getPkgPath(args)
	if pkgPath != "" && isStandardImportPath(pkgPath) {
		stdDir := filepath.Join(cleanRoot, "src", filepath.FromSlash(pkgPath))
		if fi, err := os.Stat(stdDir); err == nil && fi.IsDir() {
			for _, f := range goFiles {
				if strings.HasSuffix(f, ".cgo1.go") {
					if cgoFileOriginatesFromGoroot(f, cleanRoot) {
						return true
					}
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
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "//line ") {
			target := strings.TrimPrefix(l, "//line ")
			if colon := strings.IndexByte(target, ':'); colon != -1 {
				target = target[:colon]
			}
			target = strings.TrimSpace(target)
			if target != "" && fileOriginatesInDir(target, cleanRoot) {
				return true
			}
		}
	}
	return false
}

func findGosreeBin() string {
	if bin := os.Getenv("GOSREE_BIN"); bin != "" {
		if fi, err := os.Stat(bin); err == nil && !fi.IsDir() {
			return bin
		}
	}
	self, err := os.Executable()
	if err == nil {
		adj := filepath.Join(filepath.Dir(self), "gosree")
		if fi, err := os.Stat(adj); err == nil && !fi.IsDir() {
			return adj
		}
	}
	if p, err := exec.LookPath("gosree"); err == nil {
		return p
	}
	return ""
}

func cleanEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOFLAGS=") {
			val := strings.TrimPrefix(e, "GOFLAGS=")
			var kept []string
			for _, f := range strings.Fields(val) {
				if !strings.HasPrefix(f, "-toolexec") {
					kept = append(kept, f)
				}
			}
			if len(kept) > 0 {
				env = append(env, "GOFLAGS="+strings.Join(kept, " "))
			}
		} else {
			env = append(env, e)
		}
	}
	return env
}

func runGoSreeAnalysis(goFiles []string) error {
	gosreeBin := findGosreeBin()
	if gosreeBin != "" {
		cmd := exec.Command(gosreeBin, goFiles...)
		cmd.Env = cleanEnv()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		return nil
	}

	// Fallback to in-process analysis using the registered analysis passes.
	return runInProcessAnalysis(goFiles)
}

func runInProcessAnalysis(goFiles []string) error {
	var absFiles []string
	for _, f := range goFiles {
		abs, err := filepath.Abs(f)
		if err == nil {
			absFiles = append(absFiles, abs)
		} else {
			absFiles = append(absFiles, f)
		}
	}
	dir := filepath.Dir(absFiles[0])
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  dir,
	}
	pkgs, err := packages.Load(cfg, absFiles...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosree-gate: failed to load package: %v\n", err)
		return err
	}
	analyzers := []*analysis.Analyzer{
		annot.Analyzer,
		unsafeban.Analyzer,
		ownership.Analyzer,
		borrow.Analyzer,
		errcheck.Analyzer,
	}
	graph, err := checker.Analyze(analyzers, pkgs, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gosree-gate: analysis failed: %v\n", err)
		return err
	}
	var diags int
	for _, act := range graph.Roots {
		if act.Err != nil {
			fmt.Fprintf(os.Stderr, "gosree-gate: %s error: %v\n", act.Analyzer.Name, act.Err)
			return act.Err
		}
		for _, d := range act.Diagnostics {
			diags++
			pos := act.Package.Fset.Position(d.Pos)
			fmt.Fprintf(os.Stderr, "%s: %s\n", pos, d.Message)
		}
	}
	if diags > 0 {
		return errors.New("safety findings detected")
	}
	return nil
}

func execTool(tool string, args []string) {
	cmd := exec.Command(tool, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "gosree-gate: failed to execute %s: %v\n", tool, err)
		os.Exit(exitFailed)
	}
	os.Exit(exitClean)
}
