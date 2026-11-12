package gusseterr_test

import (
	"os"
	"strconv"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// advCondChain is one function of nBlocks blocks and nSlots slots in which every block but the
// last ends in a two-way Cond whose targets are both the next block, so each block owns the
// maximum four abstract states (entry, exit, two edges) in either engine.
func advCondChain(nBlocks, nSlots int) gusseterr.FuncIR {
	blocks := make([]gusseterr.BlockIR, nBlocks)
	for i := range blocks {
		if i == nBlocks-1 {
			blocks[i].Term = gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 1, RetSlot: gusseterr.NoneU32}
			continue
		}
		blocks[i].Term = gusseterr.Terminator{
			Kind: gusseterr.TermCond, Target: uint32(i + 1), FalseBB: uint32(i + 1),
			CondKind: gusseterr.CondOpaque, CondSlot: gusseterr.NoneU32,
		}
	}
	return gusseterr.FuncIR{Flags: gusseterr.FuncReturnsError, SlotFlags: make([]uint8, nSlots), Blocks: blocks}
}

// advManyClasses is the H3 cost worst case: nClasses error slots, each shared by perClass
// tuples (so each class is a candidate set of up to MAX_CANON_CLASS), every tuple bound in its
// own block of one long Jump chain (binds all reach each other, so nothing merges and every
// bind block pays a full reachability sweep over the remaining chain). nBlocks must exceed
// nClasses*perClass. One single-edge phi makes the function a merge candidate.
func advManyClasses(nClasses, perClass, nBlocks int) gusseterr.FuncIR {
	nSlots := nClasses + 1
	slotFlags := make([]uint8, nSlots)
	for i := 0; i < nClasses; i++ {
		slotFlags[i] = gusseterr.SlotIsError
	}
	nT := nClasses * perClass
	tuples := make([]gusseterr.TupleMeta, nT)
	blocks := make([]gusseterr.BlockIR, nBlocks)
	for t := 0; t < nT; t++ {
		cls := t % nClasses
		tuples[t] = gusseterr.TupleMeta{ErrSlot: uint32(cls), CallSiteID: uint32(t)}
		blocks[t].Ops = []gusseterr.OpIR{{
			Kind: gusseterr.OpBindTuple, SiteID: uint32(t), TupleID: uint32(t), SlotA: uint32(cls),
		}}
	}
	for i := range blocks {
		if i == nBlocks-1 {
			blocks[i].Term = gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 1, RetSlot: gusseterr.NoneU32}
		} else {
			blocks[i].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(i + 1)}
		}
	}
	last := nBlocks - 1
	blocks[last].Phis = []gusseterr.PhiIR{{DstSlot: uint32(nClasses), Incoming: []gusseterr.PhiEdge{{PredBB: uint32(last - 1), SrcSlot: 0}}}}
	return gusseterr.FuncIR{Flags: gusseterr.FuncReturnsError, SlotFlags: slotFlags, Tuples: tuples, Blocks: blocks}
}

// advExclusiveBinds builds one function with k mutually exclusive branches (a Cond chain), each
// binding its own tuple whose error slot feeds one phi at the join; the join checks the merged
// error with `err != nil` and uses the companion (slot 0 of each branch is its companion). When
// checkErr is false the join uses the companion without a check. tail inserts that many Jump
// blocks after the join so reachability sweeps are long.
func advExclusiveBinds(k, tail int, checkErr bool) gusseterr.FuncIR {
	// slots: [0..k) companions, [k..2k) per-branch errs, 2k merged err, 2k+1 merged companion.
	nSlots := 2*k + 2
	slotFlags := make([]uint8, nSlots)
	for i := 0; i < k; i++ {
		slotFlags[k+i] = gusseterr.SlotIsError
	}
	slotFlags[2*k] = gusseterr.SlotIsError
	tuples := make([]gusseterr.TupleMeta, k)
	for i := range tuples {
		tuples[i] = gusseterr.TupleMeta{ErrSlot: uint32(k + i), CallSiteID: uint32(100 + i)}
	}
	// blocks: dispatch chain d_0..d_{k-1}, bind blocks b_0..b_{k-1}, join, check, use, ret, tail.
	dispatch := func(i int) int { return i }
	bind := func(i int) int { return k + i }
	join := 2 * k
	use := 2*k + 1
	ret := 2*k + 2
	tailStart := 2*k + 3
	blocks := make([]gusseterr.BlockIR, 2*k+3+tail)
	for i := 0; i < k; i++ {
		next := dispatch(i + 1)
		if i == k-1 {
			next = join
		}
		blocks[dispatch(i)].Term = gusseterr.Terminator{
			Kind: gusseterr.TermCond, Target: uint32(bind(i)), FalseBB: uint32(next),
			CondKind: gusseterr.CondOpaque, CondSlot: gusseterr.NoneU32,
		}
		blocks[bind(i)].Ops = []gusseterr.OpIR{{
			Kind: gusseterr.OpBindTuple, SiteID: uint32(10 + i), TupleID: uint32(i),
			SlotA: uint32(k + i), Companions: []uint32{uint32(i)},
		}}
		blocks[bind(i)].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(join)}
	}
	incoming := make([]gusseterr.PhiEdge, 0, k)
	cincoming := make([]gusseterr.PhiEdge, 0, k)
	for i := 0; i < k; i++ {
		incoming = append(incoming, gusseterr.PhiEdge{PredBB: uint32(bind(i)), SrcSlot: uint32(k + i)})
		cincoming = append(cincoming, gusseterr.PhiEdge{PredBB: uint32(bind(i)), SrcSlot: uint32(i)})
	}
	blocks[join].Phis = []gusseterr.PhiIR{
		{DstSlot: uint32(2 * k), Incoming: incoming},
		{DstSlot: uint32(2*k + 1), Incoming: cincoming},
	}
	if checkErr {
		blocks[join].Term = gusseterr.Terminator{
			Kind: gusseterr.TermCond, Target: uint32(ret), FalseBB: uint32(use),
			CondKind: gusseterr.CondNotNil, CondSlot: uint32(2 * k),
		}
	} else {
		blocks[join].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(use)}
	}
	blocks[use].Ops = []gusseterr.OpIR{{Kind: gusseterr.OpUseCompanion, SiteID: 999, SlotA: uint32(2*k + 1)}}
	if tail > 0 {
		blocks[use].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(tailStart)}
		for t := 0; t < tail; t++ {
			if t == tail-1 {
				blocks[tailStart+t].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(ret)}
			} else {
				blocks[tailStart+t].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(tailStart + t + 1)}
			}
		}
	} else {
		blocks[use].Term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(ret)}
	}
	blocks[ret].Term = gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: 1000, RetSlot: gusseterr.NoneU32}
	return gusseterr.FuncIR{Flags: gusseterr.FuncReturnsError, SlotFlags: slotFlags, Tuples: tuples, Blocks: blocks}
}

// advChain is n concrete-error-pointer functions where function i calls function i+1 and returns
// the result; only the last is externally may-nil. With callers at lower indices than callees
// each sweep of the summary fixpoint propagates exactly one step, so the fixpoint needs n
// changing sweeps. reverse places callees first (one sweep suffices).
func advChain(n int, reverse bool) []gusseterr.FuncIR {
	funcs := make([]gusseterr.FuncIR, n)
	idx := func(i int) int {
		if reverse {
			return n - 1 - i
		}
		return i
	}
	for i := 0; i < n; i++ {
		fn := gusseterr.FuncIR{Flags: gusseterr.FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0}}
		if i == n-1 {
			fn.Flags |= gusseterr.FuncExternalMayNil
		} else {
			fn.Blocks = []gusseterr.BlockIR{{
				Ops:  []gusseterr.OpIR{{Kind: gusseterr.OpCallConcretePtr, SiteID: uint32(i + 1), SlotA: 0, SlotB: uint32(idx(i + 1))}},
				Term: gusseterr.Terminator{Kind: gusseterr.TermReturn, SiteID: uint32(i + 1), RetSlot: 0},
			}}
		}
		funcs[idx(i)] = fn
	}
	return funcs
}

func advEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}
