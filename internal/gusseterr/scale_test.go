package gusseterr_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// scatteredTwoChainPackage mirrors scattered_two_chain_package in
// rust/gosree-errsolve/tests/solver_limits_and_joins.rs: n functions in two call chains laid out
// in the index order k -> k*7919 mod n, so a callee is sometimes ahead of its caller and
// sometimes behind it. Each chain function calls the next in its chain, boxes the concrete error
// pointer it got into an error (site 3) and returns it. Chain A (the first half) ends in an
// external function that may return nil, so every box in it is ERR004; chain B ends in a fresh
// non-nil pointer, so nothing in it is. inA reports, per index, whether it is in chain A.
func scatteredTwoChainPackage(t *testing.T, n int) (funcs []gusseterr.FuncIR, inA []bool) {
	t.Helper()
	if n%2 != 0 || n%7919 == 0 {
		t.Fatalf("n=%d: need an even n that 7919 does not divide", n)
	}
	pos := func(k int) int { return k * 7919 % n }
	half := n / 2
	funcs = make([]gusseterr.FuncIR, n)
	inA = make([]bool, n)
	for k := range n {
		isA := k < half
		end := n - 1
		if isA {
			end = half - 1
		}
		inA[pos(k)] = isA
		switch {
		case k == end && isA:
			funcs[pos(k)] = gusseterr.FuncIR{Flags: gusseterr.FuncReturnsConcreteErrPtr | gusseterr.FuncExternalMayNil}
		case k == end:
			funcs[pos(k)] = gusseterr.FuncIR{
				Flags:     gusseterr.FuncReturnsConcreteErrPtr,
				SlotFlags: []uint8{0},
				Blocks: []gusseterr.BlockIR{{
					Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpAssignSlot, SiteID: 1, SlotA: 0, SlotB: gusseterr.NoneU32, NilHint: gusseterr.AssignNilNonNil}},
					Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 2, RetSlot: 0},
				}},
			}
		default:
			funcs[pos(k)] = gusseterr.FuncIR{
				Flags:     gusseterr.FuncReturnsConcreteErrPtr,
				SlotFlags: []uint8{0},
				Blocks: []gusseterr.BlockIR{{
					Ops: []gusseterr.OpIR{
						{Kind: gusseterr.OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: uint32(pos(k + 1))},
						{Kind: gusseterr.OpBoxConcreteErr, SiteID: 3, SlotA: 0},
					},
					Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 2, RetSlot: 0},
				}},
			}
		}
	}
	return funcs, inA
}

// TestPackageOverOldFunctionLimitSolves: a package of more functions than the 16,384 the solver
// used to accept (ScholarLM's internal/wisdev with its tests has 18,973), with errors propagated
// across functions in both index directions, solves on this build's engine (Rust over gusset
// with cgo, the oracle without) with exactly the expected summaries and findings, and the
// engine's response frame is byte-identical to the pure-Go oracle's. Before the limit was raised
// EncodePackage refused it ("func count 20480 exceeds 16384").
func TestPackageOverOldFunctionLimitSolves(t *testing.T) {
	const n = 20_480
	funcs, inA := scatteredTwoChainPackage(t, n)
	ctx := context.Background()

	frame, err := gusseterr.EncodePackage(funcs)
	if err != nil {
		t.Fatalf("EncodePackage(%d functions): %v", n, err)
	}
	engineResp, err := gusseterr.SolveFrame(ctx, frame)
	if err != nil {
		t.Fatalf("SolveFrame (NativeActive=%v): %v", gusseterr.NativeActive(), err)
	}
	oracleResp, err := gusseterr.SolveFrameOracle(ctx, frame)
	if err != nil {
		t.Fatalf("SolveFrameOracle: %v", err)
	}
	if !bytes.Equal(engineResp, oracleResp) {
		t.Fatalf("engine (NativeActive=%v) and oracle response frames differ: %d vs %d bytes", gusseterr.NativeActive(), len(engineResp), len(oracleResp))
	}

	res, err := gusseterr.Solve(ctx, funcs)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !slices.Equal(res.MayReturnNil, inA) {
		t.Fatal("summaries: want may-nil exactly on chain A")
	}
	var want []gusseterr.Finding
	for i := range n {
		if inA[i] && len(funcs[i].Blocks) > 0 {
			want = append(want, gusseterr.Finding{FuncIdx: uint32(i), SiteID: 3, RuleCode: gusseterr.RuleERR004, SubKind: gusseterr.SubERR004MayNil})
		}
	}
	if len(want) != n/2-1 || !slices.Equal(res.Findings, want) {
		t.Fatalf("findings: got %d, want the %d ERR004 boxes of chain A", len(res.Findings), len(want))
	}
	if !bytes.Equal(gusseterr.EncodeResult(res), engineResp) {
		t.Fatal("Solve and SolveFrame disagree")
	}
}

// TestFunctionCountLimitIsExact: MaxFuncs functions encode and decode; one more is refused by the
// encoder (both builds) and, sent as a raw frame, by the decoder of this build's engine.
func TestFunctionCountLimitIsExact(t *testing.T) {
	at := make([]gusseterr.FuncIR, gusseterr.MaxFuncs)
	frame, err := gusseterr.EncodePackage(at)
	if err != nil {
		t.Fatalf("EncodePackage(MaxFuncs): %v", err)
	}
	if got, err := gusseterr.DecodePackage(frame); err != nil || len(got) != gusseterr.MaxFuncs {
		t.Fatalf("DecodePackage(MaxFuncs): %d functions, err %v", len(got), err)
	}
	if _, err := gusseterr.EncodePackage(make([]gusseterr.FuncIR, gusseterr.MaxFuncs+1)); err == nil || !strings.Contains(err.Error(), "func count") {
		t.Fatalf("EncodePackage(MaxFuncs+1): err = %v, want a func-count limit error", err)
	}
	// The same frame with its count raised by one and one more empty function appended.
	over := append([]byte(nil), frame...)
	binary.LittleEndian.PutUint32(over[4:8], gusseterr.MaxFuncs+1)
	over = append(over, make([]byte, 13)...) // flags + three zero counts: one empty function
	for name, solve := range map[string]func(context.Context, []byte) ([]byte, error){
		"engine": gusseterr.SolveFrame,
		"oracle": gusseterr.SolveFrameOracle,
	} {
		if _, err := solve(context.Background(), over); err == nil || !strings.Contains(err.Error(), "num_funcs") {
			t.Fatalf("%s: MaxFuncs+1 frame: err = %v, want a num_funcs limit error", name, err)
		}
	}
}
