package main

import (
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestCensusClasses runs the census over a fixture module with one dependency type per class and
// checks each hatch's worst class and closure.
func TestCensusClasses(t *testing.T) {
	dir, err := filepath.Abs("testdata/census")
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: dir}, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("fixture did not type-check")
	}
	const file, scope = "internal/svc/svc.go", "func Serve("
	var rows []annotation
	for _, name := range []string{"f", "p", "g", "s", "t", "e", "h", "n", "so", "w", "q", "missing"} {
		rows = append(rows, annotation{file: file, scope: scope, verdict: "IMM_BLOCKED:" + name})
	}
	// A test-file row and a row with another verdict are not counted.
	rows = append(rows,
		annotation{file: "internal/svc/svc_test.go", scope: scope, verdict: "IMM_BLOCKED:f"},
		annotation{file: file, scope: scope, verdict: "FP:JOIN_SHAPE"})

	rep, err := censusOf(pkgs, summaries(analyze(pkgs)), rows, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"f": classOK, "p": classWLock, "g": classG, "s": classW, "t": classCWritten, "e": classX, "h": classWLock,
		"n": classCFrozen, "so": classCWritten, "w": classOK, "q": classG,
		"missing": "(unresolved)",
	}
	if len(rep.Hatches) != len(want) {
		t.Fatalf("got %d hatches, want %d: %+v", len(rep.Hatches), len(want), rep.Hatches)
	}
	for _, h := range rep.Hatches {
		if h.Worst != want[h.Name] {
			t.Errorf("hatch %s (root %s): worst %s, want %s; closure %v", h.Name, h.Root, h.Worst, want[h.Name], h.Closure)
		}
	}
	if rep.Unresolved != 1 {
		t.Errorf("unresolved = %d, want 1", rep.Unresolved)
	}
	// p, s and h reach a post-construction write; g's locked writes are guardable, not counted.
	if rep.WithWrites != 3 {
		t.Errorf("with post-construction writes = %d, want 3", rep.WithWrites)
	}
	for _, ct := range rep.Types {
		if ct.Type == "example.com/censusfixture/internal/svc.Frozen" && !slices.Contains(ct.Children, "example.com/censusfixture/internal/svc.Leaf") {
			t.Errorf("Frozen's interface field store of *Leaf was not followed: children %v", ct.Children)
		}
	}
}

func TestModulePath(t *testing.T) {
	for in, want := range map[string]string{
		"module example.com/m\n\ngo 1.27\n": "example.com/m",
		"// c\nmodule \"quoted/m\"\n":       "quoted/m",
		"modulex foo\n":                     "",
		"go 1.27\n":                         "",
	} {
		if got := modulePath([]byte(in)); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", in, got, want)
		}
	}
}
