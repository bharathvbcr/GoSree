package ownership_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

const cutHead = `package p

import "sync"

type S struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n int
}
`

// cutLayers writes n layers of four pointer variables over f, each holding the addresses of all
// four below, and a closure that unlocks s.mu written into f through the top layer. Past about six
// layers the walk of f's address stops at cellWalkBudget.
func cutLayers(b *strings.Builder, n int) {
	typ := "func()"
	prev := []string{"f"}
	for layer := range n {
		typ = "*" + typ
		var cur []string
		for i := range 4 {
			name := fmt.Sprintf("v%d_%d", layer, i)
			cur = append(cur, name)
			fmt.Fprintf(b, "\tvar %s %s\n", name, typ)
			for _, p := range prev {
				fmt.Fprintf(b, "\t%s = &%s\n", name, p)
			}
		}
		prev = cur
	}
	b.WriteString("\tfunc() {\n")
	for _, p := range prev {
		fmt.Fprintf(b, "\t\t_ = %s\n", p)
	}
	b.WriteString("\t}()\n")
	fmt.Fprintf(b, "\t%s%s = func() { s.mu.Unlock() }\n", strings.Repeat("*", n), prev[0])
}

// cutShape is one place the call f() of a variable whose walk was cut can stand, with f called
// with no arguments, so what it may unlock is only what it captured.
func cutShape(shape string, n int) string {
	var b strings.Builder
	b.WriteString(cutHead)
	switch shape {
	case "call": // f() between the lock and the write
		b.WriteString("\nfunc (s *S) m() {\n\tf := func() {}\n")
		cutLayers(&b, n)
		b.WriteString("\ts.mu.Lock()\n\tf()\n\ts.n++ // REPORT\n\ts.mu.Unlock()\n}\n")
	case "go": // the goroutine may run the unlock at any time
		b.WriteString("\nfunc (s *S) m() {\n\tf := func() {}\n")
		cutLayers(&b, n)
		b.WriteString("\ts.mu.Lock()\n\tgo f()\n\ts.n++ // REPORT\n\ts.mu.Unlock()\n}\n")
	case "helper": // f() in a method the locked caller calls: its release summary
		b.WriteString("\nfunc (s *S) helper() {\n\tf := func() {}\n")
		cutLayers(&b, n)
		b.WriteString("\tf()\n}\n\nfunc (s *S) m() {\n\ts.mu.Lock()\n\ts.helper()\n\ts.n++ // REPORT\n\ts.mu.Unlock()\n}\n")
	case "closure": // f() in a closure called where it is made, which captures s
		b.WriteString("\nfunc (s *S) m() {\n\ts.mu.Lock()\n\tfunc() {\n\tf := func() {}\n")
		cutLayers(&b, n)
		b.WriteString("\tf()\n\t}()\n\ts.n++ // REPORT\n\ts.mu.Unlock()\n}\n")
	}
	return b.String()
}

// TestWalkCutShortLosesWhatIsHeld: when the walk of f's address stops at its budget, something it
// did not read may have stored into f a closure that unlocks whatever it captured. The call f()
// hands nothing over, so releasing only what it is handed releases nothing; the walk being cut
// must count as not knowing what is held after the call, wherever the call is: in the function
// that holds the lock (24d7618 fixed this one), started as a goroutine, or in a method or closure
// the locked function calls, which reach it through their release summaries. Before 0ce4de8 the
// walk was not cut at these sizes and each was reported; from 0ce4de8 on, each went silent from
// six layers.
func TestWalkCutShortLosesWhatIsHeld(t *testing.T) {
	for _, shape := range []string{"call", "go", "helper", "closure"} {
		for _, n := range []int{2, 6, 8} {
			src := cutShape(shape, n)
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
				t.Errorf("%s, %d layers: f may unlock s.mu, and the write at line %d was not reported (got %q)", shape, n, want, got)
			}
		}
	}
}
