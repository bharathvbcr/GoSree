package adversarial_cfg

// ============================================================================
// Adversarial SSA CFG Borrow Checking Suite (24 Tests)
// ============================================================================

// --- Category 1: Non-Lexical Lifetimes with Complex Branching & Joins (Tests 1-6) ---

// 1. Diamond CFG: Sequential borrow expiry across independent branches (Clean Pass)
func DiamondCFGSequentialExpiry(cond bool) int {
	x := 10
	if cond {
		b1 := &x /*gocap:mut*/
		*b1 = 20
	} else {
		b2 := &x /*gocap:mut*/
		*b2 = 30
	}
	b3 := &x /*gocap:mut*/
	*b3 = 40
	return x
}

// 2. Diamond CFG: Active borrow across join invalidates owner mutation (BORROW003)
func DiamondCFGBorrowActiveAcrossJoin(cond bool) int {
	x := 10
	var b *int
	if cond {
		b = &x /*gocap:rob*/
	} else {
		b = &x /*gocap:rob*/
	}
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	return *b
}

// 3. Early return drop: owner mutated on non-returning branch (Clean Pass)
func EarlyReturnDrop(cond bool) int {
	x := 10
	if cond {
		b := &x /*gocap:mut*/
		*b = 20
		return x
	}
	x = 30
	return x
}

// 4. Multi-branch switch NLL expiry: all branch borrows expired before join (Clean Pass)
func SwitchBranchNLL(tag int) int {
	x := 10
	switch tag {
	case 1:
		b1 := &x /*gocap:mut*/
		*b1 = 20
	case 2:
		b2 := &x /*gocap:rob*/
		println(*b2)
	default:
		x = 30
	}
	b3 := &x /*gocap:mut*/
	*b3 = 50
	return x
}

// 5. Switch branch borrow conflicting with active outer borrow (BORROW002)
func SwitchBranchConflict(tag int) {
	x := 10
	bOuter := &x /*gocap:rob*/
	switch tag {
	case 1:
		bInner := &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
		*bInner = 20
	}
	println(*bOuter)
}

// 6. Select statement branch NLL expiry (Clean Pass)
func SelectBranchNLL(ch1, ch2 chan int) int {
	x := 10
	select {
	case <-ch1:
		b := &x /*gocap:mut*/
		*b = 100
	case <-ch2:
		b := &x /*gocap:mut*/
		*b = 200
	default:
		x = 300
	}
	bFinal := &x /*gocap:mut*/
	*bFinal = 400
	return x
}

// --- Category 2: Loops, Breaks, Continues (Tests 7-12) ---

// 7. Loop body local borrow with continue (Clean Pass)
func LoopContinueClean(n int) {
	x := 10
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			continue
		}
		b := &x /*gocap:mut*/
		*b = i
	}
}

// 8. Loop body local borrow with break followed by owner mutation (Clean Pass)
func LoopBreakCleanMutation(n int) int {
	x := 10
	for i := 0; i < n; i++ {
		b := &x /*gocap:mut*/
		*b = i
		if i == 5 {
			break
		}
	}
	x = 99
	return x
}

// 9. Nested loop with labeled break and NLL expiry (Clean Pass)
func LabeledBreakNLL() int {
	x := 10
Outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			b := &x /*gocap:mut*/
			*b = i + j
			if i == 1 && j == 1 {
				break Outer
			}
		}
	}
	x = 50
	return x
}

// 10. Mutation inside loop while outer borrow active across backedge (BORROW003)
func LoopBackedgeOwnerMutation(n int) {
	x := 10
	b := &x /*gocap:mut*/
	for i := 0; i < n; i++ {
		x = i // want `BORROW003: cannot assign to 'x' because it is borrowed`
		*b = i + 1
	}
}

// 11. Outer borrow used after loop, mutated inside loop body (BORROW003)
func LoopBodyOwnerMutationBorrowUsedAfter() {
	x := 10
	b := &x /*gocap:rob*/
	for i := 0; i < 2; i++ {
		x++ // want `BORROW003: cannot assign to 'x' because it is borrowed`
	}
	println(*b)
}

// 12. Loop-carried borrow assigned to outer pointer with break (BORROW002)
func LoopCarriedEscapeWithBreak() {
	x := 10
	var outer *int
	for i := 0; i < 5; i++ {
		outer = &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable: loop-carried borrow escapes to outer scope`
		if i == 2 {
			break
		}
	}
	*outer = 42
}

// --- Category 3: Defer Statements (Tests 13-15) ---

// 13. Multiple defer statements extending lifetime to return (BORROW003)
func MultipleDefersExtendingLifetime() {
	x := 10
	b1 := &x /*gocap:rob*/
	defer println(*b1)
	b2 := &x /*gocap:rob*/
	defer println(*b2)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
}

// 14. Conditional defer extending lifetime (BORROW003)
func ConditionalDeferExtendingLifetime(cond bool) {
	x := 10
	if cond {
		b := &x /*gocap:rob*/
		defer println(*b)
	}
	x = 30 // want `BORROW003: cannot assign to 'x' because it is borrowed`
}

// 15. Unrelated defer does not prevent sequential expiry (Clean Pass)
func DeferUnrelatedCleanNLL() int {
	x := 10
	defer println("done")
	b := &x /*gocap:mut*/
	*b = 20
	x = 30
	return x
}

// --- Category 4: Owner Variable Mutation & Shadowing Resolution (Tests 16-20) ---

// 16. Parameter shadowed by type switch variable (Clean Pass)
func ParamShadowedByTypeSwitch(x any, ch chan int) {
	val := 100
	b := &val /*gocap:rob*/
	switch x := x.(type) {
	case int:
		x = x + 1
		_ = x
	}
	println(*b)
}

// 17. Deeply nested blocks shadowing struct owner (BORROW003 on true owner only)
type CfgPoint struct {
	X int
	Y int
}

func DeepNestedBlockShadowing() {
	pt := CfgPoint{X: 1, Y: 2}
	b := &pt.X /*gocap:mut*/
	{
		pt := 99
		{
			pt := "hello"
			_ = pt
		}
		pt = 100
		_ = pt
	}
	pt.X = 10 // want `BORROW003: cannot assign to 'pt' because it is borrowed`
	*b = 20
}

// 18. Tuple assignment owner mutation detection (BORROW003)
func TupleAssignOwnerMutation() {
	x := 10
	y := 20
	b := &x /*gocap:rob*/
	x, y = 30, 40 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	_ = y
	println(*b)
}

// 19. Compound assignment operator mutation detection (BORROW003)
func CompoundAssignMutation() {
	x := 10
	b := &x /*gocap:rob*/
	x += 5 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	println(*b)
}

// 20. Multi-dimensional array element invalidation (BORROW003)
func MultiDimArrayOwnerMutation() {
	var grid [2][2]int
	b := &grid[0][1] /*gocap:mut*/
	grid[0][1] = 99 // want `BORROW003: cannot assign to 'grid' because it is borrowed`
	*b = 42
}

// --- Category 5: Conflicting Borrows & Aliasing (Tests 21-24) ---

// 21. Disjoint branch mutable borrows without join conflict (Clean Pass)
func DisjointBranchMutBorrows(cond bool) {
	x := 10
	if cond {
		b1 := &x /*gocap:mut*/
		*b1 = 20
	} else {
		b2 := &x /*gocap:mut*/
		*b2 = 30
	}
}

// 22. Sequential read-only borrow after mutable borrow expiry (Clean Pass)
func SequentialRobAfterMubExpiry() int {
	x := 10
	{
		m := &x /*gocap:mut*/
		*m = 25
	}
	r := &x /*gocap:rob*/
	return *r
}

// 23. Re-borrow mutable from mutable while active conflict (BORROW002)
func ReborrowMutFromMutConflict() {
	x := 10
	b1 := &x /*gocap:mut*/
	b2 := b1 /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable more than once at a time`
	*b1 = 20
	*b2 = 30
}

// 24. Three-way conflict: rob + mub + rob (BORROW002)
func ThreeWayRobMubConflict() {
	x := 10
	r1 := &x /*gocap:rob*/
	m := &x /*gocap:mut*/ // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	r2 := &x /*gocap:rob*/ // want `BORROW002: cannot borrow 'x' as read-only because it is also borrowed as mutable`
	println(*r1)
	*m = 100
	println(*r2)
}
