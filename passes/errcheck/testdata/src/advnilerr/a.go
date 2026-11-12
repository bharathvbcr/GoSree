package advnilerr

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

// Reads that do observe: all clean.
func deref(i int) error {
	p := lookup(i)
	if p != nil {
		c := (*p).Code
		record(c)
		return nil
	}
	return nil
}

func embedded(i int) error {
	p := lookupW(i)
	if p != nil {
		record(p.Code)
		return nil
	}
	return nil
}

func embeddedExtra(i int) error {
	p := lookupW(i)
	if p != nil {
		record(p.Extra)
		return nil
	}
	return nil
}

func viaAlias(i int) error {
	p := lookup(i)
	q := p
	if p != nil {
		record(q.Code)
		return nil
	}
	return nil
}
