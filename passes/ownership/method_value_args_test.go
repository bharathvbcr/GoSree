package ownership_test

import (
	"strings"
	"testing"
)

// A method value (g := r.m) is a closure over a wrapper whose parameters are the method's without
// the receiver, so a call of g lines its arguments up with the wrapper's parameters as they
// stand. The trace took the argument one place to the left when it asked what a parameter of the
// wrapper may be: the second parameter was answered with the first argument, and the first with
// nothing. A method that hands back one of its arguments was so read as handing back the other.
const methodValueArgsHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type S struct{ fn func(*T) }

type R struct{}

func (r *R) first(a, b func(*T)) func(*T) { return a }

func (r *R) second(a, b func(*T)) func(*T) { return b }

func (r *R) third(a, b, c func(*T)) func(*T) { return c }

func (r *R) firstAt(a, b *S) *S { return a }

func (r *R) secondAt(a, b *S) *S { return b }

type G[X any] struct{ x X }

func (g *G[X]) first(a, b func(*T)) func(*T) { return a }

func (g *G[X]) second(a, b func(*T)) func(*T) { return b }
`

// methodValueArgsDiags gives the uses of x after it was published that are reported in use. The
// methods above are checked too, each on its own, and are not what is asked about here.
func methodValueArgsDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, methodValueArgsHead+"\nfunc use(r *R) int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", "is used after it was published")
}

func TestMethodValueArgumentsLineUpWithItsParameters(t *testing.T) {
	for name, body := range map[string]string{
		"a function, the first of two":            "g := r.first\n\th := g(p.Store, noop)\n\th(x)",
		"a function, the second of two":           "g := r.second\n\th := g(noop, p.Store)\n\th(x)",
		"a function, the third of three":          "g := r.third\n\th := g(noop, noop, p.Store)\n\th(x)",
		"a pointer, the first of two":             "g := r.firstAt\n\tq := g(&S{fn: p.Store}, &S{fn: noop})\n\tq.fn(x)",
		"a pointer, the second of two":            "g := r.secondAt\n\tq := g(&S{fn: noop}, &S{fn: p.Store})\n\tq.fn(x)",
		"the method called directly, the first":   "h := r.first(p.Store, noop)\n\th(x)",
		"the method called directly, the second":  "h := r.second(noop, p.Store)\n\th(x)",
		"the method called directly, a pointer":   "q := r.secondAt(&S{fn: noop}, &S{fn: p.Store})\n\tq.fn(x)",
		"a method expression, the second of two":  "g := (*R).second\n\th := g(r, noop, p.Store)\n\th(x)",
		"a method value called where it is taken": "h := (r.second)(noop, p.Store)\n\th(x)",
		"of a generic type, the first of two":     "gv := &G[int]{}\n\tg := gv.first\n\th := g(p.Store, noop)\n\th(x)",
		"of a generic type, the second of two":    "gv := &G[int]{}\n\tg := gv.second\n\th := g(noop, p.Store)\n\th(x)",
	} {
		if got := methodValueArgsDiags(t, body); len(got) == 0 {
			t.Errorf("%s: the argument the method hands back publishes x, and the use after it was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"a function, the first of two":           "g := r.first\n\th := g(noop, p.Store)\n\th(x)",
		"a function, the second of two":          "g := r.second\n\th := g(p.Store, noop)\n\th(x)",
		"a function, the third of three":         "g := r.third\n\th := g(p.Store, p.Store, noop)\n\th(x)",
		"a pointer, the first of two":            "g := r.firstAt\n\tq := g(&S{fn: noop}, &S{fn: p.Store})\n\tq.fn(x)",
		"a pointer, the second of two":           "g := r.secondAt\n\tq := g(&S{fn: p.Store}, &S{fn: noop})\n\tq.fn(x)",
		"the method called directly, the first":  "h := r.first(noop, p.Store)\n\th(x)",
		"the method called directly, the second": "h := r.second(p.Store, noop)\n\th(x)",
		"a method expression, the second of two": "g := (*R).second\n\th := g(r, p.Store, noop)\n\th(x)",
		"of a generic type, the first of two":    "gv := &G[int]{}\n\tg := gv.first\n\th := g(noop, p.Store)\n\th(x)",
		"of a generic type, the second of two":   "gv := &G[int]{}\n\tg := gv.second\n\th := g(p.Store, noop)\n\th(x)",
	} {
		if got := methodValueArgsDiags(t, body); len(got) != 0 {
			t.Errorf("%s: the argument the method hands back does not publish x, and a publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

// A method that calls a field of one of its struct parameters publishes its own parameter x when
// that field may be the publisher, and is told so where the call is (x is not its to publish).
// Which field that is comes from the arguments at the method's call sites.
//
// Called through a method value, the method has no call site in sight: the call is the wrapper's,
// and the index reads the bodies of the functions in the source and of their closures
// (sourceFuncs), not of the wrappers made for them. The answer then falls back on
// every store to that field of that struct type in the package, so the publisher is found whichever
// argument holds it. That errs toward reporting and is pinned here as what is known, beside the
// direct call, which reads the argument that is passed.
func TestMethodValueStructArguments(t *testing.T) {
	const head = "package p\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar p atomic.Pointer[T]\n\nfunc noop(*T) {}\n\ntype S struct{ fn func(*T) }\n\ntype R struct{}\n\n"
	const first = "func (r *R) call(a, b S, x *T) { a.fn(x) }\n"
	const second = "func (r *R) call(a, b S, x *T) { b.fn(x) }\n"
	const value = "\nfunc use(r *R, x *T) {\n\tg := r.call\n\tg("
	const direct = "\nfunc use(r *R, x *T) {\n\tr.call("
	for name, c := range map[string]struct {
		method, call, args string
		want               bool
	}{
		"a method value, the first's field is called, the first publishes":   {first, value, "S{fn: p.Store}, S{fn: noop}", true},
		"a method value, the second's field is called, the second publishes": {second, value, "S{fn: noop}, S{fn: p.Store}", true},
		"called directly, the first's field is called, the first publishes":  {first, direct, "S{fn: p.Store}, S{fn: noop}", true},
		"called directly, the first's field is called, the second publishes": {first, direct, "S{fn: noop}, S{fn: p.Store}", false},
		"called directly, the second's field is called, the first publishes": {second, direct, "S{fn: p.Store}, S{fn: noop}", false},
		"known: a method value, the first's field, the second publishes":     {first, value, "S{fn: noop}, S{fn: p.Store}", true},
		"known: a method value, the second's field, the first publishes":     {second, value, "S{fn: p.Store}, S{fn: noop}", true},
	} {
		got := advDiagLines(t, head+c.method+c.call+c.args+", x)\n}\n", "parameter x is published")
		if c.want && len(got) == 0 {
			t.Errorf("%s: a publish of the method's parameter x was not reported", name)
		}
		if !c.want && len(got) != 0 {
			t.Errorf("%s: the field the method calls does not publish, and a publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
