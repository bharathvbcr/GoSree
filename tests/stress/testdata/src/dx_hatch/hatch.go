// Package dx_hatch attacks the //gosree:unsafe escape hatch. A hatch is a span (annot.go:258: the
// attached node's Pos..End), and every pass mutes every finding of every rule inside it; the
// ownership pass also mutes everything a hatched transfer causes, wherever it lies (check.go:19).
// Nothing ties a hatch to the finding its reason is about, so one hatch written for a pointer cast
// hides a use-after-move on the same line, in a goroutine it starts, or on the next statement.
package dx_hatch

import (
	_ "unsafe"
	u "unsafe"
)

type Job struct{ ID int }

// 1. Same line. Control first: without the hatch both findings are reported.
func sameLineControl(ch chan *Job) uintptr {
	j := &Job{}
	ch <- j
	return uintptr(u.Pointer(&j.ID)) // HIT(SAFE001) HIT(SAFE003)
}

func sameLine(ch chan *Job) uintptr {
	j := &Job{}
	ch <- j
	return uintptr(u.Pointer(&j.ID)) //gosree:unsafe the address is only logged // MISS(SAFE003): muted by an unrelated hatch written for the pointer cast; j was sent and is read here
}

// 2. A hatch on the function, written for one cast, reaches into the goroutine the function starts.
// Control first: the same body without the hatch.
func funcLevelControl(p *int) uintptr {
	j := &Job{}
	go func() { j.ID = 2 }()
	j.ID = 1                     // HIT(SAFE003)
	return uintptr(u.Pointer(p)) // HIT(SAFE001)
}

//
//gosree:unsafe the cast at the end is reviewed: p outlives the call
func funcLevel(p *int) uintptr {
	j := &Job{}
	go func() { j.ID = 2 }()
	j.ID = 1 // MISS(SAFE003): muted by an unrelated hatch: the function-level hatch is about the cast, this is a race with the goroutine above
	return uintptr(u.Pointer(p))
}

// 3. A trailing hatch on an if line covered the whole if statement, body included, until 2026-11-10.
// A hatch written on the same line as code now covers that line's statement only, not the bodies of
// blocks that continue past the line: here the condition, not the body.
func ifLine(p *int) uintptr {
	if p != nil { //gosree:unsafe p is checked non-nil before the cast
		j := &Job{}
		go func() { j.ID = 2 }()
		j.ID = 1                     // HIT(SAFE003) no longer muted by the hatch trailing the if line three lines up
		return uintptr(u.Pointer(p)) // HIT(SAFE001) the cast the hatch's reason is about needs its own hatch, on this line
	}
	return 0
}

// 4. A hatch trailing the last line of a multi-line statement covers that statement and no other.
// Until 2026-11-10 it attached to the NEXT statement (no node starts on the hatch's own line), so the
// statement it was written for was reported and the one after it was muted.
func multiLine(p *byte) {
	a := u.Add(
		u.Pointer(p), 1) //gosree:unsafe offset 1 is inside the buffer
	b := u.Add(u.Pointer(p), 99) // HIT(SAFE001)
	_, _ = a, b
}

// 5. A hatch on a transfer covers what the transfer causes, including a write on an EARLIER line: the
// next iteration's write races with the receiver whose reads the reason vouches for.
func loopAboveControl(ch chan *Job) {
	j := &Job{}
	for i := 0; i < 2; i++ {
		j.ID = i // HIT(SAFE003)
		ch <- j
	}
}

func loopAbove(ch chan *Job) {
	j := &Job{}
	for i := 0; i < 2; i++ {
		j.ID = i // MISS(SAFE003): muted by the hatch on the send below it; the next iteration writes j while the receiver reads it
		//gosree:unsafe the receiver only reads ID
		ch <- j
	}
}

// 6. The borrow pass skips a conflict when EITHER borrow lies in a hatch (passes/borrow/check.go:788):
// a hatch on the first borrow's line, written for something else, hides a conflict lines later.
func borrowControl() uintptr {
	x, y := 1, 2
	b1, raw := &x /*gosree:mut*/, uintptr(u.Pointer(&y)) // HIT(SAFE001)
	b2 := &x                                            /*gosree:mut*/ // HIT(BORROW002)
	*b1 = 2
	*b2 = 3
	return raw
}

func borrowHatched() uintptr {
	x, y := 1, 2
	b1, raw := &x /*gosree:mut*/, uintptr(u.Pointer(&y)) //gosree:unsafe raw is only logged
	b2 := &x                                            /*gosree:mut*/ // MISS(BORROW002): muted by an unrelated hatch on b1's line, written for the cast of y
	*b1 = 2
	*b2 = 3
	return raw
}

// 7. A hatch rejected as malformed (no reason) was still installed and still muted until 2026-11-10.
func noReason(p *int) uintptr {
	//gosree:unsafe // HIT(SAFE007)
	return uintptr(u.Pointer(p)) // HIT(SAFE001) a directive gosree rejects has no effect
}

// 8. A reason made of one zero-width space was a reason until 2026-11-10.
func invisibleReason(p *int) uintptr {
	//gosree:unsafe ​ // HIT(SAFE007) the reason is U+200B ZERO WIDTH SPACE, invisible in review; DIRECTIVES.md 2.7 requires a reason
	return uintptr(u.Pointer(p)) // HIT(SAFE001) the rejected hatch mutes nothing
}

// 9. A hatch that mutes nothing. DIRECTIVES.md 2.7: "it is flagged as stale". It is only counted in
// ownership.Stats.UnsafeStale, which `gosree ./...` never prints.
func stale() int {
	//gosree:unsafe nothing here needs it // MISS(SAFE007): stale hatch, never reported to a gosree user
	return 1
}

// 10. A hatch on a //go:linkname declaration mutes the bodiless function and, since 2026-11-10, the
// //go:linkname line in its own doc comment: a hatch in a declaration's doc comment covers the doc
// comment too. Until then that comment lay before FuncDecl.Pos, outside every span.
//
//go:linkname nanotime runtime.nanotime // the declaration's hatch covers its doc comment
//gosree:unsafe reviewed: monotonic clock read for tracing
func nanotime() int64
