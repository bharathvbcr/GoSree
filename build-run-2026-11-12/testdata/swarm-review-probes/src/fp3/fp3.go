// Package fp3: narrowing the lost class. One race per function.
package fp3

import "sync"

type Job struct{ ID int }

var global *Job

var sm sync.Map

func cond() bool { return global == nil }

// N1: no loop, no WaitGroup: two goroutines each write items[0] through separate &items[0].
func N1() {
	items := make([]Job, 2)
	a := &items[0]
	go func() { a.ID = 1 }()
	b := &items[0]
	go func() { b.ID = 2 }()
}

// N2: in-loop alias of one outer pointer.
func N2() {
	var wg sync.WaitGroup
	j := &Job{}
	for i := 0; i < 3; i++ {
		k := j
		wg.Go(func() { k.ID = 2 })
	}
	wg.Wait()
}

// N3: read-only capture, then go write(j).
func write(j *Job) { j.ID = 3 }

func N3() {
	j := &Job{}
	go func() { _ = j.ID }()
	go write(j)
}

// N4: read-only capture, then a goroutine with j as an argument that writes.
func N4() {
	j := &Job{}
	go func() { _ = j.ID }()
	go func(x *Job) { x.ID = 3 }(j)
}

// N5: read-only capture, then return j (the caller may write).
func N5() *Job {
	j := &Job{}
	go func() { _ = j.ID }()
	return j
}

// N6: read-only capture, then publish to a global.
func N6() {
	j := &Job{}
	go func() { _ = j.ID }()
	global = j
}

// N7: read-only capture, then sync.Map.Store.
func N7() {
	j := &Job{}
	go func() { _ = j.ID }()
	sm.Store("k", j)
}

// N8: two &items[0] with no goroutine read before: plain go statements, no loop, struct pointer.
func N8() {
	j := &Job{}
	a := j
	go func() { a.ID = 1 }()
	b := j
	go func() { b.ID = 2 }()
}

// N9: the same as N1, but array instead of slice.
func N9() {
	var items [2]Job
	a := &items[0]
	go func() { a.ID = 1 }()
	b := &items[0]
	go func() { b.ID = 2 }()
}

// R1: rb_deadcode, real loop: a send that reaches itself sends j twice.
func R1(ch chan *Job) {
	j := &Job{}
	for cond() {
		ch <- j
	}
	j.ID = 1
}

// R2: a counted loop sending the same pointer three times.
func R2(ch chan *Job) {
	j := &Job{}
	for i := 0; i < 3; i++ {
		ch <- j
	}
}
