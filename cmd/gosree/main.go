// Command gosree checks Go packages for ownership and memory-safety violations.
//
//	gosree ./...
//	go vet -vettool=$(command -v gosree) ./...
//
// Exit status, in both drivers: 0 when every package was loaded, type-checked and analysed and
// nothing was reported; 3 when findings were reported; 1 when a package could not be loaded or
// type-checked or an analyzer failed (no verdict, never 0, and it outranks 3: findings of the
// packages that did load are still printed); 2 for a flag gosree refuses. Under go vet the status
// is go vet's own (1 for findings and for failures).
//
// Flags that would make a run cover less or pass what it should not are handled before the driver
// sees them: -fix and -diff are refused; -tags is passed to the go command (the driver ignores
// it); -json keeps the findings exit status (the driver exits 0 with findings in this mode);
// -test=false and -NAME=false (or -NAME, which runs only that pass) print a warning on stderr, as
// does a wildcard pattern that matches no package when another pattern does (exit status unchanged).
package main

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/passes/borrow"
	"github.com/bharathvbcr/gosree/passes/errcheck"
	"github.com/bharathvbcr/gosree/passes/ownership"
	"github.com/bharathvbcr/gosree/passes/unsafeban"
)

// childEnv marks the process that runs the driver for a -json parent.
const childEnv = "GOSREE_JSON_CHILD"

var analyzers = []*analysis.Analyzer{annot.Analyzer, unsafeban.Analyzer, ownership.Analyzer, borrow.Analyzer, errcheck.Analyzer}

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
			// After GOFLAGS carries -tags, so go list resolves patterns as the driver will.
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

// unitcheckerMode reports whether go vet is driving: it runs the tool once per package with flags
// and the path of one .cfg file as the only positional argument. Package patterns before a trailing
// .cfg argument make it a standalone run, and so does a .cfg that is not a regular file (a package
// directory named x.cfg).
func unitcheckerMode(args []string) bool {
	rest := args[flagEnd(args):]
	if len(rest) != 1 || !strings.HasSuffix(rest[0], ".cfg") {
		return false
	}
	fi, err := os.Stat(rest[0])
	return err != nil || fi.Mode().IsRegular() // a missing file is vet's error to report, not a pattern
}

// cfgDirAsPattern keeps multichecker from mistaking a package directory named x.cfg for go vet's
// config file: it decides by the name alone when that is the only argument, so the directory is
// given as "x.cfg/." (the same package).
func cfgDirAsPattern(args []string) []string {
	end := flagEnd(args)
	rest := args[end:]
	if len(rest) != 1 || !strings.HasSuffix(rest[0], ".cfg") || unitcheckerMode(args) {
		return args
	}
	if fi, err := os.Stat(rest[0]); err != nil || !fi.IsDir() {
		return args
	}
	return append(slices.Clone(args[:end]), strings.TrimSuffix(rest[0], "/")+"/.")
}

// vetxOnly reports whether go vet runs the tool on a dependency only to collect facts: the package
// is then not one the user asked about, and a warning about the run would repeat for every one.
func vetxOnly(args []string) bool {
	if !unitcheckerMode(args) {
		return false
	}
	data, err := os.ReadFile(args[len(args)-1]) // the one positional argument, the .cfg path
	if err != nil {
		return false
	}
	var cfg struct{ VetxOnly bool }
	return json.Unmarshal(data, &cfg) == nil && cfg.VetxOnly
}

// runJSON runs the driver in a child process and returns the exit status for its output. With -json
// the driver prints the findings and exits 0, so a gate that trusts the status would pass code
// with findings; here a findings object makes the status 3 and an error object makes it 1.
func runJSON(args []string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gosree: cannot locate itself to run -json:", err)
		return exitFailed
	}
	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			fmt.Fprintln(os.Stderr, "gosree: cannot run the driver for -json:", err)
			return exitFailed
		}
	}
	cleaned := dedupJSON(out.Bytes())
	os.Stdout.Write(cleaned)
	if code != exitClean {
		return code
	}
	return jsonExit(cleaned)
}

type jsonDiagKey struct {
	Posn    string `json:"posn"`
	Message string `json:"message"`
}

// dedupJSON removes findings from a package's in-package test variant ("P [P.test]") that were
// already emitted for the base package ("P"), matching text mode and `go vet -json`. Test-only
// findings and analyzer error objects are preserved untouched.
func dedupJSON(data []byte) []byte {
	if len(bytes.TrimSpace(data)) == 0 {
		return data
	}
	var tree map[string]map[string]jsontext.Value
	if err := json.Unmarshal(data, &tree); err != nil {
		return data
	}
	modified := false
	for pkgKey, byAnalyzer := range tree {
		basePkg, rest, ok := strings.Cut(pkgKey, " [")
		if !ok || !strings.HasSuffix(rest, ".test]") {
			continue
		}
		baseAnalyzers, ok := tree[basePkg]
		if !ok {
			continue
		}
		for analyzer, testRaw := range byAnalyzer {
			baseRaw, ok := baseAnalyzers[analyzer]
			if !ok {
				continue
			}
			var baseDiags []jsonDiagKey
			var testItems []jsontext.Value
			if json.Unmarshal(baseRaw, &baseDiags) != nil || json.Unmarshal(testRaw, &testItems) != nil {
				continue
			}
			if len(baseDiags) == 0 || len(testItems) == 0 {
				continue
			}
			seen := make(map[jsonDiagKey]bool, len(baseDiags))
			for _, d := range baseDiags {
				seen[d] = true
			}
			kept := make([]jsontext.Value, 0, len(testItems))
			for _, item := range testItems {
				var d jsonDiagKey
				if json.Unmarshal(item, &d) == nil && seen[d] {
					modified = true
					continue
				}
				kept = append(kept, item)
			}
			if len(kept) == len(testItems) {
				continue
			}
			if len(kept) == 0 {
				delete(byAnalyzer, analyzer)
				continue
			}
			if encoded, err := json.Marshal(kept); err == nil {
				byAnalyzer[analyzer] = jsontext.Value(encoded)
			}
		}
	}
	if !modified {
		return data
	}
	rewritten, err := json.Marshal(tree, json.Deterministic(true), jsontext.WithIndent("\t"))
	if err != nil {
		return data
	}
	return append(rewritten, '\n')
}

// jsonExit derives the exit status from the driver's -json output: a map of package to analyzer to
// either a list of findings or an {"error": ...} object. Output it cannot read is a failure.
func jsonExit(data []byte) int {
	if len(bytes.TrimSpace(data)) == 0 {
		return exitClean
	}
	var tree map[string]map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		fmt.Fprintln(os.Stderr, "gosree: cannot read the driver's -json output:", err)
		return exitFailed
	}
	code, failed := exitClean, false
	for _, byAnalyzer := range tree {
		for _, v := range byAnalyzer {
			switch v := v.(type) {
			case []any:
				if len(v) > 0 {
					code = max(code, exitFindings)
				}
			case map[string]any:
				if _, isErr := v["error"]; isErr {
					failed = true
				}
			}
		}
	}
	if failed {
		return exitFailed // as the text mode: a failed analysis outranks findings
	}
	return code
}
