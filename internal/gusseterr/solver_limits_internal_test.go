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

// callCycle is callChain closed into a cycle: function i returns the pointer returned by function
// i+1, and the last function returns a literal nil pointer and calls function 0. Every function
// is in one strongly connected component, so visiting its members in index order flips exactly
// one summary per sweep, as callChain did for the whole package before phase 1 went callee-first.
func callCycle(n int) []FuncIR {
	funcs := callChain(n)
	last := &funcs[n-1]
	*last = FuncIR{
		Flags:     FuncReturnsConcreteErrPtr,
		SlotFlags: []uint8{0},
		Blocks: []BlockIR{{
			Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: 0}},
			Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: NoneU32, RetFlags: RetConcreteLiteralNil},
		}},
	}
	return funcs
}

// TestOracleCallgraphCapFailsClosed: the sweep cap applies to each strongly connected component.
// It used to be exercised with callChain(10); callee-first order solves a chain one function per
// component, so the cap needs a cycle to be reached, which callCycle(9) gives with the same
// numbers (nine changing sweeps and a confirming tenth).
func TestOracleCallgraphCapFailsClosed(t *testing.T) {
	ctx := context.Background()
	funcs := callCycle(9)

	_, err := solveOracle(ctx, funcs, 3)
	if err == nil || !strings.Contains(err.Error(), "did not converge within 3 iterations") {
		t.Fatalf("cap of 3 sweeps over a 9-function cycle: err = %v, want a non-convergence error", err)
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
	// The same chain left open is ten singleton components, each needing two sweeps.
	if _, err := solveOracle(ctx, callChain(10), 2); err != nil {
		t.Fatalf("an acyclic chain needs two sweeps per component under any length: %v", err)
	}
	if _, err := solveOracle(ctx, callChain(10), 1); err == nil {
		t.Fatal("a cap of one sweep cannot confirm a fixpoint")
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
