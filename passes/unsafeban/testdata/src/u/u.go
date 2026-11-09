package u

import (
	"reflect"
	"unsafe"
)

func addr(p *int) uintptr {
	return uintptr(unsafe.Pointer(p)) // want `SAFE001: unsafe.Pointer bypasses Go's memory safety`
}

// Compile-time layout queries stay allowed.
func size() uintptr { return unsafe.Sizeof(0) + unsafe.Alignof(0) }

// The escape hatch covers the whole function, signature included.
//
//safe:unsafe interop test: the caller pins p for the duration of the call
func raw(p *int) unsafe.Pointer { return unsafe.Pointer(p) }

func viaReflect(v reflect.Value) {
	v.SetInt(1) // want `SAFE001: reflect.Value.SetInt writes or exposes memory through reflection`
	_ = v.Int()
}

func statementHatch(p *int) {
	//safe:unsafe the test checks statement-level suppression
	_ = unsafe.Add(unsafe.Pointer(p), 0)
	_ = unsafe.Add(unsafe.Pointer(p), 8) // want `SAFE001: unsafe.Add` `SAFE001: unsafe.Pointer`
}
