package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnchor checks that an annotation lands on the line it names inside its scope, and that every
// way of not matching is an error rather than a silently unannotated corpus.
func TestAnchor(t *testing.T) {
	src := strings.Split(`func TestA(t *testing.T) {
	x := 0
}

func TestB(t *testing.T) {
	y := 0
	y := 0
}

func TestA2(t *testing.T) {
	x := 0
}`, "\n")
	ann := func(scope, line string) annotation {
		return annotation{scope: scope, line: line}
	}
	if i, err := anchor(src, ann("func TestA(", "\tx := 0")); err != nil || i != 1 {
		t.Errorf("TestA x: got line %d, %v; want 1", i, err)
	}
	if i, err := anchor(src, ann("func TestA2(", "\tx := 0")); err != nil || i != 10 {
		t.Errorf("TestA2 x: got line %d, %v; want 10", i, err)
	}
	if i, err := anchor(src, ann("func TestB(", "")); err != nil || i != 4 {
		t.Errorf("TestB scope line: got line %d, %v; want 4", i, err)
	}
	second := ann("func TestB(", "\ty := 0")
	second.occurrence = 2
	if i, err := anchor(src, second); err != nil || i != 6 {
		t.Errorf("TestB second y: got line %d, %v; want 6", i, err)
	}
	third := second
	third.occurrence = 3
	for _, tc := range []struct {
		a    annotation
		want string
	}{
		{ann("func TestC(", "\tx := 0"), "not found"},
		{ann("func TestA", "\tx := 0"), "more than once"},
		{ann("func TestB(", "\tx := 0"), "no line"}, // x is only in other functions
		{ann("func TestA(", "x := 0"), "no line"},   // exact match, indentation included
		{ann("func TestB(", "\ty := 0"), "give an occurrence"},
		{third, "not 3"},
	} {
		if _, err := anchor(src, tc.a); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("anchor(%q, %q, %d): got %v, want an error containing %q", tc.a.scope, tc.a.line, tc.a.occurrence, err, tc.want)
		}
	}
}

// TestApplyAnnotations checks the two placements: a trailing directive is appended to its line, and a
// leading one is inserted above with the line's indentation, both found in the original text. A
// trailing directive on a line that already has a comment is refused: it would become part of that
// comment and never parse.
func TestApplyAnnotations(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\nfunc F(fn func()) {\n\tgo fn() // start\n\tx := 0\n}\n"
	write := func() {
		if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	anns := []annotation{
		{file: "p.go", scope: "func F(", directive: "//safe:iso fn", before: true},
		{file: "p.go", scope: "func F(", line: "\tgo fn() // start", directive: "//safe:unsafe reason", before: true},
		{file: "p.go", scope: "func F(", line: "\tx := 0", directive: "//safe:guarded_by mu"},
	}
	if err := applyAnnotations(dir, anns); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "p.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := "package p\n\n//safe:iso fn\nfunc F(fn func()) {\n\t//safe:unsafe reason\n\tgo fn() // start\n\tx := 0 //safe:guarded_by mu\n}\n"
	if string(got) != want {
		t.Errorf("annotated:\n%s\nwant:\n%s", got, want)
	}

	write()
	err = applyAnnotations(dir, []annotation{{file: "p.go", scope: "func F(", line: "\tgo fn() // start", directive: "//safe:unsafe reason"}})
	if err == nil || !strings.Contains(err.Error(), "already has a comment") {
		t.Errorf("trailing directive after a comment: got %v, want a refusal", err)
	}
}
