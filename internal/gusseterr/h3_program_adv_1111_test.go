package gusseterr_test

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// This file is a ground-truth soundness oracle for the ERR002 tuple-merge (H3). It generates
// random structured programs over one companion variable v and one error variable err
// (assignments from tuple calls, err-only reassignments, `if err != nil { return }` checks,
// uses of v, switches with any number of arms, loops), lowers each to SSA-form FuncIR with
// phis, and enumerates every path of the structured program (loops unrolled 0..2 times) to
// decide which uses of v read a value whose error was not proven nil on that path. A use that
// is unchecked on some path but not reported by either engine is unsound.

type advKind int

const (
	advBind advKind = iota
	advErrOnly
	advCheck
	advUse
	advSwitch
	advLoop
)

type advStmt struct {
	kind advKind
	id   uint32      // bind: tuple identity; use: site id
	arms [][]advStmt // switch arms (last arm may be empty = fall-through path), loop body in arms[0]
}

type advGen struct {
	rng  *rand.Rand
	next uint32
}

func (g *advGen) stmts(depth, n int) []advStmt {
	out := make([]advStmt, 0, n)
	for range n {
		out = append(out, g.stmt(depth))
	}
	return out
}

func (g *advGen) stmt(depth int) advStmt {
	g.next++
	id := g.next
	r := g.rng.IntN(100)
	switch {
	case r < 22:
		return advStmt{kind: advBind, id: id}
	case r < 30:
		return advStmt{kind: advErrOnly, id: id}
	case r < 46:
		return advStmt{kind: advCheck, id: id}
	case r < 62:
		return advStmt{kind: advUse, id: id}
	case r < 92 && depth > 0:
		k := 2 + g.rng.IntN(3)
		arms := make([][]advStmt, k)
		for i := range arms {
			arms[i] = g.stmts(depth-1, g.rng.IntN(3))
		}
		return advStmt{kind: advSwitch, id: id, arms: arms}
	case depth > 0:
		return advStmt{kind: advLoop, id: id, arms: [][]advStmt{g.stmts(depth-1, 1+g.rng.IntN(3))}}
	}
	return advStmt{kind: advUse, id: id}
}

// ---- ground truth ----

type advState struct {
	vFrom, errFrom uint32
	vProven        bool
}

func advWalk(ss []advStmt, st advState, viol map[uint32]bool, k func(advState)) {
	if len(ss) == 0 {
		k(st)
		return
	}
	s, rest := ss[0], ss[1:]
	switch s.kind {
	case advBind:
		advWalk(rest, advState{vFrom: s.id, errFrom: s.id}, viol, k)
	case advErrOnly:
		st.errFrom = 0
		advWalk(rest, st, viol, k)
	case advCheck:
		if st.errFrom != 0 && st.errFrom == st.vFrom {
			st.vProven = true
		}
		advWalk(rest, st, viol, k)
	case advUse:
		if st.vFrom != 0 && !st.vProven {
			viol[s.id] = true
		}
		advWalk(rest, st, viol, k)
	case advSwitch:
		for _, arm := range s.arms {
			advWalk(arm, st, viol, func(s2 advState) { advWalk(rest, s2, viol, k) })
		}
	case advLoop:
		var iter func(n int, st advState)
		iter = func(n int, st advState) {
			advWalk(rest, st, viol, k)
			if n < 2 {
				advWalk(s.arms[0], st, viol, func(s2 advState) { iter(n+1, s2) })
			}
		}
		iter(0, st)
	}
}

// ---- lowering ----

type advLower struct {
	blocks   []gusseterr.BlockIR
	slotFlag []uint8
	tuples   []gusseterr.TupleMeta
	cur      int
	v, e     uint32
	uses     []uint32
	site     uint32
}

func (l *advLower) newBlock() int {
	l.blocks = append(l.blocks, gusseterr.BlockIR{})
	return len(l.blocks) - 1
}

func (l *advLower) newSlot(isErr bool) uint32 {
	var f uint8
	if isErr {
		f = gusseterr.SlotIsError
	}
	l.slotFlag = append(l.slotFlag, f)
	return uint32(len(l.slotFlag) - 1)
}

func (l *advLower) emit(op gusseterr.OpIR) {
	l.blocks[l.cur].Ops = append(l.blocks[l.cur].Ops, op)
}

func (l *advLower) jump(from, to int) {
	l.blocks[from].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(to)}
}

func (l *advLower) lower(ss []advStmt) {
	for _, s := range ss {
		l.site++
		switch s.kind {
		case advBind:
			v, e := l.newSlot(false), l.newSlot(true)
			l.tuples = append(l.tuples, gusseterr.TupleMeta{ErrSlot: e, CallSiteID: l.site})
			l.emit(gusseterr.OpIR{Kind: gusseterr.OpBindTuple, SiteID: l.site, TupleID: uint32(len(l.tuples) - 1), SlotA: e, Companions: []uint32{v}})
			l.v, l.e = v, e
		case advErrOnly:
			e := l.newSlot(true)
			l.emit(gusseterr.OpIR{Kind: gusseterr.OpBindErr, SiteID: l.site, SlotA: e})
			l.e = e
		case advCheck:
			ret, next := l.newBlock(), l.newBlock()
			l.blocks[ret].Term = gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: l.site, RetSlot: gusseterr.NoneU32}
			l.blocks[l.cur].Term = gusseterr.Terminator{Kind: gusseterr.TermCond, Target: uint32(ret), FalseBB: uint32(next), CondKind: gusseterr.CondNotNil, CondSlot: l.e}
			l.cur = next
		case advUse:
			l.uses = append(l.uses, s.id)
			l.emit(gusseterr.OpIR{Kind: gusseterr.OpUseCompanion, SiteID: 100000 + s.id, SlotA: l.v})
		case advSwitch:
			l.lowerSwitch(s)
		case advLoop:
			l.lowerLoop(s)
		}
	}
}

func (l *advLower) lowerSwitch(s advStmt) {
	k := len(s.arms)
	arms := make([]int, k)
	for i := range arms {
		arms[i] = l.newBlock()
	}
	join := l.newBlock()
	d := l.cur
	for i := 0; i < k-1; i++ {
		var next int
		if i == k-2 {
			next = arms[k-1]
		} else {
			next = l.newBlock()
		}
		l.blocks[d].Term = gusseterr.Terminator{Kind: gusseterr.TermCond, Target: uint32(arms[i]), FalseBB: uint32(next), CondKind: gusseterr.CondOpaque, CondSlot: gusseterr.NoneU32}
		d = next
	}
	v0, e0 := l.v, l.e
	type end struct {
		blk  int
		v, e uint32
	}
	ends := make([]end, k)
	for i, arm := range s.arms {
		l.cur, l.v, l.e = arms[i], v0, e0
		l.lower(arm)
		ends[i] = end{l.cur, l.v, l.e}
		l.jump(l.cur, join)
	}
	var vin, ein []gusseterr.PhiEdge
	vSame, eSame := true, true
	for _, en := range ends {
		vin = append(vin, gusseterr.PhiEdge{PredBB: uint32(en.blk), SrcSlot: en.v})
		ein = append(ein, gusseterr.PhiEdge{PredBB: uint32(en.blk), SrcSlot: en.e})
		vSame = vSame && en.v == ends[0].v
		eSame = eSame && en.e == ends[0].e
	}
	l.cur = join
	if vSame {
		l.v = ends[0].v
	} else {
		l.v = l.newSlot(false)
		l.blocks[join].Phis = append(l.blocks[join].Phis, gusseterr.PhiIR{DstSlot: l.v, Incoming: vin})
	}
	if eSame {
		l.e = ends[0].e
	} else {
		l.e = l.newSlot(true)
		l.blocks[join].Phis = append(l.blocks[join].Phis, gusseterr.PhiIR{DstSlot: l.e, Incoming: ein})
	}
}

func (l *advLower) lowerLoop(s advStmt) {
	pre := l.cur
	h, body, exit := l.newBlock(), l.newBlock(), l.newBlock()
	l.jump(pre, h)
	hv, he := l.newSlot(false), l.newSlot(true)
	preV, preE := l.v, l.e
	l.blocks[h].Term = gusseterr.Terminator{Kind: gusseterr.TermCond, Target: uint32(body), FalseBB: uint32(exit), CondKind: gusseterr.CondOpaque, CondSlot: gusseterr.NoneU32}
	l.cur, l.v, l.e = body, hv, he
	l.lower(s.arms[0])
	latch := l.cur
	l.jump(latch, h)
	l.blocks[h].Phis = []gusseterr.PhiIR{
		{DstSlot: hv, Incoming: []gusseterr.PhiEdge{{PredBB: uint32(pre), SrcSlot: preV}, {PredBB: uint32(latch), SrcSlot: l.v}}},
		{DstSlot: he, Incoming: []gusseterr.PhiEdge{{PredBB: uint32(pre), SrcSlot: preE}, {PredBB: uint32(latch), SrcSlot: l.e}}},
	}
	l.cur, l.v, l.e = exit, hv, he
}

func advBuild(ss []advStmt) (gusseterr.FuncIR, []uint32) {
	l := &advLower{}
	l.v, l.e = l.newSlot(false), l.newSlot(true)
	l.cur = l.newBlock()
	l.lower(ss)
	l.blocks[l.cur].Term = gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 99999, RetSlot: gusseterr.NoneU32}
	return gusseterr.FuncIR{Flags: gusseterr.FuncReturnsError, SlotFlags: l.slotFlag, Tuples: l.tuples, Blocks: l.blocks}, l.uses
}

func advDescribe(ss []advStmt, ind string, sb *strings.Builder) {
	for _, s := range ss {
		switch s.kind {
		case advBind:
			fmt.Fprintf(sb, "%sv, err = call%d()\n", ind, s.id)
		case advErrOnly:
			fmt.Fprintf(sb, "%serr = other%d()\n", ind, s.id)
		case advCheck:
			fmt.Fprintf(sb, "%sif err != nil { return }\n", ind)
		case advUse:
			fmt.Fprintf(sb, "%suse(v) // site %d\n", ind, s.id)
		case advSwitch:
			fmt.Fprintf(sb, "%sswitch {\n", ind)
			for i, a := range s.arms {
				fmt.Fprintf(sb, "%scase %d:\n", ind, i)
				advDescribe(a, ind+"\t", sb)
			}
			fmt.Fprintf(sb, "%s}\n", ind)
		case advLoop:
			fmt.Fprintf(sb, "%sfor cond {\n", ind)
			advDescribe(s.arms[0], ind+"\t", sb)
			fmt.Fprintf(sb, "%s}\n", ind)
		}
	}
}

// advRun solves prog with Rust and the oracle, requires identical frames, and returns the
// ERR002 use sites reported plus the ground-truth violating sites.
func advRun(t *testing.T, prog []advStmt) (reported, truth map[uint32]bool, uses []uint32, src string) {
	t.Helper()
	fn, uses := advBuild(prog)
	req, err := gusseterr.EncodePackage([]gusseterr.FuncIR{fn})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	ctx := context.Background()
	got, err := gusseterr.SolveFrame(ctx, req)
	if err != nil {
		t.Fatalf("SolveFrame: %v", err)
	}
	want, err := gusseterr.SolveFrameOracle(ctx, req)
	if err != nil {
		t.Fatalf("SolveFrameOracle: %v", err)
	}
	var sb strings.Builder
	advDescribe(prog, "", &sb)
	if !bytes.Equal(got, want) {
		g, _ := gusseterr.DecodeResult(got)
		w, _ := gusseterr.DecodeResult(want)
		t.Fatalf("ENGINE DIVERGENCE\nprogram:\n%s\nrust:   %+v\noracle: %+v", sb.String(), g.Findings, w.Findings)
	}
	res, err := gusseterr.DecodeResult(got)
	if err != nil {
		t.Fatal(err)
	}
	reported = map[uint32]bool{}
	for _, f := range res.Findings {
		if f.RuleCode == gusseterr.RuleERR002 && f.SiteID >= 100000 && f.SiteID < 200000 {
			reported[f.SiteID-100000] = true
		}
	}
	truth = map[uint32]bool{}
	advWalk(prog, advState{}, truth, func(advState) {})
	return reported, truth, uses, sb.String()
}

// TestMergedTuplesNeverClearAnUncheckedUse is the H3 soundness oracle. Any use that some path
// reaches with the companion's error unproven must be reported by the solver.
func TestMergedTuplesNeverClearAnUncheckedUse(t *testing.T) {
	if !gusseterr.NativeActive() {
		t.Log("native engine inactive: both sides are the oracle")
	}
	n := advEnvInt("ADV_PROGS", 2000)
	rng := rand.New(rand.NewPCG(uint64(advEnvInt("ADV_SEED", 1)), 0xad7))
	var unsound, fps, truthN int
	for i := range n {
		g := &advGen{rng: rng}
		prog := g.stmts(2, 3+rng.IntN(4))
		reported, truth, uses, src := advRun(t, prog)
		for _, u := range uses {
			if truth[u] && !reported[u] {
				unsound++
				if unsound <= 3 {
					t.Errorf("UNSOUND program %d: use site %d is unchecked on some path but not reported\n%s", i, u, src)
				}
			}
			if !truth[u] && reported[u] {
				fps++
			}
			if truth[u] {
				truthN++
			}
		}
	}
	t.Logf("programs=%d truthViolations=%d unsound=%d falsePositives=%d", n, truthN, unsound, fps)
}
