package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// These fixtures read a cycle member's provisional (scratch) result a second time in one round.
// That read used to mark every trace in progress provisional, the cycle head included, so the head
// stopped after one round with an unsettled result and the write through it went unreported, with
// no ImmCut either. The want on `return f(&h.v, ...)` is a report the checker made before the
// fixpoint existed too (85357e9); why it is made there is not established.

func runImmScratch(t *testing.T, pkg, src string) {
	t.Helper()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{pkg + "/" + pkg + ".go": src})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, pkg)
}

// TestImmScratchSecondReadInRound: f calls the cycle member g at two sites.
func TestImmScratchSecondReadInRound(t *testing.T) {
	runImmScratch(t, "immtwo", `package immtwo

//gosree:imm
type Imm struct{ v int } // want Imm:"imm"

var c int

func f(p *int, h *Imm, n int) *int {
	switch c {
	case 1:
		return g(p, h, n)
	case 2:
		return g(p, h, n+1)
	case 3:
		return f(&h.v, h, n-1) // want "write to a field of"
	}
	return p
}

func g(p *int, h *Imm, n int) *int { return f(p, h, n) }

func useF(q *int, x *Imm) { *f(q, x, 3) = 1 } // want "write to a field of"
`)
}

// TestImmScratchSecondReadThroughHelper: the second read of g comes through k.
func TestImmScratchSecondReadThroughHelper(t *testing.T) {
	runImmScratch(t, "immhelp", `package immhelp

//gosree:imm
type Imm struct{ v int } // want Imm:"imm"

var c int

func f(p *int, h *Imm, n int) *int {
	switch c {
	case 1:
		return g(p, h, n)
	case 2:
		return k(p, h, n)
	case 3:
		return f(&h.v, h, n-1) // want "write to a field of"
	}
	return p
}

func g(p *int, h *Imm, n int) *int { return f(p, h, n) }

func k(p *int, h *Imm, n int) *int { return g(p, h, n) }

func useF(q *int, x *Imm) { *f(q, x, 3) = 1 } // want "write to a field of"
`)
}

// TestImmScratchThreeCycleEitherEntry: the cycle f -> g -> k -> {f, g}, entered from k or from f
// first. Both writes are reported in either order.
func TestImmScratchThreeCycleEitherEntry(t *testing.T) {
	const head = `package immthree

//gosree:imm
type Imm struct{ v int } // want Imm:"imm"

var c int

func f(p *int, h *Imm) *int {
	if c == 1 {
		return g(p, h)
	}
	if c == 2 {
		return f(&h.v, h) // want "write to a field of"
	}
	return p
}

func g(p *int, h *Imm) *int { return k(p, h) }

func k(p *int, h *Imm) *int {
	if c == 3 {
		return f(p, h)
	}
	return g(p, h)
}
`
	for name, body := range map[string]string{
		"k first": `
func useK(q *int, x *Imm) { *k(q, x) = 1 } // want "write to a field of"

func useF(q *int, x *Imm) { *f(q, x) = 1 } // want "write to a field of"
`,
		"f first": `
func useF(q *int, x *Imm) { *f(q, x) = 1 } // want "write to a field of"

func useK(q *int, x *Imm) { *k(q, x) = 1 } // want "write to a field of"
`,
	} {
		t.Run(name, func(t *testing.T) { runImmScratch(t, "immthree", head+body) })
	}
}
