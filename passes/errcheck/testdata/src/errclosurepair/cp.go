package errclosurepair

import (
	"errors"
	"io"
)

type T struct{ n int }

var errNo = errors.New("no")

func workV() (*T, error) {
	if len("x") == 2 {
		return nil, errNo
	}
	return &T{n: 1}, nil
}

func use(t *T) int { return t.n }

func run(f func()) { f() }

// Checked: a closure assigns the captured (v, err) pair and the enclosing function checks err
// before using v. The pair escapes the closure together, so nothing is reported.
func Checked() (int, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// CheckedNilFirst checks in the other branch order.
func CheckedNilFirst() (int, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	if err == nil {
		return use(v), nil
	}
	return 0, err
}

// CheckedIs inspects the error with errors.Is after the nil check.
func CheckedIs() (int, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, errNo
		}
		return 0, err
	}
	return use(v), nil
}

// CalledLater binds the closure to a variable and calls it once, later. The effects happen at the
// call, which is where the pair is tracked.
func CalledLater() (int, error) {
	var v *T
	var err error
	f := func() { v, err = workV() }
	n := 1
	f()
	if err != nil {
		return 0, err
	}
	return use(v) + n, nil
}

// CheckedConditional calls the closure on one path only. At the join the pair is bound on one
// path and not on the other, so the enclosing function cannot hold the check: the store is
// reported (a known false positive, kept to fail closed).
func CheckedConditional(c bool) (int, error) {
	var v *T
	var err error
	if c {
		func() { v, err = workV() }() // want "ERR002"
	}
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// ReassignedValue replaces v after the closure call, so the use is not the closure's result. The
// non-closure twin (v, err := workV(); v = &T{}; _ = err) is clean too: the old closure-side
// report was the same false positive as the checked case.
func ReassignedValue() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	v = &T{n: 2}
	_ = err
	return use(v)
}

// Direct is the same pair without a closure, the control case.
func Direct() (int, error) {
	v, err := workV()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// Unchecked never checks err: the use in the enclosing function is reported.
func Unchecked() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	_ = err
	return use(v) // want "ERR002.*result of errclosurepair.workV\\(\\)"
}

// UncheckedConditional calls the closure on one path only and never checks err.
func UncheckedConditional(c bool) int {
	var v *T
	var err error
	if c {
		func() { v, err = workV() }() // want "ERR002"
	}
	_ = err
	return use(v)
}

// ErrReassigned overwrites err after the closure call, so the check proves nothing about v.
func ErrReassigned() (int, error) {
	var v *T
	var err error
	func() { v, err = workV() }()
	err = nil
	if err != nil {
		return 0, err
	}
	return use(v), nil // want "ERR002"
}

// StoresOnlyValue stores v out of the closure but keeps the error in a local: the pair does not
// escape, so the store is reported.
func StoresOnlyValue() int {
	var v *T
	func() {
		var e error
		v, e = workV() // want "ERR002"
		_ = e
	}()
	return use(v)
}

// ShadowedErr declares new locals in the closure; err is not the captured one.
func ShadowedErr() int {
	var v *T
	var err error
	func() {
		w, err := workV()
		v = w // want "ERR002"
		_ = err
	}()
	_ = err
	return use(v)
}

// UsedInClosure reads v back inside the closure before err is checked. The closure keeps its own
// obligation, so it is untracked and the store is reported.
func UsedInClosure() (int, error) {
	var v *T
	var err error
	func() {
		v, err = workV() // want "ERR002"
		_ = use(v)
	}()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// Deferred runs the closure at function exit, after the use, so the enclosing function cannot
// track it: the store is reported.
func Deferred() (int, error) {
	var v *T
	var err error
	defer func() { v, err = workV() }() // want "ERR002"
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// Escaped hands the closure to another function: it may run anywhere, so the store is reported.
func Escaped() (int, error) {
	var v *T
	var err error
	run(func() { v, err = workV() }) // want "ERR002"
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// CalledTwice runs one closure value twice: not a single tracked call, so it is reported.
func CalledTwice() (int, error) {
	var v *T
	var err error
	f := func() { v, err = workV() } // want "ERR002"
	f()
	f()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// SharedCells captures the pair in a second closure that can rewrite err between the call and the
// check, so the first closure is not tracked and its store is reported.
func SharedCells() (int, error) {
	var v *T
	var err error
	func() { v, err = workV() }() // want "ERR002"
	func() { err = nil }()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// Nested assigns the pair from a closure inside a closure: the cells are free variables of the
// outer closure, which is not tracked, so the inner store is reported.
func Nested() (int, error) {
	var v *T
	var err error
	func() {
		func() { v, err = workV() }() // want "ERR002"
	}()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

// TwoStores assigns the pair twice in the closure: not a single pair, so reported.
func TwoStores() (int, error) {
	var v *T
	var err error
	func() {
		v, err = workV() // want "ERR002"
		if err != nil {
			return
		}
		v, err = workV() // want "ERR002"
	}()
	if err != nil {
		return 0, err
	}
	return use(v), nil
}

func setup() error { return errNo }

// ErrOverwritten leaves err from setup unread and then runs the closure, which overwrites it. The
// non-closure twin (err := setup(); v, err := workV()) reports the same ERR003; the site is the
// call inside the closure.
func ErrOverwritten() int {
	var v *T
	var err error
	err = setup()
	func() { v, err = workV() }() // want "ERR003.*overwrites an earlier unchecked error"
	if err != nil {
		return 0
	}
	return use(v)
}

// NeverUsed binds the pair and uses neither: nothing is read from the closure's result. The
// non-closure twin is clean too.
func NeverUsed() int {
	var v *T
	var err error
	func() { v, err = workV() }()
	_ = v
	_ = err
	return 1
}

// Goroutine starts the closure on another goroutine: the enclosing function cannot track it.
func Goroutine() int {
	var v *T
	var err error
	go func() { v, err = workV() }() // want "ERR002"
	if err != nil {
		return 0
	}
	return use(v)
}

// VarClosure assigns the literal to a variable that is reassigned nowhere else. SSA promotes the
// variable, so the call runs the literal directly and is tracked like an immediate call.
func VarClosure() int {
	var v *T
	var err error
	var f func()
	f = func() { v, err = workV() }
	f()
	if err != nil {
		return 0
	}
	return use(v)
}

// AddrTaken rewrites err through a pointer between the call and the check: the store clears the
// link, so the check proves nothing and the use is reported.
func AddrTaken() int {
	var v *T
	var err error
	p := &err
	func() { v, err = workV() }()
	*p = nil
	if err != nil {
		return 0
	}
	return use(v) // want "ERR002.*result of errclosurepair.workV\\(\\)"
}

// LoopChecked runs the closure in a loop and leaves the loop on error; every return follows a
// checked call.
func LoopChecked() int {
	var v *T
	var err error
	for {
		func() { v, err = workV() }()
		if err != nil {
			break
		}
		return use(v)
	}
	return 0
}

// LoopUseBefore uses v on the next iteration before the call, and a return is reachable without
// the call: untracked, so the store is reported.
func LoopUseBefore() int {
	var v *T
	var err error
	n := 0
	for i := 0; i < 2; i++ {
		n += use(v)
		func() { v, err = workV() }() // want "ERR002"
		if err != nil {
			return 0
		}
	}
	return n
}

// Named results are read by the return itself, which checks only a literal-nil or blanked error, so
// a pair that lands in a result cell is not tracked and the closure store is reported. The twin
// without a closure is reported at its return as well.

func NamedNilErr() (v *T, err error) {
	func() { v, err = workV() }() // want "ERR002"
	return v, nil
}

func NamedBare() (v *T, err error) {
	func() { v, err = workV() }() // want "ERR002"
	err = nil
	return
}

// NamedChecked checks err before the bare return: a residual false positive at the store, kept so
// that every named-result shape fails closed.
func NamedChecked() (v *T, err error) {
	func() { v, err = workV() }() // want "ERR002"
	if err != nil {
		return
	}
	return v, nil
}

// NamedDeferred also has a deferred function that rewrites err: another closure captures the cell.
func NamedDeferred() (v *T, err error) {
	defer func() {
		if err != nil {
			err = errNo
		}
	}()
	func() { v, err = workV() }() // want "ERR002"
	return v, nil
}

// NamedRecover assigns err on the recovered path.
func NamedRecover() (v *T, err error) {
	defer func() {
		if recover() != nil {
			err = errNo
		}
	}()
	func() { v, err = workV() }() // want "ERR002"
	return v, nil
}
