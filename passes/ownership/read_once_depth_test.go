package ownership_test

import (
	"strings"
	"testing"
)

// Where a pointer leads nowhere in sight, the stores to the field anywhere in the package are
// read once on a walk. A reading that is made at the depth limit reads nothing: every value it
// would follow is one step too deep. If that reading counted as the one reading, a later one
// from a shallower step would not be made, and what it would have found is lost: the call is
// then "could not be followed to the end" where it was a publish before the stores were read
// once. Here the function called is the field read directly, or the same field read through
// twelve conversions, and the deep one is the first the walk comes to.
func TestStoresByTypeAreReadAgainFromAShallowerStep(t *testing.T) {
	const head = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type S struct{ fn func(*T) }

type F1 func(*T)

type F2 func(*T)

func arm(r *S) { r.fn = %s }
`
	deep := "q.fn"
	for i := range 12 {
		if i%2 == 0 {
			deep = "F1(" + deep + ")"
		} else {
			deep = "F2(" + deep + ")"
		}
	}
	body := "\nfunc Use(q *S, c bool) int {\n\tx := &T{}\n\tvar g func(*T)\n\tif c {\n\t\tg = " + deep + "\n\t} else {\n\t\tg = q.fn\n\t}\n\tg(x)\n\treturn x.n\n}\n"
	if got := advDiagLinesAfter(t, strings.Replace(head, "%s", "p.Store", 1), body, "is used after it was published"); len(got) == 0 {
		t.Errorf("the field may hold the publisher, read directly, and the use of x after the call was not reported")
	}
	if got := advDiagLinesAfter(t, strings.Replace(head, "%s", "noop", 1), body, "is used after it was published"); len(got) != 0 {
		t.Errorf("the field never holds the publisher, and a publish of x was reported: %s", strings.Join(got, "; "))
	}
}

// The stores by type are read once for each field of each struct type: a call that may be one
// field or another of a struct that leads nowhere, or the same field of two struct types, is
// read for both.
func TestStoresByTypeAreReadForEachFieldAndType(t *testing.T) {
	const head = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type A struct{ a, b func(*T) }

type B struct{ a, b func(*T) }
`
	for name, c := range map[string]struct{ arm, pick string }{
		"another field of the struct": {"func arm(r *A) { r.b = %s }", "g := qa.a\n\tif c {\n\t\tg = qa.b\n\t}"},
		"the field of another struct": {"func arm(r *B) { r.a = %s }", "g := qa.a\n\tif c {\n\t\tg = qb.a\n\t}"},
	} {
		body := "\nfunc Use(qa *A, qb *B, c bool) int {\n\tx := &T{}\n\t" + c.pick + "\n\tg(x)\n\treturn x.n\n}\n"
		if got := advDiagLinesAfter(t, head+"\n"+strings.Replace(c.arm, "%s", "p.Store", 1)+"\n", body, "is used after it was published"); len(got) == 0 {
			t.Errorf("%s holds the publisher, and the use of x after the call was not reported", name)
		}
		if got := advDiagLinesAfter(t, head+"\n"+strings.Replace(c.arm, "%s", "noop", 1)+"\n", body, "is used after it was published"); len(got) != 0 {
			t.Errorf("%s: no field holds the publisher, and a publish of x was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
