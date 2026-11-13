package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Adversarial tests for ft-8462 (the last -flags decides; repeated -test / -NAME warn). Each case
// states what the stock x/tools driver does, since preflight must never decide differently.

var (
	a62BinOnce sync.Once
	a62BinPath string
	a62BinErr  error
)

func a62Bin(t *testing.T) string {
	t.Helper()
	a62BinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gosree-adv8462-*")
		if err != nil {
			a62BinErr = err
			return
		}
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			a62BinErr = err
			return
		}
		a62BinPath = filepath.Join(dir, "gosree")
		cmd := exec.Command("go", "build", "-o", a62BinPath, "./cmd/gosree")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			a62BinErr = err
			t.Logf("build failed: %s", out)
		}
	})
	if a62BinErr != nil {
		t.Fatalf("build gosree: %v", a62BinErr)
	}
	return a62BinPath
}

// advModule writes a tiny module: clean, find (BORROW004), tagged (finding only under -tags foo) and
// bad (type error).
func a62Module(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	find := "package %s\n\nfunc F() *int {\n\tx := 10\n\treturn &x\n}\n"
	files := map[string]string{
		"go.mod":          "module scratch\n\ngo 1.24\n",
		"clean/c.go":      "package clean\n\nfunc F() int { return 1 }\n",
		"find/f.go":       strings.Replace(find, "%s", "find", 1),
		"tagged/t.go":     "package tagged\n\nfunc G() int { return 1 }\n",
		"tagged/g_foo.go": "//go:build foo\n\n" + strings.Replace(find, "%s", "tagged", 1),
		"bad/b.go":        "package bad\n\nfunc B() int { return \"x\" }\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func a62Run(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, a62Bin(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %q: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

const repeatWarn = "was given more than once with different values"

// BREAK (only the first row is reachable: preflight strips -tags from the arguments it passes on unless -flags makes it pass them unchanged): a value flag consumes
// "-flags -tags -flags=false" the driver reads -flags=true and -tags="-flags=false", prints its flag
// set and exits 0. isMetaCommand walks every argument below flagEnd, including the consumed value,
// so the last "-flags" it sees is the tags value, and the no-packages usage error (exit 2) replaces
// the driver's exit 0. On 8df1d77 the first truthy -flags returned true, giving exit 0.
func TestAdv8462IsMetaCommandSkipsValueFlagArgument(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"-flags", "-tags", "-flags=false"}, true}, // driver: -flags=true, -tags=-flags=false
	} {
		if got := isMetaCommand(c.args); got != c.want {
			t.Errorf("isMetaCommand(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

// BREAK, end to end: the documented exit 0 of a -flags query becomes 2.
func TestAdv8462FlagsQueryBehindValueFlagExits0(t *testing.T) {
	stdout, stderr, code := a62Run(t, a62Module(t), nil, "-flags", "-tags", "-flags=false")
	if code != 0 || !strings.Contains(stdout, `"Name"`) {
		t.Fatalf("-flags -tags -flags=false: exit %d, want 0 and the flag JSON (the stock driver prints it)\nstdout: %.200s\nstderr: %s", code, stdout, stderr)
	}
}

// Defended: where preflight and the driver could differ on what a flag value means, they agree.
func TestAdv8462DriverAgreement(t *testing.T) {
	dir := a62Module(t)
	for _, c := range []struct {
		name   string
		args   []string
		exit   int
		stderr string // a substring, or "" for none required
		warns  int    // number of "gosree: WARNING:" lines
	}{
		{"flags junk", []string{"-flags=junk"}, 2, "invalid boolean value", 0},
		{"flags 2", []string{"-flags=2"}, 2, "invalid boolean value", 0},
		{"flags junk then false, no packages", []string{"-flags=junk", "-flags=false"}, 2, "", 0},
		{"test TRUE", []string{"-test=TRUE", "./clean"}, 0, "", 0},
		{"test yes", []string{"-test=yes", "./clean"}, 2, "invalid boolean value", 0},
		{"test empty", []string{"-test=", "./clean"}, 2, "invalid boolean value", 0},
		{"double dash", []string{"--test=false", "./clean"}, 0, "test files are not analysed", 1},
		{"test false as two args", []string{"-test", "false"}, 1, "package false", 0},
		{"flag after package is a package", []string{"./clean", "-test=false"}, 1, "leading dash", 0},
		{"flag after --", []string{"--", "-test=false", "./clean"}, 1, "leading dash", 0},
		{"help after package", []string{"./clean", "help"}, 1, "", 0},
		{"help after --", []string{"--", "help"}, 0, "", 0},
		{"help first", []string{"help"}, 0, "", 0},
		{"analyzer prefix", []string{"-safe", "./clean"}, 2, "flag provided but not defined", 0},
		{"analyzer extension", []string{"-borrowx", "./clean"}, 2, "flag provided but not defined", 0},
		{"analyzer flag with dot", []string{"-borrow.x", "./clean"}, 2, "flag provided but not defined", 0},
		{"triple dash (warns, then the driver rejects: noise, exit unchanged)", []string{"---test=false", "./clean"}, 2, "bad flag syntax", 1},
	} {
		stdout, stderr, code := a62Run(t, dir, nil, c.args...)
		if code != c.exit {
			t.Errorf("%s %q: exit %d, want %d\nstdout: %.200s\nstderr: %.400s", c.name, c.args, code, c.exit, stdout, stderr)
		}
		if c.stderr != "" && !strings.Contains(stderr, c.stderr) {
			t.Errorf("%s %q: stderr lacks %q: %.400s", c.name, c.args, c.stderr, stderr)
		}
		if n := strings.Count(stderr, "gosree: WARNING:"); n != c.warns {
			t.Errorf("%s %q: %d warnings, want %d: %.400s", c.name, c.args, n, c.warns, stderr)
		}
	}
}

// Defended: the repeat warning names one flag once however often it flips, and the covers-less
// warning is printed once per condition, never twice for the same thing.
func TestAdv8462WarningCounts(t *testing.T) {
	names := []string{"safeannot", "safeunsafe", "safeownership", "borrow", "safeerr"}
	count := func(args []string) (repeat, less, off int, all []string) {
		p, err := parseCommandLine(args, names)
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		for _, w := range p.warnings {
			switch {
			case strings.Contains(w, repeatWarn):
				repeat++
			case strings.HasPrefix(w, "-test=false:"):
				less++
			case strings.HasPrefix(w, "analysis passes are switched off"):
				off++
			}
		}
		return repeat, less, off, p.warnings
	}

	// -test=false -test -test=false -test: the last value is true, so no "covers less"; one repeat.
	r, l, o, all := count([]string{"-test=false", "-test", "-test=false", "-test", "./x"})
	if r != 1 || l != 0 || o != 0 || len(all) != 1 || !strings.Contains(all[0], "-test=true, decides") {
		t.Errorf("four -test: %q", all)
	}
	// Ending on false: one repeat, one covers-less, and the repeat names the value that decides.
	r, l, _, all = count([]string{"-test=false", "-test", "-test=false", "./x"})
	if r != 1 || l != 1 || len(all) != 2 || !strings.Contains(all[0], "-test=false, decides") {
		t.Errorf("three -test: %q", all)
	}
	// Five alternating values of one flag: one warning, naming the fifth.
	r, l, _, all = count([]string{"-test", "-test=0", "-test=1", "-test=f", "-test=t", "./x"})
	if r != 1 || l != 0 || len(all) != 1 || !strings.Contains(all[0], "-test=true, decides") {
		t.Errorf("five -test: %q", all)
	}
	// Every analyzer repeated: one repeat warning each, in first-given order, and one covers-less
	// warning for the whole line.
	var args []string
	for _, n := range names {
		args = append(args, "-"+n+"=false", "-"+n)
	}
	args = append(args, "./x")
	r, _, o, all = count(args)
	if r != len(names) || o != 0 {
		t.Errorf("every analyzer repeated, ending true: repeat=%d off=%d %q", r, o, all)
	}
	args = args[:0]
	for _, n := range names {
		args = append(args, "-"+n, "-"+n+"=false", "-"+n)
		args = append(args, "-"+n+"=false")
	}
	args = append(args, "./x")
	r, _, o, all = count(args)
	if r != len(names) || o != 1 {
		t.Errorf("every analyzer repeated, ending false: repeat=%d off=%d %q", r, o, all)
	}
	// An analyzer name that is a prefix of another: they are different flags.
	p, err := parseCommandLine([]string{"-a=false", "-ab", "-a=false", "./x"}, []string{"a", "ab"})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range p.warnings {
		if strings.Contains(w, repeatWarn) {
			t.Errorf("-a and -ab are separate flags, but: %q", w)
		}
	}
}

// Defended: with -json the parent runs preflight and the child skips it, so a repeat warns once, and
// the exit status is the findings status as without the repeat.
func TestAdv8462JSONChildWarnsOnce(t *testing.T) {
	dir := a62Module(t)
	for _, c := range []struct {
		args []string
		exit int
	}{
		{[]string{"-json", "-test=false", "-test", "./clean"}, 0},
		{[]string{"-json", "-test=false", "-test", "./find"}, 3},
		{[]string{"-json", "-test", "-test=false", "./find"}, 3},
		{[]string{"-json", "-test=false", "-test", "./bad"}, 1},
		{[]string{"-json", "-borrow=false", "-borrow", "./find"}, 3},
	} {
		_, stderr, code := a62Run(t, dir, nil, c.args...)
		if code != c.exit {
			t.Errorf("%q: exit %d, want %d\n%.400s", c.args, code, c.exit, stderr)
		}
		if n := strings.Count(stderr, repeatWarn); n != 1 {
			t.Errorf("%q: repeat warning printed %d times, want 1\n%.600s", c.args, n, stderr)
		}
	}
	// -test -test=false covers less: that warning once too, with and without -json.
	for _, args := range [][]string{{"-test", "-test=false", "./clean"}, {"-json", "-test", "-test=false", "./clean"}} {
		_, stderr, _ := a62Run(t, dir, nil, args...)
		if n := strings.Count(stderr, "test files are not analysed"); n != 1 {
			t.Errorf("%q: covers-less warning %d times, want 1\n%.600s", args, n, stderr)
		}
	}
}

// Defended: the ft-a0d4 exit-code table (build-run-2026-11-12/exitcodes-table.md) is unchanged.
func TestAdv8462ExitCodeTable(t *testing.T) {
	dir := a62Module(t)
	targets := []string{"./clean", "./find", "./tagged", "./bad", "./nope", "./find ./bad"}
	rows := []struct {
		mode string
		want []int // per target
	}{
		{"", []int{0, 3, 0, 1, 1, 1}},
		{"-json", []int{0, 3, 0, 1, 1, 1}},
		{"-fix", []int{2, 2, 2, 2, 2, 2}},
		{"-diff", []int{2, 2, 2, 2, 2, 2}},
		{"-tags=foo", []int{0, 3, 3, 1, 1, 1}},
		{"-tags=bar", []int{0, 3, 0, 1, 1, 1}},
		{"-test=false", []int{0, 3, 0, 1, 1, 1}},
		{"-borrow=false", []int{0, 0, 0, 1, 1, 1}},
	}
	for _, r := range rows {
		for i, tg := range targets {
			var args []string
			if r.mode != "" {
				args = append(args, r.mode)
			}
			args = append(args, strings.Fields(tg)...)
			_, stderr, code := a62Run(t, dir, nil, args...)
			if code != r.want[i] {
				t.Errorf("%q: exit %d, want %d\n%.300s", args, code, r.want[i], stderr)
			}
		}
	}
	// Extra rows of the table.
	for _, c := range []struct {
		args []string
		exit int
	}{
		{[]string{"-tags", "foo,bar", "./tagged"}, 3},
		{[]string{"-tags", "foo bar", "./tagged"}, 2},
		{[]string{"-nosuchflag", "./clean"}, 2},
		{[]string{"-json", "./clean", "./nope"}, 1},
		{[]string{"-json", "-borrow=false", "./find"}, 0},
		{[]string{"-json", "-tags=foo", "./tagged"}, 3},
		{[]string{"-flags", "-flags=false"}, 2}, // the one documented change (was 1)
		{[]string{"-flags"}, 0},
		{[]string{"-flags=false", "-flags"}, 0},
		{[]string{"-V=full"}, 0},
		{[]string{"-h"}, 0},
		{nil, 2},
	} {
		_, stderr, code := a62Run(t, dir, nil, c.args...)
		if code != c.exit {
			t.Errorf("%q: exit %d, want %d\n%.300s", c.args, code, c.exit, stderr)
		}
	}
}
