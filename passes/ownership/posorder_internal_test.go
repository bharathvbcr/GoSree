package ownership

import (
	"go/token"
	"sort"
	"testing"

	"golang.org/x/tools/go/analysis"
)

// TestFirstPositionDoesNotFollowLoadOrder: where several events explain a finding, or a package
// variable has several writes, the message names the first. "First" used to be the lowest
// token.Pos, and token.Pos orders files by when they were added to the file set, so the same code
// loaded in another order named another reason: on the reference service ten rows named one of
// three package variables from run to run. The two file sets below hold the same two files added
// in opposite orders, and the choice must be the same in both.
func TestFirstPositionDoesNotFollowLoadOrder(t *testing.T) {
	pick := func(order []string) (reason string, writeFile string) {
		fset := token.NewFileSet()
		at := map[string]token.Pos{}
		for _, name := range order {
			f := fset.AddFile(name, -1, 100)
			f.SetLines([]int{0, 10, 20, 30})
			at[name] = f.Pos(25)
		}
		st := &state{pass: &analysis.Pass{Fset: fset}}
		e := st.earliest([]event{
			{pos: at["b.go"], desc: "package variable inB"},
			{pos: token.NoPos, desc: "a reason with no position"},
			{pos: at["a.go"], desc: "package variable inA"},
		})
		ws := []token.Pos{at["b.go"], at["a.go"]}
		sort.Slice(ws, func(i, j int) bool { return st.comparePos(ws[i], ws[j]) < 0 })
		return e.desc, fset.File(ws[0]).Name()
	}
	for _, order := range [][]string{{"a.go", "b.go"}, {"b.go", "a.go"}} {
		reason, write := pick(order)
		if reason != "package variable inA" || write != "a.go" {
			t.Errorf("files loaded as %v: reason %q, first write in %s; want the ones in a.go", order, reason, write)
		}
	}
}

// TestFirstPositionWithinAFile: inside one file the first is the earlier place, a position that is
// not valid comes last, and two events at one position are told apart by their description.
func TestFirstPositionWithinAFile(t *testing.T) {
	fset := token.NewFileSet()
	f := fset.AddFile("a.go", -1, 100)
	st := &state{pass: &analysis.Pass{Fset: fset}}
	if got := st.earliest([]event{{pos: f.Pos(40), desc: "later"}, {pos: f.Pos(7), desc: "earlier"}, {desc: "nowhere"}}); got.desc != "earlier" {
		t.Errorf("got %q, want the event earlier in the file", got.desc)
	}
	if got := st.earliest([]event{{pos: f.Pos(7), desc: "b"}, {pos: f.Pos(7), desc: "a"}}); got.desc != "a" {
		t.Errorf("got %q, want the description that sorts first", got.desc)
	}
	if got := st.earliest([]event{{desc: "y"}, {desc: "x"}}); got.desc != "x" {
		t.Errorf("got %q, want the description that sorts first where neither has a position", got.desc)
	}
}
