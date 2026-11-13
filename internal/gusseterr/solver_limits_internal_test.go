package gusseterr

import (
	"context"
	"errors"
	"fmt"
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
// is in one strongly connected component.
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
//
// The numbers moved when the order inside a component became callee-first (reverse finish order
// of the search over callers) instead of function-index order. In index order callCycle(9) flipped
// one function per sweep (nine changing sweeps and a confirming tenth: caps 3/9/10). In the new
// order the nil returned by the last function reaches every other function in the first sweep, in
// order, except function 0, which calls function 1 and was solved first: it flips in the second
// sweep, and a third sweep confirms. So the cycle needs a cap of 3 and fails closed at 2 and 1. A
// cap one under the sweeps needed fails closed, and the fixpoint is never exported unconverged:
// that is the property the old numbers pinned, and it is pinned on both a cycle and a hub ladder
// (whose back edges need one sweep per member, so its cap is large).
func TestOracleCallgraphCapFailsClosed(t *testing.T) {
	ctx := context.Background()
	funcs := callCycle(9)

	for _, cap := range []int{1, 2} {
		_, err := solveOracle(ctx, funcs, cap)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("did not converge within %d iterations", cap)) || !errors.Is(err, ErrCallgraphBudgetExceeded) {
			t.Fatalf("cap of %d sweeps over a 9-function cycle: err = %v, want a non-convergence error", cap, err)
		}
	}
	// Two sweeps change a summary; a third, unchanged sweep confirms the fixpoint. A fixpoint
	// reached on the last permitted sweep without that confirmation is not trusted.
	if _, err := solveOracle(ctx, funcs, 3); err != nil {
		t.Fatalf("cap of 3 sweeps converges (2 changing + 1 confirming): %v", err)
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
	// A shape that needs many sweeps: the exact cap passes and one under fails closed.
	ladder := hubLadder(12)
	var st phase1Stats
	if _, err := solveOracleLimited(ctx, ladder, 64, Phase1WorkFactor, Phase1WorkFloor, &st); err != nil {
		t.Fatal(err)
	}
	if st.sweeps < 10 {
		t.Fatalf("the hub ladder of 12 needed only %d sweeps; it must exercise a large cap", st.sweeps)
	}
	if _, err := solveOracle(ctx, ladder, int(st.sweeps)); err != nil {
		t.Fatalf("cap exactly the %d sweeps needed: %v", st.sweeps, err)
	}
	if _, err := solveOracle(ctx, ladder, int(st.sweeps)-1); err == nil {
		t.Fatalf("cap one under the %d sweeps needed was accepted", st.sweeps)
	}
	// The same chain left open is singleton components needing two sweeps each, at any length.
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
