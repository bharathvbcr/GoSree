// Package user never imports unsafe, yet reinterprets memory through the names package lib exports.
// docs/RULES.md (SAFE001) flags "any symbol from package unsafe (unsafe.Pointer, ...)": these are
// unsafe.Pointer conversions, spelled through another package, and the pass matches only
// identifiers whose object belongs to package unsafe (passes/unsafeban/unsafeban.go:59).
package user

import "dx_unsafe_spell/lib"

// In package lib the same conversions are flagged (lib.go: Q's declaration; P and Addr are hatched).
func viaAlias(p *int) *float64 {
	return (*float64)(lib.P(p)) // MISS(SAFE001): type pun *int -> *float64 through an exported alias of unsafe.Pointer; package user never names unsafe
}

func viaDefined(p *int) *float64 {
	return (*float64)(lib.Q(p)) // MISS(SAFE001): type pun through an exported defined type whose underlying type is unsafe.Pointer
}

func viaValue(p *int) *[4]int {
	return (*[4]int)(lib.Addr(p)) // MISS(SAFE001): unsafe.Pointer value converted to a 4x larger array pointer; no identifier names unsafe, so nothing matches
}

// Control: the same package without the conversion is plain Go.
func plain(p *int) *int { return p }
