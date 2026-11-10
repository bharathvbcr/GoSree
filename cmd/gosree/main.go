// Command gocap checks Go packages for ownership and memory-safety violations.
//
//	gocap ./...
//	go vet -vettool=$(command -v gocap) ./...
//
// Exit status, in both drivers: 0 when every package was loaded, type-checked and analysed and
// nothing was reported; 3 when findings were reported; 1 when a package could not be loaded or
// type-checked or an analyzer failed (no verdict, never 0, and it outranks 3: findings of the
// packages that did load are still printed); 2 for a flag gocap refuses. Under go vet the status
// is go vet's own (1 for findings and for failures).
//
// Flags that would make a run cover less or pass what it should not are handled before the driver
// sees them: -fix and -diff are refused; -tags is passed to the go command (the driver ignores
// it); -json keeps the findings exit status (the driver exits 0 with findings in this mode);
// -test=false and -NAME=false (or -NAME, which runs only that pass) print a warning on stderr.
package main

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/bharathvbcr/gocap/internal/annot"
	"github.com/bharathvbcr/gocap/passes/borrow"
	"github.com/bharathvbcr/gocap/passes/errcheck"
	"github.com/bharathvbcr/gocap/passes/ownership"
	"github.com/bharathvbcr/gocap/passes/unsafeban"
)

// childEnv marks the process that runs the driver for a -json parent.
const childEnv = "GOCAP_JSON_CHILD"

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
				fmt.Fprintln(os.Stderr, "gocap: WARNING: "+w)
			}
		}
		os.Args = append(os.Args[:1:1], p.args...)
		if !unitcheckerMode(p.args) && p.goflags != "" {
			os.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+p.goflags))
		}
		if p.json && !unitcheckerMode(p.args) {
			os.Exit(runJSON(p.args))
		}
	}
	multichecker.Main(analyzers...)
}

// unitcheckerMode reports whether go vet is driving: it runs the tool once per package with the
// path of a .cfg file as the only argument.
func unitcheckerMode(args []string) bool {
	return len(args) > 0 && strings.HasSuffix(args[len(args)-1], ".cfg")
}

// vetxOnly reports whether go vet runs the tool on a dependency only to collect facts: the package
// is then not one the user asked about, and a warning about the run would repeat for every one.
func vetxOnly(args []string) bool {
	if !unitcheckerMode(args) {
		return false
	}
	data, err := os.ReadFile(args[len(args)-1])
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
		fmt.Fprintln(os.Stderr, "gocap: cannot locate itself to run -json:", err)
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
			fmt.Fprintln(os.Stderr, "gocap: cannot run the driver for -json:", err)
			return exitFailed
		}
	}
	os.Stdout.Write(out.Bytes())
	if code != exitClean {
		return code
	}
	return jsonExit(out.Bytes())
}

// jsonExit derives the exit status from the driver's -json output: a map of package to analyzer to
// either a list of findings or an {"error": ...} object. Output it cannot read is a failure.
func jsonExit(data []byte) int {
	if len(bytes.TrimSpace(data)) == 0 {
		return exitClean
	}
	var tree map[string]map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		fmt.Fprintln(os.Stderr, "gocap: cannot read the driver's -json output:", err)
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
