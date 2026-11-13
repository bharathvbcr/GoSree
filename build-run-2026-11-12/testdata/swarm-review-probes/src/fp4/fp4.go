// Package fp4: further narrowing.
package fp4

import "sync"

type Job struct{ ID int }

// S1: read-only capture, then send j on a parameter channel.
func S1(ch chan<- *Job) {
	j := &Job{}
	go func() { _ = j.ID }()
	ch <- j
}

// S2: read-only capture, then send on a local buffered channel.
func S2() chan *Job {
	ch := make(chan *Job, 1)
	j := &Job{}
	go func() { _ = j.ID }()
	ch <- j
	return ch
}

// L1: loop of plain go statements (no WaitGroup), same element.
func L1(n int) {
	items := make([]Job, n)
	for i := range items {
		it := &items[0]
		go func() { it.ID = i }()
	}
}

// L2: loop of plain go statements, in-loop alias of an outer pointer.
func L2() {
	j := &Job{}
	for i := 0; i < 3; i++ {
		k := j
		go func() { k.ID = i }()
	}
}

// L3: wg.Add/go/Done loop, same element.
func L3(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[0]
		wg.Add(1)
		go func() { defer wg.Done(); it.ID = i }()
	}
	wg.Wait()
}

// L4: wg.Go loop, no Wait at all.
func L4(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[0]
		wg.Go(func() { it.ID = i })
	}
}

// L5: wg.Go loop of the outer pointer itself (the surviving loopGoOneWait shape), for comparison.
func L5() {
	var wg sync.WaitGroup
	j := &Job{}
	for i := 0; i < 3; i++ {
		wg.Go(func() { j.ID = 2 })
	}
	wg.Wait()
}

// L6: wg.Go loop, pointer read from a map each iteration (same key).
func L6(m map[string]*Job) {
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		p := m["k"]
		wg.Go(func() { p.ID = i })
	}
	wg.Wait()
}

// L7: wg.Go loop, a local *Job allocated outside, field pointer taken per iteration.
func L7() {
	var wg sync.WaitGroup
	j := &Job{}
	for i := 0; i < 3; i++ {
		ip := &j.ID
		wg.Go(func() { *ip = i })
	}
	wg.Wait()
}
