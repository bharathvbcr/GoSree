package errnilerrptr

import "fmt"

type codeErr struct {
	Code int
	Msg  string
}

func (e *codeErr) Error() string { return e.Msg }

func lookup(i int) *codeErr {
	if i > 0 {
		return &codeErr{Code: i}
	}
	return nil
}

func record(code int) { fmt.Println(code) }

// Field read of a concrete error pointer after the nil check observes the error.
func fieldReadClean(i int) error {
	p := lookup(i)
	if p != nil {
		record(p.Code)
		return nil
	}
	return nil
}

// A method call on the pointer observes it too.
func methodCallClean(i int) error {
	p := lookup(i)
	if p != nil {
		fmt.Println(p.Error())
		return nil
	}
	return nil
}

// Passing the pointer along observes it.
func passedAlongClean(i int) error {
	p := lookup(i)
	if p != nil {
		fmt.Println(p)
		return nil
	}
	return nil
}

// The pointer is checked non-nil and dropped without being read: still nilerr.
func droppedStillReports(i int) error {
	p := lookup(i)
	if p != nil {
		return nil // want "ERR003"
	}
	return nil
}

// The plain error variant keeps reporting.
func plainErrReports() error {
	err := fmt.Errorf("x")
	if err != nil {
		return nil // want "ERR003"
	}
	return nil
}
