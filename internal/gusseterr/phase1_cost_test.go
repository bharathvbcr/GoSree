package gusseterr

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// hubChain builds n functions: a hub that calls every member of a chain of n-1, and the chain,
// in which member j calls member j+1 and the last member returns a literal nil pointer. With
// hubFirst the hub has index 0 and the members 1..n-1, otherwise the members are 0..n-2 and the
// hub is n-1. With callersFirst member j calls the member with the next higher index (the
// shape whose summaries flip one per index-order sweep); otherwise the next lower one. With
// closed the last member also calls the hub, which puts the hub and the whole chain in one cycle.
func hubChain(n int, hubFirst, callersFirst, closed bool) []FuncIR {
	m := n - 1
	hub := uint32(n - 1)
	member := func(j int) uint32 { return uint32(j) }
	if hubFirst {
		hub = 0
		member = func(j int) uint32 { return uint32(j + 1) }
	}
	// pos maps chain position j (0 = the member that returns nil) to its index.
	pos := func(j int) uint32 {
		if callersFirst {
			return member(m - 1 - j)
		}
		return member(j)
	}
	funcs := make([]FuncIR, n)
	hubOps := make([]OpIR, 0, m+1)
	for j := range m {
		hubOps = append(hubOps, OpIR{Kind: OpCallConcretePtr, SiteID: uint32(j + 1), SlotA: 0, SlotB: pos(j)})
	}
	hubOps = append(hubOps, OpIR{Kind: OpAssignSlot, SiteID: uint32(m + 1), SlotA: 1, SlotB: NoneU32, NilHint: AssignNilNonNil})
	funcs[hub] = FuncIR{
		Flags:     FuncReturnsConcreteErrPtr,
		SlotFlags: []uint8{0, 0},
		Blocks:    []BlockIR{{Ops: hubOps, Term: Terminator{Kind: TermReturn, SiteID: 1, RetSlot: 1}}},
	}
	for j := range m {
		fn := FuncIR{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0}}
		if j == 0 {
			var ops []OpIR
			if closed {
				ops = []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: hub}}
			}
			fn.Blocks = []BlockIR{{Ops: ops, Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: NoneU32, RetFlags: RetConcreteLiteralNil}}}
		} else {
			fn.Blocks = []BlockIR{{
				Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 1, SlotA: 0, SlotB: pos(j - 1)}},
				Term: Terminator{Kind: TermReturn, SiteID: 2, RetSlot: 0},
			}}
		}
		funcs[pos(j)] = fn
	}
	return funcs
}

// TestPhase1WorkIsLinearOnHubAndChain: phase-1 work on a hub calling every member of a
// caller-first chain grows linearly with the package, not quadratically. The measure is the
// work units of the phase-1 solves (blocks plus ops per solve), which depend on the package
// alone, not on the machine's speed.
func TestPhase1WorkIsLinearOnHubAndChain(t *testing.T) {
	sizes := []int{4096, 16384}
	if !testing.Short() {
		sizes = append(sizes, 65536)
	}
	for _, hubFirst := range []bool{true, false} {
		var prev uint64
		for _, n := range sizes {
			var st phase1Stats
			res, err := solveOracleLimited(context.Background(), hubChain(n, hubFirst, true, false), n+1, Phase1WorkFactor, Phase1WorkFloor, &st)
			if err != nil {
				t.Fatalf("hubFirst=%v n=%d: %v", hubFirst, n, err)
			}
			for i, nilable := range res.MayReturnNil {
				if want := i != hubIndex(n, hubFirst); nilable != want {
					t.Fatalf("hubFirst=%v n=%d: func %d may-nil = %v, want %v", hubFirst, n, i, nilable, want)
				}
			}
			if want := uint64(n); st.solves != want || st.work != packageSize(hubChain(n, hubFirst, true, false)) {
				t.Fatalf("hubFirst=%v n=%d: %d solves and %d work units, want one solve per function (%d) and the package size (%d)",
					hubFirst, n, st.solves, st.work, want, packageSize(hubChain(n, hubFirst, true, false)))
			}
			t.Logf("hubFirst=%v n=%d: %d solves, %d work units (%.1f per function)", hubFirst, n, st.solves, st.work, float64(st.work)/float64(n))
			if prev != 0 && st.work > prev*5 {
				t.Fatalf("hubFirst=%v: work grew %.1fx for a 4x package (%d -> %d): quadratic", hubFirst, float64(st.work)/float64(prev), prev, st.work)
			}
			prev = st.work
		}
	}
}

func hubIndex(n int, hubFirst bool) int {
	if hubFirst {
		return 0
	}
	return n - 1
}

// packageSize is the phase-1 budget unit of a package: blocks plus ops over every function.
func packageSize(funcs []FuncIR) uint64 {
	var size uint64
	for i := range funcs {
		size += uint64(len(funcs[i].Blocks))
		for _, b := range funcs[i].Blocks {
			size += uint64(len(b.Ops))
		}
	}
	return size
}

// selfRecursive returns a nil pointer on one path and the result of its own call on another.
func selfRecursive() FuncIR {
	return FuncIR{
		Flags:     FuncReturnsConcreteErrPtr,
		SlotFlags: []uint8{0},
		Blocks: []BlockIR{
			{Term: Terminator{Kind: TermCond, Target: 1, FalseBB: 2, CondKind: CondOpaque, CondSlot: NoneU32}},
			{Term: Terminator{Kind: TermReturn, SiteID: 1, RetSlot: NoneU32, RetFlags: RetConcreteLiteralNil}},
			{
				Ops:  []OpIR{{Kind: OpCallConcretePtr, SiteID: 2, SlotA: 0, SlotB: 0}},
				Term: Terminator{Kind: TermReturn, SiteID: 3, RetSlot: 0},
			},
		},
	}
}

// TestPhase1ShapesMatchNaiveAndEngine: every call-graph shape the order change could get wrong
// (empty, single, self-recursive, mutual recursion, one cycle through every function, a hub and
// chain in all four index orders, closed into a cycle) gets the summaries and findings of the
// naive sweep-everything solver from the oracle and from Solve (the Rust engine when linked).
func TestPhase1ShapesMatchNaiveAndEngine(t *testing.T) {
	ctx := context.Background()
	mutual := []FuncIR{selfRecursive(), selfRecursive()}
	mutual[0].Blocks[2].Ops[0].SlotB = 1
	mutual[1].Blocks[2].Ops[0].SlotB = 0
	mutual[1].Blocks[1].Term.RetFlags = 0 // only function 0 can return nil by itself
	shapes := map[string][]FuncIR{
		"empty":               nil,
		"single non-nil":      {{Flags: FuncReturnsConcreteErrPtr, SlotFlags: []uint8{0}, Blocks: []BlockIR{{Ops: []OpIR{{Kind: OpAssignSlot, SlotA: 0, SlotB: NoneU32, NilHint: AssignNilNonNil}}, Term: Terminator{Kind: TermReturn, RetSlot: 0}}}}},
		"self recursive":      {selfRecursive()},
		"mutual recursion":    mutual,
		"cycle of 1000":       callCycle(1000),
		"chain of 1000":       callChain(1000),
		"hub first, forward":  hubChain(600, true, true, false),
		"hub first, backward": hubChain(600, true, false, false),
		"hub last, forward":   hubChain(600, false, true, false),
		"hub last, backward":  hubChain(600, false, false, false),
		"closed hub, small":   hubChain(60, true, true, true),
		"closed hub last":     hubChain(60, false, true, true),
	}
	for name, funcs := range shapes {
		want, err := naiveSolve(ctx, funcs)
		if err != nil {
			t.Fatalf("%s naive: %v", name, err)
		}
		for engine, solve := range map[string]func(context.Context, []FuncIR) (PackageResult, error){"oracle": SolveOracle, "Solve": Solve} {
			got, err := solve(ctx, funcs)
			if err != nil {
				t.Fatalf("%s %s: %v", name, engine, err)
			}
			if !reflect.DeepEqual(got.MayReturnNil, want.MayReturnNil) && (len(got.MayReturnNil) != 0 || len(want.MayReturnNil) != 0) || !slices.Equal(got.Findings, want.Findings) {
				t.Fatalf("%s %s differs from the naive solver", name, engine)
			}
		}
	}
	if !naiveMayNil(t, "self recursive", shapes["self recursive"])[0] {
		t.Fatal("the self-recursive function must be may-nil: it has a nil return path")
	}
	if got := naiveMayNil(t, "mutual recursion", mutual); !got[0] || !got[1] {
		t.Fatalf("mutual recursion summaries = %v, want both may-nil (0 returns nil, 1 returns 0's result)", got)
	}
}

func naiveMayNil(t *testing.T, name string, funcs []FuncIR) []bool {
	t.Helper()
	res, err := naiveSolve(context.Background(), funcs)
	if err != nil {
		t.Fatalf("%s naive: %v", name, err)
	}
	return res.MayReturnNil
}

// TestPhase1ChainAtMaxFuncsInBothEngines: a hub calling a caller-first chain of exactly MaxFuncs
// functions is solved once per function by both engines (this took 186 s on f0dea98) and they
// agree; one function more is refused by the encoder.
func TestPhase1ChainAtMaxFuncsInBothEngines(t *testing.T) {
	ctx := context.Background()
	funcs := hubChain(MaxFuncs, true, true, false)
	var st phase1Stats
	want, err := solveOracleLimited(ctx, funcs, len(funcs)+1, Phase1WorkFactor, Phase1WorkFloor, &st)
	if err != nil {
		t.Fatal(err)
	}
	if st.solves != MaxFuncs || st.work != packageSize(funcs) {
		t.Fatalf("%d solves, %d work units at MaxFuncs; want %d solves and the package size %d", st.solves, st.work, MaxFuncs, packageSize(funcs))
	}
	got, err := Solve(ctx, funcs)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.MayReturnNil, want.MayReturnNil) || !slices.Equal(got.Findings, want.Findings) {
		t.Fatal("Solve and the oracle disagree on the MaxFuncs hub and chain")
	}
	if _, err := EncodePackage(append(funcs, FuncIR{})); err == nil {
		t.Fatal("MaxFuncs+1 functions must be refused")
	}
}

// TestClosedHubChainFailsClosed: a hub calling every member of a chain that closes back on the
// hub is one cycle, quadratic in sweeps, and phase 1 refuses it by its work budget in both
// engines instead of running for minutes: the same error, after a bounded amount of work.
func TestClosedHubChainFailsClosed(t *testing.T) {
	ctx := context.Background()
	const n = 4096
	closed := hubChain(n, true, true, true)
	size := packageSize(closed)
	var st phase1Stats
	_, err := solveOracleLimited(ctx, closed, n+1, Phase1WorkFactor, Phase1WorkFloor, &st)
	if err == nil || !strings.Contains(err.Error(), "phase-1 work budget exceeded") {
		t.Fatalf("closed hub and chain: err = %v, want the work budget error", err)
	}
	budget := max(uint64(Phase1WorkFloor), size*Phase1WorkFactor)
	if st.work <= budget || st.work > budget+size {
		t.Fatalf("refused after %d work units; the budget is %d and one solve is at most %d", st.work, budget, size)
	}
	want := fmt.Sprintf("phase-1 work budget exceeded: %d work units against a budget of %d", st.work, budget)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to name %q", err, want)
	}
	if _, err := Solve(ctx, closed); err == nil || !strings.Contains(err.Error(), "phase-1 work budget exceeded") {
		t.Fatalf("Solve on the closed hub and chain: err = %v, want the work budget error", err)
	}
	if _, err := SolveOracle(ctx, closed); err == nil || !strings.Contains(err.Error(), "phase-1 work budget exceeded") {
		t.Fatalf("SolveOracle on the closed hub and chain: err = %v, want the work budget error", err)
	}
	// The same shape, left open, is solved in linear work.
	if _, err := Solve(ctx, hubChain(n, true, true, false)); err != nil {
		t.Fatalf("the open hub and chain: %v", err)
	}
}

// TestPhase1BudgetBoundary: the budget is max(floor, factor x size), the check is "work over
// budget", and the error text is the one the Rust engine prints (its twin test pins the same
// string).
func TestPhase1BudgetBoundary(t *testing.T) {
	ctx := context.Background()
	funcs := callChain(10) // nine solves of two work units each
	var st phase1Stats
	if _, err := solveOracleLimited(ctx, funcs, 11, 0, 18, &st); err != nil || st.work != 18 {
		t.Fatalf("budget exactly the work (18): err = %v, work = %d", err, st.work)
	}
	_, err := solveOracleLimited(ctx, funcs, 11, 0, 17, &st)
	want := fmt.Sprintf("phase-1 work budget exceeded: 18 work units against a budget of 17 (ops plus blocks of every function solved; the budget is %d times the package's ops plus blocks, at least %d)", Phase1WorkFactor, Phase1WorkFloor)
	if err == nil || err.Error() != want {
		t.Fatalf("budget one under the work: err = %v, want %q", err, want)
	}
	// factor x size beats a lower floor: size 18 x 2 = 36.
	if _, err := solveOracleLimited(ctx, funcs, 11, 2, 1, &st); err != nil {
		t.Fatalf("factor 2 x size 18 = 36 >= 18 work: %v", err)
	}
	if _, err := solveOracleLimited(ctx, funcs, 11, 0, 0, &st); err == nil {
		t.Fatal("a zero budget refuses any package with a solve")
	}
	if _, err := solveOracleLimited(ctx, nil, 1, 0, 0, &st); err != nil {
		t.Fatalf("an empty package does no work and passes any budget: %v", err)
	}
}

// pollCtx is a context that reports cancellation from its failAt-th Err call on and counts the calls.
type pollCtx struct {
	context.Context
	calls, failAt int
}

func (c *pollCtx) Err() error {
	c.calls++
	if c.failAt > 0 && c.calls >= c.failAt {
		return context.Canceled
	}
	return nil
}

// TestPhase1PollsCancellationPerComponentAndSweep: the context is polled at least once per
// component and per sweep, and a cancellation at any one poll (in the component search or in the
// swept loop) stops the solve with the context's error.
func TestPhase1PollsCancellationPerComponentAndSweep(t *testing.T) {
	for name, funcs := range map[string][]FuncIR{
		"chain":  callChain(40),
		"cycle":  callCycle(25),
		"hub":    hubChain(30, true, true, false),
		"closed": hubChain(12, true, true, true),
	} {
		var st phase1Stats
		counter := &pollCtx{Context: context.Background()}
		if _, err := solveOracleLimited(counter, funcs, len(funcs)+1, Phase1WorkFactor, Phase1WorkFloor, &st); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if uint64(counter.calls) < st.sccs+st.sweeps {
			t.Fatalf("%s: %d polls for %d components and %d sweeps", name, counter.calls, st.sccs, st.sweeps)
		}
		for failAt := 1; failAt <= counter.calls; failAt++ {
			c := &pollCtx{Context: context.Background(), failAt: failAt}
			if _, err := solveOracleLimited(c, funcs, len(funcs)+1, Phase1WorkFactor, Phase1WorkFloor, &phase1Stats{}); err == nil {
				t.Fatalf("%s: cancellation at poll %d of %d was ignored", name, failAt, counter.calls)
			}
		}
	}
}

// TestCallSCCsIsIterativeAndCallersFirst: the component search finds the right components on a
// chain far deeper than a recursive search could hold on a goroutine stack, and emits callers first.
func TestCallSCCsIsIterativeAndCallersFirst(t *testing.T) {
	const n = 2_000_000
	callers := make([][]uint32, n) // callers[v] = {v-1}: v-1 calls v, one long chain
	active := make([]bool, n)
	for v := range n {
		active[v] = true
		if v > 0 {
			callers[v] = []uint32{uint32(v - 1)}
		}
	}
	members, ends, err := callSCCs(context.Background(), callers, active)
	if err != nil {
		t.Fatal(err)
	}
	if len(ends) != n || len(members) != n {
		t.Fatalf("%d components over %d members, want %d singletons", len(ends), len(members), n)
	}
	// Function 0 calls 1 calls 2 ...: callers first means 0 is emitted before 1.
	if members[0] != 0 || members[n-1] != n-1 {
		t.Fatalf("emission order starts at %d and ends at %d, want callers first (0 ... n-1)", members[0], members[n-1])
	}
	// One cycle through every function is one component.
	callers[0] = []uint32{uint32(n - 1)}
	members, ends, err = callSCCs(context.Background(), callers, active)
	if err != nil || len(ends) != 1 || len(members) != n {
		t.Fatalf("cycle through %d functions: %d components, err %v", n, len(ends), err)
	}
}
