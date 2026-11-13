// Package fp: footprint refinement probes. Each function has one racy access after a goroutine
// capture; the comment says where the race is.
package fp

import (
	"sort"
	"sync"
)

type Job struct {
	ID int
	P  *Pt
	M  map[string]int
	S  []int
}

type Pt struct{ X, Y int }

func (j *Job) Set(v int) { j.ID = v }

func write(j *Job) { j.ID = 9 }

// B1: goroutine reads, parent writes through a callee.
func B1() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	write(j) // race
	<-done
}

// B2: goroutine reads, parent writes through a local alias.
func B2() {
	j := &Job{}
	k := j
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	k.ID = 1 // race
	<-done
}

// B3: goroutine reads, parent writes via method.
func B3() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	j.Set(1) // race
	<-done
}

// B4: goroutine reads j.P.X, parent writes the pointee through a copied pointer.
func B4() {
	j := &Job{P: &Pt{}}
	done := make(chan bool)
	go func() { _ = j.P.X; done <- true }()
	q := j.P
	q.X = 1 // race
	<-done
}

// B5: goroutine reads, parent hands j to another goroutine that writes.
func B5() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	go func() { j.ID = 3 }() // race with the first goroutine
	<-done
}

// B6: goroutine reads S[0], parent sorts S (library write).
func B6() {
	j := &Job{S: []int{3, 1}}
	done := make(chan bool)
	go func() { _ = j.S[0]; done <- true }()
	sort.Ints(j.S) // race
	<-done
}

// B7: goroutine reads key "a", parent writes key "b": concurrent map read and write.
func B7() {
	m := map[string]int{}
	done := make(chan bool)
	go func() { _ = m["a"]; done <- true }()
	m["b"] = 1 // race (fatal: concurrent map read and map write)
	<-done
}

// B7b: map in a field.
func B7b() {
	j := &Job{M: map[string]int{}}
	done := make(chan bool)
	go func() { j.M["a"] = 1; done <- true }()
	j.M["b"] = 2 // race
	<-done
}

// B8: only the parent locks.
func B8() {
	var mu sync.Mutex
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	mu.Lock()
	j.ID = 1 // race: goroutine does not hold mu
	mu.Unlock()
	<-done
}

var enabled = false

// B9: a package variable (not a const) guards the write; it may be set at run time.
func B9() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	if enabled {
		j.ID = 1 // race when enabled is true
	}
	<-done
}

// B10: disjoint fields, but the parent writes the whole struct.
func B10() {
	p := Pt{}
	done := make(chan bool)
	go func() { p.X = 1; done <- true }()
	p = Pt{Y: 2} // race on X
	<-done
}

// B11: goroutine reads, parent writes via a pointer to the field.
func B11() {
	j := &Job{}
	done := make(chan bool)
	go func() { _ = j.ID; done <- true }()
	ip := &j.ID
	*ip = 4 // race
	<-done
}

// B12: goroutine reads slice element via range, parent appends in place (cap allows).
func B12() {
	s := make([]int, 1, 4)
	done := make(chan bool)
	go func() {
		for _, v := range s[:2] {
			_ = v
		}
		done <- true
	}()
	s = append(s, 5) // race: writes s[1] in the shared backing array
	<-done
	_ = s
}

// B13: read-only goroutine, parent writes after an unrelated channel op (not a join).
func B13() {
	j := &Job{}
	other := make(chan bool, 1)
	go func() { _ = j.ID }()
	other <- true
	j.ID = 1 // race
}

// B14: goroutine reads via interface method call; parent writes.
type reader interface{ Read() int }

func (j *Job) Read() int { return j.ID }

func B14() {
	j := &Job{}
	var r reader = j
	done := make(chan bool)
	go func() { _ = r.Read(); done <- true }()
	j.ID = 1 // race
	<-done
}

// B15: goroutine reads j via a loop of wg.Go, parent writes before Wait.
func B15() {
	var wg sync.WaitGroup
	j := &Job{}
	for i := 0; i < 3; i++ {
		wg.Go(func() { _ = j.ID })
	}
	j.ID = 1 // race: Wait has not happened
	wg.Wait()
}

// B16: per-element, but the element is the same every iteration.
func B16(items []Job) {
	var wg sync.WaitGroup
	for i := range items {
		_ = i
		it := &items[0]
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
}

// B17: per-element with a non-injective index.
func B17(items []Job) {
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i/2]
		wg.Go(func() { it.ID = i })
	}
	wg.Wait()
}

// B18: per-element, parent touches an element before Wait.
func B18(items []Job) {
	var wg sync.WaitGroup
	for i := range items {
		it := &items[i]
		wg.Go(func() { it.ID = i })
	}
	items[0].ID = 7 // race
	wg.Wait()
}

// B19: per-element via an outer-loop index reused in an inner loop.
func B19(items []Job) {
	var wg sync.WaitGroup
	for i := range items {
		for k := 0; k < 2; k++ {
			it := &items[i]
			wg.Go(func() { it.ID = k })
		}
	}
	wg.Wait()
}
