// Package dx_x_line is the next round after dx_line: //line directives that renumber a directive's
// line, or its target's, so that a line-based attacher would pick other code. Attachment uses
// physical lines, so each directive stays with the code written next to it. Markers are keyed by
// physical lines too.
package dx_x_line

import (
	"sync"
	"unsafe"
)

// A trailing hatch renumbered to the line of a cast in another function.
func victim(p *int) uintptr {
//line line.go:300
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

func trailing() int {
	x := 1
//line line.go:300
	return x //gosree:unsafe returning a local by value is fine
}

// A doc-comment hatch whose comment is renumbered so that the line after it is the victim's.
func victim2(p *int) uintptr {
//line line.go:400
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

//line line.go:399
//gosree:unsafe nothing in this function needs it
func harmless() int { return 1 }

// An inline /*line*/ directive in the middle of a line.
func victim3(p *int) uintptr {
	return /*line line.go:500:1*/ uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

func inlineLine() int {
	y := 2
	/*line line.go:499:1*/ //gosree:unsafe reviewed
	return y
}

// A guard renumbered onto another field's line: the guard stays on the field it is written next to.
type T struct {
	mu sync.Mutex
//line line.go:600
	a int
//line line.go:600
	b int //gosree:guarded_by mu
}

func (t *T) touch() {
	t.a++
	t.b++ // HIT(SAFE009)
}
