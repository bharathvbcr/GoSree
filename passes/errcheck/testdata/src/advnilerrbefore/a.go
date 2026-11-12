package advnilerrbefore

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

// Read before the nil check, then the check, then drop.
func readBeforeCheck(i int) error {
	p := lookup(i)
	record(p.Code)
	if p != nil {
		return nil // want "ERR003"
	}
	return nil
}
