package moves

import (
	"sync"
	"time"
)

// ---- Accepted: the goroutine hands what it was given back at a join. ----

// WaitGroup: the goroutine's last write precedes Done; the parent waits on the same WaitGroup.
func joinWaitGroup() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		x = 1
		wg.Done()
	}()
	wg.Wait()
	return x
}

// A deferred Done runs after everything else in the goroutine.
func joinDeferDone() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		x = 1
	}()
	wg.Wait()
	return x
}

// One Add covering two goroutines, each calling Done once: Wait joins both.
func joinTwoGoroutines() int {
	x, y := 0, 0
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		x = 1
		wg.Done()
	}()
	go func() {
		y = 1
		wg.Done()
	}()
	wg.Wait()
	return x + y
}

// A pointer captured by the goroutine comes back with everything reachable from it.
func joinPointer() int {
	j := &Job{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		j.Data = make([]byte, 4)
		wg.Done()
	}()
	wg.Wait()
	return len(j.Data)
}

// Channel handshake: the goroutine sends after its last use; the parent receives from the same
// channel, of which the goroutine is the only sender.
func joinSend() int {
	v := 0
	_ = v
	c := make(chan int)
	go func() {
		v = 1
		c <- 0
	}()
	<-c
	v = 2
	return v
}

// close is a release; a comma-ok receive observes it.
func joinClose() int {
	v := 0
	_ = v
	c := make(chan int, 1)
	go func() {
		v = 1
		close(c)
	}()
	_, _ = <-c
	v = 2
	return v
}

// Ranging over the channel until it is closed joins at the loop exit.
func joinRange() int {
	v := 0
	_ = v
	c := make(chan int)
	go func() {
		v = 1
		close(c)
	}()
	for range c {
	}
	v = 2
	return v
}

// The goroutine reads x into the value it sends; the parent's write comes after the receive.
func joinValue() int {
	x := 0
	c := make(chan int, 1)
	done := make(chan bool)
	go func() {
		c <- x
		done <- true
	}()
	x = <-c
	<-done
	return x
}

// A send in a select is a release when nothing touches the moved memory after the select.
func joinSelectSend() int {
	x := 0
	_ = x
	c := make(chan bool, 1)
	other := make(chan bool)
	go func() {
		x = 1
		select {
		case c <- true:
		case <-other:
		}
	}()
	<-c
	x = 2
	return x
}

// An argument of the go statement comes back the same way as a capture.
func joinArgument() int {
	j := &Job{}
	c := make(chan int)
	go func(j *Job) {
		j.ID = 1
		c <- 0
	}(j)
	<-c
	j.ID = 2
	return j.ID
}

// ---- Near misses: still reported. ----

// Done comes before the goroutine's last write.
func doneBeforeWrite() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		x = 1
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// The parent waits on a different WaitGroup from the one the goroutine signals.
func waitOtherGroup() int {
	x := 0
	var wg1, wg2 sync.WaitGroup
	wg1.Add(1)
	wg2.Add(1)
	go func() {
		x = 1
		wg1.Done()
	}()
	go func() {
		wg2.Done()
	}()
	wg2.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// n goroutines touch the same variable: they race with each other before any Wait.
func loopOfGoroutines() int {
	x := 0
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { // want `SAFE003: variable x is used after it was captured by a goroutine`
			x++
			wg.Done()
		}()
	}
	wg.Wait()
	return x
}

// Two goroutines send on one channel: a single receive does not say which one finished.
func twoSenders() int {
	v1, v2 := 0, 0
	_ = v1 + v2
	c := make(chan int, 2)
	go func() {
		v1 = 1
		c <- 1
	}()
	go func() {
		v2 = 2
		c <- 2
	}()
	<-c
	v1 = 3 // want `SAFE003: variable v1 is used after it was captured by a goroutine.*not one that joins`
	return v1
}

// The parent itself sent on the buffered channel, so its receive may take its own value.
func parentSendsFirst() int {
	x := 0
	_ = x
	c := make(chan int, 2)
	c <- 0
	go func() {
		x = 1
		c <- 1
	}()
	<-c
	x = 2 // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
	return x
}

// One closure value started twice is two goroutines.
func startedTwice() int {
	x := 0
	_ = x
	c := make(chan int)
	f := func() {
		x = 1
		c <- 0
	}
	go f()
	go f() // want `SAFE003: the closure is used after it was captured by a goroutine`
	<-c
	return 0
}

// Deferred calls run last-registered first: the write runs after Done.
func deferredWriteAfterDone() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() { x = 1 }()
		defer wg.Done()
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// The goroutine moves x on to a goroutine of its own before Done.
func movedOn() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		go func() { x = 1 }()
		wg.Done()
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// A closure over x built before Done and called after it still touches x.
func closureCalledAfterDone() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		set := func() { x = 1 }
		wg.Done()
		set()
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// Add inside the goroutine may run after Wait has already returned.
func addInGoroutine() int {
	x := 0
	var wg sync.WaitGroup
	go func() {
		wg.Add(1)
		x = 1
		wg.Done()
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// Add(1) with two Done goroutines: Wait can return after the other one's Done.
func addTooSmall() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		x = 1
		wg.Done()
	}()
	go func() {
		wg.Done()
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// A channel parameter may have other senders the function cannot see.
func chanParam(c chan int) int {
	x := 0
	_ = x
	go func() {
		x = 1
		c <- 0
	}()
	<-c
	x = 2 // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
	return x
}

func sendOne(c chan int) { c <- 1 }

// A channel passed to a function may gain a sender the rule cannot see.
func chanPassed() int {
	x := 0
	_ = x
	c := make(chan int, 2)
	go func() {
		x = 1
		c <- 0
	}()
	sendOne(c)
	<-c
	x = 2 // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
	return x
}

// A select receive case is not a join.
func selectReceive() int {
	x := 0
	_ = x
	c := make(chan int)
	other := make(chan int)
	go func() {
		x = 1
		c <- 0
	}()
	select {
	case <-c:
	case <-other:
	}
	x = 2 // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
	return x
}

func setInt(p *int, c chan int) {
	*p = 1
	c <- 0
}

// go with a named function is not a join source.
func namedGoroutine() int {
	p := new(int)
	c := make(chan int)
	go setInt(p, c)
	<-c
	*p = 2 // want `SAFE003: value from new is used after it was passed to a goroutine.*not one that joins`
	return *p
}

// WaitGroup reuse: the second Add does not come before the first goroutine starts.
func waitGroupReuse() int {
	x := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		x = 1
		wg.Done()
	}()
	wg.Wait()
	wg.Add(1)
	go func() { // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
		x = 2
		wg.Done()
	}()
	wg.Wait()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}

// A callback registered inside the goroutine can run after the goroutine's release.
func callbackInGoroutine() int {
	y := 0
	_ = y
	c := make(chan bool)
	go func() {
		time.AfterFunc(time.Millisecond, func() { y = 1 })
		c <- true
	}()
	<-c
	y = 2 // want `SAFE003: variable y is used after it was captured by a goroutine.*not one that joins`
	return y
}

// Mutex hand-offs are not modelled.
func mutexHandoff() int {
	x := 0
	_ = x
	var mu sync.Mutex
	mu.Lock()
	go func() {
		x = 1
		mu.Unlock()
	}()
	mu.Lock()
	return x // want `SAFE003: variable x is used after it was captured by a goroutine.*not one that joins`
}
