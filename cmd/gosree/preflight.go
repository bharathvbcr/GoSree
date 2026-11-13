package main

import (
	"errors"
	"fmt"
	"slices"
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

// cmdFlag is one flag of the command line as the driver's flag package reads it.
type cmdFlag struct {
	name     string
	value    string
	hasValue bool // "-name=value", or a value flag that took the next argument
	start    int  // index of the flag's first argument
	end      int  // index just past its last argument (a consumed value included)
}

// scanFlags reads the leading flags: the ones before the first positional argument or "--". A value
// flag without "=" consumes the next argument, so that argument is part of the flag and never a
// flag of its own. It is the one walk every reader of the command line goes through, so they cannot
// disagree about where a flag starts or what is its value. end is the index of the first positional
// argument.
func scanFlags(args []string) (flags []cmdFlag, end int) {
	i := 0
	for ; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || len(arg) < 2 || arg[0] != '-' {
			break
		}
		f := cmdFlag{name: strings.TrimLeft(arg, "-"), start: i}
		if k := strings.IndexByte(f.name, '='); k >= 0 {
			f.name, f.value, f.hasValue = f.name[:k], f.name[k+1:], true
		} else if valueFlags[f.name] && i+1 < len(args) {
			i++
			f.value, f.hasValue = args[i], true
		}
		f.end = i + 1
		flags = append(flags, f)
	}
	return flags, i
}

// flagEnd returns the index of the first positional argument: where the driver's flags stop.
func flagEnd(args []string) int {
	_, end := scanFlags(args)
	return end
}

// truthy reads a boolean flag's value as the flag package does; an unparsable value is the driver's
// error to report, so it counts as set.
func (f cmdFlag) truthy() bool {
	if !f.hasValue {
		return true
	}
	b, err := strconv.ParseBool(f.value)
	return err != nil || b
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
	testsOff := false          // the last value of -test is false
	given := map[string]bool{} // the last value of each flag that is warned about when repeated
	var changed []string       // those flags, in the order their value first changed
	record := func(name string, v bool) {
		if prev, ok := given[name]; ok && prev != v && !slices.Contains(changed, name) {
			changed = append(changed, name)
		}
		given[name] = v
	}
	printFlags := false // the last value of -flags
	flags, end := scanFlags(args)
	for _, f := range flags {
		name := f.name
		truthy := f.truthy
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
			// The driver reads -flags only after parsing the whole command line, so its last value
			// decides whether it prints the flag set and exits. Every other flag is still checked:
			// with -flags -flags=false the driver analyses.
			printFlags = truthy()
		case name == "fix" || name == "diff":
			if truthy() {
				return plan{}, errors.New("-" + name + " is not supported: gosree offers no suggested fixes, and the driver exits 0 and prints no findings in this mode, so a gate would pass code that has findings")
			}
		case name == "tags":
			if v := strings.TrimSpace(f.value); v != "" {
				if strings.ContainsAny(v, " \t") {
					return plan{}, errors.New("-tags takes a comma-separated list; a space-separated list is not supported")
				}
				p.goflags += " -tags=" + v
			}
			continue // consumed here: the driver would accept it and ignore it
		case name == "json":
			p.json = truthy()
		case name == "test":
			testsOff = !truthy() // like every flag, the last -test decides
			record("test", !testsOff)
		case known[name]:
			if _, seen := enabled[name]; !seen {
				order = append(order, name)
			}
			enabled[name] = truthy()
			record(name, enabled[name])
		}
		kept = append(kept, args[f.start:f.end]...)
	}
	p.args = append(kept, args[end:]...)
	if printFlags {
		return plan{args: args}, nil // the driver prints its flags and exits without analysing
	}

	for _, name := range changed {
		p.warnings = append(p.warnings, fmt.Sprintf("-%s was given more than once with different values: the last, -%s=%t, decides", name, name, given[name]))
	}
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
