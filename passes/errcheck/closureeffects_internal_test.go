package errcheck

import (
	"fmt"
	"strings"
	"testing"
)

// chainSrc builds closures nested depth deep over ONE captured resource: the innermost writes it
// and Big closes it, so the write is replayed through every layer.
func chainSrc(depth int) string {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"io\"\n\nfunc Big(w io.WriteCloser) {\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "%sfunc() {\n", strings.Repeat("\t", i+1))
	}
	fmt.Fprintf(&b, "%s_, _ = w.Write(nil)\n", strings.Repeat("\t", depth+1))
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%s}()\n", strings.Repeat("\t", i+1))
	}
	b.WriteString("\tw.Close()\n}\n")
	return b.String()
}

// nestedClosedSrc is nestedSrc (each level writes its own captured variable) with every variable
// closed by Big, so every variable is relevant to the summary of every layer above its writer.
func nestedClosedSrc(depth int) string {
	src := nestedSrc(depth)
	var b strings.Builder
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "\tw%d.Close()\n", i)
	}
	return strings.TrimSuffix(src, "}\n") + b.String() + "}\n"
}

// perUnit logs the plan work at each depth and fails when the work per unit grows by more than 10%
// from one depth to the next, where unit(d) is the size of the input the work is spent on.
func perUnit(t *testing.T, src func(int) string, unit func(int) uint64) {
	t.Helper()
	var prev float64
	for _, d := range []int{25, 50, 100, 200} {
		w := planWorkOf(t, src(d))
		per := float64(w) / float64(unit(d))
		t.Logf("depth=%d plan work %d, per unit %.3f", d, w, per)
		if prev != 0 && per > prev*1.10 {
			t.Fatalf("work per unit grows more than 10%% at depth %d: %.3f after %.3f", d, per, prev)
		}
		prev = per
	}
}

// A resource captured through every layer costs the same constant per layer: the summaries are
// built once each and a layer adds one entry, not the layers below it.
func TestClosureEffectsChainIsLinear(t *testing.T) {
	perUnit(t, chainSrc, func(d int) uint64 { return uint64(d) })
}

// With a distinct variable per layer that Big closes, the work is the size of the captured-variable
// lists the closures carry (the layer at depth i names d-i variables, d(d+1)/2 in all), which is the
// size of the SSA the analyzer is handed: one entry of work per entry of input, each built once.
// That is quadratic in the depth (1,617 at 25 and 65,492 at 200, against 494 and 3,994 without the
// replay): every layer's summary lists the variables written below it. Only a variable some code
// closes by name enters a summary, so nestedSrc, which closes nothing, costs what it did before.
func TestClosureEffectsNestedClosedIsLinearInInput(t *testing.T) {
	perUnit(t, nestedClosedSrc, func(d int) uint64 { return uint64(d*(d+1)/2 + d) })
}
