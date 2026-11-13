package gusseterr_test

import (
	"context"
	"slices"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// ft-e92e design evidence: the per-store versioning of passes/errcheck/DESIGN_ERR005_PATHS.md
// needs no new wire op and no solver change, because a version is an ordinary slot, a store is an
// OpAssignSlot, a join is an ordinary PhiIR, and the written flag already joins at a phi
// (writableKind.join in oracle.go, WriteMode::join in rust/gosree-errsolve). These hand-built
// functions are the lowering the design would emit; both engines must agree on each.
//
// Slot numbering in every function: a version of an access path is one slot.

func versionJump(to uint32) gusseterr.Terminator {
	return gusseterr.Terminator{Kind: gusseterr.TermJump, Target: to}
}

func versionCond(t, f uint32) gusseterr.Terminator {
	return gusseterr.Terminator{Kind: gusseterr.TermCond, Target: t, FalseBB: f, CondKind: gusseterr.CondOpaque, CondSlot: gusseterr.NoneU32}
}

func versionReset(slot uint32) gusseterr.OpIR {
	return gusseterr.OpIR{Kind: gusseterr.OpAssignSlot, SlotA: slot, SlotB: gusseterr.NoneU32, NilHint: gusseterr.AssignNilMayNil}
}

func versionWrite(slot uint32) gusseterr.OpIR {
	return gusseterr.OpIR{Kind: gusseterr.OpMarkWritable, SlotA: slot}
}

func versionClose(site, slot uint32) gusseterr.OpIR {
	return gusseterr.OpIR{Kind: gusseterr.OpUnhandledClose, SiteID: site, SlotA: slot}
}

func TestE92eVersionShapesRunInBothEngines(t *testing.T) {
	stop := gusseterr.Terminator{Kind: gusseterr.TermStop}
	phi := func(dst, a, b uint32) gusseterr.PhiIR {
		return gusseterr.PhiIR{DstSlot: dst, Incoming: []gusseterr.PhiEdge{{PredBB: 1, SrcSlot: a}, {PredBB: 2, SrcSlot: b}}}
	}
	finding := func(fn, site uint32) gusseterr.Finding {
		return gusseterr.Finding{FuncIdx: fn, SiteID: site, RuleCode: gusseterr.RuleERR005, SubKind: gusseterr.SubERR005SyncClose}
	}

	// A diamond: version 0 is written on one arm, a store makes version 1 on the other, the join is a
	// phi version. `if c { h.w.Write() } else { h.w = open() }; h.w.Close()`.
	diamond := func(armA, armB gusseterr.OpIR) gusseterr.FuncIR {
		return gusseterr.FuncIR{
			SlotFlags: make([]uint8, 3),
			Blocks: []gusseterr.BlockIR{
				{Term: versionCond(1, 2)},
				{Ops: []gusseterr.OpIR{armA}, Term: versionJump(3)},
				{Ops: []gusseterr.OpIR{armB}, Term: versionJump(3)},
				{Phis: []gusseterr.PhiIR{phi(2, 0, 1)}, Ops: []gusseterr.OpIR{versionClose(7, 2)}, Term: stop},
			},
		}
	}

	funcs := []gusseterr.FuncIR{
		// 0: written on one arm, replaced on the other: the join may be written, so it reports.
		diamond(versionWrite(0), versionReset(1)),
		// 1: replaced on both arms: nothing written reaches the join.
		diamond(versionReset(0), versionReset(1)),
		// 2: an alias pinned to version 0 closes after a store made version 1: the old object was
		// written, the new one was not. `a := h.w; a.Write(); h.w = open(); a.Close(); h.w.Close()`.
		{
			SlotFlags: make([]uint8, 2),
			Blocks: []gusseterr.BlockIR{{
				Ops:  []gusseterr.OpIR{versionWrite(0), versionReset(1), versionClose(8, 0), versionClose(9, 1)},
				Term: stop,
			}},
		},
		// 3: a holder that is a new object on each loop iteration: the entry version of its path is
		// reset where the holder is defined (the loop header), so a write of the last iteration does
		// not reach the Close of the next. `for n := head; ...; n = n.next { if c { n.w.Write() }; n.w.Close() }`.
		loopFunc(true, versionClose(10, 0)),
		// 4: the same without the reset: the flag is carried round the back edge and the Close
		// reports. This is the false positive the reset exists to prevent.
		loopFunc(false, versionClose(11, 0)),
	}

	ctx := context.Background()
	oracle, err := gusseterr.SolveOracle(ctx, funcs)
	if err != nil {
		t.Fatalf("SolveOracle: %v", err)
	}
	engine, err := gusseterr.Solve(ctx, funcs)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := []gusseterr.Finding{finding(0, 7), finding(2, 8), finding(4, 11)}
	for name, got := range map[string][]gusseterr.Finding{"oracle": oracle.Findings, "engine": engine.Findings} {
		sorted := slices.Clone(got)
		slices.SortFunc(sorted, func(a, b gusseterr.Finding) int {
			if a.FuncIdx != b.FuncIdx {
				return int(a.FuncIdx) - int(b.FuncIdx)
			}
			return int(a.SiteID) - int(b.SiteID)
		})
		if !slices.Equal(sorted, want) {
			t.Errorf("%s findings = %+v, want %+v", name, sorted, want)
		}
	}
	if !slices.Equal(oracle.Findings, engine.Findings) {
		t.Errorf("engines disagree: oracle %+v, engine %+v", oracle.Findings, engine.Findings)
	}
}

// loopFunc is bb0 -> header bb1 (optional reset of slot 0, then a cond) -> bb2 writes slot 0 and
// jumps back, or -> bb3 which runs closeOp and stops.
func loopFunc(reset bool, closeOp gusseterr.OpIR) gusseterr.FuncIR {
	var header []gusseterr.OpIR
	if reset {
		header = append(header, versionReset(0))
	}
	return gusseterr.FuncIR{
		SlotFlags: make([]uint8, 1),
		Blocks: []gusseterr.BlockIR{
			{Term: versionJump(1)},
			{Ops: header, Term: versionCond(2, 3)},
			{Ops: []gusseterr.OpIR{versionWrite(0)}, Term: versionJump(1)},
			{Ops: []gusseterr.OpIR{closeOp}, Term: gusseterr.Terminator{Kind: gusseterr.TermStop}},
		},
	}
}
