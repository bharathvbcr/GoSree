// WITNESS: panic
//
// A concrete error pointer (*TxError) is laundered through `any` and asserted back to `error`.
// In Go, `a.(error)` succeeds when `a` holds `(*TxError)(nil)` because `*TxError` implements
// `error`, yielding a non-nil `error` interface whose `.Error()` call panics on nil dereference.
// GoSree's ERR004 statically convicts the `.(error)` boxing.
package main

import "fmt"

type TxError struct{ Op string }

func (e *TxError) Error() string { return "tx failed: " + e.Op }

func commit(ok bool) error {
	var txErr *TxError
	if !ok {
		txErr = &TxError{Op: "commit"}
	}
	var boxed any = txErr
	return boxed.(error) // HIT(ERR004)
}

func main() {
	if err := commit(true); err != nil {
		fmt.Println(err.Error())
	}
}
