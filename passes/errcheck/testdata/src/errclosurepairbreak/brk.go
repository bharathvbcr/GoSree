package errclosurepairbreak

import "errors"

type T struct{ n int }

var errNo = errors.New("no")

func workV() (*T, error) {
	if len("x") == 2 {
		return nil, errNo
	}
	return &T{n: 1}, nil
}

func use(t *T) int { return t.n }

// FALSE NEGATIVES introduced by 4b7ffd3. Named results are heap cells, so a closure that assigns
// them is a tracked pair; but the Return lowering (lower.go: case *ssa.Return) reports a companion
// only when the returned error is a literal nil constant or was blanked, and here the returned
// error is a load of the err cell. The closure's own store used to be reported (base reports every
// C_ function below at the closure store); the fix drops that report and nothing replaces it. Each
// T_ twin (no closure) is reported by both builds. The expected markers on the C_ functions state
// what the twin does, so these cases failed until the plan excluded named results. They are now reported at the closure
// store, as on base (the C_ markers sit on the store, not the return): fail closed.

func C_NamedNilErr() (v *T, err error) {
	func() { v, err = workV() }() // want "ERR002"
	return v, nil
}

func T_NamedNilErr() (v *T, err error) {
	v, err = workV()
	return v, nil // want "ERR002" "ERR003"
}

func C_NamedErrNilled() (v *T, err error) {
	func() { v, err = workV() }() // want "ERR002"
	err = nil
	return
}

func T_NamedErrNilled() (v *T, err error) {
	v, err = workV()
	err = nil
	return // want "ERR002" "ERR003"
}

func C_NamedOtherErr() (v *T, err error) {
	var e2 error
	func() { v, err = workV() }() // want "ERR002"
	_ = err
	return v, e2
}

func T_NamedOtherErr() (v *T, err error) {
	var e2 error
	v, err = workV()
	_ = err
	return v, e2 // want "ERR002"
}
