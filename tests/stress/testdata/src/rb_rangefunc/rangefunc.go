// Package rb_rangefunc feeds the analyzers range-over-func loops (Go 1.23), whose bodies SSA turns
// into synthetic closures called by the iterator, plus range-over-int and iter.Pull.
package rb_rangefunc

import (
	"iter"
	"slices"
)

type Job struct{ ID int }

func seq(yield func(int) bool) {
	for i := range 3 {
		if !yield(i) {
			return
		}
	}
}

func seq2(yield func(int, *Job) bool) {
	for i := range 3 {
		if !yield(i, &Job{ID: i}) {
			return
		}
	}
}

// sendInBody: the pointer is sent inside the body and written after the loop.
func sendInBody(ch chan *Job) {
	j := &Job{}
	for range seq {
		ch <- j // HIT(SAFE002): the hazard with the write after the loop is real; it is reported here under SAFE002
		break
	}
	j.ID = 1 // MISS(SAFE003): the send happened inside the range-over-func body (a synthetic closure); the move does not survive the loop
}

// sendInBodyPlain is the plain-for twin.
func sendInBodyPlain(ch chan *Job) {
	j := &Job{}
	for range 3 {
		ch <- j
		break
	}
	j.ID = 1 // HIT(SAFE003)
}

// sendBeforeUseInBody: sent before the loop, used inside the synthetic body.
func sendBeforeUseInBody(ch chan *Job) {
	j := &Job{}
	ch <- j // FP(SAFE003): the use inside the range-over-func body (line 53) is reported at the send line instead
	for x := range seq {
		j.ID = x // MISS(SAFE003): reported at the send (line 51) rather than here
	}
}

// sendBeforeUsePlain is its plain-for twin.
func sendBeforeUsePlain(ch chan *Job) {
	j := &Job{}
	ch <- j
	for x := range 3 {
		j.ID = x // HIT(SAFE003)
	}
}

// controlFlow: break, continue, return, defer and goto inside a range-over-func body.
func controlFlow(ch chan *Job) int {
	j := &Job{}
	n := 0
	for i, k := range seq2 {
		if i == 0 {
			continue
		}
		if i == 1 {
			defer func() { n++ }()
		}
		if k.ID > 5 {
			goto out
		}
		if i == 2 {
			ch <- j // FP(SAFE002): a captured variable sent inside a range-over-func body is treated as a parameter of the synthetic yield closure, though the body runs on this goroutine and j is owned here and never used after the send
			return n
		}
		if i > 2 {
			break
		}
	}
out:
	j.ID = 2
	return n
}

// nested range-over-func with labeled continue.
func nested(ch chan *Job) {
	j := &Job{}
outer:
	for i := range seq {
		for k := range seq {
			if k == i {
				continue outer
			}
			if k > i {
				ch <- j // HIT(SAFE002): the hazard with the write after the loop is real; it is reported here under SAFE002
				break outer
			}
		}
	}
	j.ID = 3 // MISS(SAFE003): the send happened inside the range-over-func body (a synthetic closure); the move does not survive the loop
}

// pull: iter.Pull drives the iterator from a separate coroutine.
func pull(ch chan *Job) {
	next, stop := iter.Pull2(seq2)
	defer stop()
	for {
		_, k, ok := next()
		if !ok {
			break
		}
		ch <- k  // HIT(SAFE002)
		k.ID = 1 // HIT(SAFE003)
	}
}

// slicesAll: a standard-library iterator.
func slicesAll(ch chan *Job, js []*Job) {
	for _, k := range slices.All(js) {
		k.ID++
	}
	own := &Job{}
	for range slices.Values([]int{1}) {
		ch <- own // HIT(SAFE002): the hazard with the write after the loop is real; it is reported here under SAFE002
	}
	own.ID = 4 // MISS(SAFE003): the send happened inside the range-over-func body (a synthetic closure); the move does not survive the loop
}

// rangeIntSend: range over int, the send in the last iteration.
func rangeIntSend(ch chan *Job) {
	j := &Job{}
	for i := range 4 {
		if i == 3 {
			ch <- j
		}
	}
	j.ID = 5 // HIT(SAFE003)
}

// goInBody: a go statement started inside a range-over-func body captures j.
func goInBody() {
	j := &Job{}
	for range seq {
		go func() { j.ID = 1 }() // HIT(SAFE002): the race with line 155 is real; it is reported here under SAFE002
		break
	}
	j.ID = 2 // MISS(SAFE003): j was captured by a goroutine started inside the range-over-func body; the plain-for twin reports this line
}

// goInBodyPlain is its twin.
func goInBodyPlain() {
	j := &Job{}
	for range 1 {
		go func() { j.ID = 1 }()
		break
	}
	j.ID = 2 // HIT(SAFE003)
}

// genericIter is a generic iterator over pointers.
func genericIter[T any](xs []*T) iter.Seq[*T] {
	return func(yield func(*T) bool) {
		for _, x := range xs {
			if !yield(x) {
				return
			}
		}
	}
}

func genericIterUse(ch chan *Job) {
	j := &Job{}
	for x := range genericIter([]*Job{j}) {
		x.ID = 9
	}
	ch <- j
}
