// Package er_typednil stress-tests ERR004: boxing concrete pointer error types (*T) that may be
// nil into error interfaces, producing non-nil interface headers that fool `err != nil`.
package er_typednil

type MyErr struct{ Code int }

func (e *MyErr) Error() string { return "my error" }

type CodedError interface {
	error
	ErrCode() int
}

func (e *MyErr) ErrCode() int { return e.Code }

type holder struct{ err *MyErr }

func mayNilHelper(ok bool) *MyErr {
	if ok {
		return nil
	}
	return &MyErr{Code: 500}
}

func chainHelper(ok bool) *MyErr {
	return mayNilHelper(ok)
}

func alwaysNonNil() *MyErr {
	return &MyErr{Code: 400}
}

// 1. Direct uninitialized concrete error pointer boxed into error.
func directZero() error {
	var e *MyErr
	return e // HIT(ERR004)
}

// 2. Conditional initialization leaves nil path reaching error return.
func conditionalNil(fail bool) error {
	var e *MyErr
	if fail {
		e = &MyErr{Code: 1}
	}
	return e // HIT(ERR004)
}

// 3. Chained intra-package helper returning maybe-nil *MyErr boxed into error.
func chainedCall(ok bool) error {
	return chainHelper(ok) // HIT(ERR004)
}

// 4. Concrete error pointer loaded from struct field may be nil.
func fromStructField(h *holder) error {
	return h.err // HIT(ERR004)
}

// 5. Concrete error pointer from map lookup returns typed nil on missing key.
func fromMapLookup(m map[string]*MyErr, k string) error {
	return m[k] // HIT(ERR004)
}

// 6. Laundering into a sub-interface of error (CodedError) still produces a non-nil interface!
func subInterfaceLaunder(fail bool) CodedError {
	var e *MyErr
	if fail {
		e = &MyErr{Code: 2}
	}
	return e // HIT(ERR004)
}

// 7. Laundering through `any` followed by `.(error)` type assertion in the same function:
// `a` holds dynamic type *MyErr with nil pointer, so `a.(error)` succeeds and returns typed-nil!
func anyAssertLaunder(fail bool) error {
	var e *MyErr
	if fail {
		e = &MyErr{Code: 3}
	}
	var a any = e
	return a.(error) // HIT(ERR004)
}

// 8. Interprocedural `any` laundering: helper returns `any` (losing static *MyErr provenance across
// the function signature), and caller asserts `.(error)`.
func hideInAny(ok bool) any {
	var e *MyErr
	if !ok {
		e = &MyErr{Code: 4}
	}
	return e
}

func interprocAnyLaunder(ok bool) error {
	a := hideInAny(ok)
	err, _ := a.(error) // MISS(ERR004): hideInAny erases *MyErr to `any` across a function boundary before `.(error)`
	return err
}

// --- Clean controls (must produce zero diagnostics) ---

func guardedReturn(fail bool) error {
	var e *MyErr
	if fail {
		e = &MyErr{Code: 1}
	}
	if e != nil {
		return e
	}
	return nil
}

func freshLiteralReturn() error {
	return &MyErr{Code: 42}
}

func provenNonNilHelperReturn() error {
	return alwaysNonNil()
}
