// Package bw_nll attacks the non-lexical-lifetime model of the borrow checker: where a borrow's
// last use is, and whether the checker sees a use that is still ahead of it.
package bw_nll

func use(p *int) { *p++ }

// 1. control: two overlapping mutable borrows in straight-line code.
func straightConflict() {
	x := 0
	b1 := &x //gocap:mut
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 2. loop back-edge in a single basic block: b1 is used at the top of every iteration, b2 is
// created after that use in the same block. b1 is used again on the next iteration, so it is live
// across the whole loop and b2 overlaps it (Rust E0499). isBorrowActiveAt scans the rest of b2's
// block and then BFSes successors with the start block pre-marked visited, so the use of b1 at
// the top of the same block, reached only around the back edge, is never seen.
func loopBackEdgeSameBlock(n int) {
	x := 0
	b1 := &x //gocap:mut
	for i := 0; i < n; i++ {
		*b1 = i
		b2 := &x //gocap:mut // MISS(BORROW002): b1 is used on the next iteration, so it is live here; docs/BORROW_CHECKING_AND_NLL.md §3 defines Live(b) by any path to a use, back edges included
		*b2 = i
	}
}

// 3. control for 2: the same shape with a branch in the body, so the earlier use of b1 sits in a
// different basic block from b2's creation and the forward BFS reaches it through the header.
func loopBackEdgeOtherBlock(n int) {
	x := 0
	b1 := &x //gocap:mut
	for i := 0; i < n; i++ {
		if i > 0 {
			*b1 = i
		}
		b2 := &x //gocap:mut // HIT(BORROW002)
		*b2 = i
	}
}

// 4. owner write in a loop, same block as an earlier borrow use: x is written while b is live
// (b is read again on the next iteration).
func loopOwnerWriteSameBlock(n int) {
	x := 0
	b := &x //gocap:rob
	s := 0
	for i := 0; i < n; i++ {
		s += *b
		x = i // HIT(BORROW003)
	}
	_ = s
}

// 5. borrow kept alive only through a deferred closure: the deferred func runs at return, after
// the owner write, so b is live there.
func deferredClosure() {
	x := 0
	b := &x //gocap:mut
	defer func() { *b = 1 }()
	x = 2 // HIT(BORROW003)
}

// 6. borrow kept alive through a closure that is called later: f holds b, and the call to f
// after the second borrow is a use of b.
func closureCalledLater() {
	x := 0
	b1 := &x //gocap:mut
	f := func() { *b1 = 1 }
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 2
	f()
}

// 7. a function value that captured b is passed down to a helper that calls it: the use is
// hidden in `run(f)`, which only names f.
func run(f func()) { f() }

func closurePassedDown() {
	x := 0
	b1 := &x //gocap:mut
	f := func() { *b1 = 1 }
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 2
	run(f)
}

// 8. use through a pointer-to-borrow: pb := &b1 is created before the second borrow, and **pb is
// used after it. The second-order alias keeps b1 live.
func pointerToBorrow() {
	x := 0
	b1 := &x //gocap:mut
	pb := &b1
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 1
	**pb = 2
}

// 9. laundering the alias through a store: the borrow is copied into c by `*pc = b1`, which the
// alias tracker (assignments with an identifier LHS only) never sees. c then outlives b2.
func storeLaunder() {
	x := 0
	b1 := &x //gocap:mut
	var c *int
	pc := &c
	*pc = b1 // HIT(BORROW001)
	b2 := &x //gocap:mut // MISS(BORROW002): c holds b1 (written through *pc) and is used below, so b1 is live; docs/BORROW_CHECKING_AND_NLL.md §3 counts "any value derived from it"
	*b2 = 1
	*c = 2
}

// 10. a borrow returned through an identity helper keeps the original alive: q is derived from b1
// and is used after b2 is taken.
func identity(p *int) *int { return p }

func throughIdentity() {
	x := 0
	b1 := &x //gocap:mut
	q := identity(b1)
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 1
	*q = 2
}

// 11. a goto-made loop: the use of b1 sits at the label, before b2's creation in source order but
// after it in control flow.
func gotoLoop(n int) {
	x := 0
	b1 := &x //gocap:mut
	i := 0
again:
	*b1 = i
	b2 := &x //gocap:mut // MISS(BORROW002): the goto back edge reaches `*b1 = i` again, so b1 is live here (same same-block back-edge blind spot as loopBackEdgeSameBlock)
	*b2 = i
	i++
	if i < n {
		goto again
	}
}

// 11b. FP: a borrow variable declared before the loop, re-borrowed and consumed inside each
// iteration, never read after the loop. checkLoopEscapes is purely syntactic: any `v = &x` with a
// mut annotation inside a loop where v is declared outside is reported, whether or not the value
// survives the iteration. Rust accepts this (`let mut b; for .. { b = &mut x; *b += 1; }`).
func reborrowEachIteration(n int) {
	x := 0
	var b *int
	for i := 0; i < n; i++ {
		b = &x //gocap:mut // FP(BORROW002): b is written and last used within the same iteration; nothing is carried across the back edge or out of the loop
		*b += i
	}
	x = 1
}

// 11c. FP: reassigning a borrow variable to a different owner ends the first loan, but every later
// textual use of b is still counted as a use of the first borrow (collectUses keys on the variable,
// not on the reaching definition), so a fresh borrow of x after the reassignment is rejected. Rust
// NLL accepts the twin (`let mut b = &mut x; *b = 1; b = &mut y; let c = &mut x; *c = 2; *b = 3;`).
func reassignToOtherOwner() {
	x, y := 0, 0
	b := &x //gocap:mut
	*b = 1
	b = &y  //gocap:mut
	c := &x //gocap:mut // FP(BORROW002): b now points at y; the loan on x died at the reassignment
	*c = 2
	*b = 3
}

// 12. FP probe: sequential reuse after the last use, the case NLL exists for.
func sequentialReuse() {
	x := 0
	b1 := &x //gocap:mut
	*b1 = 1
	b2 := &x //gocap:mut
	*b2 = 2
}

// 13. FP probe: a borrow passed down to a helper and used again after the helper returns, with no
// second borrow in between. Reborrowing for a call is fine in Rust.
func passDownThenUse() {
	x := 0
	b := &x //gocap:mut
	use(b)
	*b = 3
}

// 14. FP probe: shared borrows read in a loop.
func sharedInLoop(n int) int {
	x := 5
	r1 := &x //gocap:rob
	s := 0
	for i := 0; i < n; i++ {
		r2 := &x //gocap:rob
		s += *r1 + *r2
	}
	return s
}

// 15. FP probe: the early-return error path. b's last use is on the error branch; the success path
// takes a fresh mutable borrow after the branch.
func earlyReturn(fail bool) int {
	x := 0
	b := &x //gocap:mut
	if fail {
		*b = -1
		return x
	}
	b2 := &x //gocap:mut
	*b2 = 1
	return x
}
