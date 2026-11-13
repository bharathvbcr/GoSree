package gusseterr

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
)

// scanSweeps is the phase 1 of solveOracle before its sweeps were driven by heaps: every sweep
// scans every index in order and solves the dirty ones. It returns the summaries and the number
// of sweeps run, the last one being the confirming sweep that changed nothing.
func scanSweeps(t *testing.T, funcsIn []FuncIR) ([]bool, int) {
	t.Helper()
	ctx := context.Background()
	funcs := make([]FuncIR, len(funcsIn))
	for i := range funcsIn {
		var err error
		if funcs[i], err = withCanonicalTuples(ctx, funcsIn[i]); err != nil {
			t.Fatal(err)
		}
	}
	n := len(funcs)
	mayReturnNil := make([]bool, n)
	for i, fn := range funcs {
		if fn.Flags&FuncExternalMayNil != 0 || (fn.Flags&FuncReturnsConcreteErrPtr != 0 && len(fn.Blocks) == 0) {
			mayReturnNil[i] = true
		}
	}
	callers := make([][]uint32, n)
	for idx := range funcs {
		for _, b := range funcs[idx].Blocks {
			for _, op := range b.Ops {
				if op.Kind == OpCallConcretePtr {
					if l := len(callers[op.SlotB]); l == 0 || callers[op.SlotB][l-1] != uint32(idx) {
						callers[op.SlotB] = append(callers[op.SlotB], uint32(idx))
					}
				}
			}
		}
	}
	dirty := make([]bool, n)
	for i := range dirty {
		dirty[i] = true
	}
	for sweeps := 1; ; sweeps++ {
		anyChanged := false
		for idx := range funcs {
			if mayReturnNil[idx] || !dirty[idx] || funcs[idx].Flags&FuncReturnsConcreteErrPtr == 0 {
				continue
			}
			dirty[idx] = false
			fnMayNil, _, err := solveFuncOracle(ctx, uint32(idx), &funcs[idx], mayReturnNil, false)
			if err != nil {
				t.Fatal(err)
			}
			if fnMayNil {
				mayReturnNil[idx] = true
				anyChanged = true
				for _, c := range callers[idx] {
					dirty[c] = true
				}
			}
		}
		if !anyChanged {
			return mayReturnNil, sweeps
		}
	}
}

// randomCallGraph is n functions returning a concrete error pointer, each calling up to three
// others chosen at random (self-calls and cycles included) on separate branches and returning
// one of the results, or a fresh non-nil pointer. A few are external may-nil leaves.
func randomCallGraph(r *rand.Rand, n int) []FuncIR {
	funcs := make([]FuncIR, n)
	for i := range funcs {
		if r.IntN(8) == 0 {
			funcs[i] = FuncIR{Flags: FuncReturnsConcreteErrPtr | FuncExternalMayNil}
			continue
		}
		k := r.IntN(4) // calls
		if k == 0 {
			funcs[i] = FuncIR{
				Flags:     FuncReturnsConcreteErrPtr,
				SlotFlags: []uint8{0},
				Blocks: []BlockIR{{
					Ops:  []OpIR{{Kind: OpAssignSlot, SiteID: 1, SlotA: 0, SlotB: NoneU32, NilHint: AssignNilNonNil}},
					Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: 0},
				}},
			}
			continue
		}
		// Block j (1..k) calls one callee into slot 0, then either leaves for the return block
		// k+1 or goes on to block j+1, so the returned value joins every callee's result.
		ret := uint32(k + 1)
		blocks := []BlockIR{{Term: Terminator{Kind: TermJump, Target: 1}}}
		for j := 1; j <= k; j++ {
			next := Terminator{Kind: TermJump, Target: ret}
			if j < k {
				next = Terminator{Kind: TermCond, Target: ret, FalseBB: uint32(j + 1), CondKind: CondOpaque, CondSlot: NoneU32}
			}
			blocks = append(blocks, BlockIR{
				Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: uint32(10 + j), SlotA: 0, SlotB: uint32(r.IntN(n))}},
				Term: next,
			})
		}
		blocks = append(blocks, BlockIR{Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: 0}})
		funcs[i] = FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0}, Blocks: blocks}
	}
	return funcs
}

// TestSCCPhase1MatchesScanSummaries: the callee-first phase 1 reaches the summaries of the
// index-order scan it replaced. On random call graphs (cycles, self-calls, callers on both sides
// of their callees) the summaries are the scan's. The scan's sweep count is no longer the
// component sweep count (callee-first order needs fewer sweeps over the whole package and each
// component has its own cap), so the old "exactly the scan's sweeps" and "one fewer sweep
// fails" assertions were retired with the order that produced them; len(funcs)+1 per component
// is the proven bound and must converge. The summaries, which the scan fixes uniquely, are the
// contract, and the differential test against the naive solver covers them again.
func TestSCCPhase1MatchesScanSummaries(t *testing.T) {
	ctx := context.Background()
	r := rand.New(rand.NewPCG(0x5eed, 0x1111))
	maxSweeps := 0
	for trial := range 400 {
		n := 1 + r.IntN(60)
		funcs := randomCallGraph(r, n)
		want, sweeps := scanSweeps(t, funcs)
		maxSweeps = max(maxSweeps, sweeps)
		res, err := solveOracle(ctx, funcs, n+1)
		if err != nil {
			t.Fatalf("trial %d (n=%d): cap of n+1 sweeps per component: %v", trial, n, err)
		}
		for i := range want {
			if res.MayReturnNil[i] != want[i] {
				t.Fatalf("trial %d (n=%d): func %d summary %v, scan %v", trial, n, i, res.MayReturnNil[i], want[i])
			}
		}
	}
	if maxSweeps < 3 {
		t.Fatalf("the random graphs never needed more than %d sweeps; they do not exercise re-queueing", maxSweeps)
	}
}

// TestFrameByteLimitFailsClosed: the encoder stops at the frame bound, and both decoders refuse a
// frame over MaxFrameBytes before reading it (the zeroed allocation is never touched).
func TestFrameByteLimitFailsClosed(t *testing.T) {
	funcs := callChain(64)
	full, err := encodePackage(funcs, MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := encodePackage(funcs, len(full)); err != nil || len(got) != len(full) {
		t.Fatalf("frame exactly at the bound: %d bytes, err %v", len(got), err)
	}
	if _, err := encodePackage(funcs, len(full)-1); err == nil || !strings.Contains(err.Error(), "frame bytes") {
		t.Fatalf("frame one byte over the bound: err = %v, want a frame-bytes limit error", err)
	}

	huge := make([]byte, MaxFrameBytes+1)
	if _, err := DecodePackage(huge); err == nil || !strings.Contains(err.Error(), "frame_bytes") {
		t.Fatalf("oracle decoder, MaxFrameBytes+1: err = %v, want a frame_bytes limit error", err)
	}
	if _, err := SolveFrameOracle(context.Background(), huge); err == nil || !strings.Contains(err.Error(), "frame_bytes") {
		t.Fatalf("SolveFrameOracle, MaxFrameBytes+1: err = %v, want a frame_bytes limit error", err)
	}
}
