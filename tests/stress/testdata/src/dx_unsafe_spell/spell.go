// Package dx_unsafe_spell tries every spelling of package unsafe inside one package. SAFE001 matches
// identifiers that resolve to unsafe's members, so the spelling of the import does not matter here;
// these lines are regression locks. The holes are across a package boundary: see lib and user.
package dx_unsafe_spell

import (
	u "unsafe"
)

// Renamed import.
func renamed(p *int) uintptr {
	return uintptr(u.Pointer(p)) // HIT(SAFE001)
}

// Every pointer-making member.
func members(p *byte, n int) {
	_ = u.Add(u.Pointer(p), 1)             // HIT(SAFE001)
	_ = u.Slice(p, n)                      // HIT(SAFE001)
	_ = u.String(p, n)                     // HIT(SAFE001)
	_ = u.StringData("x")                  // HIT(SAFE001)
	_ = u.SliceData([]byte{1})             // HIT(SAFE001)
	_ = u.Sizeof(n) + u.Alignof(n)         // layout constants stay allowed
	_ = u.Offsetof(struct{ a, b int }{}.b) // layout constants stay allowed
}

// A defined type over unsafe.Pointer, and a struct field of that type: the declaration names unsafe.
type Raw u.Pointer // HIT(SAFE001)

type holder struct {
	p u.Pointer // HIT(SAFE001)
}

// Generic code instantiated with unsafe.Pointer: the type argument names unsafe at the call site.
func id[T any](t T) T { return t }

func generic(h holder) {
	_ = id[u.Pointer](h.p) // HIT(SAFE001)
}

// Using a package-local defined type over unsafe.Pointer converts without naming unsafe again. The
// declaration above is the one finding for the package, which is enough to stop the build.
func viaLocalRaw(p *int) *float64 {
	return (*float64)(Raw(p))
}
