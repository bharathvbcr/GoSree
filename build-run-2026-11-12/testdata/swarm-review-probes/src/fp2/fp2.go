// Package fp2: read-classification and per-element probes. One racy line per function.
package fp2

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
)

type Job struct {
	ID    int
	C     int64
	M     map[string]int
	S     []int
	cache *int
	fn    func(*Job)
}

// Get looks like a read but lazily writes cache.
func (j *Job) Get() int {
	if j.cache == nil {
		v := j.ID
		j.cache = &v
	}
	return *j.cache
}

// D1: both sides call the lazy getter: two writes of cache.
func D1() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.Get(); done <- true }()
	_ = j.Get() // race on cache
	<-done
}

// D2: goroutine reads, parent hands j to an unknown function value.
func D2(f func(*Job)) {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	f(j) // may write
	<-done
}

// D3: goroutine reads, parent calls a stored function field on j.
func D3() {
	j := &Job{fn: func(x *Job) { x.ID = 1 }}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	j.fn(j) // writes
	<-done
}

// D4: goroutine reads, json.Unmarshal into j in the parent.
func D4(data []byte) {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	_ = json.Unmarshal(data, j) // writes
	<-done
}

// D5: goroutine reads, reflect writes in the parent.
func D5() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	reflect.ValueOf(j).Elem().Field(0).SetInt(1) // writes
	<-done
}

// D6: goroutine ranges the map, parent deletes.
func D6() {
	j := &Job{M: map[string]int{"a": 1}}
	done := make(chan bool)
	go func() {
		for range j.M {
		}
		done <- true
	}()
	delete(j.M, "a") // race
	<-done
}

// D7: goroutine reads the map, parent clear()s it.
func D7() {
	j := &Job{M: map[string]int{"a": 1}}
	done := make(chan bool)
	go func() { _ = j.M["a"]; done <- true }()
	clear(j.M) // race
	<-done
}

// D8: goroutine reads the map, parent maps.Copy into it.
func D8(o map[string]int) {
	j := &Job{M: map[string]int{"a": 1}}
	done := make(chan bool)
	go func() { _ = j.M["a"]; done <- true }()
	maps.Copy(j.M, o) // race
	<-done
}

// D9: goroutine reads S, parent slices.Sort.
func D9() {
	j := &Job{S: []int{2, 1}}
	done := make(chan bool)
	go func() { _ = j.S[1]; done <- true }()
	slices.Sort(j.S) // race
	<-done
}

// D10: atomic only on the goroutine side.
func D10() {
	j := &Job{}
	done := make(chan bool)
	go func() { atomic.AddInt64(&j.C, 1); done <- true }()
	_ = j.C // race: plain read vs atomic write
	<-done
}

// D11: goroutine reads, parent copy()s into S.
func D11(src []int) {
	j := &Job{S: make([]int, 2)}
	done := make(chan bool)
	go func() { _ = j.S[0]; done <- true }()
	copy(j.S, src) // race
	<-done
}

// D12: goroutine reads j, parent sends j on a channel to a writer goroutine.
func D12() {
	j := &Job{}
	ch := make(chan *Job, 1)
	go func() {
		x := <-ch
		x.ID = 5
	}()
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	ch <- j // hands j to a writer; races with the reader
	<-done
}

// E1: per-element fan-out, local slice, same element every iteration.
func E1(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[0]
		wg.Go(func() { it.ID = i }) // race between iterations
	}
	wg.Wait()
}

// E2: per-element, local slice, non-injective index.
func E2(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i/2]
		wg.Go(func() { it.ID = i }) // race between iterations
	}
	wg.Wait()
}

// E3: per-element, parent writes an element before Wait.
func E3(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i]
		wg.Go(func() { it.ID = i })
	}
	items[0].ID = 7 // race
	wg.Wait()
}

// E4: per-element, inner loop reuses the outer index.
func E4(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := range items {
		for k := 0; k < 2; k++ {
			it := &items[i]
			wg.Go(func() { it.ID = k }) // two goroutines per element race
		}
	}
	wg.Wait()
}

// E5: per-element, but the loop variable is reassigned in the body.
func E5(n int) {
	items := make([]Job, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		it := &items[i]
		wg.Go(func() { it.ID = i })
		i = 0 // the next iteration starts at 1 again: element 1 is written by many goroutines
		if n > 100 {
			break
		}
	}
	wg.Wait()
}

// E6: per-element, the goroutine also reads a neighbour.
func E6(n int) {
	items := make([]Job, n+1)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		it := &items[i]
		nx := &items[i+1]
		wg.Go(func() { it.ID = nx.ID }) // reads i+1 while the next goroutine writes it
	}
	wg.Wait()
}

// E7: per-element via range over a slice of pointers that may repeat.
func E7(ps []*Job) {
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Go(func() { p.ID = 1 }) // ps may hold one pointer twice
	}
	wg.Wait()
}

// E8: per-element of a local slice of pointers that do repeat.
func E8() {
	x := &Job{}
	ps := []*Job{x, x}
	var wg sync.WaitGroup
	for i := range ps {
		p := ps[i]
		wg.Go(func() { p.ID = 1 }) // same pointee twice
	}
	wg.Wait()
}
