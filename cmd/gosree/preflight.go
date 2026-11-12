package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Exit codes. multichecker and the standalone driver already use 0, 1 and 3; the others are listed
// so the contract is in one place.
const (
	exitClean    = 0 // every package loaded, type-checked and analysed, and nothing was reported
	exitFailed   = 1 // a package could not be loaded or type-checked, or an analyzer failed: no verdict
	exitUsage    = 2 // a flag gosree does not accept, or one it refuses because it would change the verdict
	exitFindings = 3 // every package was analysed and at least one finding was reported
)

// valueFlags are the flags of the driver that take a value, so that "-tags x" is read as one flag.
var valueFlags = map[string]bool{
	"tags": true, "c": true, "debug": true, "cpuprofile": true, "memprofile": true, "trace": true,
}

// flagEnd returns the index of the first positional argument: where the driver's flags stop. It
// follows the rule parseCommandLine uses (a flag package stops at the first non-flag or "--", and a
// value flag may take the next argument).
func flagEnd(args []string) int {
	i := 0
	for ; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || len(arg) < 2 || arg[0] != '-' {
			break
		}
		name := strings.TrimLeft(arg, "-")
		if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(args) {
			i++
		}
	}
	return i
}

// plan is what gosree does with its command line before the driver sees it.
type plan struct {
	args     []string // the arguments the driver gets (without argv[0])
	goflags  string   // text appended to GOFLAGS, so go list honours -tags
	json     bool     // -json: the driver exits 0 even with findings, so gosree re-derives the exit code
	warnings []string // printed to stderr before the run
}

// parseCommandLine reads the flags that decide what a run covers or what its exit status means.
// analyzers are the names of the analysis passes, which the driver exposes as -NAME flags.
//
// It refuses -fix and -diff (gosree has no suggested fixes, and the driver exits 0 without printing
// any finding when -fix is given), honours -tags by handing it to the go command (the driver
// accepts it and does nothing), and warns when a flag makes a run cover less: -NAME=false or -NAME
// switching analysis passes off, -test=false dropping the test files.
func parseCommandLine(args []string, analyzers []string) (plan, error) {
	p := plan{args: args}
	known := map[string]bool{}
	for _, a := range analyzers {
		known[a] = true
	}
	enabled := map[string]bool{} // an analyzer flag that was given: its last value
	var order []string           // analyzer flags in the order first given
	var kept []string
	testsOff := false
	i := 0
	for ; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || len(arg) < 2 || arg[0] != '-' {
			break
		}
		start := i
		name := strings.TrimLeft(arg, "-")
		value, hasValue := "", false
		if k := strings.IndexByte(name, '='); k >= 0 {
			name, value, hasValue = name[:k], name[k+1:], true
		}
		take := func() string { // the value of a flag whose value is the next argument
			if !hasValue && valueFlags[name] && i+1 < len(args) {
				i++
				value, hasValue = args[i], true
			}
			return value
		}
		truthy := func() bool {
			if !hasValue {
				return true
			}
			b, err := strconv.ParseBool(value)
			return err != nil || b // an unparsable value is the driver's error to report
		}
		switch {
		case name == "V":
			return plan{args: args}, nil // the driver answers this without analysing anything
		case name == "help" || name == "h":
			if truthy() {
				return plan{args: args}, nil
			}
			// The driver does not define -help or -h: the flag package prints usage and exits 0 for
			// any value, so -h=false would pass code it never analysed. Drop it and keep checking.
			continue
		case name == "flags":
			// -flags prints the flag set and exits only when true; -flags=false is a no-op to the
			// driver, so the rest of the command line still needs checking.
			if truthy() {
				return plan{args: args}, nil
			}
		case name == "fix" || name == "diff":
			if truthy() {
				return plan{}, errors.New("-" + name + " is not supported: gosree offers no suggested fixes, and the driver exits 0 and prints no findings in this mode, so a gate would pass code that has findings")
			}
		case name == "tags":
			if v := strings.TrimSpace(take()); v != "" {
				if strings.ContainsAny(v, " \t") {
					return plan{}, errors.New("-tags takes a comma-separated list; a space-separated list is not supported")
				}
				p.goflags += " -tags=" + v
			}
			continue // consumed here: the driver would accept it and ignore it
		case name == "json":
			p.json = truthy()
		case name == "test":
			if !truthy() {
				testsOff = true
			}
		case known[name]:
			if _, seen := enabled[name]; !seen {
				order = append(order, name)
			}
			enabled[name] = truthy()
		default:
			take()
		}
		kept = append(kept, args[start:i+1]...)
	}
	p.args = append(kept, args[i:]...)

	if testsOff {
		p.warnings = append(p.warnings, "-test=false: test files are not analysed, so findings in them are not reported")
	}
	var anyTrue, anyFalse bool
	for _, name := range order {
		if enabled[name] {
			anyTrue = true
		} else {
			anyFalse = true
		}
	}
	var off []string
	for _, a := range analyzers {
		switch {
		case anyTrue && !enabled[a]:
			off = append(off, a) // -NAME given: only the named analyzers run
		case !anyTrue && anyFalse && hasFalse(enabled, a):
			off = append(off, a)
		}
	}
	if len(off) > 0 {
		var what []string
		for _, a := range off {
			what = append(what, "-"+a+" ("+covers[a]+")")
		}
		p.warnings = append(p.warnings, "analysis passes are switched off by the command line: "+strings.Join(what, "; ")+
			". Exit status "+strconv.Itoa(exitClean)+" then does not mean those rules were checked")
	}
	return p, nil
}

func hasFalse(enabled map[string]bool, name string) bool {
	v, ok := enabled[name]
	return ok && !v
}

// covers says what each analysis pass checks, for the warning when one is switched off.
var covers = map[string]string{
	"safeannot":     "directive parsing, SAFE007",
	"safeunsafe":    "SAFE001, the unsafe ban",
	"safeownership": "the ownership and sharing rules SAFE002 to SAFE009 and CAP003",
	"borrow":        "the borrow rules",
	"safeerr":       "the error-discipline rules ERR001 to ERR005",
}

// usage formats a refusal.
func usage(err error) string { return fmt.Sprintf("gosree: %v", err) }
