package ownership_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// layeredUnlockClosure builds n layers of four pointer variables over f, each holding the
// addresses of all four below, and writes a closure that unlocks s.mu through the top layer.
// f is called with no arguments, so what it may unlock is only what it captured.
func layeredUnlockClosure(n int) string {
	var b strings.Builder
	b.WriteString(`package p

import "sync"

type S struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n int
}

func (s *S) m() {
	f := func() {}
`)
	typ := "func()"
	prev := []string{"f"}
	for layer := range n {
		typ = "*" + typ
		var cur []string
		for i := range 4 {
			name := fmt.Sprintf("v%d_%d", layer, i)
			cur = append(cur, name)
			fmt.Fprintf(&b, "\tvar %s %s\n", name, typ)
			for _, p := range prev {
				fmt.Fprintf(&b, "\t%s = &%s\n", name, p)
			}
		}
		prev = cur
	}
	b.WriteString("\tfunc() {\n")
	for _, p := range prev {
		fmt.Fprintf(&b, "\t\t_ = %s\n", p)
	}
	b.WriteString("\t}()\n")
	fmt.Fprintf(&b, "\t%s%s = func() { s.mu.Unlock() }\n", strings.Repeat("*", n), prev[0])
	b.WriteString("\ts.mu.Lock()\n\tf()\n\ts.n++ // REPORT\n\ts.mu.Unlock()\n}\n")
	return b.String()
}

// TestWalkCutShortLosesWhatIsHeld: when the walk of f's address stops at its budget, something it
// did not read may have stored into f a closure that unlocks whatever it captured. The call f()
// hands nothing over, so releasing only what it is handed releases nothing; the walk being cut
// must instead count as not knowing what is held after the call. At 6 or more layers the budget
// (cellWalkBudget) cut the walk and the write after f() went unreported (0ce4de8).
func TestWalkCutShortLosesWhatIsHeld(t *testing.T) {
	for _, n := range []int{2, 6, 8} {
		src := layeredUnlockClosure(n)
		want := 0
		for i, l := range strings.Split(src, "\n") {
			if strings.Contains(l, "// REPORT") {
				want = i + 1
			}
		}
		found := false
		got := advDiagLines(t, src, "SAFE009")
		for _, d := range got {
			if strings.HasPrefix(d, strconv.Itoa(want)+":") {
				found = true
			}
		}
		if !found {
			t.Errorf("%d layers: f may unlock s.mu, and the write at line %d was not reported (got %q)", n, want, got)
		}
	}
}
