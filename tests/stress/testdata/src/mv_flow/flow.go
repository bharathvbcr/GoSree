// Package mv_flow probes the flow-sensitive half of the move check (passes/ownership/cfg.go
// useAfter): which paths after a move count, and what kills them.
package mv_flow

type Job struct {
	ID   int
	Next *Job
}

// move in one branch only, use after the merge.
func oneBranch(ch chan *Job, c bool) {
	j := &Job{}
	if c {
		ch <- j
	}
	j.ID = 1 // HIT(SAFE003)
}

// goto back over the move.
func gotoLoop(ch chan *Job) {
	j := &Job{}
	n := 0
again:
	j.ID = n // HIT(SAFE003)
	if n == 0 {
		ch <- j
		n++
		goto again
	}
}

// labeled continue: move in the inner loop, use in the outer.
func labeledContinue(ch chan *Job) {
	j := &Job{}
outer:
	for i := 0; i < 2; i++ {
		j.ID = i // HIT(SAFE003)
		for k := 0; k < 2; k++ {
			ch <- j
			continue outer
		}
	}
}

// switch fallthrough carries the path from the moving case into the next.
func fallthroughUse(ch chan *Job, n int) {
	j := &Job{}
	switch n {
	case 0:
		ch <- j
		fallthrough
	case 1:
		j.ID = 1 // HIT(SAFE003)
	}
}

// recover path: the use is in the deferred closure that runs while panicking.
func recoverUse(ch chan *Job) {
	j := &Job{}
	defer func() { // HIT(SAFE003): reported on the defer, which runs after the move
		if recover() != nil {
			j.ID = 1
		}
	}()
	ch <- j
	panic("x")
}

// move inside a closure invoked later.
func closureMove(ch chan *Job) {
	j := &Job{}
	send := func() { ch <- j } // HIT(SAFE002): a closure that is not a goroutine body does not own its captures
	send()
	j.ID = 1
}

// closure that moves, invoked twice.
func closureTwice(ch chan *Job) {
	j := &Job{}
	send := func() { ch <- j } // HIT(SAFE002): a closure that is not a goroutine body does not own its captures
	send()
	send()
}

// select with default: the send may not happen, but on the path where it does, the use races.
func selectDefault(ch chan *Job) {
	j := &Job{}
	select {
	case ch <- j:
	default:
	}
	j.ID = 1 // HIT(SAFE003)
}

// conditional re-initialisation: only one path gets a fresh value.
func condReinit(ch chan *Job, c bool) {
	j := &Job{}
	ch <- j // HIT(SAFE003): the use at the merge below is a phi of the moved value, reported here on the edge that carries it
	if c {
		j = &Job{}
	}
	j.ID = 1
}

// unconditional re-initialisation: the new value is owned. Must be accepted.
func reinit(ch chan *Job) {
	j := &Job{}
	ch <- j
	j = &Job{}
	j.ID = 1
}

// re-initialisation in a loop: each iteration sends a fresh value. Must be accepted.
func reinitLoop(ch chan *Job) {
	j := &Job{}
	for i := 0; i < 3; i++ {
		ch <- j
		j = &Job{ID: i}
	}
}

// shadowing: the inner j is a different variable.
func shadow(ch chan *Job) {
	j := &Job{}
	{
		j := &Job{}
		ch <- j
	}
	j.ID = 1
}

// shadowing the other way: inner j aliases the outer one.
func shadowAlias(ch chan *Job) {
	j := &Job{}
	{
		j := j
		ch <- j
	}
	j.ID = 1 // HIT(SAFE003)
}

// use hidden in an if-statement init.
func initUse(ch chan *Job) {
	j := &Job{}
	ch <- j
	if n := j.ID; n > 0 { // HIT(SAFE003)
		return
	}
}

// use hidden in a for post statement.
func forPost(ch chan *Job) {
	for j := (&Job{}); j != nil; j = j.Next { // HIT(SAFE003): j.Next is read after j was sent
		ch <- j
	}
}

// move via a go statement with arguments, use after.
func goArg() {
	j := &Job{}
	go func(p *Job) { p.ID = 2 }(j)
	j.ID = 1 // HIT(SAFE003)
}

// move by a goroutine whose function value is chosen at run time.
func goDynamic(c bool) {
	j := &Job{}
	f := func() { j.ID = 2 }
	if c {
		f = func() {}
	}
	go f()
	j.ID = 1 // HIT(SAFE003)
}

// a closure value stored in a struct field and started from there.
type Task struct{ Run func() }

func goField() {
	j := &Job{}
	t := &Task{Run: func() { j.ID = 2 }}
	go t.Run()
	j.ID = 1 // HIT(SAFE003)
}

// closure value returned by a helper and started.
func maker(j *Job) func() { return func() { j.ID = 2 } }

func goMaker() {
	j := &Job{}
	go maker(j)()
	j.ID = 1 // HIT(SAFE003)
}
