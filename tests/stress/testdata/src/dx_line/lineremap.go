// Package dx_line attacks directive attachment with //line directives. go/token applies //line to
// every Position, and until 2026-11-10 annot attached by Position().Line while a hatch's span is in
// raw offsets, so two physically distant statements given the same line number became one line to
// the attacher and a hatch could be pointed at code in another function. Attachment now uses
// physical lines (PositionFor(pos, false)); dx_x_line holds the next variants.
//
// The harness keys markers by the same adjusted positions, so every marker below sits on the
// physical line it describes and is read at that line's remapped number.
package dx_line

import "unsafe"

// The victim: a pointer cast with no hatch near it. Its statement is renumbered to line 100.
func early(p *int) uintptr {
//line lineremap.go:100
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001) the hatch in late(), many lines below, stays there: attachment uses physical lines, which //line does not renumber
}

// The control: an identical cast with no //line games is reported.
//
//line lineremap.go:200
func control(p *int) uintptr {
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

// The hatch: it looks attached to a harmless return. Renumbered to line 99, so attach looks for the
// first node on line 100, which is early's return statement.
func late() int {
	x := 1
//line lineremap.go:99
	//gosree:unsafe returning a local by value is fine
	return x
}
