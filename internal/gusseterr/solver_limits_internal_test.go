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

// TestPublicCallgraphCapScalesWithFuncCount: the cap is derived from the function count (the
// proven convergence bound), so chains longer than any fixed constant still converge, while
// the internal capped entry keeps the non-convergence error reachable.
func TestPublicCallgraphCapScalesWithFuncCount(t *testing.T) {
	for _, n := range []int{4097, MaxFuncs} {
		res, err := SolveOracle(context.Background(), callChain(n))
		if err != nil {
			t.Fatalf("chain of %d functions: %v", n, err)
		}
		for i, b := range res.MayReturnNil {
			if !b {
				t.Fatalf("chain of %d: func %d summary not converged", n, i)
			}
		}
	}
}

// phiMergedTuples is a function whose two tuples bind on exclusive branches and are merged by
// a phi, so canonicalTuples has a class of two to examine.
func phiMergedTuples() FuncIR {
	return FuncIR{
		SlotFlags: []uint8{0, SlotIsError, 0, SlotIsError, 0, SlotIsError, 0},
		Tuples:    []TupleMeta{{ErrSlot: 1}, {ErrSlot: 3}},
		Blocks: []BlockIR{
			{Term: Terminator{Kind: TermCond, Target: 1, FalseBB: 2, CondKind: CondOpaque, CondSlot: NoneU32}},
			{Ops: []OpIR{{Kind: OpBindTuple, TupleID: 0, SlotA: 1, Companions: []uint32{2}}}, Term: Terminator{Kind: TermJump, Target: 3}},
			{Ops: []OpIR{{Kind: OpBindTuple, TupleID: 1, SlotA: 3, Companions: []uint32{4}}}, Term: Terminator{Kind: TermJump, Target: 3}},
			{
				Phis: []PhiIR{
					{DstSlot: 6, Incoming: []PhiEdge{{PredBB: 1, SrcSlot: 2}, {PredBB: 2, SrcSlot: 4}}},
					{DstSlot: 5, Incoming: []PhiEdge{{PredBB: 1, SrcSlot: 1}, {PredBB: 2, SrcSlot: 3}}},
				},
				Term: Terminator{Kind: TermStop},
			},
		},
	}
}

func TestCanonicalTuplesHonoursCancellation(t *testing.T) {
	fn := phiMergedTuples()
	canon, err := canonicalTuples(context.Background(), &fn)
	if err != nil || len(canon) != 2 || canon[1] != 0 {
		t.Fatalf("live ctx: canon = %v, err = %v, want tuple 1 merged into 0", canon, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := canonicalTuples(ctx, &fn); err == nil {
		t.Fatal("cancelled ctx: canonicalTuples must stop with the context error")
	}
	if _, err := SolveOracle(ctx, []FuncIR{fn}); err == nil {
		t.Fatal("cancelled ctx: SolveOracle must fail")
	}
}
