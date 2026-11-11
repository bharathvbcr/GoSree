package main

import "testing"

// TestUnitcheckerMode: go vet runs the tool with flags and exactly one .cfg file. A command line that
// merely ends in a .cfg argument, after other package arguments, is a standalone run: treating it as
// go vet's skipped the -json child, so the exit status ignored the findings.
func TestUnitcheckerMode(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"/tmp/vet/x.cfg"}, true},
		{[]string{"-flags=x", "x.cfg"}, true},
		{[]string{"-tags", "a,b", "x.cfg"}, true},
		{[]string{"-V=full"}, false},
		{[]string{"-json", "./a", "./b.cfg"}, false},
		{[]string{"./a", "./b.cfg"}, false},
		{[]string{"a.cfg", "b.cfg"}, false},
		{[]string{"-json", "--", "a.cfg"}, false},
		{[]string{"-json", "./a"}, false},
		{[]string{"-json"}, false},
		{nil, false},
	} {
		if got := unitcheckerMode(tc.args); got != tc.want {
			t.Errorf("unitcheckerMode(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestFlagEndAgreesWithParse: the positional split unitcheckerMode uses is the one parseCommandLine
// makes.
func TestFlagEndAgreesWithParse(t *testing.T) {
	for _, args := range [][]string{
		{"-json", "./a", "./b.cfg"},
		{"-tags", "x", "./a"},
		{"-c", "2", "-json=false", "./a"},
		{"-safeownership=false", "--", "./a"},
		{"./a"},
		{},
	} {
		p, err := parseCommandLine(args, []string{"safeownership"})
		if err != nil {
			t.Fatal(err)
		}
		rest := args[flagEnd(args):]
		if len(rest) > len(p.args) || len(p.args[len(p.args)-len(rest):]) != len(rest) {
			t.Fatalf("%q: positional tail %q is not the tail of %q", args, rest, p.args)
		}
		for i := range rest {
			if p.args[len(p.args)-len(rest)+i] != rest[i] {
				t.Errorf("%q: positional tail %q, parse kept %q", args, rest, p.args)
			}
		}
	}
}
