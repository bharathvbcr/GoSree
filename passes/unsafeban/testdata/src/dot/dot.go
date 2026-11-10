package dot

import (
	. "unsafe"
)

// A dot import does not hide unsafe: the identifier still resolves to package unsafe.
func addr(p *int) uintptr {
	return uintptr(Pointer(p)) // want `SAFE001: unsafe.Pointer bypasses Go's memory safety`
}

// An alias of an unsafe member is a use of it.
type raw = Pointer // want `SAFE001: unsafe.Pointer bypasses Go's memory safety`

// Compile-time layout queries stay allowed through a dot import too.
func size() uintptr { return Sizeof(0) + Alignof(0) }

// A function declared without a body is implemented in assembly or linked in from elsewhere.
func asmAdd(a, b int) int // want `SAFE001: function asmAdd has no Go body`

// The escape hatch covers a bodiless declaration as well.
//
//gosree:unsafe the assembly is reviewed separately
func asmSub(a, b int) int
