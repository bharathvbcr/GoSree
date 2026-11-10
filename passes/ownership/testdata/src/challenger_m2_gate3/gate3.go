package challenger_m2_gate3

type Packet struct {
	ID int
}

func (p *Packet) process(b *int) {
	p.ID = *b
}

type Worker struct {
	val int
}

func (w *Worker) consume(p *Packet) {
	w.val = p.ID
}

func sink3(b1 *int, b2 *int, b3 *int) {}
func sinkMulti(p1 *Packet, b1 *int, p2 *Packet, b2 *int) {}

// 1. Select with 3 borrows
func selectThreeBorrows(ch1, ch2, ch3 chan *int) {
	x := 10
	y := 20
	z := 30
	//gosree:mut
	b1 := &x
	//gosree:mut
	b2 := &y
	//gosree:rob
	b3 := &z
	select { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b2' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b3' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- b1:
	case ch2 <- b2:
	case ch3 <- b3:
	}
}

// 2. Select with 4 borrows
func selectFourBorrows(ch1, ch2, ch3, ch4 chan *int) {
	w := 1
	x := 2
	y := 3
	z := 4
	//gosree:mut
	b1 := &w
	//gosree:rob
	b2 := &x
	//gosree:mut
	b3 := &y
	//gosree:rob
	b4 := &z
	select { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b3' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b4' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- b1:
	case ch2 <- b2:
	case ch3 <- b3:
	case ch4 <- b4:
	}
}

// 3. Select with default clause first
func selectDefaultFirst(ch1 chan *Packet, ch2 chan *int) int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	default:
	case ch1 <- p:
	case ch2 <- b:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

// 4. Select with default clause in the middle
func selectDefaultMiddle(ch1 chan *Packet, ch2 chan *int) int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- p:
	default:
	case ch2 <- b:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

// 5. Select with default clause at the end
func selectDefaultLast(ch1 chan *Packet, ch2 chan *int) int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- p:
	case ch2 <- b:
	default:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

// 6. Select inside a loop with both \iso and borrow
func selectInLoop(ch1 chan *Packet, ch2 chan *int) {
	x := 42
	//gosree:mut
	b := &x
	for i := 0; i < 3; i++ {
		p := &Packet{ID: i}
		select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
		case ch1 <- p:
		case ch2 <- b:
		}
		p.ID = 100 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	}
}

// 7. Select in loop where \iso is allocated outside loop (multi-case select)
func selectInLoopIsoOutside(ch1, ch2 chan *Packet) {
	p := &Packet{ID: 1}
	for i := 0; i < 3; i++ {
		select { // want `SAFE003: composite literal is used after it was sent on a channel in select`
		case ch1 <- p:
		case ch2 <- p:
		}
	}
}

// 8. Goroutine method call where receiver is \iso and argument is borrow
func goMethodIsoReceiverBorrowArg() int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go p.process(b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
	p.ID = 20       // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p.ID
}

// 9. Goroutine method call where receiver is a borrow and argument is \iso
func goMethodBorrowReceiverIsoArg() int {
	w := Worker{val: 10}
	//gosree:mut
	bw := &w
	p := &Packet{ID: 20}
	go bw.consume(p) // want `CAP003: stack-scoped borrow 'bw' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
	p.ID = 30        // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p.ID
}

// 10. Goroutine call with 3 borrows
func goThreeBorrows() {
	x := 10
	y := 20
	z := 30
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	//gosree:mut
	b3 := &z
	go sink3(b1, b2, b3) // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement` `CAP003: stack-scoped borrow 'b3' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 11. Goroutine call with multiple \iso arguments and multiple borrows
func goMultiIsoAndMultiBorrow() int {
	p1 := &Packet{ID: 1}
	p2 := &Packet{ID: 2}
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	go sinkMulti(p1, b1, p2, b2) // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
	p1.ID = 10                   // want `SAFE003: composite literal is used after it was passed to a goroutine`
	p2.ID = 20                   // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p1.ID + p2.ID
}

// 12. Complex select with receive, default, multiple borrows, and \iso
func selectComplexMix(ch1 chan *Packet, ch2, ch3 chan *int, recvCh chan int) int {
	p := &Packet{ID: 10}
	x := 42
	y := 84
	//gosree:mut
	b1 := &x
	//gosree:rob
	b2 := &y
	select { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b2' \(\\rob\) cannot cross goroutine boundary: cannot be sent on a channel`
	case <-recvCh:
	case ch1 <- p:
	case ch2 <- b1:
	default:
	case ch3 <- b2:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

// 13. Method expression in go statement
func goMethodExpression() int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go (*Packet).process(p, b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
	p.ID = 20                  // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p.ID
}

// 14. Closure capturing borrow while passing \iso argument
func goClosureCaptureBorrowAndIsoArg() int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go func(arg *Packet) { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = *b
	}(p)
	p.ID = 20 // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p.ID
}

// 15. Closure capturing both \iso and borrow
func goClosureCaptureIsoAndBorrow() int {
	p := &Packet{ID: 10}
	x := 42
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		p.ID = *b
	}()
	p.ID = 20 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.ID
}

// 16. Closure capturing two borrows
func goClosureCaptureTwoBorrows() {
	x := 10
	y := 20
	//gosree:mut
	b1 := &x
	//gosree:mut
	b2 := &y
	go func() { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine` `CAP003: stack-scoped borrow 'b2' \(\\mub\) cannot cross goroutine boundary: cannot be captured by a goroutine`
		_ = *b1
		_ = *b2
	}()
}

// 17. Closure capturing \iso only (baseline)
func goClosureCaptureIsoOnly() int {
	p := &Packet{ID: 10}
	go func() {
		p.ID = 42
	}()
	p.ID = 20 // want `SAFE003: variable p is used after it was captured by a goroutine`
	return p.ID
}




