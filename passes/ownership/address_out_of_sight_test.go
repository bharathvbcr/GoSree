package ownership_test

import (
	"strings"
	"testing"
)

// A function variable whose address goes somewhere that is not read (a method of an interface, a
// package variable) may be written there. The trace answered with the stores it could see and
// that they may not be all, which is true and finds nothing: a publisher written through the
// address was not among the functions the variable may be. For a struct the stores to the field
// anywhere in the package stand in; for a variable the stores made through a pointer of its type
// now do, leaving out the ones made to another allocation.
const addressOutOfSightHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type S struct{ fn func(*T) }

type setter interface{ set(*func(*T)) }

var keepF *func(*T)

var keepH *func(any)

func pubAny(v any) { p.Store(v.(*T)) }
`

const addressOutOfSightWriters = `
type O struct{}

func (o *O) set(pf *func(*T)) { *pf = p.Store }

func arm() { *keepF = p.Store }
`

const addressOutOfSightQuietWriters = `
type O struct{}

func (o *O) set(pf *func(*T)) { *pf = noop }

func arm() { *keepF = noop }
`

func addressOutOfSightDiags(t *testing.T, writers, body string) []string {
	t.Helper()
	return advDiagLinesAfter(t, addressOutOfSightHead+writers, "\nfunc use(o setter, c bool) int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", "is used after it was published")
}

func TestFunctionVariableWrittenWhereItsAddressGoes(t *testing.T) {
	for name, body := range map[string]string{
		"passed to a method of an interface":                   "f := noop\n\to.set(&f)\n\tf(x)",
		"kept in a package variable and written through it":    "f := noop\n\tkeepF = &f\n\tarm()\n\tf(x)",
		"a struct's field, passed to a method of an interface": "s := S{fn: noop}\n\to.set(&s.fn)\n\ts.fn(x)",
		"a pointer variable that holds it is passed on":        "f := noop\n\tq := &f\n\tif c {\n\t\tq = keepF\n\t}\n\to.set(q)\n\tf(x)",
	} {
		if got := addressOutOfSightDiags(t, addressOutOfSightWriters, body); len(got) == 0 {
			t.Errorf("%s: what is written through the address publishes, and the use of x after the call was not reported", name)
		}
		if got := addressOutOfSightDiags(t, addressOutOfSightQuietWriters, body); len(got) != 0 {
			t.Errorf("%s, with nothing that publishes written through such a pointer: reported: %s", name, strings.Join(got, "; "))
		}
	}
	// A store through a pointer that is this variable's or another's is a store that may be to
	// this one, and is not read where the variable is: the pointer is a choice between two.
	if got := addressOutOfSightDiags(t, addressOutOfSightQuietWriters, "f, g := noop, noop\n\tpf := &g\n\tif c {\n\t\tpf = &f\n\t}\n\t*pf = p.Store\n\tf(x)\n\t_ = g"); len(got) == 0 {
		t.Errorf("written through a pointer that may be its own: the use of x after the call was not reported")
	}
	// A publisher stored to another variable is not written to this one, also when the store is
	// made through a pointer or by a closure.
	for name, body := range map[string]string{
		"another variable is written through a pointer": "g, h := noop, noop\n\tpg := &g\n\tif c {\n\t\tpg = &h\n\t}\n\t*pg = p.Store\n\tf := noop\n\to.set(&f)\n\tf(x)",
		"another variable is written by a closure":      "g := noop\n\tw := func() { g = p.Store }\n\tw()\n\tf := noop\n\to.set(&f)\n\tf(x)\n\t_ = g",
		"a field of a struct holds the publisher":       "u := &S{fn: noop}\n\tu.fn = p.Store\n\tf := noop\n\to.set(&f)\n\tf(x)",
		"an element of an array holds the publisher":    "var arr [2]func(*T)\n\tarr[1] = p.Store\n\tf := noop\n\to.set(&f)\n\tf(x)\n\t_ = arr",
		"a function of another type is stored":          "f := noop\n\to.set(&f)\n\tvar h func(any)\n\tph := &h\n\tif c {\n\t\tph = keepH\n\t}\n\t*ph = pubAny\n\tf(x)",
	} {
		if got := addressOutOfSightDiags(t, addressOutOfSightQuietWriters, body); len(got) != 0 {
			t.Errorf("%s: nothing that publishes is written to f, and a use after publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
	// A variable whose address stays in sight is not written through a pointer of some other
	// function, whatever is stored through pointers elsewhere in the package.
	for name, body := range map[string]string{
		"the address stays here":           "f := noop\n\tq := &f\n\t*q = noop\n\tf(x)",
		"another variable's address goes":  "f, g := noop, noop\n\to.set(&g)\n\tf(x)\n\t_ = g",
		"a closure writes it and no other": "f := noop\n\tw := func() { f = noop }\n\tw()\n\tf(x)",
	} {
		if got := addressOutOfSightDiags(t, addressOutOfSightWriters, body); len(got) != 0 {
			t.Errorf("%s: nothing that publishes is written to f, and a use after publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
