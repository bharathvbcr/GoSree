package main

import (
	"slices"
	"strings"
	"testing"
)

// The driver reads -flags after parsing the whole command line, so its last value decides; the
// "is this a query that needs no packages" check must agree with parseCommandLine on that.
func TestIsMetaCommandLastFlagsDecides(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"-flags"}, true},
		{[]string{"-flags=false"}, false},
		{[]string{"-flags=false", "-flags"}, true},
		{[]string{"-flags=0", "-flags=t"}, true},
		{[]string{"-flags", "-flags=false"}, false},
		{[]string{"-flags=true", "-flags=false"}, false},
		{[]string{"-flags=1", "-flags=0"}, false},
		{[]string{"-flags=t", "-flags=f"}, false},
		{[]string{"--flags", "--flags=false"}, false},
		{[]string{"-flags", "-json", "-flags=false"}, false},
		{[]string{"-flags", "-flags=false", "-flags"}, true},
		{[]string{"-flags", "-flags=false", "-V=full"}, true},
		{[]string{"-flags", "-flags=false", "-h"}, true},
		{[]string{"-flags", "-flags=false", "help"}, true},
	} {
		if got := isMetaCommand(c.args); got != c.want {
			t.Errorf("isMetaCommand(%q) = %v, want %v", c.args, got, c.want)
		}
		p, err := parseCommandLine(c.args, testAnalyzers)
		if err != nil {
			t.Errorf("%q: %v", c.args, err)
			continue
		}
		// parseCommandLine hands the driver the unchanged arguments exactly when it is a query.
		if _, isFlagsQuery := lastFlags(c.args); isFlagsQuery && !slices.Equal(p.args, c.args) {
			t.Errorf("%q: parseCommandLine and isMetaCommand disagree about -flags", c.args)
		}
	}
}

// lastFlags is the test's own reading of -flags: its last value, and whether it was given.
func lastFlags(args []string) (value, given bool) {
	for _, a := range args[:flagEnd(args)] {
		n := strings.TrimLeft(a, "-")
		switch n {
		case "flags", "flags=true", "flags=1", "flags=t":
			value, given = true, true
		case "flags=false", "flags=0", "flags=f":
			value, given = false, true
		}
	}
	return value, given
}

func TestRepeatedFlagWarnings(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		warn []string // a substring of each warning, in order
	}{
		{"test false then true covers more", []string{"-test=false", "-test", "./..."},
			[]string{"-test was given more than once"}},
		{"test false then true, spelled 0 and 1", []string{"-test=0", "-test=1", "./..."},
			[]string{"-test=true"}},
		{"test true then false covers less, once", []string{"-test", "-test=false", "./..."},
			[]string{"-test was given more than once", "-test=false: test files are not analysed"}},
		{"test false twice warns once", []string{"-test=false", "-test=false", "./..."},
			[]string{"-test=false: test files are not analysed"}},
		{"test true twice is silent", []string{"-test", "-test=true", "./..."}, nil},
		{"test three values", []string{"-test=false", "-test", "-test=false", "./..."},
			[]string{"-test was given more than once", "-test=false: test files are not analysed"}},
		{"analyzer off then on", []string{"-borrow=false", "-borrow", "./..."},
			[]string{"-borrow was given more than once", "analysis passes are switched off"}},
		{"analyzer on then off", []string{"-safeunsafe", "-safeunsafe=false", "./..."},
			[]string{"-safeunsafe was given more than once", "-safeunsafe (SAFE001"}},
		{"analyzer off twice is silent about repeats", []string{"-safeunsafe=false", "-safeunsafe=false", "./..."},
			[]string{"-safeunsafe (SAFE001"}},
	} {
		p, err := parseCommandLine(c.args, testAnalyzers)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(p.warnings) != len(c.warn) {
			t.Errorf("%s: warnings %q, want %d", c.name, p.warnings, len(c.warn))
			continue
		}
		for i, w := range c.warn {
			if !strings.Contains(p.warnings[i], w) {
				t.Errorf("%s: warning %q lacks %q", c.name, p.warnings[i], w)
			}
		}
	}
}
