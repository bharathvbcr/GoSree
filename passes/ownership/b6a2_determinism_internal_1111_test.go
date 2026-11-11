package ownership

import (
	"go/token"
	"go/types"
	"slices"
	"testing"
)

// earliest receives events collected from a map walk, so its answer must not depend on the order
// of events that share a position.
func TestEarliestIgnoresInputOrderOnTies(t *testing.T) {
	evs := []event{
		{pos: 20, desc: "package variable c"},
		{pos: 10, desc: "package variable b"},
		{pos: 10, desc: "package variable a"},
		{pos: 10, desc: "package variable d"},
	}
	want := earliest(slices.Clone(evs))
	if want.desc != "package variable a" {
		t.Fatalf("earliest = %q, want the smallest description at the earliest position", want.desc)
	}
	for i := range evs {
		rot := append(slices.Clone(evs[i:]), evs[:i]...)
		if got := earliest(rot); got.desc != want.desc {
			t.Fatalf("rotation %d: earliest = %q, want %q", i, got.desc, want.desc)
		}
		slices.Reverse(rot)
		if got := earliest(rot); got.desc != want.desc {
			t.Fatalf("reversed rotation %d: earliest = %q, want %q", i, got.desc, want.desc)
		}
	}
}

// Two variables declared at one position: the map walk must not choose between them.
func TestVarAtPosIsDeterministic(t *testing.T) {
	pkg := types.NewPackage("p", "p")
	set := map[*types.Var]bool{}
	for _, name := range []string{"m", "k", "z", "a", "q", "b", "x", "c", "y", "d"} {
		set[types.NewVar(token.Pos(5), pkg, name, types.Typ[types.Int])] = true
	}
	for range 50 {
		if v := varAtPos(set, 5); v == nil || v.Name() != "a" {
			t.Fatalf("varAtPos = %v, want the smallest name a", v)
		}
	}
	if v := varAtPos(set, 6); v != nil {
		t.Fatalf("varAtPos(6) = %v, want nil", v)
	}
}
