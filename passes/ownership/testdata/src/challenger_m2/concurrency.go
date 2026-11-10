package challenger_m2

import (
	"sync"
)

type Packet struct {
	ID   int
	Data []byte
}

var leakedPacket *Packet // want `SAFE006: package variable leakedPacket is written outside init`

func sink(p *Packet) {}

// ============================================================================
// Group 1: \iso Move Invalidation Across Channels and Goroutines (SAFE003)
// ============================================================================

// 1.1 Read field after channel send
func isoSendReadField(ch chan *Packet) int {
	p := &Packet{ID: 1}
	ch <- p
	return p.ID // want `SAFE003: composite literal is used after it was sent on a channel`
}

// 1.2 Write field after channel send
func isoSendWriteField(ch chan *Packet) {
	p := &Packet{ID: 1}
	ch <- p
	p.ID = 2 // want `SAFE003: composite literal is used after it was sent on a channel`
}

// 1.3 Pass to function after channel send
func isoSendPassToFunc(ch chan *Packet) {
	p := &Packet{ID: 1}
	ch <- p
	sink(p) // want `SAFE003: composite literal is used after it was sent on a channel`
}

// 1.4 Reassignment after channel send kills the move
func isoSendReassigned(ch chan *Packet) int {
	p := &Packet{ID: 1}
	ch <- p
	p = &Packet{ID: 2}
	return p.ID // Clean Pass: re-bound to fresh allocation
}

// 1.5 Goroutine capture then parent access
func isoGoroutineCapture() int {
	p := &Packet{ID: 1}
	go func() {
		p.ID = 2
	}()
	p.ID = 3 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.ID
}

// 1.6 Goroutine argument then parent access
func isoGoroutineArg() int {
	p := &Packet{ID: 1}
	go sink(p)
	p.ID = 2 // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p.ID
}

// 1.7 Two goroutines capturing the same variable
func isoDoubleGoroutineCapture() {
	p := &Packet{ID: 1}
	go func() { p.ID = 2 }()
	go func() { p.ID = 3 }() // want `SAFE003: variable p is used after it was captured by a goroutine`
}

// 1.8 Repeated channel send in a loop
func isoLoopSend(ch chan *Packet) {
	p := &Packet{ID: 1}
	for range 2 {
		ch <- p // want `SAFE003: composite literal is used after it was sent on a channel`
	}
}

// 1.9 Aliased variable use after send
func isoAliasedSend(ch chan *Packet) int {
	p := &Packet{ID: 1}
	alias := p
	ch <- p
	return alias.ID // want `SAFE003: composite literal is used after it was sent on a channel`
}

// 1.10 Flow-sensitive conditional move: else branch is NOT invalidated
func isoConditionalSendElse(ch chan *Packet, cond bool) int {
	p := &Packet{ID: 1}
	if cond {
		ch <- p
		return 0
	} else {
		p.ID = 10 // Clean Pass: send did not execute along this branch
		return p.ID
	}
}

// 1.11 Flow-sensitive conditional move: merge point IS invalidated
func isoConditionalSendMerge(ch chan *Packet, cond bool) int {
	p := &Packet{ID: 1}
	if cond {
		ch <- p
	}
	return p.ID // want `SAFE003: composite literal is used after it was sent on a channel`
}

// ============================================================================
// Group 2: Select Statements with Send and Default / Receive Branches
// ============================================================================

// 2.1 Use inside send branch after send
func selectSendThenUseInCase(ch chan *Packet) {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
		p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	default:
	}
}

// 2.2 Send branch continues, use after select
func selectSendThenUseAfterSelect(ch chan *Packet) int {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
		// fallthrough to end of select
	default:
		return 0
	}
	return p.ID // want `SAFE003: composite literal is used after it was sent on a channel in select`
}

// 2.3 Default branch preserves ownership (Clean Pass)
func selectDefaultPreservesOwnership(ch chan *Packet) int {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
		return 0
	default:
		p.ID = 20 // Clean Pass: send was not selected
		return p.ID
	}
}

// 2.4 Multi-case select with disjoint values preserves unselected values
func selectMultiSendDisjoint(ch1, ch2 chan *Packet) int {
	p1 := &Packet{ID: 1}
	p2 := &Packet{ID: 2}
	select {
	case ch1 <- p1:
		p2.ID = 20 // Clean Pass: p2 was not sent in case 1
		return p2.ID
	case ch2 <- p2:
		p1.ID = 10 // Clean Pass: p1 was not sent in case 2
		return p1.ID
	default:
		p1.ID = 100 // Clean Pass: neither was sent
		p2.ID = 200 // Clean Pass: neither was sent
		return p1.ID + p2.ID
	}
}

// 2.5 Multi-case select with violations in both send cases
func selectMultiSendViolation(ch1, ch2 chan *Packet) {
	p1 := &Packet{ID: 1}
	p2 := &Packet{ID: 2}
	select {
	case ch1 <- p1:
		p1.ID = 10 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	case ch2 <- p2:
		p2.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	default:
	}
}

// 2.6 Blocking select: send case uses value after send
func blockingSelectSendThenUse(ch chan *Packet, other chan int) {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
		p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	case <-other:
	}
}

// 2.7 Blocking select: unselected receive branch uses value (FALSE POSITIVE BUG!)
// In standard Go semantics, if <-other executes, ch <- p was NOT executed, so p should be preserved.
// However, passes/ownership/graph.go:285 sets cb = nil for blocking selects, causing useAfter to traverse
// into the receive case and falsely report SAFE003.
func blockingSelectReceiveCase(ch chan *Packet, other chan int) int {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
		return 0
	case <-other:
		p.ID = 20
		return p.ID
	}
}

// ============================================================================
// Group 3: Fork-Join Synchronization Recovery & Invalid Joins
// ============================================================================

// 3.1 Valid sync.WaitGroup recovery (Clean Pass)
func validWaitGroupJoin() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.ID = 42
	}()
	wg.Wait()
	p.ID = 100 // Clean Pass: ownership restored after Wait()
	return p.ID
}

// 3.2 Valid channel send join (Clean Pass)
func validChannelJoin() int {
	p := &Packet{ID: 0}
	done := make(chan struct{})
	go func() {
		p.ID = 42
		done <- struct{}{}
	}()
	<-done
	p.ID = 100 // Clean Pass: ownership restored after <-done
	return p.ID
}

// 3.3 Valid channel close join (Clean Pass)
func validChannelCloseJoin() int {
	p := &Packet{ID: 0}
	done := make(chan struct{})
	go func() {
		p.ID = 42
		close(done)
	}()
	<-done
	p.ID = 100 // Clean Pass: ownership restored after channel close
	return p.ID
}

// 3.4 Valid if-else branches inside goroutine each calling Done (Clean Pass)
func validIfElseDoneJoin(cond bool) int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if cond {
			p.ID = 1
			wg.Done()
		} else {
			p.ID = 2
			wg.Done()
		}
	}()
	wg.Wait()
	p.ID = 3 // Clean Pass
	return p.ID
}

// 3.5 Invalid join: Add count too small (Add(1) with 2 goroutines)
func invalidJoinAddMismatchedTooSmall() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.ID = 42
	}()
	go func() {
		defer wg.Done()
	}()
	wg.Wait()
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.6 Invalid join: Add count too large (Add(2) with 1 goroutine)
func invalidJoinAddMismatchedTooLarge() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.ID = 42
	}()
	wg.Wait()
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.7 Invalid join: dynamic Add count rejected
func invalidJoinDynamicAdd(n int) int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(n)
	go func() {
		defer wg.Done()
		p.ID = 42
	}()
	wg.Wait()
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.8 Invalid join: Add called inside loop
func invalidJoinAddInLoop() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	for range 1 {
		wg.Add(1)
	}
	go func() {
		defer wg.Done()
		p.ID = 42
	}()
	wg.Wait()
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.9 Invalid join: goroutine started inside loop (flagged at loop go statement)
func invalidJoinGoInLoop() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	for range 1 {
		go func() { // want `SAFE003: variable p is used after it was captured by a goroutine`
			defer wg.Done()
			p.ID = 42
		}()
	}
	wg.Wait()
	return p.ID
}

// 3.10 Invalid join: Done called before last access in goroutine
func invalidJoinDoneBeforeWrite() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		p.ID = 42
	}()
	wg.Wait()
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.11 Invalid join: goroutine leaks value to global before Done
func invalidJoinLeakBeforeDone() int {
	p := &Packet{ID: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		leakedPacket = p
	}()
	wg.Wait()
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.12 Invalid join: two senders on channel join
func invalidJoinChannelTwoSenders() int {
	p := &Packet{ID: 0}
	done := make(chan struct{}, 2)
	go func() {
		p.ID = 42
		done <- struct{}{}
	}()
	go func() {
		done <- struct{}{}
	}()
	<-done
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.13 Invalid join: parent sends on channel join
func invalidJoinChannelParentSend() int {
	p := &Packet{ID: 0}
	done := make(chan struct{}, 2)
	done <- struct{}{}
	go func() {
		p.ID = 42
		done <- struct{}{}
	}()
	<-done
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// 3.14 Invalid join: receive inside non-deterministic select is not a guaranteed join
func invalidJoinChannelSelect() int {
	p := &Packet{ID: 0}
	done := make(chan struct{})
	other := make(chan struct{})
	go func() {
		p.ID = 42
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-other:
	}
	p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine.*not one that joins`
	return p.ID
}

// ============================================================================
// Group 4: Concurrency Escape of Stack Borrows (CAP003)
// ============================================================================

// 4.1 Stack borrow (\mub) sent on channel in select (on same line as select)
func mubSelectSendSameLine(ch chan *int) {
	x := 10
	//gosree:mut
	b := &x
	select { case ch <- b: default: } // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// 4.2 Stack borrow (\rob) sent on channel in select (on same line as select)
func robSelectSendSameLine(ch chan *int) {
	x := 10
	//gosree:rob
	r := &x
	select { case ch <- r: default: } // want `CAP003: stack-scoped borrow 'r' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// 4.3 Stack borrow (\mub) sent on channel in multiline select (standard formatting)
// VULNERABILITY / SAFETY ESCAPE:
// When formatted across multiple lines as standard in Go, passes/ownership/check.go:260
// fails to match because targetLine (from *ssa.Select) is the line of 'select', whereas
// ast.CommClause.Pos() is the line of 'case'. Thus CAP003 is silently missed here!
func mubSelectSendMultiline(ch chan *int) {
	x := 10
	//gosree:mut
	b := &x
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch <- b:
	default:
	}
}

// 4.4 Synchronous closure calling borrow passes cleanly
func syncClosureNoEscape() int {
	x := 10
	//gosree:mut
	b := &x
	func() {
		*b = 42
	}()
	return x // Clean Pass: synchronous closure
}
