package advnilerrcap

import "fmt"

type codeErr struct {
	Code int
	Msg  string
}

func (e *codeErr) Error() string { return e.Msg }

type wrapErr struct {
	codeErr
	Extra int
}

func (e *wrapErr) Error() string { return "wrap" }

func lookup(i int) *codeErr {
	if i > 0 {
		return &codeErr{Code: i}
	}
	return nil
}

func lookupW(i int) *wrapErr { return nil }

func record(code int) { fmt.Println(code) }

// BREAK: same shape as fieldReadClean, but p is captured by a closure so it lives in a heap cell;
// the field read does not observe the cell, so nilerr still fires.
func capturedByClosure(i int) error {
	p := lookup(i)
	f := func() *codeErr { return p }
	_ = f
	if p != nil {
		record(p.Code)
		return nil
	}
	return nil
}
