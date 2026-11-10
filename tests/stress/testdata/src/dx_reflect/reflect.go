// Package dx_reflect attacks the reflection half of SAFE001. docs/RULES.md lists
// "reflect.Value.UnsafePointer, reflect.Value.UnsafeAddr, reflect.NewAt, reflect.Value.Set*", and the
// pass's message is "writes or exposes memory through reflection". reflectWrite
// (passes/unsafeban/unsafeban.go:79) matches only a call whose callee is a selector resolving
// directly to one of those reflect methods, by name prefix.
package dx_reflect

import (
	"encoding/json"
	"reflect"
)

// Controls: the listed APIs, called directly.
func listed(v reflect.Value, p *int) {
	v.SetInt(1)                            // HIT(SAFE001)
	v.Elem().Set(reflect.ValueOf(2))       // HIT(SAFE001)
	v.SetLen(0)                            // HIT(SAFE001)
	v.SetMapIndex(v, v)                    // HIT(SAFE001)
	_ = v.UnsafeAddr()                     // HIT(SAFE001)
	_ = v.UnsafePointer()                  // HIT(SAFE001)
	_ = reflect.NewAt(v.Type(), nil)       // HIT(SAFE001)
	reflect.Value.SetInt(v, 1)             // HIT(SAFE001)
	_ = reflect.ValueOf(p).Elem().CanSet() // reads stay allowed
}

// Embedding reflect.Value still resolves to reflect's method.
type wrapped struct{ reflect.Value }

func embedded(w wrapped) {
	w.SetInt(1) // HIT(SAFE001)
}

// A method value: the write happens at a call whose callee is a plain identifier.
func methodValue(v reflect.Value) {
	set := v.SetInt // HIT(SAFE001): method value of reflect.Value.SetInt
	set(1)
}

// Interface dispatch: the callee resolves to the local interface's method, implemented by reflect.Value.
type intSetter interface{ SetInt(int64) }

func viaInterface(v reflect.Value) {
	var s intSetter = v
	s.SetInt(1) // HIT(SAFE001): reflect.Value.SetInt called through a local interface
}

// A generic constraint does the same.
func viaConstraint[T interface{ SetInt(int64) }](t T) {
	t.SetInt(1) // HIT(SAFE001): reflect.Value.SetInt reached through a type parameter's method set
}

func callConstraint(v reflect.Value) { viaConstraint(v) }

// Writers that do not start with "Set".
func otherWriters(dst, src reflect.Value, xs []int) {
	reflect.Copy(dst, src)      // HIT(SAFE001): reflect.Copy writes dst's elements through reflection
	dst.Clear()                 // HIT(SAFE001): Value.Clear zeroes a map or slice through reflection
	swap := reflect.Swapper(xs) // HIT(SAFE001): reflect.Swapper returns a function that writes xs's elements through reflection
	swap(0, 1)
}

// Value.Pointer is UnsafeAddr's twin: it returns the raw address as a uintptr.
func rawAddress(v reflect.Value) uintptr {
	return v.Pointer() // HIT(SAFE001): Value.Pointer exposes the raw address
}

// Decoding into a value writes its fields by reflection inside encoding/json. The rule does not
// claim stdlib-internal reflection, so this is a control: nothing is reported, and nothing should
// be under SAFE001 (immutability of decoded types is SAFE005's business).
type config struct{ Port int }

func decode(b []byte) (config, error) {
	var c config
	err := json.Unmarshal(b, &c)
	return c, err
}
