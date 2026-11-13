package errcheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// The adversary's fixtures for ft-c7dd's ERR005 half (branch run2/c7dd-err005-wip, beb8a61), the
// spec of ft-fe6e. The wants in errc7ddadv, errc7ddadvgap and errc7ddadvdup state what a design
// with access-path keying and a replay of every closure would report. ft-fe6e does the closure
// replay only (a closure called once, on every path to a return, going nowhere else), so a want on
// a field path, an embedded field, an escaping closure or a deferred closure stays unreported and
// is pinned in adv_fe6e_miss.golden; what is reported is pinned in adv_fe6e.golden. The fixtures
// are unchanged.

var c7ddSpecPackages = []string{"errc7ddadv", "errc7ddadvfp", "errc7ddadvmiss", "errc7ddadvgap", "errc7ddadvdup"}

// BREAK (new false positives): nothing in errc7ddadvfp is reported on 90fa6e9 or on the tip ft-fe6e
// started from, and in every function the Close is of a resource that was not written. The package
// has no want markers.
func TestAdvC7ddBreakNewFalsePositives(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errc7ddadvfp")
}

// BREAK (new misses): each ERR005 here is reported on 90fa6e9 and on the tip ft-fe6e started from.
// The plain twin aliasThenReplaceTwin is still reported.
func TestAdvC7ddBreakNewMiss(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "errc7ddadvmiss")
}

// diagnosticsOf runs the analyzer on the packages and returns every diagnostic, in the order the
// framework reports them, as "package/file:line:col: message".
func diagnosticsOf(t *testing.T, pkgs ...string) []string {
	t.Helper()
	var out []string
	for _, r := range analysistest.Run(&recordT{}, analysistest.TestData(), errcheck.Analyzer, pkgs...) {
		for _, d := range r.Diagnostics {
			p := r.Pass.Fset.Position(d.Pos)
			out = append(out, fmt.Sprintf("%s/%s:%d:%d: %s", r.Pass.Pkg.Path(), filepath.Base(p.Filename), p.Line, p.Column, d.Message))
		}
	}
	return out
}

var wantMarker = regexp.MustCompile(`// want "`)

// wantedLines is "package/file:line" for each line of the packages' sources that carries a want.
func wantedLines(t *testing.T, pkgs ...string) []string {
	t.Helper()
	var out []string
	for _, pkg := range pkgs {
		dir := filepath.Join(analysistest.TestData(), "src", pkg)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources for %s: %v", pkg, err)
		}
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(src), "\n") {
				if wantMarker.MatchString(line) {
					out = append(out, fmt.Sprintf("%s/%s:%d", pkg, filepath.Base(f), i+1))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func lineOf(diag string) string {
	parts := strings.SplitN(diag, ":", 3)
	return parts[0] + ":" + parts[1]
}

func checkGolden(t *testing.T, name string, lines []string) {
	t.Helper()
	golden := filepath.Join(analysistest.TestData(), name)
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "\n") + "\n"; got != string(want) {
		t.Fatalf("differs from %s:\n%s", golden, got)
	}
}

// Ten runs give identical diagnostics in identical order. What is reported equals adv_fe6e.golden,
// which both engines (cgo/Rust and CGO_ENABLED=0/oracle) must reproduce byte for byte. Nothing is
// reported on a line without a want, and no Close line is reported twice (once as a Close and once
// as a deferred Close). What the fixtures want and the analyzer does not report equals
// adv_fe6e_miss.golden.
func TestAdvC7ddSpecSnapshot(t *testing.T) {
	first := diagnosticsOf(t, c7ddSpecPackages...)
	if len(first) == 0 {
		t.Fatal("no diagnostics")
	}
	for i := 1; i < 10; i++ {
		got := diagnosticsOf(t, c7ddSpecPackages...)
		if strings.Join(got, "\n") != strings.Join(first, "\n") {
			t.Fatalf("run %d differs from run 0:\n%s\n---\n%s", i, strings.Join(got, "\n"), strings.Join(first, "\n"))
		}
	}
	sorted := append([]string(nil), first...)
	sort.Strings(sorted)
	checkGolden(t, "adv_fe6e.golden", sorted)

	wanted := map[string]bool{}
	for _, l := range wantedLines(t, c7ddSpecPackages...) {
		wanted[l] = true
	}
	reported, err005 := map[string]bool{}, map[string]bool{}
	for _, d := range first {
		l := lineOf(d)
		if !wanted[l] {
			t.Errorf("reported on a line with no want: %s", d)
		}
		if strings.Contains(d, "ERR005") {
			if err005[l] {
				t.Errorf("more than one ERR005 at %s", l)
			}
			err005[l] = true
		}
		reported[l] = true
	}
	var missed []string
	for l := range wanted {
		if !reported[l] {
			missed = append(missed, l)
		}
	}
	sort.Strings(missed)
	checkGolden(t, "adv_fe6e_miss.golden", missed)
}
