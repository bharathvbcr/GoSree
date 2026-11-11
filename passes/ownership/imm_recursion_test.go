package ownership_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestImmTraceCycleOrderIndependent: f and g call each other and f returns the address of an imm
// field. Tracing *f(y) first computes g's returns while f is still being traced; caching that
// partial result for the pass made the later *g(x) write go unreported. Both writes are reported
// whichever is checked first.
func TestImmTraceCycleOrderIndependent(t *testing.T) {
	const head = `package immcycle

//gosree:imm
type Imm struct{ v int } // want Imm:"imm"

var c bool

func f(h *Imm) *int {
	if c {
		return g(h)
	}
	return &h.v
}

func g(h *Imm) *int { return f(h) }
`
	for name, body := range map[string]string{
		"f first": `
func useF(y *Imm) { *f(y) = 2 } // want "write to a field of"

func useG(x *Imm) { *g(x) = 1 } // want "write to a field of"
`,
		"g first": `
func useG(x *Imm) { *g(x) = 1 } // want "write to a field of"

func useF(y *Imm) { *f(y) = 2 } // want "write to a field of"
`,
	} {
		t.Run(name, func(t *testing.T) {
			dir, cleanup, err := analysistest.WriteFiles(map[string]string{"immcycle/immcycle.go": head + body})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			analysistest.Run(t, dir, ownership.Analyzer, "immcycle")
		})
	}
}

// TestImmTraceRecursiveReturnsTerminate: a write through the result of a recursive function with
// many `return find(...)` sites. Tracing the written address into the callee's returns once per
// return ordering took exponential time (k=9: 34 s, k=12: over 180 s); each callee is now traced
// once per pass. The write to the imm type is still reported.
func TestImmTraceRecursiveReturnsTerminate(t *testing.T) {
	const k = 16
	var b strings.Builder
	b.WriteString(`package immrec

//gosree:imm
type Node struct { // want Node:"imm"
	kind int
	mark bool
`)
	for i := range k {
		fmt.Fprintf(&b, "\tc%d *Node\n", i)
	}
	b.WriteString(`}

func find(n *Node, k int) *Node {
	switch n.kind {
`)
	for i := range k {
		fmt.Fprintf(&b, "\tcase %d:\n\t\treturn find(n.c%d, k)\n", i, i)
	}
	b.WriteString(`	}
	return n
}

func use(r *Node) {
	find(r, 1).mark = true // want "write to a field of"
}
`)
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"immrec/immrec.go": b.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	start := time.Now()
	analysistest.Run(t, dir, ownership.Analyzer, "immrec")
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("analysing a recursive function with %d return sites took %s (want < 20s)", k, elapsed)
	}
}
