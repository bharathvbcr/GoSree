package gusseterr

import (
	"context"
	"strings"
	"testing"
)

// callChain mirrors call_chain in rust/gosree-errsolve/tests/solver_limits_and_joins.rs:
// function i returns the concrete error pointer returned by function i+1, and the last
// function is external and may return nil. Visiting functions in index order flips exactly one
// summary per sweep.
func callChain(n int) []FuncIR {
	funcs := make([]FuncIR, n)
	for i := range funcs {
		if i+1 == n {
			funcs[i] = FuncIR{Flags: FuncReturnsConcreteErrPtr | FuncExternalMayNil}
			continue
		}
		funcs[i] = FuncIR{
			Flags:     FuncReturnsConcreteErrPtr,
			SlotFlags: []uint8{0},
			Blocks: []BlockIR{{
				Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: uint32(i + 1)}},
				Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: 0},
			}},
		}
	}
	return funcs
}

func TestOracleCallgraphCapFailsClosed(t *testing.T) {
	ctx := context.Background()
	funcs := callChain(10)

	_, err := solveOracle(ctx, funcs, 3)
	if err == nil || !strings.Contains(err.Error(), "did not converge within 3 iterations") {
		t.Fatalf("cap of 3 sweeps over a 10-function chain: err = %v, want a non-convergence error", err)
	}
	// Nine sweeps each flip one summary; a tenth, unchanged sweep confirms the fixpoint. A fixpoint
	// reached on the last permitted sweep without that confirmation is not trusted.
	if _, err := solveOracle(ctx, funcs, 9); err == nil {
		t.Fatal("cap of 9 sweeps must report non-convergence: the 9th sweep still changed a summary")
	}
	if _, err := solveOracle(ctx, funcs, 10); err != nil {
		t.Fatalf("cap of 10 sweeps converges (9 changing + 1 confirming): %v", err)
	}
	res, err := solveOracle(ctx, funcs, 64)
	if err != nil {
		t.Fatalf("converging solve: %v", err)
	}
	for i, b := range res.MayReturnNil {
		if !b {
			t.Fatalf("func %d summary is not may-nil after convergence", i)
		}
	}
}

func TestPublicCallgraphCapIsTheDocumentedConstant(t *testing.T) {
	funcs := callChain(MaxCallgraphIters + 2)
	_, err := SolveOracle(context.Background(), funcs)
	want := "did not converge within 4096 iterations"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("SolveOracle on a chain longer than MaxCallgraphIters: err = %v, want %q", err, want)
	}
}
