// Package lib exports unsafe.Pointer under other names. Each export is flagged here, or carries one
// reviewed hatch, and then every importer gets unsafe.Pointer without a finding (see package user).
package lib

import "unsafe"

// P is an alias of unsafe.Pointer. The library reviewed it once and hatched it.
//
//gocap:unsafe interop type name, reviewed in this package
type P = unsafe.Pointer

// Q is a defined type over unsafe.Pointer; conversions to and from it follow unsafe.Pointer's rules.
type Q unsafe.Pointer // HIT(SAFE001)

// Addr returns an unsafe.Pointer value: an importer converts it without ever naming a type.
//
//gocap:unsafe returns the address for an interop table, reviewed here
func Addr(p *int) unsafe.Pointer { return unsafe.Pointer(p) }
