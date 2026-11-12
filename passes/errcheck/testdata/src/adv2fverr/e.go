// Package adv2fverr: ERR001-ERR004 diagnostics around captured error variables, identical at
// aabf10f and 9244bb2 in both engines. The ERR004 lines on a captured *MyErr that is assigned a
// non-nil value (e2) are a pre-existing imprecision, pinned as-is.
package adv2fverr

import (
	"errors"
	"os"
	"strconv"
)

type MyErr struct{}

func (*MyErr) Error() string { return "my" }

func mk() *MyErr { return nil }

func nz() *MyErr { return &MyErr{} }

func might() error { return errors.New("x") }

// E1: captured *MyErr assigned nil-able in closure, boxed afterwards in the closure.
func e1() error {
	var e *MyErr
	f := func() error {
		e = mk()
		return e // want "ERR004"
	}
	return f()
}

// E2: captured *MyErr assigned non-nil, then boxed (no ERR004 expected).
func e2() error {
	var e *MyErr
	f := func() error {
		e = nz()
		return e // want "ERR004"
	}
	return f()
}

// E3: captured *MyErr guarded after assignment.
func e3() error {
	var e *MyErr
	f := func() error {
		e = mk()
		if e != nil {
			return e // want "ERR004"
		}
		return nil
	}
	return f()
}

// E4: captured *MyErr boxed in the outer function after the closure assigned it.
func e4() error {
	var e *MyErr
	f := func() { e = mk() }
	f()
	return e // want "ERR004"
}

// E5: captured err overwritten twice without a read.
func e5() error {
	var err error
	f := func() {
		err = might()
		err = might()
	}
	f()
	return err
}

// E6: captured err assigned then discarded call result.
func e6() error {
	var err error
	f := func() {
		err = might()
		might() // want "ERR001"
	}
	f()
	return err
}

// E7: captured companion value then use.
func e7() int {
	var n int
	var err error
	f := func() {
		n, err = strconv.Atoi("1") // want "ERR002"
		_, _ = n, err
	}
	f()
	return n
}

// E8: captured *MyErr nil literal then boxed.
func e8() error {
	var e *MyErr
	f := func() error {
		e = nil
		return e // want "ERR004"
	}
	return f()
}

// E9: captured file, store from os.Create with checked error, nilerr shape.
func e9() error {
	var err error
	f := func() error {
		_, err = os.Open("x")
		if err != nil {
			return nil // want "ERR003"
		}
		return err
	}
	return f()
}

// E10: captured *MyErr assigned in loop then boxed.
func e10(n int) error {
	var e *MyErr
	f := func() error {
		for i := 0; i < n; i++ {
			e = mk()
		}
		return e // want "ERR004"
	}
	return f()
}

// E11: captured *MyErr checked non-nil before the store, boxed after.
func e11() error {
	e := nz()
	f := func() error {
		if e == nil {
			return nil
		}
		e = mk()
		return e // want "ERR004"
	}
	return f()
}
