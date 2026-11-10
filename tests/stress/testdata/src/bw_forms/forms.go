// Package bw_forms runs one mechanism (a second mutable borrow while the first is live, and a
// borrow stored in a package variable) through every spelling of a borrow annotation. The
// spellings are documented as equivalent (docs/DIRECTIVES.md), so each pair must behave alike.
package bw_forms

import "github.com/bharathvbcr/gocap/cap"

var global *int // HIT(SAFE006)

// 1. trailing line comment.
func lineComment() {
	x := 0
	b1 := &x //gocap:mut
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 2. inline block comment before the operand.
func blockBefore() {
	x := 0
	b1 := /*gocap:mut*/ &x
	b2 := /*gocap:mut*/ &x // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 3. directive on the line above the assignment.
func lineAbove() {
	x := 0
	//gocap:mut
	b1 := &x
	//gocap:mut
	b2 := &x // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 4. cap.BorrowMut marker call.
func markerCall() {
	x := 0
	b1 := cap.BorrowMut(&x)
	b2 := cap.BorrowMut(&x) // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 5. var declaration with the cap.Mut[T] type.
func typedVar() {
	x := 0
	var b1 cap.Mut[int] = &x
	var b2 cap.Mut[int] = &x // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 6. cap.Mut[T] declared, assigned later.
func typedVarAssignedLater() {
	x := 0
	var b1, b2 cap.Mut[int]
	b1 = &x
	b2 = &x // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 7. legacy //safe: prefix.
func legacyPrefix() {
	x := 0
	b1 := &x //safe:mut
	b2 := &x //safe:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 8. mixed: a marker-call borrow and a comment borrow of the same owner.
func mixed() {
	x := 0
	b1 := cap.BorrowMut(&x)
	b2 := &x //gocap:mut // HIT(BORROW002)
	*b2 = 1
	*b1 = 2
}

// 9-12. parameter spellings, each storing the borrow in a package variable.

//gocap:mub b
func paramDirective(b *int) {
	global = b // HIT(BORROW001)
}

// documented inline form: the block comment after the type, exactly as docs/DIRECTIVES.md:91
// (`func Increment(val *int /*gocap:mub*/)`) and docs/RULES.md:400/516 write it. Until 2026-11-10
// annot attached the comment to the FuncDecl on its line and rejected it for naming no parameter.
func paramInline(b *int /*gocap:mub*/) {
	global = b // HIT(BORROW001) b is a \mub borrow by the documented inline form
}

// the same block comment between the name and the type (not a documented position) is rejected as
// malformed, with a message that says where it goes.
func paramInlineBeforeType(b /*gocap:mub*/ *int) { // HIT(SAFE007)
	global = b
}

func paramTyped(b cap.Mut[int]) {
	global = b // HIT(BORROW001)
}

func paramTypedRef(b cap.Ref[int]) {
	global = b // HIT(BORROW001)
}
