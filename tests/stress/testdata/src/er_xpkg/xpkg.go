// Package er_xpkg stress-tests cross-package FuncFact propagation for ERR004.
package er_xpkg

import "er_xpkg/sub"

func unguardedCrossPkg(ok bool) error {
	return sub.MayFail(ok) // HIT(ERR004)
}

func guardedCrossPkg(ok bool) error {
	if e := sub.MayFail(ok); e != nil {
		return e
	}
	return nil
}

func provenNonNilCrossPkg() error {
	return sub.AlwaysErr("boom")
}
