package challenger_m2_gate4

import "sync"

//gosree:imm
type Config struct { // want Config:"imm"
	maxWorkers int
	timeout    int
}

type Packet struct {
	ID int
}

// 1. Closure capturing single \iso variable and single borrow with subsequent read
func closureIsoBorrowSubsequentRead() int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b
	}()
	val := p.ID // want `SAFE003: variable p is used after it was captured by a goroutine`
	return val
}

// 2. Closure capturing single \iso variable and single borrow with subsequent write
func closureIsoBorrowSubsequentWrite() {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b
	}()
	p.ID = 99 // want `SAFE003: variable p is used after it was captured by a goroutine`
}

// 3. Closure capturing single \iso variable and single borrow with subsequent write then read
func closureIsoBorrowSubsequentWriteAndRead() int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:rob
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b
	}()
	p.ID = 50   // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.ID
}

// 4. Closure capturing multiple distinct borrows
func closureMultipleDistinctBorrows() {
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = *b1
		_ = *b2
	}()
}

// 5. Closure capturing multiple occurrences of the same borrow
// Deduplication requirement: exactly ONE CAP003 per borrow variable
func closureMultipleOccurrencesSameBorrow() {
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		sum := *b1 + *b1 + *b1
		prod := *b2 * *b2
		_ = sum + prod
	}()
}

// 6. Closure capturing 3 distinct borrows with mixed capabilities
func closureThreeBorrowsMixed() {
	x := 1
	y := 2
	z := 3
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	//gosree:mut
	b3 := &z
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b3' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = *b1 + *b2 + *b3
	}()
}

// 7. Closure capturing \iso and multiple borrows with subsequent reads and writes
func closureIsoAndMultipleBorrowsReadWrite() int {
	p := &Packet{ID: 10}
	x := 100
	y := 200
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b1 + *b2
	}()
	p.ID = 300  // want `SAFE003: variable p is used after it was captured by a goroutine`
	res := p.ID
	return res
}

// 8. Closure capturing multiple \iso variables and multiple borrows
func closureMultipleIsoAndMultipleBorrows() int {
	p1 := &Packet{ID: 1}
	p2 := &Packet{ID: 2}
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p1.ID = *b1
		p2.ID = *b2
	}()
	p1.ID = 100 // want `SAFE003: variable p1 is used after it was captured by a goroutine`
	p2.ID = 200 // want `SAFE003: variable p2 is used after it was captured by a goroutine`
	return p1.ID + p2.ID
}

// 9. Variable-bound closure capturing multiple borrows
func variableBoundClosureMultipleBorrows() {
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	fn := func() {
		_ = *b1 + *b2
	}
	go fn() // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
}

// 10. Variable-bound closure capturing both \iso and borrows
func variableBoundClosureIsoAndBorrows() int {
	p := &Packet{ID: 10}
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	fn := func() {
		p.ID = *b1 + *b2
	}
	go fn() // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
	p.ID = 40 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.ID
}

// 11. Closure capturing \iso and borrow, but sender DOES NOT use \iso after move
// Expected: CAP003 on borrow, but NO SAFE003 on p since p is not touched.
func closureCaptureIsoNoSubsequentUse() {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b
	}()
	// p is not used after goroutine spawn; no SAFE003 expected
}

// 12. Closure capturing \imm struct and borrow
// Expected: CAP003 on borrow, NO diagnostic on immConfig since \imm is shareable.
func closureCaptureImmAndBorrow(cfg *Config) int {
	x := 100
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = cfg.maxWorkers + *b
	}()
	return cfg.maxWorkers // No SAFE003 because cfg is \imm
}

// 13. Closure with nested function literal capturing borrow
func closureNestedFuncLitCaptureBorrow() {
	x := 10
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		inner := func() int {
			return *b
		}
		_ = inner()
	}()
}

func resetPacket(p *Packet) {
	p.ID = 0
}

// 14. Closure capturing borrow and \iso with deferred call on \iso
func closureIsoBorrowWithDeferredUse() {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	defer resetPacket(p) // want `SAFE003: this deferred call uses variable p, which runs when the function returns, after it was captured by a goroutine`
	go func() {          // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b
	}()
}

// 15. Closure capturing \iso and borrow joined with sync.WaitGroup
func closureIsoBorrowJoinedWithWaitGroup() int {
	var wg sync.WaitGroup
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	wg.Add(1)
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		defer wg.Done()
		p.ID = *b
	}()
	wg.Wait()
	return p.ID
}

// 16. Closure capturing \iso and borrow used before WaitGroup join
func closureIsoBorrowUsedBeforeJoin() int {
	var wg sync.WaitGroup
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	wg.Add(1)
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		defer wg.Done()
		p.ID = *b
	}()
	p.ID = 20 // want `SAFE003: variable p is used after it was captured by a goroutine`
	wg.Wait()
	return p.ID
}

// 17. Closure with branching control flow capturing borrows conditionally
func closureBranchingBorrows(cond bool) {
	x := 1
	y := 2
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		if cond {
			_ = *b1
		} else {
			_ = *b2
		}
	}()
}

// 18. Closure in loop capturing borrow declared outside the loop
func closureInLoopCapturingOuterBorrow() {
	x := 10
	//gosree:mut
	b := &x
	for i := 0; i < 3; i++ {
		p := &Packet{ID: i}
		go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `SAFE003: variable b is used after it was captured by a goroutine`
			p.ID = *b
		}()
		p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine`
	}
}

// 19. Closure in loop capturing borrow declared inside the loop
func closureInLoopCapturingInnerBorrow() {
	for i := 0; i < 3; i++ {
		x := i
		//gosree:mut
		b := &x
		p := &Packet{ID: i}
		go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
			p.ID = *b
		}()
		p.ID = 100 // want `SAFE003: variable p is used after it was captured by a goroutine`
	}
}

// 20. Closure capturing multiple distinct borrows inside switch statement
func closureSwitchBorrows(val int) {
	x := 10
	y := 20
	z := 30
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	//gosree:mut
	b3 := &z
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b3' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		switch val {
		case 1:
			_ = *b1
		case 2:
			_ = *b2
		default:
			_ = *b3
		}
	}()
}
