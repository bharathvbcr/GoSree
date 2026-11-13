package errcheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// ft-e92e, design round: erre92e pins what ERR005 reports on the tip for every shape the per-store
// versioning design (DESIGN_ERR005_PATHS.md) touches. Nothing here asserts the design; it asserts
// today, so a later implementation round starts from a green suite and every change it makes shows
// up as a diff of a want, a golden line or an annotation.

// TestE92eBaseline: the wants of erre92e are today's output, line for line.
func TestE92eBaseline(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), errcheck.Analyzer, "erre92e")
}

// TestE92eSnapshot: the diagnostics, in the order the framework reports them, equal e92e.golden.
// The cgo build (Rust solver) and CGO_ENABLED=0 (the Go oracle) both compare against the one file,
// so their output is byte-identical on this package when both pass. Ten runs are identical.
func TestE92eSnapshot(t *testing.T) {
	first := diagnosticsOf(t, "erre92e")
	if len(first) == 0 {
		t.Fatal("no diagnostics")
	}
	for i := 1; i < 10; i++ {
		if got := diagnosticsOf(t, "erre92e"); strings.Join(got, "\n") != strings.Join(first, "\n") {
			t.Fatalf("run %d differs from run 0:\n%s\n---\n%s", i, strings.Join(got, "\n"), strings.Join(first, "\n"))
		}
	}
	checkGolden(t, "e92e.golden", first)
}

var targetMarker = regexp.MustCompile(`^// e92e-target: (report|stay silent|leave)$`)

// TestE92eTargetsAnnotated: every function of erre92e (other than the helpers that carry no ERR005
// site) says what the design intends, and the annotation is consistent with today's output:
//
//   - "stay silent" and "leave" functions report nothing today, or report exactly what they
//     report today (a leave shape is unchanged by definition), so a silent target is never a shape
//     that reports today;
//   - a "report" function either reports today (a shape that must keep reporting) or is a win the
//     implementation round has yet to deliver. The wins are logged.
func TestE92eTargetsAnnotated(t *testing.T) {
	path := filepath.Join(analysistest.TestData(), "src", "erre92e", "a.go")
	fset := token.NewFileSet()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	reported := map[int]bool{}
	for _, d := range diagnosticsOf(t, "erre92e") {
		var line int
		parts := strings.SplitN(d, ":", 4)
		if len(parts) < 3 {
			t.Fatalf("unparsable diagnostic %q", d)
		}
		for _, c := range parts[1] {
			line = line*10 + int(c-'0')
		}
		reported[line] = true
	}

	helpers := map[string]bool{"open": true, "note": true, "sink": true, "reset": true, "swapW": true}
	var wins []string
	count := map[string]int{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || helpers[fn.Name.Name] {
			continue
		}
		target := ""
		if fn.Doc != nil {
			for _, c := range fn.Doc.List {
				if m := targetMarker.FindStringSubmatch(c.Text); m != nil {
					if target != "" {
						t.Errorf("%s has two e92e-target lines", fn.Name.Name)
					}
					target = m[1]
				}
			}
		}
		if target == "" {
			t.Errorf("%s has no `// e92e-target:` line", fn.Name.Name)
			continue
		}
		count[target]++
		from, to := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
		today := false
		for l := from; l <= to; l++ {
			today = today || reported[l]
		}
		switch {
		case target == "stay silent" && today:
			t.Errorf("%s: target is silent but the tip reports it", fn.Name.Name)
		case target == "report" && !today:
			wins = append(wins, fn.Name.Name)
		}
	}
	sort.Strings(wins)
	t.Logf("targets: %v; wins still to deliver (%d): %s", count, len(wins), strings.Join(wins, ", "))
}
