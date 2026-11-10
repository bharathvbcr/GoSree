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
	f := v.SetInt // want `SAFE001: reflect.Value.SetInt writes or exposes memory through reflection`
	f(2)
	_ = v.Addr()          // want `SAFE001: reflect.Value.Addr writes or exposes memory through reflection`
	_ = v.Pointer()       // want `SAFE001: reflect.Value.Pointer writes or exposes memory through reflection`
	v.Clear()             // want `SAFE001: reflect.Value.Clear writes or exposes memory through reflection`
	v.Grow(4)             // want `SAFE001: reflect.Value.Grow writes or exposes memory through reflection`
	_ = reflect.Copy(v, v) // want `SAFE001: reflect.Copy writes or exposes memory through reflection`
	newAt := reflect.NewAt // want `SAFE001: reflect.NewAt writes or exposes memory through reflection`
	_ = newAt
	swap := reflect.Swapper([]int{1, 2}) // want `SAFE001: reflect.Swapper writes or exposes memory through reflection`
	_ = swap
}

type intSetter interface {
	SetInt(int64)
	Pointer() uintptr
}

func viaReflectIface(v reflect.Value) {
	var s intSetter = v
	s.SetInt(1)      // want `SAFE001: reflect.Value.SetInt writes or exposes memory through reflection`
	_ = s.Pointer()  // want `SAFE001: reflect.Value.Pointer writes or exposes memory through reflection`
	fn := s.SetInt   // want `SAFE001: reflect.Value.SetInt writes or exposes memory through reflection`
	fn(2)
	v.Send(v)        // want `SAFE001: reflect.Value.Send writes or exposes memory through reflection`
	_ = v.TrySend(v) // want `SAFE001: reflect.Value.TrySend writes or exposes memory through reflection`
}

func viaReflectConstraint[T interface{ SetInt(int64) }](t T) {
	t.SetInt(1) // want `SAFE001: reflect.Value.SetInt writes or exposes memory through reflection`
}

func statementHatch(p *int) {
	//safe:unsafe the test checks statement-level suppression
	_ = unsafe.Add(unsafe.Pointer(p), 0)
	_ = unsafe.Add(unsafe.Pointer(p), 8) // want `SAFE001: unsafe.Add` `SAFE001: unsafe.Pointer`
}
