// Package dx_x_hatchspan is the next round after dx_hatch:48: how far a //gosree:unsafe reaches. The
// rule: a hatch covers the code written under it. On its own line above a statement, or in a
// declaration's doc comment, it covers that whole statement or declaration (doc comment included).
// Written on the same line as code, it covers that line's statement except the bodies of blocks in
// it that span several lines.
package dx_x_hatchspan

import "unsafe"

// A trailing hatch on a for header covers the header, not the body.
func forHeader(p *int, n int) uintptr {
	var s uintptr
	for i := 0; i < n; i++ { //gosree:unsafe n is small
		s += uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
	}
	return s
}

// A trailing hatch on a switch header covers the tag, not the clauses.
func switchHeader(p *int, n int) uintptr {
	switch n { //gosree:unsafe the tag is an int
	case 0:
		return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
	}
	return 0
}

// A trailing hatch on a case line covers the case expressions, not the statements under it.
func caseLine(p *int, n int) uintptr {
	switch n {
	case 1: //gosree:unsafe one is the reviewed case
		return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
	}
	return 0
}

// A trailing hatch on a go statement's line covers the statement, not the goroutine's body.
func goLine(p *int, done chan uintptr) {
	go func() { //gosree:unsafe the goroutine only reads p
		done <- uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
	}()
}

// A hatch trailing the closing line of a goroutine's literal covers the go statement, not the body:
// the body spans several lines.
func goClose(p *int, done chan uintptr) {
	go func() {
		done <- uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
	}() //gosree:unsafe the goroutine only reads p
}

// A trailing hatch on a func header covers the signature, not the body.
func funcHeader(p *int) uintptr { //gosree:unsafe reviewed
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

// A hatch trailing an opening brace alone covers nothing, and is reported.
func elseBrace(p *int, ok bool) uintptr {
	if ok {
		return 0
	} else { //gosree:unsafe the else branch is reviewed // HIT(SAFE007)
		return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
	}
}

// Controls: the same code with the hatch on its own line above the statement is covered whole.
func ownLineAbove(p *int, n int) uintptr {
	var s uintptr
	//gosree:unsafe n is small and p outlives the loop
	for i := 0; i < n; i++ {
		s += uintptr(unsafe.Pointer(p))
	}
	return s
}

// A function literal that opens and closes on the hatch's line is part of that line's statement.
func oneLineLiteral(p *int) uintptr {
	f := func() uintptr { return uintptr(unsafe.Pointer(p)) } //gosree:unsafe p outlives f
	return f()
}

// A multi-line statement without a block: a hatch trailing its first line covers all of it.
func multiLineCall(p, q *int) uintptr {
	return uintptr(unsafe.Pointer(p)) + //gosree:unsafe both pointers outlive the call
		uintptr(unsafe.Pointer(q))
}

// A declaration-level hatch covers the whole function, goroutine bodies included: under the rule
// above, the goroutine is written under the hatch. Whether it should is an owner decision.
//
//gosree:unsafe the casts in this function are reviewed
func declLevel(p *int, done chan uintptr) {
	go func() {
		done <- uintptr(unsafe.Pointer(p))
	}()
}
