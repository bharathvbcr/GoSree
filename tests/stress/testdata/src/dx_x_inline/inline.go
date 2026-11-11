// Package dx_x_inline is the next round after bw_forms:96: a borrow or iso directive written inside a
// parameter list. The one accepted place is directly after a parameter's type (before the comma or
// the closing parenthesis, or after the comma that ends the parameter's line); it applies to every
// name of that parameter declaration, as the type does. Anywhere else in a parameter, receiver,
// type-parameter or result list it is reported.
package dx_x_inline

import "github.com/bharathvbcr/gosree/cap"

type Job struct{ ID int }

var (
	global *int // HIT(SAFE006)
	jobs   *Job // HIT(SAFE006)
)

// Accepted: after the type of a receiver.
func (j *Job /*gosree:mub*/) keep() {
	jobs = j // HIT(BORROW001)
}

// Accepted: after the type of a two-name field; it covers both names.
func both(a, b *int /*gosree:mub*/) {
	global = a // HIT(BORROW001)
	global = b // HIT(BORROW001)
}

// Accepted: args choose among the names of the field.
func one(a, b *int /*gosree:mub b*/) {
	global = a
	global = b // HIT(BORROW001)
}

// Accepted: one parameter per line, the directive after the comma that ends the line.
func perLine(
	a *int, //gosree:mub
	b *int,
) {
	global = a // HIT(BORROW001)
	global = b
}

// Accepted: a function literal's parameter, checked like the cap.Mut spelling.
func inFunc() {
	f := func(b *int /*gosree:mub*/) {
		global = b // HIT(BORROW001)
	}
	g := func(b cap.Mut[int]) {
		global = b // HIT(BORROW001)
	}
	_, _ = f, g
}

// Package-level function literals are checked with both parameter borrow spellings.
var lit = func(b *int /*gosree:mub*/) {
	global = b // HIT(BORROW001)
}

var litTyped = func(b cap.Mut[int]) {
	global = b // HIT(BORROW001)
}

// Accepted: iso after the type moves the caller's argument.
func own(j *Job /*gosree:iso*/) { _ = j }

func caller() {
	j := &Job{}
	own(j)
	j.ID = 1 // HIT(SAFE003)
}

// Rejected: before the name, between the names, after the comma before the next parameter.
func before(/*gosree:mub*/ b *int) { global = b } // HIT(SAFE007)

func between(a /*gosree:mub*/, b *int) { global = a } // HIT(SAFE007)

func afterComma(a *int, /*gosree:mub*/ b *int) { global = b } // HIT(SAFE007)

// gofmt's spelling of the line above: the printer moves the comment before the comma, where it
// follows a's type. The formatted line reads, and is applied, as a borrow of a; b, which the author
// meant, is no longer annotated, and the SAFE007 above is gone. annot cannot tell this text from an
// intended annotation of a; the only stable place for the directive is after the type.
func afterCommaFormatted(a *int /*gosree:mub*/, b *int) { global = a; global = b } // HIT(BORROW001)

// Rejected: a result, an unnamed parameter, a type parameter.
func result() (p *int /*gosree:mub*/) { return nil } // HIT(SAFE007)

func unnamed(*int /*gosree:mub*/) {} // HIT(SAFE007)

func typeParam[T any /*gosree:mub*/](t T) { _ = t } // HIT(SAFE007)

// Rejected: an interface method and a function type have no body to check.
type I interface {
	M(p *int /*gosree:mub*/) // HIT(SAFE007)
}

type F func(p *int /*gosree:rob*/) // HIT(SAFE007)

// Rejected: verbs that do not belong in a parameter list, and an unknown verb.
func hatchInList(p *int /*gosree:unsafe reviewed*/) { _ = p } // HIT(SAFE007)

func unknownInList(p *int /*gosree:mubb*/) { _ = p } // HIT(SAFE007)

// Rejected: the inline form conflicting with a doc-comment directive on the same parameter, and naming
// a parameter the field does not declare.
//
//gosree:rob b
func conflict(b *int /*gosree:mub*/) { _ = b } // HIT(SAFE007)

func misnamed(a *int /*gosree:mub b*/) { _ = a } // HIT(SAFE007)
