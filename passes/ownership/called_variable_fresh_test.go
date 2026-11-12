package ownership_test

import (
	"strings"
	"testing"
)

// A call of a variable returns a fresh value when every function the variable may hold does. The
// functions here are fresh by what their bodies do (they return an allocation), which is known
// from their summaries and not from a table, and a call whose targets were all of that kind was
// answered "no contract": its result was "not owned", and a function that returned it was "not
// known to return a fresh value".
//
// mkOld comes before old on purpose. Its body names no function, so nothing in it says that its
// summary depends on old's; the summaries are worked out callees first, and mkOld is reached while
// old's is still the one every function starts with. It has to be worked out again when old's
// changes.
const calledVariableFreshHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func mkOld() *T { return mkG() }

func mkEither(c bool) *T {
	m := newA
	if c {
		m = old
	}
	return m()
}

var shared = &T{}

func old() *T { return shared }

var mkG = old

func newA() *T { return &T{} }

func newB() *T { return &T{n: 1} }

func mk(c bool) *T {
	m := newA
	if c {
		m = newB
	}
	return m()
}

func via(f func() *T) *T { return f() }
`

func calledVariableFreshDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, calledVariableFreshHead+"\nfunc use(c bool) {\n\t"+body+"\n}\n", "is not owned here")
}

func TestCallOfAVariableThatHoldsFreshFunctionsIsFresh(t *testing.T) {
	for name, body := range map[string]string{
		"called where it is published":           "m := newA\n\tif c {\n\t\tm = newB\n\t}\n\tx := m()\n\tp.Store(x)",
		"one function, through a variable cell":  "m := newA\n\tset := func() { m = newB }\n\tset()\n\tx := m()\n\tp.Store(x)",
		"returned by a function that calls it":   "x := mk(c)\n\tp.Store(x)",
		"returned by a function it is passed to": "x := via(newA)\n\tp.Store(x)",
		"control: called by name":                "x := newA()\n\tp.Store(x)",
	} {
		if got := calledVariableFreshDiags(t, body); len(got) != 0 {
			t.Errorf("%s: every function that may run returns a fresh value, and the publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
	for name, body := range map[string]string{
		"one of two is not fresh":                        "m := newA\n\tif c {\n\t\tm = old\n\t}\n\tx := m()\n\tp.Store(x)",
		"the one in a package variable is not fresh":     "x := mkG()\n\tp.Store(x)",
		"returned by a function, one of two not fresh":   "x := mkEither(c)\n\tp.Store(x)",
		"returned by a function that calls the variable": "x := mkOld()\n\tp.Store(x)",
		"passed one that is not fresh":                   "x := via(old)\n\tp.Store(x)",
		"control: called by name, not fresh":             "x := old()\n\tp.Store(x)",
	} {
		if got := calledVariableFreshDiags(t, body); len(got) == 0 {
			t.Errorf("%s: a function that may run returns memory that is shared, and the publish was not reported", name)
		}
	}
}
