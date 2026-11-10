package t1_escape

import (
	"time"

	"github.com/bharathvbcr/gosree/cap"
)

// TC-T1-8.1: Stack borrow (\mub) sent on channel rejected (CAP003)
func MubChannelSend(ch chan *int) {
	x := 10
	//gosree:mut
	b := &x
	ch <- b // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// TC-T1-8.2: Stack borrow (\rob) sent on channel rejected (CAP003)
func RobChannelSend(ch chan *int) {
	x := 10
	//gosree:rob
	r := &x
	ch <- r // want `CAP003: stack-scoped borrow 'r' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// TC-T1-8.3: Stack borrow (\mub) captured in go closure rejected (CAP003)
func MubGoroutineCapture() {
	x := 10
	//gosree:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	}()
}

// TC-T1-8.4: Stack borrow (\rob) captured in go closure rejected (CAP003)
func RobGoroutineCapture() {
	x := 10
	//gosree:rob
	r := &x
	go func() { // want `CAP003: stack-scoped borrow 'r' .* cannot cross goroutine boundary: cannot be captured by a goroutine`
		println(*r)
	}()
}

// TC-T1-8.5: Stack borrow passed as argument to go statement rejected (CAP003)
func Worker(p *int) {}

func MubGoArg() {
	x := 10
	//gosree:mut
	b := &x
	go Worker(b) // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be passed as argument to go statement`
}

// TC-T1-8.6: cap.BorrowMut sent on channel rejected (CAP003)
func MarkerMubChannelSend(ch chan *int) {
	x := 10
	b := cap.BorrowMut(&x)
	ch <- b // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// TC-T1-8.7: cap.BorrowMut captured in go closure rejected (CAP003)
func MarkerMubGoroutineCapture() {
	x := 10
	b := cap.BorrowMut(&x)
	go func() { // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	}()
}

// TC-T1-8.8: cap.BorrowMut captured in time.AfterFunc spawner callback rejected (CAP003)
func MarkerMubAfterFuncCapture() {
	x := 10
	b := cap.BorrowMut(&x)
	time.AfterFunc(time.Second, func() { // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	})
}
