// Package dx_x_nearmiss is the next round after dx_directive: spellings of a directive that a reader
// takes for one and parseDirective does not accept. Each is reported as SAFE007 at the comment and
// has no effect, so the check it asks for visibly does not run (fail closed), and prose that merely
// starts with "safe:" or "gocap:" stays clean.
//
// Do not gofmt this file: it holds the spellings gofmt produces from typos, next to the typos.
package dx_x_nearmiss

import (
	"sync"
	"unsafe"
)

type Job struct{ ID int }

type S struct {
	mu sync.Mutex
	a  int //  safe:guarded_by mu // HIT(SAFE007) two spaces after the slashes
	b  int //	safe:guarded_by mu // HIT(SAFE007) a tab after the slashes
	c  int // safe: guarded_by mu // HIT(SAFE007) spaces after the slashes and after the colon
	d  int //SAFE:GUARDED_BY mu // HIT(SAFE007) all capitals
	e  int //Safe:guarded-by mu // HIT(SAFE007) wrong prefix case and a hyphen: within edit distance 2 of guarded_by
	f  int //safe:gaurded_by mu // HIT(SAFE007) exact prefix, transposed verb: an unknown directive, with a suggestion
	g  int /*safe: guarded_by mu*/ // control: the exact prefix with a space after the colon is a directive, and it is applied
}

func (s *S) unlocked() {
	// None of the guards on a..f took effect, and each is reported where it is written.
	s.a++
	s.b++
	s.c++
	s.d++
	s.e++
	s.f++
	s.g++ // HIT(SAFE009)
}

// A hatch spelled wrong mutes nothing: the cast under it is still reported, and so is the spelling.
func hatchSpelledWrong(p *int) uintptr {
	//GOCAP:unsafe reviewed // HIT(SAFE007)
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

func hatchSpaced(p *int) uintptr {
	// gocap:unsafe reviewed // HIT(SAFE007)
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

func hatchTypo(p *int) uintptr {
	//Gocap:unsfae reviewed // HIT(SAFE007) prefix case and a transposition, within edit distance 1 of unsafe
	return uintptr(unsafe.Pointer(p)) // HIT(SAFE001)
}

// A near miss on a later line of a multi-line block comment, reported at the comment's start.
/* HIT(SAFE007) the second line of this comment reads as //gocap:iso j
 * gocap:iso j
 */
func blockIso(j *Job) { _ = j }

// The gofmt pairs. Each typo is written as typed, then as gofmt rewrites it inside a doc comment:
// gofmt keeps "//x:y" unspaced only when y starts with a lower-case letter or a digit.
//
//gocap:Iso j // HIT(SAFE007) as typed: an unknown directive
func isoTyped(j *Job) { _ = j }

// gofmt's spelling of the line above.
//
// gocap:Iso j // HIT(SAFE007) after gofmt: a near miss
func isoFormatted(j *Job) { _ = j }

// An empty verb, as typed.
//
//gocap: // HIT(SAFE007)
func emptyTyped() {}

// gofmt's spelling of the line above.
//
// gocap: // HIT(SAFE007)
func emptyFormatted() {}

// Prose is left alone: after "safe:" or "gocap:" and a space, only a verb spelled exactly, followed by
// white space or the end of the line, is taken for a directive.
//
// safe: this lock is held by the caller
// gocap: see docs/DIRECTIVES.md
// Safe: unsafe.Pointer is never used here.
// safe: is this right? It is.
// Thread-safe: yes.
// gocap:isolation is a design note, not a verb.
func prose() {}
