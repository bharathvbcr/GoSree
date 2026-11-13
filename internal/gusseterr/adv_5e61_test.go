package gusseterr

// Adversarial tests for ft-5e61 (callee-first phase 1, the phase-1 work budget, the decoded-size
// bound, encoder pre-sizing, the oracle response cap). Each test states what it attacks. A test
// that FAILS on commit 65e2cd4 documents a break; the rest pin a defended case.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------------------------
// The pre-change phase 1 (f0dea98): global index-order dirty sweeps, with counters.
// ---------------------------------------------------------------------------------------------

type oldPhase1Result struct {
	mayNil []bool
	sweeps int
	solves uint64
	work   uint64
	err    error
}

// oldPhase1 is phase 1 of f0dea98's solveOracle (one global dirty worklist in index order, the
// cap applying to all sweeps together), with sweep, solve and work counters.
func oldPhase1(ctx context.Context, funcsIn []FuncIR, maxIters int) oldPhase1Result {
	funcs := make([]FuncIR, len(funcsIn))
	for i := range funcsIn {
		funcs[i], _ = withCanonicalTuples(ctx, funcsIn[i])
	}
	n := len(funcs)
	may := make([]bool, n)
	for i, fn := range funcs {
		if fn.Flags&FuncExternalMayNil != 0 || (fn.Flags&FuncReturnsConcreteErrPtr != 0 && len(fn.Blocks) == 0) {
			may[i] = true
		}
	}
	callers := make([][]uint32, n)
	unit := make([]uint64, n)
	for idx := range funcs {
		unit[idx] = uint64(len(funcs[idx].Blocks)) * max(uint64(len(funcs[idx].SlotFlags)), 1) // the phase-1 work unit since the slot-aware unit (B)
		for bi := range funcs[idx].Blocks {
			unit[idx] += uint64(len(funcs[idx].Blocks[bi].Ops))
			for _, op := range funcs[idx].Blocks[bi].Ops {
				if op.Kind != OpCallConcretePtr {
					continue
				}
				c := op.SlotB
				if k := len(callers[c]); k == 0 || callers[c][k-1] != uint32(idx) {
					callers[c] = append(callers[c], uint32(idx))
				}
			}
		}
	}
	dirty := make([]bool, n)
	this := make(u32MinHeap, n)
	for i := range dirty {
		dirty[i] = true
		this[i] = uint32(i)
	}
	var next u32MinHeap
	r := oldPhase1Result{mayNil: may}
	converged := false
	for range maxIters {
		r.sweeps++
		any := false
		for len(this) > 0 {
			idx := this.pop()
			if may[idx] || !dirty[idx] || funcs[idx].Flags&FuncReturnsConcreteErrPtr == 0 {
				continue
			}
			dirty[idx] = false
			if len(funcs[idx].Blocks) == 0 {
				may[idx] = true
				any = true
				continue
			}
			f, _, err := solveFuncOracle(ctx, idx, &funcs[idx], may, false)
			if err != nil {
				r.err = err
				return r
			}
			r.solves++
			r.work += unit[idx]
			if f {
				may[idx] = true
				any = true
				for _, c := range callers[idx] {
					if dirty[c] {
						continue
					}
					dirty[c] = true
					if c > idx {
						this.push(c)
					} else {
						next.push(c)
					}
				}
			}
		}
		if !any {
			converged = true
			break
		}
		this, next = next, this
	}
	if !converged {
		r.err = errors.New("not converged")
	}
	return r
}

// ---------------------------------------------------------------------------------------------
// A call-graph generator aimed at the SCC order: inactive functions in the middle of chains,
// DAGs, rings, hubs, self-loops, duplicate edges.
// ---------------------------------------------------------------------------------------------

const (
	nkNilSource = iota // active, returns a literal nil pointer on one path
	nkPass             // active, returns the result of a call (flips with its callees)
	nkSafe             // active, returns a non-nil pointer unless a callee path says otherwise
	nkNoFlag           // inactive: not flagged as returning a concrete error pointer
	nkBodyless         // inactive: flagged, no body (seeded may-nil)
	nkSeeded           // inactive: FuncExternalMayNil
	nkKinds
)

func adv5Graph(rng *rand.Rand) []FuncIR {
	n := 2 + rng.IntN(30)
	if rng.IntN(10) == 0 {
		n = 60 + rng.IntN(100)
	}
	topo := rng.IntN(7)
	kinds := make([]int, n)
	for i := range kinds {
		switch r := rng.IntN(20); {
		case r < 3:
			kinds[i] = nkNilSource
		case r < 11:
			kinds[i] = nkPass
		case r < 14:
			kinds[i] = nkSafe
		case r < 16:
			kinds[i] = nkNoFlag
		case r < 18:
			kinds[i] = nkBodyless
		default:
			kinds[i] = nkSeeded
		}
	}
	callees := func(i int) []uint32 {
		var cs []uint32
		add := func(c int) {
			if c >= 0 && c < n {
				cs = append(cs, uint32(c))
			}
		}
		switch topo {
		case 0: // random edges, self-loops and duplicates included
			for range rng.IntN(4) {
				add(rng.IntN(n))
			}
		case 1: // forward chain (callers precede callees)
			add(i + 1)
		case 2: // backward chain
			add(i - 1)
		case 3: // DAG towards higher indices
			for range rng.IntN(3) {
				add(i + 1 + rng.IntN(n))
			}
		case 4: // DAG towards lower indices
			for range rng.IntN(3) {
				add(i - 1 - rng.IntN(n))
			}
		case 5: // ring with chords
			add((i + 1) % n)
			if rng.IntN(4) == 0 {
				add(rng.IntN(n))
			}
		default: // hub (function 0 or n-1) calls everyone, everyone calls the next, last calls hub
			if i == 0 || i == n-1 {
				for c := range n {
					add(c)
				}
			}
			add(i + 1)
			if i == n-1 {
				add(0)
			}
		}
		if rng.IntN(5) == 0 && len(cs) > 0 {
			cs = append(cs, cs[rng.IntN(len(cs))]) // duplicate edge
		}
		if rng.IntN(15) == 0 {
			add(i) // self-loop
		}
		return cs
	}
	site := uint32(0)
	next := func() uint32 { site++; return site }
	funcs := make([]FuncIR, n)
	for i := range funcs {
		k := kinds[i]
		fn := FuncIR{}
		if k != nkNoFlag {
			fn.Flags |= FuncReturnsConcreteErrPtr
		}
		if k == nkSeeded {
			fn.Flags |= FuncExternalMayNil
		}
		if k == nkBodyless {
			funcs[i] = fn
			continue
		}
		cs := callees(i)
		nSlots := len(cs) + 2
		fn.SlotFlags = make([]uint8, nSlots)
		var ops []OpIR
		for j, c := range cs {
			ops = append(ops, OpIR{Kind: OpCallConcretePtr, SiteID: next(), SlotA: uint32(j), SlotB: c})
			if rng.IntN(3) == 0 {
				ops = append(ops, OpIR{Kind: OpBoxConcreteErr, SiteID: next(), SlotA: uint32(j)})
			}
		}
		safeSlot := uint32(nSlots - 1)
		ops = append(ops, OpIR{Kind: OpAssignSlot, SiteID: next(), SlotA: safeSlot, SlotB: NoneU32, NilHint: AssignNilNonNil})
		retSlot := safeSlot
		if k == nkPass && len(cs) > 0 {
			retSlot = uint32(rng.IntN(len(cs)))
		}
		retFlags := uint8(0)
		if k == nkNilSource {
			retFlags = RetConcreteLiteralNil
		}
		fn.Blocks = []BlockIR{
			{Ops: ops, Term: Terminator{Kind: TermCond, Target: 1, FalseBB: 2, CondKind: CondOpaque, CondSlot: NoneU32}},
			{Term: Terminator{Kind: TermReturn, SiteID: next(), RetSlot: retSlot}},
			{Term: Terminator{Kind: TermReturn, SiteID: next(), RetSlot: safeSlot, RetFlags: retFlags}},
		}
		if k == nkSafe {
			fn.Blocks[2].Term.RetFlags = 0
		}
		funcs[i] = fn
	}
	return funcs
}

func sameResult(a, b PackageResult) bool {
	return slices.Equal(a.MayReturnNil, b.MayReturnNil) && slices.Equal(a.Findings, b.Findings)
}

// ATTACK 1. Fixpoint equivalence: summaries (phase 1) and findings (phase 2) of the callee-first
// solver equal the naive sweep-everything solver and the f0dea98 index-order scan, in the oracle
// and in Solve, and the native and oracle response frames are byte-identical. Inactive functions
// sit in chains and cycles; the new solver also never does more work than the old one.
func TestAdv5e61FixpointEquivalence(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(0x5e61, 0xadd))
	const trials = 1500
	flips, findingsSeen, moreWork := 0, 0, 0
	var totalNew, totalOld uint64
	for trial := range trials {
		pkg := adv5Graph(rng)
		want, err := naiveSolve(ctx, pkg)
		if err != nil {
			t.Fatalf("trial %d naive: %v", trial, err)
		}
		old := oldPhase1(ctx, pkg, len(pkg)+1)
		if old.err != nil || !slices.Equal(old.mayNil, want.MayReturnNil) {
			t.Fatalf("trial %d: the reference old scan disagrees with naive: %v", trial, old.err)
		}
		for _, b := range want.MayReturnNil {
			if b {
				flips++
			}
		}
		findingsSeen += len(want.Findings)
		var st phase1Stats
		got, err := solveOracleLimited(ctx, pkg, len(pkg)+1, Phase1WorkFactor, Phase1WorkFloor, &st)
		if err != nil {
			t.Fatalf("trial %d oracle: %v", trial, err)
		}
		if !sameResult(got, want) {
			t.Fatalf("trial %d (%d funcs): oracle != naive\nsummaries got  %v\nsummaries want %v\nfindings got %v\nfindings want %v", trial, len(pkg), got.MayReturnNil, want.MayReturnNil, got.Findings, want.Findings)
		}
		// EXPECTATION CHANGED (ft-5e61 fix A): inside a cycle the sweep order is now the reverse
		// finish order of the search over callers, a heuristic that is not guaranteed to beat
		// function-index order on every graph (6 of these 1,500 do slightly more work, at most
		// 1.3x). Between components callee-first still never loses. So the per-graph bound is 2x
		// the scan's work, and the total over all graphs must not exceed the scan's.
		totalNew += st.work
		totalOld += old.work
		if st.work > 2*old.work+16 {
			moreWork++
			t.Errorf("trial %d: callee-first did MORE than twice the phase-1 work of the index-order scan: %d solves/%d units vs %d/%d", trial, st.solves, st.work, old.solves, old.work)
		}
		native, err := Solve(ctx, pkg)
		if err != nil {
			t.Fatalf("trial %d Solve: %v", trial, err)
		}
		if !sameResult(native, want) {
			t.Fatalf("trial %d (%d funcs): Solve != naive", trial, len(pkg))
		}
		frame, err := EncodePackage(pkg)
		if err != nil {
			t.Fatal(err)
		}
		nb, nerr := SolveFrame(ctx, frame)
		ob, oerr := SolveFrameOracle(ctx, frame)
		if (nerr == nil) != (oerr == nil) || !bytes.Equal(nb, ob) {
			t.Fatalf("trial %d: engines differ: native len %d err %v, oracle len %d err %v", trial, len(nb), nerr, len(ob), oerr)
		}
	}
	t.Logf("%d graphs, %d may-nil summaries, %d findings, native active = %v; phase-1 work %d units vs %d for the index-order scan", trials, flips, findingsSeen, NativeActive(), totalNew, totalOld)
	if totalNew > totalOld {
		t.Errorf("over %d graphs callee-first did %d units of phase-1 work against the scan's %d", trials, totalNew, totalOld)
	}
	if flips < trials || findingsSeen < trials/10 {
		t.Fatalf("generator too tame: %d summaries true, %d findings", flips, findingsSeen)
	}
}

// ATTACK 1b. An inactive callee's may-nil is seen by an active caller in a different SCC, and
// an inactive function between two active ones carries no information in either direction.
func TestAdv5e61InactiveCalleesAcrossSCCs(t *testing.T) {
	ctx := context.Background()
	callRet := func(callee uint32) FuncIR {
		return FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0}, Blocks: []BlockIR{{
			Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: callee}},
			Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: 0}}}}
	}
	safe := func() FuncIR {
		return FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0}, Blocks: []BlockIR{{
			Ops:  []OpIR{{Kind: OpAssignSlot, SiteID: 1, SlotA: 0, SlotB: NoneU32, NilHint: AssignNilNonNil}},
			Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: 0}}}}
	}
	for name, inactive := range map[string]FuncIR{
		"seeded may-nil": {Flags: FuncReturnsConcreteErrPtr | FuncExternalMayNil},
		"bodyless":       {Flags: FuncReturnsConcreteErrPtr},
	} {
		// 0 -> 1 -> 2(inactive may-nil) -> (nothing); and 3 -> 0, 4 -> 3 in other SCCs.
		pkg := []FuncIR{callRet(1), callRet(2), inactive, callRet(0), callRet(3)}
		res, err := solveOracle(ctx, pkg, len(pkg)+1)
		if err != nil || !slices.Equal(res.MayReturnNil, []bool{true, true, true, true, true}) {
			t.Fatalf("%s: summaries %v err %v, want every caller of the may-nil function to be may-nil", name, res.MayReturnNil, err)
		}
		nat, err := Solve(ctx, pkg)
		if err != nil || !sameResult(nat, res) {
			t.Fatalf("%s: Solve differs: %v", name, err)
		}
	}
	// A no-flag function in the middle: active callers see it as non-nil, as the naive solver does.
	noFlag := safe()
	noFlag.Flags = 0
	pkg := []FuncIR{callRet(1), callRet(2), noFlag, callRet(1)}
	want, _ := naiveSolve(ctx, pkg)
	got, err := solveOracle(ctx, pkg, len(pkg)+1)
	if err != nil || !sameResult(got, want) {
		t.Fatalf("no-flag middle: got %v want %v err %v", got.MayReturnNil, want.MayReturnNil, err)
	}
}

// ---------------------------------------------------------------------------------------------
// ATTACK 2. The sweep cap is per SCC now.
// ---------------------------------------------------------------------------------------------

// minCap is the least cap in [1, limit] with which the callee-first oracle succeeds, or 0.
func minCap(ctx context.Context, pkg []FuncIR, limit int, want PackageResult) (int, error) {
	first := 0
	for c := 1; c <= limit; c++ {
		res, err := solveOracle(ctx, pkg, c)
		if err != nil {
			if first != 0 {
				return 0, fmt.Errorf("cap %d fails but cap %d succeeded: not monotone", c, first)
			}
			continue
		}
		if first == 0 {
			first = c
		}
		if !sameResult(res, want) {
			return 0, fmt.Errorf("cap %d converged to a DIFFERENT result (optimistic/wrong): got %v want %v", c, res.MayReturnNil, want.MayReturnNil)
		}
	}
	return first, nil
}

// Whenever a cap lets the new solver succeed its result is the exact fixpoint (never a too-small
// summary), and a cap that lets the f0dea98 global scan succeed also lets the new solver succeed.
func TestAdv5e61PerSCCCapNeverOptimisticNorStricter(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(0xca9, 0x5e61))
	relaxed := 0
	for trial := range 600 {
		pkg := adv5Graph(rng)
		want, _ := naiveSolve(ctx, pkg)
		old := oldPhase1(ctx, pkg, len(pkg)+1)
		// EXPECTATION CHANGED (ft-5e61 fix A): the cap is per component and the order inside a
		// component is no longer the scan's, so a component can need more sweeps than the scan's
		// global count (a cap of 3 where the scan needed 2). What must hold is the proven bound: a
		// component needs at most one sweep per summary that flips in it plus one confirming
		// sweep, so a cap of flips+1 always succeeds, and every cap that succeeds gives the exact
		// fixpoint (minCap checks that).
		flips := 0
		for _, b := range want.MayReturnNil {
			if b {
				flips++
			}
		}
		limit := flips + 2
		m, err := minCap(ctx, pkg, limit, want)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if m == 0 || m > flips+1 {
			t.Fatalf("trial %d: %d summaries flip but the callee-first solver needs a cap of %d (0 = never within %d): over the proven bound", trial, flips, m, limit)
		}
		if m < old.sweeps {
			relaxed++
		}
	}
	t.Logf("%d of 600 packages need a smaller cap than before", relaxed)
}

// Many small SCCs chained caller-first: the old global cap fails closed, the new one accepts and
// is exactly right. An SCC that needs exactly the cap sweeps passes at the cap and fails one under.
func TestAdv5e61CapManySmallSCCsAndExactSCC(t *testing.T) {
	ctx := context.Background()
	// k two-cycles A_i <-> B_i; A_i also calls A_{i+1}'s cycle, caller-first by index. Only the
	// last cycle contains a nil source.
	const k = 40
	pkg := make([]FuncIR, 2*k)
	callTwo := func(retSlot, a, b uint32) FuncIR {
		return FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0, 0}, Blocks: []BlockIR{{
			Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: a}, {Kind: OpCallConcretePtr, SiteID: 2, SlotA: 1, SlotB: b}},
			Term: Terminator{Kind: TermReturn, SiteID: 3, RetSlot: retSlot}}}}
	}
	for i := range k {
		a, b := uint32(2*i), uint32(2*i+1)
		nextA := uint32(2 * (i + 1))
		if i == k-1 {
			pkg[a] = FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0, 0}, Blocks: []BlockIR{{
				Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: b}},
				Term: Terminator{Kind: TermReturn, SiteID: 3, RetSlot: NoneU32, RetFlags: RetConcreteLiteralNil}}}}
		} else {
			pkg[a] = callTwo(1, b, nextA) // returns the next pair's result, and calls its own partner
		}
		pkg[b] = callTwo(0, a, a) // returns its partner's result: the pair is one cycle
	}
	want, _ := naiveSolve(ctx, pkg)
	old := oldPhase1(ctx, pkg, len(pkg)+1)
	m, err := minCap(ctx, pkg, old.sweeps+2, want)
	if err != nil || m == 0 {
		t.Fatalf("min cap: %d err %v", m, err)
	}
	t.Logf("old index-order scan needs %d sweeps, callee-first needs a cap of %d", old.sweeps, m)
	if oldRes := oldPhase1(ctx, pkg, m); oldRes.err == nil && m < old.sweeps {
		t.Fatal("old scan unexpectedly converged under its own sweep count")
	}
	if m >= old.sweeps {
		t.Fatalf("expected the chained SCCs to relax the cap (old %d, new %d)", old.sweeps, m)
	}
	// An SCC that needs exactly cap sweeps. EXPECTATION CHANGED (ft-5e61 fix A): callCycle(9) used
	// to need 10 (nine changing sweeps, one confirming) in index order; in the callee-first order
	// inside the cycle the nil reaches every function in sweep 1 but function 0, which flips in
	// sweep 2, so it needs 3. The hub ladder (TestOracleCallgraphCapFailsClosed) keeps a many-sweep shape.
	cyc := callCycle(9)
	if _, err := solveOracle(ctx, cyc, 3); err != nil {
		t.Fatalf("cap exactly the sweeps needed: %v", err)
	}
	if _, err := solveOracle(ctx, cyc, 2); err == nil || !strings.Contains(err.Error(), "did not converge within 2 iterations") {
		t.Fatalf("cap one under: %v", err)
	}
	// Production cap is len+1 and the oracle matches it on a worst-case cycle.
	if _, err := SolveOracle(ctx, callCycle(200)); err != nil {
		t.Fatalf("callCycle(200) under the production cap: %v", err)
	}
}

// ---------------------------------------------------------------------------------------------
// ATTACK 3. The budget.
// ---------------------------------------------------------------------------------------------

func TestAdv5e61BudgetBoundariesAndOverflow(t *testing.T) {
	ctx := context.Background()
	pkg := callChain(10)
	size := packageSize(pkg) // 18
	var st phase1Stats
	// factor x size exactly the work, floor 0
	if _, err := solveOracleLimited(ctx, pkg, 11, 1, 0, &st); err != nil || st.work != size {
		t.Fatalf("factor 1 x size = work: err %v work %d size %d", err, st.work, size)
	}
	// factor x size one under (size 18, work 18: factor 1 with a package of size 18 is exact; use floor 17 and factor 0)
	if _, err := solveOracleLimited(ctx, pkg, 11, 0, size-1, &st); err == nil {
		t.Fatal("budget one under the work was accepted")
	}
	// size x factor wraps to 0 modulo 2^64 (18 x 2^63): saturation must make it a huge budget, not 0.
	if _, err := solveOracleLimited(ctx, pkg, 11, 1<<63, 1, &st); err != nil {
		t.Fatalf("size x factor overflowing uint64 wrapped to a tiny budget: %v", err)
	}
	if _, err := solveOracleLimited(ctx, pkg, 11, ^uint64(0), 0, &st); err != nil {
		t.Fatalf("factor MaxUint64: %v", err)
	}
	// floor MaxUint64 never refuses; mulSat/addSat saturate.
	if got := mulSat(1<<63, 2); got != ^uint64(0) {
		t.Fatalf("mulSat(2^63, 2) = %d", got)
	}
	if got := addSat(^uint64(0), 1); got != ^uint64(0) {
		t.Fatalf("addSat(max, 1) = %d", got)
	}
}

// Many tiny functions: the floor dominates (64 x size < 2^20), and the work counted at refusal is
// within one solve of the floor.
func TestAdv5e61FloorDominatesOnTinyFunctions(t *testing.T) {
	ctx := context.Background()
	const n = 1500
	closed := hubLadder(n) // was a closed hubChain: linear since fix A
	size := packageSize(closed)
	if size*Phase1WorkFactor >= Phase1WorkFloor {
		t.Skipf("floor does not dominate for n=%d", n)
	}
	var st phase1Stats
	_, err := solveOracleLimited(ctx, closed, n+1, Phase1WorkFactor, Phase1WorkFloor, &st)
	if err == nil {
		t.Fatalf("closed hub chain n=%d not refused (work %d)", n, st.work)
	}
	if st.work <= Phase1WorkFloor || st.work > Phase1WorkFloor+size {
		t.Fatalf("refused at work %d, floor %d size %d", st.work, Phase1WorkFloor, size)
	}
}

// Both engines refuse the same closed hub-and-chain packages, at the same solve, with the same text.
// The same packages are also solved (or refused) by the f0dea98 scan: this lists the largest closed
// hub that is still accepted and the smallest refused, and what the old scan cost on the refused.
func TestAdv5e61EnginesRefuseTheSameClosedHubs(t *testing.T) {
	ctx := context.Background()
	lastOK, firstRefused := 0, 0
	for _, n := range []int{300, 600, 800, 900, 1000, 1024, 1100, 1200, 1500, 2000} {
		pkg := hubLadder(n) // was a closed hubChain: linear since fix A
		frame, err := EncodePackage(pkg)
		if err != nil {
			t.Fatal(err)
		}
		nb, nerr := SolveFrame(ctx, frame)
		ob, oerr := SolveFrameOracle(ctx, frame)
		if (nerr == nil) != (oerr == nil) || !bytes.Equal(nb, ob) {
			t.Fatalf("n=%d engines differ: native %v oracle %v", n, nerr, oerr)
		}
		if nerr != nil {
			if !strings.Contains(nerr.Error(), oerr.Error()) && nerr.Error() != oerr.Error() {
				t.Fatalf("n=%d refusal texts differ:\n native %q\n oracle %q", n, nerr, oerr)
			}
			if firstRefused == 0 {
				firstRefused = n
			}
			old := oldPhase1(ctx, pkg, n+1)
			t.Logf("n=%d refused (%v); the f0dea98 scan solved it: err=%v in %d solves/%d units over a package of %d units", n, strings.SplitN(nerr.Error(), ":", 2)[0], old.err, old.solves, old.work, packageSize(pkg))
		} else {
			lastOK = n
		}
	}
	t.Logf("largest closed hub accepted %d, smallest refused %d", lastOK, firstRefused)
}

// What a caller sees. The native path hands the Rust refusal back as a wrapped string: say exactly
// what it is, and whether errors.Is can classify it.
func TestAdv5e61RefusalErrorShape(t *testing.T) {
	ctx := context.Background()
	pkg := hubLadder(4096) // was a closed hubChain: linear since fix A
	_, nerr := Solve(ctx, pkg)
	_, oerr := SolveOracle(ctx, pkg)
	if nerr == nil || oerr == nil {
		t.Fatalf("expected refusals, got %v / %v", nerr, oerr)
	}
	t.Logf("Solve error:       %q (type %T)", nerr, nerr)
	t.Logf("SolveOracle error: %q (type %T)", oerr, oerr)
	if nerr.Error() != oerr.Error() {
		t.Errorf("engines print different refusal texts:\n native: %s\n oracle: %s", nerr, oerr)
	}
}

// ---------------------------------------------------------------------------------------------
// ATTACK 4. The decoder.
// ---------------------------------------------------------------------------------------------

func rawFrame(nFuncs uint32, body ...[]byte) []byte {
	f := append([]byte("GSE1"), binary.LittleEndian.AppendUint32(nil, nFuncs)...)
	for _, b := range body {
		f = append(f, b...)
	}
	return f
}

func le32(vs ...uint32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, v)
	}
	return b
}

// A hostile frame claims the largest counts at every level with few bytes. Whatever bound is
// injected (including MaxUint64, where the charge arithmetic could wrap) it is refused or
// truncated, never accepted, never allocates near the claim.
func TestAdv5e61HostileClaimsAtEveryBound(t *testing.T) {
	frames := map[string][]byte{
		"max ops":        rawFrame(1, []byte{0}, le32(0, 0, 1), []byte{TermStop}, le32(0), le32(MaxOpsPerBlock)),
		"max phis":       rawFrame(1, []byte{0}, le32(1, 0, 1), []byte{TermStop}, le32(MaxPhisPerBlock)),
		"max slots":      rawFrame(1, []byte{0}, le32(MaxSlots, 0, 0)),
		"max tuples":     rawFrame(1, []byte{0}, le32(0, MaxTuples, 0)),
		"max blocks":     rawFrame(1, []byte{0}, le32(0, 0, MaxBlocks)),
		"max funcs":      rawFrame(MaxFuncs, []byte{0}, le32(0, 0, 0)),
		"over max funcs": rawFrame(MaxFuncs + 1),
	}
	var before, after runtime.MemStats
	for name, f := range frames {
		for _, bound := range []uint64{0, 1, 95, 96, 1 << 20, MaxDecodedBytes, ^uint64(0)} {
			runtime.ReadMemStats(&before)
			funcs, err := decodePackage(f, bound)
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Errorf("%s bound %d: hostile frame accepted (%d funcs)", name, bound, len(funcs))
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
				t.Errorf("%s bound %d: allocated %d bytes for a %d-byte frame", name, bound, grew, len(f))
			}
		}
	}
}

// Charges exactly at 2 GiB and one over, by the real constant, with a frame whose model cost is
// computed (no real allocation of GiBs: the refusal comes before the claimed structure).
func TestAdv5e61ChargeExactlyAtTwoGiB(t *testing.T) {
	// One function whose single block claims n ops; cost = 96 + 96 + n*56. Choose n so the cost is
	// exactly MaxDecodedBytes if reachable, else the nearest reachable pair around it.
	fixed := uint64(DecodedFuncBytes + DecodedBlockBytes)
	n := (MaxDecodedBytes - fixed) / DecodedOpBytes
	if n > MaxOpsPerBlock {
		// One block cannot claim 2 GiB; claim it across blocks instead: each block claims MaxOpsPerBlock.
		perBlock := uint64(DecodedBlockBytes + MaxOpsPerBlock*DecodedOpBytes)
		blocks := (MaxDecodedBytes - DecodedFuncBytes) / perBlock
		if blocks > MaxBlocks {
			t.Fatalf("the 2 GiB bound is unreachable by one function (%d blocks needed, max %d)", blocks, MaxBlocks)
		}
		t.Logf("2 GiB needs %d blocks of %d ops in one function, or several functions", blocks, MaxOpsPerBlock)
	}
	b := decodeBudget{max: MaxDecodedBytes}
	if err := b.charge(MaxDecodedBytes/DecodedOpBytes, DecodedOpBytes); err != nil {
		t.Fatalf("a charge under the bound refused: %v", err)
	}
	b = decodeBudget{max: MaxDecodedBytes}
	if err := b.charge(MaxDecodedBytes, 1); err != nil {
		t.Fatalf("a charge exactly at the bound refused: %v", err)
	}
	if err := b.charge(1, 1); err == nil {
		t.Fatal("one byte over the bound accepted")
	}
}

// A frame whose bytes are tiny against the bound but whose decoded model passes it: with a small
// injected bound, the frame is refused where the model passes the bound, and accepted at the bound
// to the byte, for the shapes with the largest decoded/frame ratio (ops, empty blocks, phis).
func TestAdv5e61ModelPassesBoundWhileFrameIsSmall(t *testing.T) {
	shapes := map[string]FuncIR{}
	ops := make([]OpIR, 5000)
	for i := range ops {
		ops[i] = OpIR{Kind: OpUnhandledErr, SiteID: uint32(i)}
	}
	shapes["ops"] = FuncIR{SlotFlags: []uint8{0}, Blocks: []BlockIR{{Ops: ops, Term: Terminator{Kind: TermStop}}}}
	blocks := make([]BlockIR, 3000)
	for i := range blocks {
		blocks[i] = BlockIR{Term: Terminator{Kind: TermStop}}
	}
	shapes["empty blocks"] = FuncIR{SlotFlags: []uint8{0}, Blocks: blocks}
	phis := make([]PhiIR, 2000)
	for i := range phis {
		phis[i] = PhiIR{DstSlot: 0, Incoming: []PhiEdge{{PredBB: 0, SrcSlot: 0}}}
	}
	shapes["phis"] = FuncIR{SlotFlags: []uint8{0}, Blocks: []BlockIR{{Phis: phis, Term: Terminator{Kind: TermStop}}}}
	for name, fn := range shapes {
		frame, err := EncodePackage([]FuncIR{fn})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cost := decodedBytesOf([]FuncIR{fn})
		ratio := float64(cost) / float64(len(frame))
		t.Logf("%s: frame %d bytes, model %d decoded bytes (%.2fx)", name, len(frame), cost, ratio)
		if ratio > 11.25 {
			t.Errorf("%s: decoded/frame ratio %.2f exceeds the documented worst case of 11.2", name, ratio)
		}
		if _, err := decodePackage(frame, cost); err != nil {
			t.Errorf("%s at the bound: %v", name, err)
		}
		if _, err := decodePackage(frame, cost-1); err == nil {
			t.Errorf("%s one under: accepted", name)
		}
	}
}

// What the model charges against what the process holds. The model is documented as bounding what
// a decoder builds; Go's append growth leaves capacity slack that the model never charges, so the
// live heap after a decode is a multiple of the charge. A block of 129 ops is the worst case: the
// op slice is made with capacity 128 and grows once, to 256.
func TestAdv5e61DecodedHeapAgainstCharge(t *testing.T) {
	const nBlocks, opsPer = 6000, 129
	blocks := make([]BlockIR, nBlocks)
	opsProto := make([]OpIR, opsPer)
	for i := range opsProto {
		opsProto[i] = OpIR{Kind: OpUnhandledErr, SiteID: uint32(i)}
	}
	for i := range blocks {
		blocks[i] = BlockIR{Ops: opsProto, Term: Terminator{Kind: TermStop}}
	}
	fn := FuncIR{SlotFlags: []uint8{0}, Blocks: blocks}
	frame, err := EncodePackage([]FuncIR{fn})
	if err != nil {
		t.Fatal(err)
	}
	charge := decodedBytesOf([]FuncIR{fn})
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got, err := DecodePackage(frame)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	live := after.HeapAlloc - before.HeapAlloc
	runtime.KeepAlive(got)
	t.Logf("frame %d bytes, charged %d, live decoded heap %d (%.2fx the charge)", len(frame), charge, live, float64(live)/float64(charge))
	if float64(live) > 1.25*float64(charge) {
		t.Errorf("the live decoded heap is %.2fx what the model charged (%d vs %d): the 2 GiB bound admits about %.1f GiB of live memory", float64(live)/float64(charge), live, charge, float64(live)/float64(charge)*2)
	}
}

// Decoder agreement: random byte mutations of valid frames are accepted or refused identically
// by the Rust decoder (through Solve's frame path) and the Go decoder. Only the refusal of a
// decode (not of a solve) is compared.
func TestAdv5e61DecoderEnginesAgreeOnMutations(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(0xdec, 0x5e61))
	for trial := range 1200 {
		pkg := adv5Graph(rng)
		frame, err := EncodePackage(pkg)
		if err != nil {
			t.Fatal(err)
		}
		mut := slices.Clone(frame)
		for range 1 + rng.IntN(3) {
			mut[rng.IntN(len(mut))] ^= byte(1 << rng.IntN(8))
		}
		if rng.IntN(6) == 0 {
			mut = mut[:rng.IntN(len(mut))]
		}
		_, gerr := decodePackage(mut, MaxDecodedBytes)
		nb, nerr := SolveFrame(ctx, mut)
		ob, oerr := SolveFrameOracle(ctx, mut)
		if (nerr == nil) != (oerr == nil) || !bytes.Equal(nb, ob) {
			t.Fatalf("trial %d: native err %v, oracle err %v", trial, nerr, oerr)
		}
		if gerr != nil && oerr == nil {
			t.Fatalf("trial %d: decode refused (%v) but the oracle solved", trial, gerr)
		}
		if nerr != nil && oerr != nil && !strings.Contains(nerr.Error(), oerr.Error()) && nerr.Error() != oerr.Error() {
			t.Fatalf("trial %d: texts differ:\n native %q\n oracle %q", trial, nerr, oerr)
		}
	}
}

// ---------------------------------------------------------------------------------------------
// ATTACK 5. The encoder.
// ---------------------------------------------------------------------------------------------

func TestAdv5e61EncoderBoundsAndSizes(t *testing.T) {
	// A single function larger than the bound, first in the package.
	ops := make([]OpIR, 10000)
	for i := range ops {
		ops[i] = OpIR{Kind: OpUnhandledErr}
	}
	big := FuncIR{SlotFlags: []uint8{0}, Blocks: []BlockIR{{Ops: ops, Term: Terminator{Kind: TermStop}}}}
	for _, bound := range []int{-1 << 62, -1, 0, 1, 7, 8, 9, 100, 4096} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		out, err := encodePackage([]FuncIR{big}, bound)
		runtime.ReadMemStats(&after)
		if err == nil {
			t.Errorf("bound %d: a %d-byte function was encoded into %d bytes", bound, funcWireSize(&big), len(out))
		}
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<10 {
			t.Errorf("bound %d: allocated %d bytes before refusing", bound, grew)
		}
	}
	if out, err := encodePackage(nil, 0); err == nil {
		t.Errorf("empty package at bound 0 gave %d bytes", len(out))
	}
	if out, err := encodePackage(nil, 8); err != nil || len(out) != 8 {
		t.Errorf("empty package at bound 8: %v %d", err, len(out))
	}
	// Random packages: size sums equal the frame, exact bound passes, one under refuses, cap within bound.
	rng := rand.New(rand.NewPCG(0xe2c, 0x5e61))
	for trial := range 400 {
		pkg := adv5Graph(rng)
		full, err := encodePackage(pkg, MaxFrameBytes)
		if err != nil {
			t.Fatal(err)
		}
		want := 8
		for i := range pkg {
			want += int(funcWireSize(&pkg[i]))
		}
		if len(full) != want {
			t.Fatalf("trial %d: frame %d, sizes %d", trial, len(full), want)
		}
		got, err := encodePackage(pkg, len(full))
		if err != nil || !bytes.Equal(got, full) || cap(got) > len(full) {
			t.Fatalf("trial %d: exact bound: err %v cap %d len %d", trial, err, cap(got), len(full))
		}
		if _, err := encodePackage(pkg, len(full)-1); err == nil {
			t.Fatalf("trial %d: one under the frame accepted", trial)
		}
		back, err := decodePackage(full, MaxDecodedBytes)
		if err != nil || len(back) != len(pkg) {
			t.Fatalf("trial %d: round trip %v", trial, err)
		}
	}
}

// ---------------------------------------------------------------------------------------------
// ATTACK 6. Cancellation.
// ---------------------------------------------------------------------------------------------

// pollRec counts Err calls, cancels from the failAt-th, and records how many calls came after it
// (a solver that ignores the error keeps polling) and the phase-1 solves seen between polls.
type pollRec struct {
	context.Context
	calls, failAt, after int
	st                   *phase1Stats
	lastSolves, maxGap   uint64
}

func (c *pollRec) Err() error {
	c.calls++
	if c.st != nil {
		if g := c.st.solves - c.lastSolves; g > c.maxGap {
			c.maxGap = g
		}
		c.lastSolves = c.st.solves
	}
	if c.failAt > 0 && c.calls >= c.failAt {
		c.after++
		return context.Canceled
	}
	return nil
}

func TestAdv5e61CancellationOnLongChain(t *testing.T) {
	if testing.Short() {
		t.Skip("262,144-function chain")
	}
	chain := callChain(MaxFuncs) // callers first: one root, a 262,144-deep search
	var st phase1Stats
	probe := &pollRec{Context: context.Background(), st: &st}
	if _, err := solveOracleLimited(probe, chain, MaxFuncs+1, Phase1WorkFactor, Phase1WorkFloor, &st); err != nil {
		t.Fatal(err)
	}
	t.Logf("chain of %d: %d polls, %d solves, %d components, largest gap of solves between polls %d", MaxFuncs, probe.calls, st.solves, st.sccs, probe.maxGap)
	// Cancel at polls spread over the whole solve, including the very first ones (the search).
	for _, at := range []int{1, 2, 3, 10, 100, probe.calls / 4, probe.calls / 2, probe.calls - 100, probe.calls} {
		if at < 1 {
			continue
		}
		c := &pollRec{Context: context.Background(), failAt: at}
		_, err := solveOracleLimited(c, chain, MaxFuncs+1, Phase1WorkFactor, Phase1WorkFloor, &phase1Stats{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel at poll %d of %d: err = %v", at, probe.calls, err)
		}
		if c.after != 1 {
			t.Fatalf("cancel at poll %d: %d polls after the cancellation, want the solve to stop at the first", at, c.after)
		}
	}
}

// A giant cycle is one component whose sweeps solve every member: how many solves run between two
// polls, and does a cancellation inside the budgeted loop stop it? Both run on the oracle.
func TestAdv5e61CancellationInBudgetedLoop(t *testing.T) {
	cyc := callCycle(20000)
	var st phase1Stats
	probe := &pollRec{Context: context.Background(), st: &st}
	if _, err := solveOracleLimited(probe, cyc, len(cyc)+1, Phase1WorkFactor, Phase1WorkFloor, &st); err != nil {
		t.Fatal(err)
	}
	t.Logf("cycle of %d: %d polls, %d solves, %d sweeps, largest gap of solves between polls %d", len(cyc), probe.calls, st.solves, st.sweeps, probe.maxGap)
	closed := hubLadder(4096) // was a closed hubChain: linear since fix A
	var cst phase1Stats
	cp := &pollRec{Context: context.Background(), st: &cst}
	_, err := solveOracleLimited(cp, closed, 4097, Phase1WorkFactor, Phase1WorkFloor, &cst)
	if err == nil {
		t.Fatal("closed hub accepted")
	}
	for _, at := range []int{1, cp.calls / 2, cp.calls} {
		c := &pollRec{Context: context.Background(), failAt: at}
		_, err := solveOracleLimited(c, closed, 4097, Phase1WorkFactor, Phase1WorkFloor, &phase1Stats{})
		if !errors.Is(err, context.Canceled) || c.after != 1 {
			t.Fatalf("cancel at poll %d of %d in the budgeted loop: err %v, %d polls after", at, cp.calls, err, c.after)
		}
	}
	_ = reflect.DeepEqual
}

// ---------------------------------------------------------------------------------------------
// Attacks found while reading the budget: what a work unit costs.
// ---------------------------------------------------------------------------------------------

// shapeFunc is a straight-line function of nBlocks blocks over nSlots slots that returns a literal
// nil pointer, so it is solved once and flips.
func shapeFunc(nBlocks, nSlots int) FuncIR {
	blocks := make([]BlockIR, nBlocks)
	for i := range blocks {
		blocks[i].Term = Terminator{Kind: TermJump, Target: uint32(i + 1)}
	}
	blocks[nBlocks-1].Term = Terminator{Kind: TermReturn, SiteID: 1, RetSlot: NoneU32, RetFlags: RetConcreteLiteralNil}
	return FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: make([]uint8, nSlots), Blocks: blocks}
}

// The budget unit is blocks plus ops, a count that does not see the slots. Two functions of the
// same unit count (2,048 blocks, no ops) cost very different amounts to solve; the budget lets
// through as many solves of the expensive one as of the cheap one. A bound whose unit varies by
// 1000x in real cost is a bound on a count, not on work: this measures the ratio.
func TestAdv5e61WorkUnitIgnoresSlots(t *testing.T) {
	ctx := context.Background()
	timeSolve := func(fn FuncIR) (best int64, units uint64) {
		pkg := []FuncIR{fn}
		units = packageSize(pkg)
		best = 1 << 62
		for range 3 {
			var m0, m1 runtime.MemStats
			runtime.ReadMemStats(&m0)
			start := nowNanos()
			if _, err := solveOracle(ctx, pkg, 2); err != nil {
				t.Fatal(err)
			}
			d := nowNanos() - start
			runtime.ReadMemStats(&m1)
			best = min(best, d)
			t.Logf("  %d blocks x %d slots: %d ms, %d MiB allocated", len(fn.Blocks), len(fn.SlotFlags), d/1e6, (m1.TotalAlloc-m0.TotalAlloc)>>20)
		}
		return best, units
	}
	narrow, nu := timeSolve(shapeFunc(2048, 1))
	wide, wu := timeSolve(shapeFunc(2048, 4096))
	// EXPECTATION CHANGED (ft-5e61 fix B): the unit used to ignore slots, so both functions had the
	// same count and the wide one cost ~100x more per unit. The unit is now ops plus blocks x
	// max(slots, 1), the state cells MaxStateCells bounds, so the wide function counts 4096x more
	// units. What must hold is that a unit costs about the same on both: the per-unit cost ratio
	// is bounded (it is below 1 here: fixed per-solve costs make a unit of the narrow function
	// dearer).
	if wu < 1000*nu {
		t.Fatalf("units: narrow %d, wide %d; the unit does not see the 4096 slots", nu, wu)
	}
	perUnit := (float64(wide) / float64(wu)) / (float64(narrow) / float64(nu))
	t.Logf("narrow %d units in %d ns, wide %d units in %d ns; a unit costs %.2fx as much on the wide function (min of 3, under load, indicative)", nu, narrow, wu, wide, perUnit)
	if perUnit > 50 {
		t.Errorf("one work unit costs %.0fx more on a 4096-slot function than on a 1-slot one: the unit still misses what a solve costs", perUnit)
	}
	budget := max(uint64(Phase1WorkFloor), wu*Phase1WorkFactor)
	t.Logf("a package of only the wide function is solved at most %d times by its budget of %d units", budget/wu, budget)
}

// A package f0dea98 solved in a fraction of a second, from real source (see the closed hub in the
// report): it is now refused. The ratio of work to size here is about 500, against a factor of 64.
func TestAdv5e61PackageTheOldSolverAcceptedIsNowRefused(t *testing.T) {
	ctx := context.Background()
	pkg := hubChain(1500, true, true, true)
	old := oldPhase1(ctx, pkg, len(pkg)+1)
	if old.err != nil {
		t.Fatalf("the old scan refused it: %v", old.err)
	}
	if _, err := SolveOracle(ctx, pkg); err != nil {
		t.Errorf("f0dea98 solved this %d-function package with %d solves and %d work units (%.0fx its size); it is now refused: %v", len(pkg), old.solves, old.work, float64(old.work)/float64(packageSize(pkg)), err)
	}
}

func nowNanos() int64 { return time.Now().UnixNano() }
