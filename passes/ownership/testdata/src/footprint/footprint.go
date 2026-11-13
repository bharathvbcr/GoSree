// Package footprint checks the access-footprint refinement of SAFE003 (footprint.go): a use after
// a move to a goroutine is reported only when it can race with the goroutine — overlapping places,
// one side writing, no common mutex excluding them — and whenever the analysis cannot follow the
// memory.
package footprint

import (
	"sync"
	"sync/atomic"
)

type point struct{ x, y int }

type box struct {
	mu sync.Mutex
	n  int
	m  map[string]int
	p  *point
}

type reader interface{ read() int }

type counter struct{ n int }

func (c *counter) read() int { c.n++; return c.n }

func sink(int) {}

// Disjoint fields: the goroutine writes p.x, the sender p.y.
func disjointFields() {
	p := point{}
	done := make(chan bool)
	go func() {
		p.x = 1
		done <- true
	}()
	p.y = 2
	<-done
}

// The same field on both sides is a race.
func sameField() {
	p := point{}
	done := make(chan bool)
	go func() {
		p.x = 1
		done <- true
	}()
	p.x = 2 // want "SAFE003"
	<-done
}

// A read of the whole struct copies the field the goroutine writes.
func wholeStructRead() {
	p := point{}
	done := make(chan bool)
	go func() {
		p.x = 1
		done <- true
	}()
	q := p // want "SAFE003"
	_ = q
	<-done
}

// Constant indices that differ are different elements.
func disjointIndex() {
	a := make([]int, 4)
	done := make(chan bool)
	go func() {
		a[0] = 1
		done <- true
	}()
	a[1] = 2
	<-done
}

// A variable index may be the element the goroutine writes.
func variableIndex(i int) {
	a := make([]int, 4)
	done := make(chan bool)
	go func() {
		a[0] = 1
		done <- true
	}()
	a[i] = 2 // want "SAFE003"
	<-done
}

// Reading the slice header does not touch its elements.
func headerRead() {
	a := make([]int, 4)
	done := make(chan bool)
	go func() {
		a[0] = 1
		done <- true
	}()
	sink(len(a))
	<-done
}

// Appending in the sender may write the element the goroutine reads.
func appendRace() {
	a := make([]int, 1, 8)
	done := make(chan bool)
	go func() {
		sink(a[0])
		done <- true
	}()
	a = append(a, 2) // want "SAFE003"
	<-done
	_ = a
}

// Converting to a string reads every element.
func stringOfBytes() {
	b := make([]byte, 4)
	done := make(chan bool)
	go func() {
		b[0] = 'x'
		done <- true
	}()
	s := string(b) // want "SAFE003"
	_ = s
	<-done
}

// Two readers of a map do not race.
func mapReadRead() {
	m := map[string]int{"a": 1}
	done := make(chan bool)
	go func() {
		sink(m["a"])
		done <- true
	}()
	sink(m["a"])
	<-done
}

// A map write races with a map read.
func mapWriteRead() {
	m := map[string]int{"a": 1}
	done := make(chan bool)
	go func() {
		sink(m["a"])
		done <- true
	}()
	m["b"] = 2 // want "SAFE003"
	<-done
}

// Both sides under the same mutex.
func underMutex() {
	var mu sync.Mutex
	x := 0
	_ = x
	done := make(chan bool)
	go func() {
		mu.Lock()
		x = 1
		mu.Unlock()
		done <- true
	}()
	mu.Lock()
	x = 2
	mu.Unlock()
	<-done
}

// One side forgets the mutex.
func halfLocked() {
	var mu sync.Mutex
	x := 0
	_ = x
	done := make(chan bool)
	go func() {
		mu.Lock()
		x = 1
		mu.Unlock()
		done <- true
	}()
	x = 2 // want "SAFE003"
	<-done
}

// Different mutexes exclude nothing.
func twoMutexes() {
	var mu1, mu2 sync.Mutex
	x := 0
	_ = x
	done := make(chan bool)
	go func() {
		mu1.Lock()
		x = 1
		mu1.Unlock()
		done <- true
	}()
	mu2.Lock()
	x = 2 // want "SAFE003"
	mu2.Unlock()
	<-done
}

// A write under RLock races with a read under RLock.
func writeUnderRLock() {
	var mu sync.RWMutex
	x := 0
	done := make(chan bool)
	go func() {
		mu.RLock()
		x = 1
		mu.RUnlock()
		done <- true
	}()
	mu.RLock()
	sink(x) // want "SAFE003"
	mu.RUnlock()
	<-done
}

// A write under Lock excludes a read under RLock.
func lockAndRLock() {
	var mu sync.RWMutex
	x := 0
	done := make(chan bool)
	go func() {
		mu.Lock()
		x = 1
		mu.Unlock()
		done <- true
	}()
	mu.RLock()
	sink(x)
	mu.RUnlock()
	<-done
}

// Atomic operations on both sides.
func atomics() {
	var n int64
	done := make(chan bool)
	go func() {
		atomic.AddInt64(&n, 1)
		done <- true
	}()
	atomic.AddInt64(&n, 1)
	<-done
}

// A plain read races with an atomic write.
func atomicAndPlain() {
	var n int64
	done := make(chan bool)
	go func() {
		atomic.AddInt64(&n, 1)
		done <- true
	}()
	sink(int(n)) // want "SAFE003"
	<-done
}

// A pointer stored into other memory leaves what the walk can follow.
func storedElsewhere() {
	p := &point{}
	h := &box{}
	done := make(chan bool)
	go func() {
		p.x = 1
		done <- true
	}()
	h.p = p // want "SAFE003" "SAFE003"
	<-done
}

func writeX(p *point) { p.x = 3 }

func readY(p *point) int { return p.y }

// A callee that writes what it is given.
func calleeWrites() {
	p := &point{}
	done := make(chan bool)
	go func() {
		sink(p.y)
		done <- true
	}()
	writeX(p) // want "SAFE003"
	<-done
}

// A callee that only reads, against a goroutine that only reads.
func calleeReads() {
	p := &point{}
	done := make(chan bool)
	go func() {
		sink(p.y)
		done <- true
	}()
	sink(readY(p))
	<-done
}

// An interface method may do anything.
func viaInterface() {
	rs := []reader{&counter{}}
	done := make(chan bool)
	go func() {
		sink(rs[0].read())
		done <- true
	}()
	sink(len(rs)) // want "SAFE003"
	<-done
}

// A goroutine started in a loop races with its own other instances when it writes.
func loopWriter() {
	x := 0
	_ = x
	for i := 0; i < 2; i++ {
		go func() { // want "SAFE003"
			x = i
		}()
	}
}

// Readers started in a loop do not.
func loopReaders() {
	x := 7
	m := map[int]int{}
	_ = x
	for i := 0; i < 2; i++ {
		go func() {
			sink(m[i])
		}()
	}
}

// Overwriting a struct whose mutex the goroutine uses.
func mutexOverwrite() {
	b := &box{}
	done := make(chan bool)
	go func() {
		b.mu.Lock()
		b.n++
		b.mu.Unlock()
		done <- true
	}()
	*b = box{} // want "SAFE003"
	<-done
}

// A branch the compiler folds away never runs.
func deadBranch() {
	x, y := 0, 0
	done := make(chan bool)
	go func() {
		x = 1
		done <- true
	}()
	if y == 1 && x == 1 {
	}
	<-done
}

// A branch that depends on input may run.
func liveBranch(y int) {
	x := 0
	done := make(chan bool)
	go func() {
		x = 1
		done <- true
	}()
	if y == 1 && x == 1 { // want "SAFE003"
	}
	<-done
}

// A deferred read runs when the function returns, while the goroutine may still write.
func deferredRead() {
	p := &point{}
	go func() {
		p.x = 1
	}()
	defer func() { sink(p.x) }() // want "SAFE003"
}

// A closure called in place after the move writes what the goroutine reads.
func closureInPlace() {
	p := point{}
	done := make(chan bool)
	go func() {
		sink(p.x)
		done <- true
	}()
	set := func() { p.x = 2 }
	set() // want "SAFE003"
	<-done
}

// A second goroutine writing what the first reads.
func twoGoroutines() {
	p := point{}
	done := make(chan bool, 2)
	go func() {
		sink(p.x)
		done <- true
	}()
	go func() { // want "SAFE003"
		p.x = 1
		done <- true
	}()
	<-done
	<-done
}

// Two goroutines reading.
func twoReaders() {
	p := point{x: 1}
	done := make(chan bool, 2)
	go func() {
		sink(p.x)
		done <- true
	}()
	go func() {
		sink(p.x)
		done <- true
	}()
	<-done
	<-done
}

// After the WaitGroup join the sender may write.
func afterJoin() {
	p := point{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.x = 1
	}()
	wg.Wait()
	p.x = 2
}
