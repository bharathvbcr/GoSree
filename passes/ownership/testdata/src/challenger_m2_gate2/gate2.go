package challenger_m2_gate2

type Packet struct {
	ID int
}

// 1. Select with both an \iso send and a \mub borrow send
func selectMultiSendIsoAndBorrow(ch1 chan *Packet, ch2 chan *int) int {
	p := &Packet{ID: 10}
	x := 42
	//gocap:mut
	b := &x
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- p:
	case ch2 <- b:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

// 1b. Select with borrow first, then iso
func selectMultiSendBorrowFirst(ch1 chan *int, ch2 chan *Packet) int {
	p := &Packet{ID: 10}
	x := 42
	//gocap:mut
	b := &x
	select { // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- b:
	case ch2 <- p:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

// 2. Select with two different stack-scoped borrows
func selectTwoBorrows(ch1 chan *int, ch2 chan *int) {
	x := 10
	y := 20
	//gocap:mut
	b1 := &x
	//gocap:mut
	b2 := &y
	select { // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel` `CAP003: stack-scoped borrow 'b2' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	case ch1 <- b1:
	case ch2 <- b2:
	}
}

// 3. Blocking select with receive first, then send
func blockingSelectReceiveFirst(ch chan *Packet, other chan int) int {
	p := &Packet{ID: 10}
	select {
	case <-other:
		p.ID = 20 // Clean pass: other was selected, p was not sent
		return p.ID
	case ch <- p:
		return 0
	}
}

// 4. Blocking select with 3 cases: mutual independence
func blockingSelectThreeCases(ch1 chan *Packet, ch2 chan *Packet, other chan int) int {
	p1 := &Packet{ID: 1}
	p2 := &Packet{ID: 2}
	select {
	case ch1 <- p1:
		p2.ID = 20 // Clean pass: p2 was not sent in this branch
		return p2.ID
	case ch2 <- p2:
		p1.ID = 10 // Clean pass: p1 was not sent in this branch
		return p1.ID
	case <-other:
		p1.ID = 11 // Clean pass: neither was sent
		p2.ID = 22 // Clean pass: neither was sent
		return p1.ID + p2.ID
	}
}

// 5. Blocking select followed by use after select
func blockingSelectSendThenUseAfter(ch chan *Packet, other chan int) int {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
	case <-other:
	}
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel in select`
	return p.ID
}

func sink(p *Packet, b *int) {}

// 6. Goroutine call with \iso and \mub arguments
func goCallIsoAndBorrow() int {
	p := &Packet{ID: 10}
	x := 42
	//gocap:mut
	b := &x
	go sink(p, b) // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
	p.ID = 20    // want `SAFE003: composite literal is used after it was passed to a goroutine`
	return p.ID
}

func sink2(b1 *int, b2 *int) {}

// 7. Goroutine call with two borrows
func goCallTwoBorrows() {
	x := 10
	y := 20
	//gocap:mut
	b1 := &x
	//gocap:mut
	b2 := &y
	go sink2(b1, b2) // want `CAP003: stack-scoped borrow 'b1' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement` `CAP003: stack-scoped borrow 'b2' \(\\mub\) cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// 8. Normal channel sends on separate lines (Contrast Case: Works cleanly)
func multiSendSeparateLines(ch1 chan *Packet, ch2 chan *int) int {
	p := &Packet{ID: 10}
	x := 42
	//gocap:mut
	b := &x
	ch2 <- b // want `CAP003: stack-scoped borrow 'b' \(\\mub\) cannot cross goroutine boundary: cannot be sent on a channel`
	ch1 <- p
	p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel`
	return p.ID
}

// 9. Single case select (Go SSA lowers single case select to direct Send)
func singleCaseSelect(ch chan *Packet) int {
	p := &Packet{ID: 10}
	select {
	case ch <- p:
		p.ID = 20 // want `SAFE003: composite literal is used after it was sent on a channel`
		return p.ID
	}
}




