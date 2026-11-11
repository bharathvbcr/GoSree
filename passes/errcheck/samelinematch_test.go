package errcheck

import (
	"go/token"
	"testing"
)

func TestSameLineMatchIsDeterministic(t *testing.T) {
	fset := token.NewFileSet()
	f := fset.AddFile("a.go", -1, 200)
	f.SetLines([]int{0, 50, 100, 150})
	// Three entries on line 2 (offsets 50..99) and one on line 3.
	entries := map[token.Pos]string{
		f.Pos(70):  "far",
		f.Pos(58):  "left",
		f.Pos(62):  "right",
		f.Pos(110): "other-line",
	}
	call := f.Pos(60) // equidistant from 58 and 62: the lower position wins
	for i := range 200 {
		got, key, ok := sameLineMatch(fset, entries, call, nil, func(string) bool { return true })
		if !ok || got != "left" || key != f.Pos(58) {
			t.Fatalf("iteration %d: got (%q, %v, %v), want left at %v", i, got, key, ok, f.Pos(58))
		}
	}

	consumed := map[token.Pos]bool{f.Pos(58): true}
	got, _, ok := sameLineMatch(fset, entries, call, consumed, func(string) bool { return true })
	if !ok || got != "right" {
		t.Fatalf("with left consumed: got (%q, %v), want right", got, ok)
	}

	if _, _, ok := sameLineMatch(fset, entries, f.Pos(120), nil, func(v string) bool { return v != "other-line" }); ok {
		t.Fatal("an entry the predicate rejects must not match")
	}
}
