// Package sp_forkjoin attacks fork-join recovery (passes/ownership/forkjoin.go): places where the
// checker hands ownership back to the parent although the goroutine may still run, and correct
// joins it does not recognise.
package sp_forkjoin

import (
	"context"
	"sync"
)

type Job struct{ ID int }

// control: WaitGroup.Go then Wait on the same receiver is a join.
func wgGoJoin() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 2 })
	wg.Wait()
	j.ID = 1
}

// The spawner-wait join (computeJoins, sp.receiver()) trusts any Wait on the receiver, but nothing
// checks the counter: an extra Done in the parent lets Wait return while the callback still runs.
func wgGoStrayDone() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 2 })
	wg.Done()
	wg.Wait()
	j.ID = 1 // MISS(SAFE003): the spawner-wait join (forkjoin.go computeJoins, sp.receiver) trusts any Wait on the receiver; nothing checks the counter, so a stray Done lets Wait return while the callback runs. Witness sp_w_straydone
}

// The same with Add(-1).
func wgGoNegativeAdd() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 2 })
	wg.Add(-1)
	wg.Wait()
	j.ID = 1 // MISS(SAFE003): the same with Add(-1)
}

// control: captured WaitGroup, Add before go, deferred Done: a join.
func capturedJoin() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		j.ID = 2
	}()
	wg.Wait()
	j.ID = 1
}

// control: captured WaitGroup, Done before the goroutine's last use: no join.
func doneBeforeLastUse() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Add(1)
	go func() {
		wg.Done()
		j.ID = 2
	}()
	wg.Wait()
	j.ID = 1 // HIT(SAFE003)
}

// control: Add after go is not balanced by construction: no join.
func addAfterGo() {
	var wg sync.WaitGroup
	j := &Job{}
	go func() {
		defer wg.Done()
		j.ID = 2
	}()
	wg.Add(1)
	wg.Wait()
	j.ID = 1 // HIT(SAFE003)
}

// FP: Done inside a deferred closure is a correct and common shape (it is how a recover is paired
// with Done), but the WaitGroup is then captured twice over and the join is dropped.
func doneInDeferredClosure() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Add(1)
	go func() {
		defer func() { wg.Done() }()
		j.ID = 2
	}()
	wg.Wait()
	j.ID = 1 // FP(SAFE003): Done in a deferred closure runs after every access of the goroutine; the join is correct, but syncObj.walk rejects a WaitGroup captured by a closure that is not the parent
}

// FP: a WaitGroup embedded in a struct; Go and Wait reach it through two FieldAddr values, so the
// receiver comparison in waitCalls fails.
type group struct {
	sync.WaitGroup
	name string
}

func embeddedGroup() {
	var g group
	j := &Job{}
	g.Go(func() { j.ID = 2 })
	g.Wait()
	j.ID = 1 // FP(SAFE003): Go and Wait reach the embedded WaitGroup through two FieldAddr values, so waitCalls (Args[0] != recv) finds no Wait
}

// FP: an idiomatic select-based join, with an exit on cancellation. The receive in a select is not
// a recognised acquire.
func selectJoin(ctx context.Context) {
	j := &Job{}
	done := make(chan struct{})
	go func() {
		j.ID = 2
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		<-done
	}
	j.ID = 1 // FP(SAFE003): both select arms receive from done before reaching here; a receive inside a select is never an acquire (syncObj.walk, Select case)
}

// control: plain channel join.
func chanJoin() {
	j := &Job{}
	done := make(chan struct{})
	go func() {
		j.ID = 2
		close(done)
	}()
	<-done
	j.ID = 1
}

// control: a range over the channel the goroutine closes is a join too.
func rangeJoin() {
	j := &Job{}
	done := make(chan int)
	go func() {
		j.ID = 2
		done <- 1
		close(done)
	}()
	for range done {
	}
	j.ID = 1
}

// control: a second sender makes the receive ambiguous: no join.
func twoSenders() {
	j := &Job{}
	done := make(chan struct{}, 2)
	go func() {
		j.ID = 2
		done <- struct{}{}
	}()
	go func() { done <- struct{}{} }()
	<-done
	j.ID = 1 // HIT(SAFE003)
}

// control: the goroutine sends before its last write.
func sendBeforeLastWrite() {
	j := &Job{}
	done := make(chan struct{}, 1)
	go func() {
		done <- struct{}{}
		j.ID = 2
	}()
	<-done
	j.ID = 1 // HIT(SAFE003)
}

// control: cancelling a context is not a join.
func cancelNotJoin(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	j := &Job{}
	go func() {
		<-ctx.Done()
		j.ID = 2
	}()
	cancel()
	j.ID = 1 // HIT(SAFE003)
}

// Loop: wg.Go in a loop, one Wait after it, is a join (one Wait covers every callback). The
// second iteration's capture is a use after the first move: one owner at a time, by design.
func loopGoOneWait() {
	var wg sync.WaitGroup
	j := &Job{}
	for i := 0; i < 3; i++ {
		wg.Go(func() { _ = j.ID }) // HIT(SAFE003)
	}
	wg.Wait()
	j.ID = 1
}

// Loop with captured WaitGroup and Add(1) per iteration: no join (Add runs more than once).
func loopCapturedWG() {
	var wg sync.WaitGroup
	j := &Job{}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { // HIT(SAFE003)
			defer wg.Done()
			_ = j.ID
		}()
	}
	wg.Wait()
	j.ID = 1
}

// The joined goroutine hands j to a grandchild through an override-typed value (sp_launder): the
// region check in computeJoins sees neither an escape nor a transfer, so the Wait restores j while
// the grandchild still writes it.
type jobErr struct{ j *Job }

func (e *jobErr) Error() string { return "job" }

func grandchildViaError() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() {
		go func(e error) { e.(*jobErr).j.ID = 2 }(&jobErr{j: j})
	})
	wg.Wait()
	j.ID = 1 // HIT(SAFE003)
}

// control: the same hand-off as a plain pointer is a transfer, so the join is refused.
func grandchildPlain() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() {
		go func(p *Job) { p.ID = 2 }(j)
	})
	wg.Wait()
	j.ID = 1 // HIT(SAFE003)
}
