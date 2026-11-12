package ownership_test

import (
	"strings"
	"testing"
)

// A struct made in one place is written wherever its pointer goes. The trace asked what a field
// of it holds by reading the stores made through the allocation itself (s := &S{}; s.fn = ...)
// and answered that those were all. A pointer that had been returned, stored, or joined with
// another on a branch is another name for the same struct, and a store through that name was
// not read: a struct made by a constructor and then given a publisher by its caller was called
// as what the constructor had put there.
//
// Where the pointer goes somewhere the trace does not read, every store this package makes to
// that field of that struct type stands in for what may be written there, as it does where a
// pointer leads nowhere in sight, and the answer may not be all.
const structAliasHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type S struct{ fn func(*T) }

func newS() *S { return &S{fn: noop} }

type W struct{ s *S }

var keepS *S
`

func structAliasDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, structAliasHead+"\nfunc use(c bool) int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", "is used after it was published")
}

func TestStructWrittenThroughAnotherNameForItsPointer(t *testing.T) {
	for name, body := range map[string]string{
		"control: set through the allocation itself":            "s := &S{fn: noop}\n\ts.fn = p.Store\n\ts.fn(x)",
		"made by a constructor, set by the caller":              "s := newS()\n\ts.fn = p.Store\n\ts.fn(x)",
		"set through a pointer chosen on a branch":              "s := &S{fn: noop}\n\tu := &S{fn: noop}\n\tr := s\n\tif c {\n\t\tr = u\n\t}\n\tr.fn = p.Store\n\ts.fn(x)",
		"set through a pointer kept in another struct":          "s := &S{fn: noop}\n\tw := W{s: s}\n\tw.s.fn = p.Store\n\ts.fn(x)",
		"set through a pointer kept in a package variable":      "s := &S{fn: noop}\n\tkeepS = s\n\tkeepS.fn = p.Store\n\ts.fn(x)",
		"set through a pointer variable a closure captures":     "q := &S{fn: noop}\n\tq.fn = p.Store\n\tpeek := func() bool { return q != nil }\n\t_ = peek()\n\tq.fn(x)",
		"a closure sets it through the pointer variable":        "q := &S{fn: noop}\n\tset := func() { q.fn = p.Store }\n\tset()\n\tq.fn(x)",
		"known: another struct of the type holds the publisher": "s := newS()\n\tu := &S{fn: p.Store}\n\t_ = u\n\ts.fn(x)",
	} {
		if got := structAliasDiags(t, body); len(got) == 0 {
			t.Errorf("%s: the field may hold the publisher when it is called, and the use of x after that was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"made by a constructor, nothing sets it":                     "s := newS()\n\ts.fn(x)",
		"made by a constructor, set to something else":               "s := newS()\n\ts.fn = noop\n\ts.fn(x)",
		"a pointer variable a closure captures, set to something":    "q := &S{fn: noop}\n\tq.fn = noop\n\tpeek := func() bool { return q != nil }\n\t_ = peek()\n\tq.fn(x)",
		"its pointer goes nowhere, another struct has the publisher": "s := &S{fn: noop}\n\tu := &S{fn: p.Store}\n\t_ = u\n\ts.fn(x)",
	} {
		if got := structAliasDiags(t, body); len(got) != 0 {
			t.Errorf("%s: the field never holds the publisher, and a publish of x was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
