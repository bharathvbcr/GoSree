package t1_forkjoin

import "sync"

type workBuffer struct {
	count int
}

// TC-T1-6.1: sync.WaitGroup fork-join restores ownership (Clean Pass)
func waitGroupJoin() int {
	b := &workBuffer{count: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.count = 10
	}()
	wg.Wait()
	b.count = 20 // Clean Pass: ownership restored at wg.Wait()
	return b.count
}

// TC-T1-6.2: Missing wg.Wait() results in use-after-move error (SAFE003)
func waitGroupMissingWait() int {
	b := &workBuffer{count: 0}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.count = 10
	}()
	b.count = 20 // want `SAFE003: variable b is used after it was captured by a goroutine`
	return b.count
}

// TC-T1-6.3: Channel synchronization join restores ownership (Clean Pass)
func channelJoin() int {
	b := &workBuffer{count: 0}
	done := make(chan struct{})
	go func() {
		b.count = 10
		done <- struct{}{}
	}()
	<-done
	b.count = 20 // Clean Pass: restored after channel receive
	return b.count
}

// TC-T1-6.4: Multiple workers joined by WaitGroup (Clean Pass)
func multiWorkerJoin() int {
	var wg sync.WaitGroup
	x := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		x = 1
	}()
	wg.Wait()
	return x // Clean Pass
}

// TC-T1-6.5: Read-only concurrent captures with WaitGroup (Clean Pass)
func readOnlyWaitGroup() int {
	var wg sync.WaitGroup
	val := 42
	wg.Add(2)
	go func() {
		defer wg.Done()
		println(val)
	}()
	go func() {
		defer wg.Done()
		println(val)
	}()
	wg.Wait()
	return val // Clean Pass
}
