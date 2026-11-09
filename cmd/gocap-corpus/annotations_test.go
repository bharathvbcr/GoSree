package main

import (
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
	if i, err := anchor(src, ann("func TestA2(", "\tx := 0")); err != nil || i != 9 {
		t.Errorf("TestA2 x: got line %d, %v; want 9", i, err)
	}
	for _, tc := range []struct {
		a    annotation
		want string
	}{
		{ann("func TestC(", "\tx := 0"), "not found"},
		{ann("func TestA", "\tx := 0"), "more than once"},
		{ann("func TestB(", "\tx := 0"), "no line"}, // x is only in later functions
		{ann("func TestA(", "x := 0"), "no line"},   // exact match, indentation included
	} {
		if _, err := anchor(src, tc.a); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("anchor(%q, %q): got %v, want an error containing %q", tc.a.scope, tc.a.line, err, tc.want)
		}
	}
}
