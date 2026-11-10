package guardedlocal

import "sync"

// lockDiscipline is the race corpus's TestNoRaceMutex: two goroutines write x, each holding mu.
// Capturing x in the second goroutine is not a use after the first one took it: x is guarded.
func lockDiscipline() {
	var mu sync.Mutex
	var x int16 = 0 //safe:guarded_by mu
	_ = x
	ch := make(chan bool, 2)
	go func() {
		mu.Lock()
		defer mu.Unlock()
		x = 1
		ch <- true
	}()
	go func() {
		mu.Lock()
		x = 2
		mu.Unlock()
		ch <- true
	}()
	<-ch
	<-ch
}

// writeBeforeLock is TestRaceMutex: the writes come before the lock.
func writeBeforeLock() {
	var mu sync.Mutex
	var x int16 = 0 //safe:guarded_by mu
	_ = x
	ch := make(chan bool, 2)
	go func() {
		x = 1 // want `SAFE009: local variable x is guarded by mu but is written here without holding it`
		mu.Lock()
		defer mu.Unlock()
		ch <- true
	}()
	go func() {
		x = 2 // want `SAFE009: local variable x is guarded by mu but is written here without holding it`
		mu.Lock()
		mu.Unlock()
		ch <- true
	}()
	<-ch
	<-ch
}

// otherMutex is TestRaceMutex2: the second goroutine holds a different mutex.
func otherMutex() {
	var mu1 sync.Mutex
	var mu2 sync.Mutex
	var x int8 = 0 //safe:guarded_by mu1
	_ = x
	ch := make(chan bool, 2)
	go func() {
		mu1.Lock()
		defer mu1.Unlock()
		x = 1
		ch <- true
	}()
	go func() {
		mu2.Lock()
		x = 2 // want `SAFE009: local variable x is guarded by mu1 but is written here without holding it`
		mu2.Unlock()
		ch <- true
	}()
	<-ch
	<-ch
}

// writeUnderRLock is TestRaceRWMutexMultipleReaders: y is guarded too, and readers write it.
func writeUnderRLock() {
	var mu sync.RWMutex
	var x, y int64 = 0, 1 //safe:guarded_by mu
	ch := make(chan bool, 2)
	go func() {
		mu.Lock()
		defer mu.Unlock()
		x = 2
		ch <- true
	}()
	go func() {
		mu.RLock()
		y = x + 1 // want `SAFE009: local variable y is guarded by mu but is written here without holding it`
		mu.RUnlock()
		ch <- true
	}()
	<-ch
	<-ch
	_ = y // want `SAFE009: local variable y is guarded by mu but is read here without holding it`
}

// readersAndWriter is TestNoRaceRWMutexTransitive: readers hold RLock, the declaring function writes
// under Lock after starting them, and its initialisation runs before any goroutine can see x.
func readersAndWriter() {
	var mu sync.RWMutex
	x := int64(0) //safe:guarded_by mu
	ch := make(chan bool, 2)
	go func() {
		mu.RLock()
		_ = x
		mu.RUnlock()
		ch <- true
	}()
	go func() {
		mu.RLock()
		_ = x
		mu.RUnlock()
		ch <- true
	}()
	mu.Lock()
	x = 42
	mu.Unlock()
	<-ch
	<-ch
}

// afterSharing reads x unlocked once a goroutine can see it; before that, no lock is needed.
func afterSharing() {
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	x = 5
	go func() {
		mu.Lock()
		x++
		mu.Unlock()
	}()
	println(x) // want `SAFE009: local variable x is guarded by mu but is read here without holding it`
}

// muNotCaptured: a goroutine that does not capture mu cannot hold it.
func muNotCaptured() {
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	mu.Lock()
	go func() {
		x++ // want `SAFE009: local variable x is guarded by mu but is written here without holding it`
	}()
	mu.Unlock()
}

// leaves: a guarded map read under the lock must not be returned.
func leaves() map[string]int {
	var mu sync.Mutex
	m := map[string]int{} //safe:guarded_by mu
	go func() {
		mu.Lock()
		m["a"] = 1
		mu.Unlock()
	}()
	mu.Lock()
	defer mu.Unlock()
	return m // want `SAFE009: local variable m is guarded by mu, but a value read from it leaves its lock here: returned`
}

var gmu sync.Mutex

// byPackageMutex: a local guarded by a package mutex, which every closure can reach.
func byPackageMutex() {
	n := 0 //safe:guarded_by gmu
	go func() {
		gmu.Lock()
		n++
		gmu.Unlock()
	}()
	go func() {
		n-- // want `SAFE009: local variable n is guarded by gmu but is written here without holding it`
	}()
	gmu.Lock()
	n++
	gmu.Unlock()
}

// funcUnlock is TestNoRaceFuncUnlock: a function locks the mutex it is given and returns holding it,
// so the store of its result is under the lock.
func funcUnlock() {
	ch := make(chan bool, 1)
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	_ = x
	go func() {
		mu.Lock()
		x = 42
		mu.Unlock()
		ch <- true
	}()
	x = func(mu *sync.Mutex) int {
		mu.Lock()
		return 43
	}(&mu)
	mu.Unlock()
	<-ch
}

// asFunc4 is TestNoRaceAsFunc4: the closure locks the mutex it captured and returns holding it.
func asFunc4() {
	c := make(chan bool, 1)
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	_ = x
	go func() {
		x = func() int {
			mu.Lock()
			return 42
		}()
		mu.Unlock()
		c <- true
	}()
	mu.Lock()
	x = 42
	mu.Unlock()
	<-c
}

// asFunc3 is TestRaceAsFunc3: the argument is read before the call that locks.
func asFunc3() {
	c := make(chan bool, 1)
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	go func() {
		func(x int) {
			_ = x
			mu.Lock()
		}(x) // want `SAFE009: local variable x is guarded by mu but is read here without holding it`
		mu.Unlock()
		c <- true
	}()
	mu.Lock()
	x = 42
	mu.Unlock()
	<-c
}

func lockIf(mu *sync.Mutex, ok bool) int {
	if ok {
		mu.Lock()
		return 1
	}
	return 0
}

func lockAndRelease(mu *sync.Mutex) int {
	mu.Lock()
	defer mu.Unlock()
	return 1
}

// A function that locks on some paths only, or releases before returning, acquires nothing.
func notAcquired() {
	var mu sync.Mutex
	x := 0 //safe:guarded_by mu
	go func() {
		mu.Lock()
		x++
		mu.Unlock()
	}()
	x = lockIf(&mu, true) // want `SAFE009: local variable x is guarded by mu but is written here without holding it`
	mu.Unlock()
	x = lockAndRelease(&mu) // want `SAFE009: local variable x is guarded by mu but is written here without holding it`
}

// inner: a closure's own guarded local, guarded by its parent's mutex.
func inner() {
	var mu sync.Mutex
	go func() {
		n := 0 //safe:guarded_by mu
		go func() {
			mu.Lock()
			n++
			mu.Unlock()
		}()
		mu.Lock()
		n++
		mu.Unlock()
		n-- // want `SAFE009: local variable n is guarded by mu but is written here without holding it`
	}()
}
