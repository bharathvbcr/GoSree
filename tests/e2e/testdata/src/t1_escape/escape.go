package t1_escape

// TC-T1-8.1: Stack borrow (\mub) sent on channel rejected (CAP003)
func MubChannelSend(ch chan *int) {
	x := 10
	//gocap:mut
	b := &x
	ch <- b // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// TC-T1-8.2: Stack borrow (\rob) sent on channel rejected (CAP003)
func RobChannelSend(ch chan *int) {
	x := 10
	//gocap:rob
	r := &x
	ch <- r // want `CAP003: stack-scoped borrow 'r' .* cannot cross goroutine boundary: cannot be sent on a channel`
}

// TC-T1-8.3: Stack borrow (\mub) captured in go closure rejected (CAP003)
func MubGoroutineCapture() {
	x := 10
	//gocap:mut
	b := &x
	go func() { // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be captured by a goroutine`
		*b = 20
	}()
}

// TC-T1-8.4: Stack borrow (\rob) captured in go closure rejected (CAP003)
func RobGoroutineCapture() {
	x := 10
	//gocap:rob
	r := &x
	go func() { // want `CAP003: stack-scoped borrow 'r' .* cannot cross goroutine boundary: cannot be captured by a goroutine`
		println(*r)
	}()
}

// TC-T1-8.5: Stack borrow passed as argument to go statement rejected (CAP003)
func Worker(p *int) {}

func MubGoArg() {
	x := 10
	//gocap:mut
	b := &x
	go Worker(b) // want `CAP003: stack-scoped borrow 'b' .* cannot cross goroutine boundary: cannot be passed as argument to go statement`
}
