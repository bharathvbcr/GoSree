package gusseterr

import (
	"cmp"
	"context"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// naiveSolve is the pre-dirty-flag reference: the call-graph phase of 3c9abd8's solveOracle
// (re-solve every unsettled function on every sweep until a sweep changes nothing, no cap),
// followed by the unchanged diagnostic phase.
func naiveSolve(ctx context.Context, funcsIn []FuncIR) (PackageResult, error) {
	funcs := make([]FuncIR, len(funcsIn))
	for i := range funcsIn {
		var err error
		if funcs[i], err = withCanonicalTuples(ctx, funcsIn[i]); err != nil {
			return PackageResult{}, err
		}
	}
	mayReturnNil := make([]bool, len(funcs))
	for i, fn := range funcs {
		if fn.Flags&FuncExternalMayNil != 0 {
			mayReturnNil[i] = true
		} else if fn.Flags&FuncReturnsConcreteErrPtr != 0 && len(fn.Blocks) == 0 {
			mayReturnNil[i] = true
		}
	}
	for {
		anyChanged := false
		for idx := range funcs {
			if mayReturnNil[idx] || funcs[idx].Flags&FuncReturnsConcreteErrPtr == 0 {
				continue
			}
			if len(funcs[idx].Blocks) == 0 {
				mayReturnNil[idx] = true
				anyChanged = true
				continue
			}
			fnMayNil, _, err := solveFuncOracle(ctx, uint32(idx), &funcs[idx], mayReturnNil, false)
			if err != nil {
				return PackageResult{}, err
			}
			if fnMayNil {
				mayReturnNil[idx] = true
				anyChanged = true
			}
		}
		if !anyChanged {
			break
		}
	}
	var findings []Finding
	for idx := range funcs {
		if len(funcs[idx].Blocks) == 0 {
			continue
		}
		_, fds, err := solveFuncOracle(ctx, uint32(idx), &funcs[idx], mayReturnNil, true)
		if err != nil {
			return PackageResult{}, err
		}
		findings = append(findings, fds...)
	}
	slices.SortFunc(findings, func(a, b Finding) int {
		if c := cmp.Compare(a.FuncIdx, b.FuncIdx); c != 0 {
			return c
		}
		if c := cmp.Compare(a.SiteID, b.SiteID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.RuleCode, b.RuleCode); c != 0 {
			return c
		}
		return cmp.Compare(a.SubKind, b.SubKind)
	})
	findings = slices.Compact(findings)
	return PackageResult{MayReturnNil: mayReturnNil, Findings: findings}, nil
}

// advCallGraph builds a random package whose functions call each other (self, mutual, cycles,
// diamonds, chains in both index orders, repeated callees, calls in non-returning branches,
// bodyless and externally-may-nil callees) and box or return the call results, so a summary
// change moves ERR004 findings and other summaries.
func advCallGraph(rng *rand.Rand) []FuncIR {
	n := 1 + rng.IntN(24)
	if rng.IntN(8) == 0 {
		n = 30 + rng.IntN(30)
	}
	mode := rng.IntN(4) // 0 random, 1 forward chain, 2 backward chain, 3 mixed
	pickCallee := func(i int) uint32 {
		switch mode {
		case 1:
			if i+1 < n {
				return uint32(i + 1)
			}
		case 2:
			if i > 0 {
				return uint32(i - 1)
			}
		case 3:
			if rng.IntN(2) == 0 {
				return uint32(max(0, min(n-1, i+rng.IntN(3)-1)))
			}
		}
		return uint32(rng.IntN(n))
	}
	funcs := make([]FuncIR, n)
	site := uint32(0)
	for i := range funcs {
		var flags uint8
		if rng.IntN(10) < 8 {
			flags |= FuncReturnsConcreteErrPtr
		}
		if rng.IntN(12) == 0 {
			flags |= FuncExternalMayNil
		}
		nSlots := 3 + rng.IntN(3)
		fn := FuncIR{Flags: flags, SlotFlags: make([]uint8, nSlots)}
		nb := 1 + rng.IntN(5)
		if rng.IntN(9) == 0 {
			nb = 0
		}
		fn.Blocks = make([]BlockIR, nb)
		for b := range fn.Blocks {
			for range rng.IntN(4) {
				site++
				s := uint32(rng.IntN(nSlots))
				switch rng.IntN(6) {
				case 0, 1, 2:
					fn.Blocks[b].Ops = append(fn.Blocks[b].Ops, OpIR{Kind: OpCallConcretePtr, SiteID: site, SlotA: s, SlotB: pickCallee(i)})
					if rng.IntN(3) == 0 { // same callee referenced again in this function
						site++
						fn.Blocks[b].Ops = append(fn.Blocks[b].Ops, OpIR{Kind: OpCallConcretePtr, SiteID: site, SlotA: uint32(rng.IntN(nSlots)), SlotB: fn.Blocks[b].Ops[len(fn.Blocks[b].Ops)-1].SlotB})
					}
				case 3:
					fn.Blocks[b].Ops = append(fn.Blocks[b].Ops, OpIR{Kind: OpBoxConcreteErr, SiteID: site, SlotA: s})
				case 4:
					fn.Blocks[b].Ops = append(fn.Blocks[b].Ops, OpIR{Kind: OpAssignSlot, SiteID: site, SlotA: s, SlotB: uint32(rng.IntN(nSlots)), NilHint: uint8(rng.IntN(4))})
				case 5:
					fn.Blocks[b].Ops = append(fn.Blocks[b].Ops, OpIR{Kind: OpAssignSlot, SiteID: site, SlotA: s, SlotB: NoneU32, NilHint: AssignNilNonNil})
				}
			}
			site++
			switch {
			case b == nb-1:
				rf := uint8(0)
				if rng.IntN(10) == 0 {
					rf = RetConcreteLiteralNil
				}
				rs := uint32(rng.IntN(nSlots))
				if rng.IntN(6) == 0 {
					rs = NoneU32
				}
				fn.Blocks[b].Term = Terminator{Kind: TermReturn, SiteID: site, RetSlot: rs, RetFlags: rf}
			case rng.IntN(5) == 0:
				fn.Blocks[b].Term = Terminator{Kind: TermStop}
			case rng.IntN(2) == 0:
				fn.Blocks[b].Term = Terminator{Kind: TermCond, Target: uint32(b + 1), FalseBB: uint32(rng.IntN(nb)), CondKind: CondOpaque, CondSlot: NoneU32}
			default:
				fn.Blocks[b].Term = Terminator{Kind: TermJump, Target: uint32(b + 1)}
			}
		}
		funcs[i] = fn
	}
	return funcs
}

// TestDirtyFlagSummariesMatchNaiveSweeps compares the dirty-flag call-graph phase of both
// engines with the naive sweep-everything reference on random call graphs.
func TestDirtyFlagSummariesMatchNaiveSweeps(t *testing.T) {
	ctx := context.Background()
	n := 4000
	rng := rand.New(rand.NewPCG(0xd17a, 0x1111))
	flips := 0
	for trial := range n {
		pkg := advCallGraph(rng)
		want, err := naiveSolve(ctx, pkg)
		if err != nil {
			t.Fatalf("trial %d naive: %v", trial, err)
		}
		for _, c := range want.MayReturnNil {
			if c {
				flips++
			}
		}
		check := func(name string, got PackageResult, err error) {
			t.Helper()
			if err != nil {
				t.Fatalf("trial %d %s: %v", trial, name, err)
			}
			if !reflect.DeepEqual(got.MayReturnNil, want.MayReturnNil) || !slices.Equal(got.Findings, want.Findings) {
				t.Fatalf("trial %d (%d funcs) %s != naive\nsummaries got  %v\nsummaries want %v\nfindings got  %v\nfindings want %v",
					trial, len(pkg), name, got.MayReturnNil, want.MayReturnNil, got.Findings, want.Findings)
			}
		}
		o, err := SolveOracle(ctx, pkg)
		check("oracle", o, err)
		r, err := Solve(ctx, pkg)
		check("Solve", r, err)
	}
	t.Logf("%d graphs, %d summaries true in reference", n, flips)
}
