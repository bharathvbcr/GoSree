package advnilerrother

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

// The field is read on a different pointer of the same type: p is still dropped.
func otherPointer(i int) error {
	p := lookup(i)
	q := lookup(i + 1)
	if p != nil {
		record(q.Code)
		return nil // want "ERR003"
	}
	return nil
}
