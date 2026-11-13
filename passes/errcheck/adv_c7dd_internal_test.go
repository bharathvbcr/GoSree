package errcheck

import (
	"fmt"
	"strings"
	"testing"
)

// Adversarial scale checks for ft-c7dd. They read the deterministic plan-work counter, never a
// clock. planWorkOf and closurePlanWorkHook come from closureplan_internal_test.go.

// fieldClosureSrc builds one function whose closure writes each of nFields distinct fields of a
// captured holder and is called `uses` times, then closes the first field.
func fieldClosureSrc(nFields, uses int) string {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"io\"\n\ntype H struct {\n")
	for i := 0; i < nFields; i++ {
		fmt.Fprintf(&b, "\tf%d io.WriteCloser\n", i)
	}
	b.WriteString("}\n\nfunc Big(h *H) {\n\tw := func() {\n")
	for i := 0; i < nFields; i++ {
		fmt.Fprintf(&b, "\t\t_, _ = h.f%d.Write(nil)\n", i)
	}
	b.WriteString("\t}\n")
	for i := 0; i < uses; i++ {
		b.WriteString("\tw()\n")
	}
	b.WriteString("\th.f0.Close()\n}\n")
	return b.String()
}

// manyClosuresSrc builds one function with n closures that all write one captured resource, and a
// Close of that resource at the end.
func manyClosuresSrc(n int) string {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"io\"\n\nfunc Big(w io.WriteCloser) {\n")
	for i := 0; i < n; i++ {
		b.WriteString("\tfunc() { _, _ = w.Write(nil) }()\n")
	}
	b.WriteString("\tw.Close()\n}\n")
	return b.String()
}

// nestedSrc builds closures nested depth deep, each writing its own captured variable.
func nestedSrc(depth int) string {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"io\"\n\nfunc Big(")
	for i := 0; i < depth; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "w%d", i)
	}
	b.WriteString(" io.WriteCloser) {\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "%sfunc() {\n%s_, _ = w%d.Write(nil)\n", strings.Repeat("\t", i+1), strings.Repeat("\t", i+2), i)
	}
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%s}()\n", strings.Repeat("\t", i+1))
	}
	b.WriteString("}\n")
	return b.String()
}

func TestAdvC7ddScaleCounters(t *testing.T) {
	t.Run("fields-x-uses", func(t *testing.T) {
		for _, c := range [][2]int{{100, 1}, {100, 10}, {400, 1}, {400, 10}, {1000, 1}, {1000, 10}, {2000, 1}, {2000, 10}} {
			w := planWorkOf(t, fieldClosureSrc(c[0], c[1]))
			t.Logf("fields=%d uses=%d plan work %d (fields*uses=%d)", c[0], c[1], w, c[0]*c[1])
		}
	})
	t.Run("many-closures-one-resource", func(t *testing.T) {
		var per []uint64
		for _, n := range []int{100, 400, 1600} {
			w := planWorkOf(t, manyClosuresSrc(n))
			t.Logf("closures=%d plan work %d (%d per closure)", n, w, w/uint64(n))
			per = append(per, w/uint64(n))
		}
		for i := 1; i < len(per); i++ {
			if per[i] > per[0]+per[0]/10 {
				t.Fatalf("work per closure grows with the number of closures sharing one resource: %v", per)
			}
		}
	})
}

// BREAK: the effect summary of a closure lists every write of the closures nested in it, so with
// each level writing its own captured variable the work per level grows with the depth (37, 50, 75
// and 125 per level at depths 25, 50, 100 and 200) and the total is quadratic. The counter, not a
// clock, is asserted.
func TestAdvC7ddBreakNestedDepthWork(t *testing.T) {
	var per []uint64
	for _, d := range []int{25, 50, 100, 200} {
		w := planWorkOf(t, nestedSrc(d))
		t.Logf("depth=%d plan work %d (%d per level)", d, w, w/uint64(d))
		per = append(per, w/uint64(d))
	}
	for i := 1; i < len(per); i++ {
		if per[i] > per[0]*2 {
			t.Fatalf("work per nesting level grows with the depth: %v", per)
		}
	}
}
