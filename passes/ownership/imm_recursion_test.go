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

// TestImmTraceSelfRecursionThroughParam: r hands &h.v down to itself through p and returns p. The
// write through r's result is a write to the imm type; it is found by solving r's returns as a
// fixpoint, where a recursive call that contributed nothing lost it.
func TestImmTraceSelfRecursionThroughParam(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"immself/immself.go": `package immself

//gosree:imm
type Imm struct{ v int } // want Imm:"imm"

func r(p *int, h *Imm, n int) *int {
	if n > 0 {
		return r(&h.v, h, n-1)
	}
	return p
}

func useR(q *int, x *Imm) { *r(q, x, 3) = 1 } // want "write to a field of"
`})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "immself")
}

// TestImmTraceMutualRecursionTerminates: six functions in one cycle, each with eight recursive
// return sites, half to itself and half to the next. The code before the fixpoint did not finish
// in 300 s.
func TestImmTraceMutualRecursionTerminates(t *testing.T) {
	var b strings.Builder
	b.WriteString("package immscc\n\n//gosree:imm\ntype Node struct { // want Node:\"imm\"\n\tkind int\n\tmark bool\n")
	for i := range 8 {
		fmt.Fprintf(&b, "\tc%d *Node\n", i)
	}
	b.WriteString("}\n\n")
	for f := range 6 {
		fmt.Fprintf(&b, "func f%d(n *Node, p *int, k int) *int {\n\tswitch n.kind {\n", f)
		for i := range 8 {
			callee := f
			if i%2 == 0 {
				callee = (f + 1) % 6
			}
			fmt.Fprintf(&b, "\tcase %d:\n\t\treturn f%d(n.c%d, p, k)\n", i, callee, i)
		}
		b.WriteString("\t}\n\treturn p\n}\n\n")
	}
	for f := range 6 {
		fmt.Fprintf(&b, "func use%d(r *Node, q *int) { *f%d(r, q, 1) = 1 }\n", f, f)
	}
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"immscc/immscc.go": b.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	start := time.Now()
	analysistest.Run(t, dir, ownership.Analyzer, "immscc")
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("analysing a six-function recursion cycle took %s (want < 20s)", elapsed)
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
