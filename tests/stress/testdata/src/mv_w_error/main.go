// WITNESS: race
//
// error is in the shareable override table ("assumed: error values are not mutated after
// creation", internal/kinds/kinds.go), so a value of type error had no alias class: sending one was
// a transfer of nothing. Here the error is a pointer to a mutable struct; the receiver asserts it
// back and writes it. Reported since 2026-11-11: the value boxed in the interface is tracked.
package main

import "sync"

type RetryErr struct{ Attempts int }

func (e *RetryErr) Error() string { return "retry" }

func main() {
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if r, ok := (<-errs).(*RetryErr); ok {
			r.Attempts++
		}
	}()
	e := &RetryErr{}
	errs <- e
	e.Attempts++ // HIT(SAFE003)
	wg.Wait()
}
