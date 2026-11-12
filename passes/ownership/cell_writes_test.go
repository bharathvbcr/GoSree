package ownership_test

import (
	"strings"
	"testing"
)

// A local variable that a closure captures, or whose address is taken, lives in memory, and is
// written wherever one of its addresses is stored to: in its own function, and in each closure
// that captures it. The trace asked what such a variable holds by reading the stores in the
// variable's own function. A closure that overwrites it was not read, so a function variable a
// closure had set to a publisher was called as what it was first set to.
const cellWritesHead = `package p

import (
	"sync"
	"sync/atomic"
)

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type S struct {
	mu sync.Mutex
	fn func(*T)
}
`

// cellWritesDiags gives the uses of x after it was published that are reported in use. Where a
// closure makes the call, x is handed to it as an argument: a closure that publishes a variable
// it captures is told so in the closure, and that is not what is asked about here.
func cellWritesDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, cellWritesHead+"\nfunc use(c bool) int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", "is used after it was published")
}

func TestCellWrittenByAClosure(t *testing.T) {
	for name, body := range map[string]string{
		"a closure overwrites the variable":               "f := noop\n\tset := func() { f = p.Store }\n\tset()\n\tf(x)",
		"a closure inside a closure overwrites it":        "f := noop\n\tset := func() {\n\t\tin := func() { f = p.Store }\n\t\tin()\n\t}\n\tset()\n\tf(x)",
		"one closure overwrites it, another calls it":     "f := noop\n\tset := func() { f = p.Store }\n\tcall := func(y *T) { f(y) }\n\tset()\n\tcall(x)",
		"a closure overwrites it and calls it":            "f := noop\n\tboth := func(y *T) {\n\t\tf = p.Store\n\t\tf(y)\n\t}\n\tboth(x)",
		"a closure calls it, its function overwrites it":  "f := noop\n\tcall := func(y *T) { f(y) }\n\tf = p.Store\n\tcall(x)",
		"its function overwrites it on a branch":          "f := noop\n\tif c {\n\t\tf = p.Store\n\t}\n\tf(x)",
		"a struct: a closure overwrites the field":        "s := S{fn: noop}\n\tset := func() { s.fn = p.Store }\n\tset()\n\ts.fn(x)",
		"a struct: a closure overwrites it whole":         "s := S{fn: noop}\n\tset := func() { s = S{fn: p.Store} }\n\tset()\n\ts.fn(x)",
		"a struct: one closure overwrites, another calls": "s := S{fn: noop}\n\tset := func() { s.fn = p.Store }\n\tcall := func(y *T) { s.fn(y) }\n\tset()\n\tcall(x)",
		"a pointer: a closure points it at another":       "q := &S{fn: noop}\n\tset := func() { q = &S{fn: p.Store} }\n\tset()\n\tq.fn(x)",
	} {
		if got := cellWritesDiags(t, body); len(got) == 0 {
			t.Errorf("%s: the variable may hold the publisher when it is called, and the use of x after that was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"a closure overwrites the variable":               "f := noop\n\tset := func() { f = noop }\n\tset()\n\tf(x)",
		"a closure overwrites it and calls it":            "f := noop\n\tboth := func(y *T) {\n\t\tf = noop\n\t\tf(y)\n\t}\n\tboth(x)",
		"one closure overwrites it, another calls it":     "f := noop\n\tset := func() { f = noop }\n\tcall := func(y *T) { f(y) }\n\tset()\n\tcall(x)",
		"a closure overwrites another variable":           "f := noop\n\tg := noop\n\tset := func() { g = p.Store }\n\tset()\n\t_ = g\n\tf(x)",
		"a struct: a closure overwrites the field":        "s := S{fn: noop}\n\tset := func() { s.fn = noop }\n\tset()\n\ts.fn(x)",
		"a struct: another field's address is passed":     "s := S{fn: noop}\n\ts.mu.Lock()\n\ts.fn(x)\n\ts.mu.Unlock()",
		"a struct: one closure overwrites, another calls": "s := S{fn: noop}\n\tset := func() { s.fn = noop }\n\tcall := func(y *T) { s.fn(y) }\n\tset()\n\tcall(x)",
	} {
		if got := cellWritesDiags(t, body); len(got) != 0 {
			t.Errorf("%s: the variable never holds the publisher, and a publish of x was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
