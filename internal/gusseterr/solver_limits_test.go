package gusseterr_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// headerOnlyFrame is a GSE1 frame that declares one function with the given counts and then
// stops. The decoders must reject an over-budget header before they read any body.
func headerOnlyFrame(slots, tuples, blocks uint32) []byte {
	f := []byte("GSE1")
	f = binary.LittleEndian.AppendUint32(f, 1)
	f = append(f, 0)
	f = binary.LittleEndian.AppendUint32(f, slots)
	f = binary.LittleEndian.AppendUint32(f, tuples)
	f = binary.LittleEndian.AppendUint32(f, blocks)
	return f
}

func stopBlocks(n int) []gusseterr.BlockIR {
	blocks := make([]gusseterr.BlockIR, n)
	for i := range blocks {
		blocks[i].Term = gusseterr.Terminator{Kind: gusseterr.TermStop}
	}
	return blocks
}

// TestStateCellBudgetHoldsInBothEngines: a function whose blocks x slots exceeds
// MaxStateCells fails with an error from the encoder, the Go decoder and (with cgo) the Rust
// decoder, while each factor alone is within its own limit.
func TestStateCellBudgetHoldsInBothEngines(t *testing.T) {
	ctx := context.Background()
	const slots, overBlocks = 4096, 2049 // 4096*2049 = 8,392,704 > 8,388,608
	if slots > gusseterr.MaxSlots || overBlocks > gusseterr.MaxBlocks {
		t.Fatal("test factors must each be within their own limit")
	}
	if slots*overBlocks <= gusseterr.MaxStateCells || slots*(overBlocks-1) != gusseterr.MaxStateCells {
		t.Fatalf("test shape does not straddle MaxStateCells=%d", gusseterr.MaxStateCells)
	}
	const want = "state cells"

	over := []gusseterr.FuncIR{{SlotFlags: make([]uint8, slots), Blocks: stopBlocks(overBlocks)}}
	if _, err := gusseterr.EncodePackage(over); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("EncodePackage over budget: err = %v, want %q", err, want)
	}
	if _, err := gusseterr.Solve(ctx, over); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Solve over budget: err = %v, want %q", err, want)
	}

	frame := headerOnlyFrame(slots, 0, overBlocks)
	if _, err := gusseterr.DecodePackage(frame); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("DecodePackage over budget: err = %v, want %q", err, want)
	}
	if _, err := gusseterr.SolveFrameOracle(ctx, frame); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("SolveFrameOracle over budget: err = %v, want %q", err, want)
	}
	if _, err := gusseterr.SolveFrame(ctx, frame); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("SolveFrame over budget: err = %v, want %q", err, want)
	}
	// The same header just under the budget is not a limit error (it is merely truncated).
	_, err := gusseterr.SolveFrame(ctx, headerOnlyFrame(slots, 0, overBlocks-1))
	if err == nil || strings.Contains(err.Error(), want) {
		t.Fatalf("SolveFrame at the budget: err = %v, want a truncation error, not a budget error", err)
	}

	// Exactly on the budget encodes and decodes (it is not solved here: 2^23 cells is ~0.9 GiB).
	exact := []gusseterr.FuncIR{{SlotFlags: make([]uint8, slots), Blocks: stopBlocks(overBlocks - 1)}}
	enc, err := gusseterr.EncodePackage(exact)
	if err != nil {
		t.Fatalf("EncodePackage at the budget: %v", err)
	}
	if _, err := gusseterr.DecodePackage(enc); err != nil {
		t.Fatalf("DecodePackage at the budget: %v", err)
	}
}

// TestSolveEnforcesLimitsOnEveryBuild: Solve must reject an over-limit package on the build
// that has no cgo (it used to hand the unvalidated IR straight to the oracle), and the
// rejection must be the encoder's, not a side effect of the solver.
func TestSolveEnforcesLimitsOnEveryBuild(t *testing.T) {
	ctx := context.Background()
	tooMany := make([]gusseterr.FuncIR, gusseterr.MaxFuncs+1)
	if _, err := gusseterr.Solve(ctx, tooMany); err == nil || !strings.Contains(err.Error(), "func count") {
		t.Fatalf("Solve with MaxFuncs+1 functions: err = %v, want a func-count limit error", err)
	}
	badIndex := []gusseterr.FuncIR{{
		SlotFlags: []uint8{0},
		Blocks: []gusseterr.BlockIR{{
			Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpObserveErr, SlotA: 9}},
			Term: gusseterr.Terminator{Kind: gusseterr.TermStop},
		}},
	}}
	if _, err := gusseterr.Solve(ctx, badIndex); err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("Solve with an out-of-range slot: err = %v, want an out-of-bounds error (not a panic)", err)
	}
}

func errPhiLoopPackage() []gusseterr.FuncIR {
	// e := &MyErr{}; for ... { e = lookup(k) }; return e
	// slots: 0 e0 (non-nil literal), 1 e1 (maybe-nil call result), 2 header phi.
	none := gusseterr.NoneU32
	return []gusseterr.FuncIR{
		{
			Flags:     gusseterr.FuncReturnsError,
			SlotFlags: []uint8{0, 0, 0},
			Blocks: []gusseterr.BlockIR{
				{
					Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpAssignSlot, SiteID: 1, SlotA: 0, SlotB: none, NilHint: gusseterr.AssignNilNonNil}},
					Term: gusseterr.Terminator{Kind: gusseterr.TermJump, Target: 1},
				},
				{
					Phis: []gusseterr.PhiIR{{DstSlot: 2, Incoming: []gusseterr.PhiEdge{{PredBB: 0, SrcSlot: 0}, {PredBB: 2, SrcSlot: 1}}}},
					Term: gusseterr.Terminator{Kind: gusseterr.TermCond, Target: 2, FalseBB: 3, CondKind: gusseterr.CondOpaque, CondSlot: none},
				},
				{
					Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpCallConcretePtr, SiteID: 2, SlotA: 1, SlotB: 1}},
					Term: gusseterr.Terminator{Kind: gusseterr.TermJump, Target: 1},
				},
				{
					Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpBoxConcreteErr, SiteID: 3, SlotA: 2}},
					Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 4, RetSlot: 2},
				},
			},
		},
		{Flags: gusseterr.FuncReturnsConcreteErrPtr | gusseterr.FuncExternalMayNil},
	}
}

// twoBranchTuplePackage is the IR the lowering emits for
// `if c { it, err = fetchA() } else { it, err = fetchB() }; if err != nil { return }; use(it)`.
func twoBranchTuplePackage(withCheck bool) []gusseterr.FuncIR {
	none := gusseterr.NoneU32
	join := gusseterr.BlockIR{
		Phis: []gusseterr.PhiIR{
			{DstSlot: 6, Incoming: []gusseterr.PhiEdge{{PredBB: 1, SrcSlot: 2}, {PredBB: 2, SrcSlot: 4}}},
			{DstSlot: 5, Incoming: []gusseterr.PhiEdge{{PredBB: 1, SrcSlot: 1}, {PredBB: 2, SrcSlot: 3}}},
		},
		Term: gusseterr.Terminator{Kind: gusseterr.TermJump, Target: 5},
	}
	if withCheck {
		join.Term = gusseterr.Terminator{Kind: gusseterr.TermCond, Target: 4, FalseBB: 5, CondKind: gusseterr.CondNotNil, CondSlot: 5}
	}
	return []gusseterr.FuncIR{{
		SlotFlags: []uint8{0, gusseterr.SlotIsError, 0, gusseterr.SlotIsError, 0, gusseterr.SlotIsError, 0},
		Tuples:    []gusseterr.TupleMeta{{ErrSlot: 1, CallSiteID: 10}, {ErrSlot: 3, CallSiteID: 11}},
		Blocks: []gusseterr.BlockIR{
			{Term: gusseterr.Terminator{Kind: gusseterr.TermCond, Target: 1, FalseBB: 2, CondKind: gusseterr.CondOpaque, CondSlot: none}},
			{
				Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpBindTuple, SiteID: 10, TupleID: 0, SlotA: 1, Companions: []uint32{2}}},
				Term: gusseterr.Terminator{Kind: gusseterr.TermJump, Target: 3},
			},
			{
				Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpBindTuple, SiteID: 11, TupleID: 1, SlotA: 3, Companions: []uint32{4}}},
				Term: gusseterr.Terminator{Kind: gusseterr.TermJump, Target: 3},
			},
			join,
			{Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 20, RetSlot: none}},
			{
				Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpUseCompanion, SiteID: 30, SlotA: 6}},
				Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 21, RetSlot: none},
			},
		},
	}}
}

func solveBoth(t *testing.T, pkg []gusseterr.FuncIR) gusseterr.PackageResult {
	t.Helper()
	ctx := context.Background()
	req, err := gusseterr.EncodePackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := gusseterr.SolveFrameOracle(ctx, req)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	got, err := gusseterr.SolveFrame(ctx, req)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if !bytes.Equal(got, want) {
		g, _ := gusseterr.DecodeResult(got)
		w, _ := gusseterr.DecodeResult(want)
		t.Fatalf("engine and oracle disagree:\nengine: %+v\noracle: %+v", g, w)
	}
	res, err := gusseterr.DecodeResult(want)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLoopPhiOverErrorValueReachesBox(t *testing.T) {
	res := solveBoth(t, errPhiLoopPackage())
	if len(res.Findings) != 1 || res.Findings[0].RuleCode != gusseterr.RuleERR004 || res.Findings[0].SiteID != 3 {
		t.Fatalf("findings = %+v, want exactly one ERR004 at site 3 (typed nil through the loop phi)", res.Findings)
	}
}

func TestTuplesMergedByPhiPromoteAfterOneCheck(t *testing.T) {
	if res := solveBoth(t, twoBranchTuplePackage(true)); len(res.Findings) != 0 {
		t.Fatalf("checked join: findings = %+v, want none (one check covers both calls)", res.Findings)
	}
	res := solveBoth(t, twoBranchTuplePackage(false))
	if len(res.Findings) != 1 || res.Findings[0].RuleCode != gusseterr.RuleERR002 || res.Findings[0].SiteID != 30 {
		t.Fatalf("unchecked join: findings = %+v, want one ERR002 at site 30", res.Findings)
	}
}

// TestNativeCallgraphChainsConverge runs chains longer than any fixed constant through the
// linked engine: the cap is derived from the function count, so valid input converges.
func TestNativeCallgraphChainsConverge(t *testing.T) {
	for _, n := range []int{gusseterr.MaxFuncs/4 + 1, gusseterr.MaxFuncs} {
		funcs := make([]gusseterr.FuncIR, n)
		for i := range funcs {
			if i+1 == n {
				funcs[i] = gusseterr.FuncIR{Flags: gusseterr.FuncReturnsConcreteErrPtr | gusseterr.FuncExternalMayNil}
				continue
			}
			funcs[i] = gusseterr.FuncIR{
				Flags:     gusseterr.FuncReturnsConcreteErrPtr,
				SlotFlags: []uint8{0},
				Blocks: []gusseterr.BlockIR{{
					Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: uint32(i + 1)}},
					Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 2, RetSlot: 0},
				}},
			}
		}
		start := time.Now()
		res, err := gusseterr.Solve(context.Background(), funcs)
		t.Logf("chain n=%d: %v (NativeActive=%v)", n, time.Since(start), gusseterr.NativeActive())
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
