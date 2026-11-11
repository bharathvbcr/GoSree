package advfverr3b3c

import (
	"errors"
	"os"
	"sync"
)

func work() error { return errors.New("x") }

func workV() (int, error) { return 1, nil }

// nested closure assigning a captured err (FreeVar of FreeVar).
func nestedAssign() error {
	var err error
	outer := func() func() {
		return func() { err = work() }
	}
	outer()()
	if err != nil {
		return err
	}
	return nil
}

// nested closure: check then lose.
func nestedCheckLose() error {
	var err error
	outer := func() func() error {
		return func() error {
			err = work()
			if err != nil {
				return nil // want "ERR003"
			}
			return nil
		}
	}
	return outer()()
}

// nested: ignored call result assigned to captured err and never checked.
func nestedUnchecked() error {
	var err error
	outer := func() func() {
		return func() {
			err = work()
		}
	}
	outer()()
	_ = err
	return nil
}

type box struct{ err error }

// closure capturing the address of a struct field.
func fieldAddr() error {
	var b box
	p := &b.err
	f := func() { *p = work() }
	f()
	if b.err != nil {
		return b.err
	}
	return nil
}

// closure through pointer to field: check then lose.
func fieldAddrLose() error {
	var b box
	p := &b.err
	f := func() error {
		*p = work()
		if *p != nil {
			return nil // want "ERR003"
		}
		return nil
	}
	return f()
}

// goroutine closure assigning a captured err.
func goroutineErr() error {
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = work()
	}()
	wg.Wait()
	return err
}

// goroutine closure: err checked inside and dropped.
func goroutineLose() {
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		err = work()
		if err != nil {
			return
		}
	}()
	<-done
}

// method value bound to captured receiver returning an error.
type svc struct{ err error }

func (s *svc) run() error { s.err = work(); return s.err }

func methodValueErr(s *svc) error {
	run := s.run
	f := func() error {
		if err := run(); err != nil {
			return err
		}
		return nil
	}
	return f()
}

// method value with err unchecked.
func methodValueUnchecked(s *svc) {
	run := s.run
	f := func() {
		run() // want "ERR001"
	}
	f()
}

// closure defers close on a captured os.File with a captured err in the same closure.
func fileAndErr() error {
	var err error
	f, e := os.Create("x")
	if e != nil {
		return e
	}
	g := func() {
		defer f.Close() // want "ERR005"
		_, err = f.Write(nil)
	}
	g()
	return err
}

// captured err overwritten before check.
func overwritten() error {
	var err error
	f := func() {
		err = work()
		err = work()
	}
	f()
	return err
}

// captured err with multi-value call.
func multi() error {
	var err error
	f := func() {
		_, err = workV()
		if err != nil {
			return
		}
	}
	f()
	return err
}

// err param captured, checked then nil returned.
func paramCaptured(err error) func() error {
	return func() error {
		if err != nil {
			return nil // want "ERR003"
		}
		return nil
	}
}

// err param captured, nil-refinement across loads: first load nil, second must not be assumed nil.
func twoLoads(err error) func() error {
	return func() error {
		if err == nil {
			return err
		}
		return err
	}
}

// err reassigned in closure between loads with refinement.
func reassignRefine() func() error {
	var err error
	return func() error {
		if err != nil {
			return err
		}
		err = work()
		if err == nil {
			return nil
		}
		return nil // want "ERR003"
	}
}

// A concrete error pointer type that is also a writable, closable resource: the resource slot and
// the error nilness state meet on one captured variable.
type ce struct{}

func (*ce) Error() string               { return "ce" }
func (*ce) Write(p []byte) (int, error) { return 0, nil }
func (*ce) Close() error                { return nil }

func mk() *ce { return nil }

func dualCaptured() error {
	e := mk()
	f := func() error {
		_, _ = e.Write(nil)
		if e != nil {
			defer e.Close() // want "ERR005"
			return nil
		}
		return nil
	}
	return f()
}

// closure-captured err assigned through a goroutine and a deferred recover wrapper
func deferredCapturedCheck() (err error) {
	defer func() {
		if err != nil {
			_ = work()
		}
	}()
	err = work()
	return nil // want "ERR003"
}
