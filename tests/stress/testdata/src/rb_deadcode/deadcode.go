// Package rb_deadcode puts moves, uses, borrows and directives in code SSA drops as unreachable:
// after return, panic, os.Exit, an infinite loop, or a goto that jumps over it.
package rb_deadcode

import "os"

type Job struct{ ID int }

func afterReturn(ch chan *Job) {
	j := &Job{}
	ch <- j
	return
	j.ID = 1
}

func afterPanic(ch chan *Job) {
	j := &Job{}
	ch <- j
	panic("x")
	j.ID = 1
}

func afterExit(ch chan *Job) {
	j := &Job{}
	ch <- j
	os.Exit(1)
	j.ID = 1
}

func afterLoop(ch chan *Job) {
	j := &Job{}
	ch <- j
	for {
	}
	j.ID = 1
}

func gotoOver(ch chan *Job) {
	j := &Job{}
	ch <- j
	goto end
	j.ID = 1
end:
}

func deadBorrows() {
	x := 1
	return
	//gocap:mut
	p := &x
	//gocap:rob
	q := &x
	*p = *q
}

func deadUnsafe(ch chan *Job) {
	j := &Job{}
	ch <- j
	panic("y")
	//gocap:unsafe only in dead code
	j.ID = 2
}

func deadGo() {
	j := &Job{}
	return
	go func() { j.ID = 1 }()
	j.ID = 2
}

func onlyPanic() { panic("only") }

func empty() {}

func infinite() {
	for {
	}
}

func selectForever() {
	select {}
}

// deadSend: the send is unreachable, the use is live.
func deadSend(ch chan *Job) *Job {
	j := &Job{}
	if false {
		ch <- j
	}
	j.ID = 1 // FP(SAFE003): the send is in an if-false branch that never runs; the constant condition is not pruned
	return j
}

// constFalseLoop: a send inside a loop that never runs.
func constFalseLoop(ch chan *Job) {
	j := &Job{}
	for false {
		ch <- j // FP(SAFE003): the loop body never runs (for false), yet the send is treated as reaching itself on the next iteration
	}
	j.ID = 1
}
