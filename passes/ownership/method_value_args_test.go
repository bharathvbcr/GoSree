package ownership_test

import (
	"strconv"
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
// Called through a method value, the method's call is the one in the wrapper made for the value,
// which hands on the wrapper's own parameters. The index reads the bodies of the functions in the
// source and of their closures (sourceFuncs) and did not read the wrappers', so the method had no
// call site in sight and the answer fell back on every store to that field of that struct type in
// the package: the publisher was found whichever argument held it. The wrappers' calls are read
// now, and the argument is the one that is passed, as at a direct call.
func TestMethodValueStructArguments(t *testing.T) {
	const head = "package p\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar p atomic.Pointer[T]\n\nfunc noop(*T) {}\n\ntype S struct{ fn func(*T) }\n\ntype R struct{}\n\n"
	const first = "func (r *R) call(a, b S, x *T) { a.fn(x) }\n"
	const second = "func (r *R) call(a, b S, x *T) { b.fn(x) }\n"
	const value = "\nfunc use(r *R, x *T) {\n\tg := r.call\n\tg("
	const direct = "\nfunc use(r *R, x *T) {\n\tr.call("
	const expr = "\nfunc use(r *R, x *T) {\n\tg := (*R).call\n\tg(r, "
	for name, c := range map[string]struct {
		method, call, args string
		want               bool
	}{
		"a method value, the first's field is called, the first publishes":   {first, value, "S{fn: p.Store}, S{fn: noop}", true},
		"a method value, the second's field is called, the second publishes": {second, value, "S{fn: noop}, S{fn: p.Store}", true},
		"called directly, the first's field is called, the first publishes":  {first, direct, "S{fn: p.Store}, S{fn: noop}", true},
		"called directly, the first's field is called, the second publishes": {first, direct, "S{fn: noop}, S{fn: p.Store}", false},
		"called directly, the second's field is called, the first publishes": {second, direct, "S{fn: p.Store}, S{fn: noop}", false},
		"a method value, the first's field is called, the second publishes":  {first, value, "S{fn: noop}, S{fn: p.Store}", false},
		"a method value, the second's field is called, the first publishes":  {second, value, "S{fn: p.Store}, S{fn: noop}", false},
		"a method expression, the first's field, the first publishes":        {first, expr, "S{fn: p.Store}, S{fn: noop}", true},
		"a method expression, the first's field, the second publishes":       {first, expr, "S{fn: noop}, S{fn: p.Store}", false},
		// The method has a direct call that passes no publisher, and that used to be all its
		// calls: the field was read as that argument's and nothing more.
		"called directly without and through a method value with a publisher": {first, direct + "S{fn: noop}, S{fn: noop}, x)\n\tg := r.call\n\tg(", "S{fn: p.Store}, S{fn: noop}", true},
		"called directly without and through a method expression with one":    {first, direct + "S{fn: noop}, S{fn: noop}, x)\n\tg := (*R).call\n\tg(r, ", "S{fn: p.Store}, S{fn: noop}", true},
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

// A method called through a method value (g := r.m) or a method expression (g := (*R).m) is the
// method, and what is known of the method holds at the call: that it publishes an argument, that
// it hands an argument back in its result. Both were looked up under the wrapper made for the
// value, which nothing is recorded for, so the call was one of a function nothing is known about:
// the argument was passed to something that may keep it, and the use after the publish was missed.
const methodValuePublishHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

type R struct{ n int }

var pr atomic.Pointer[R]

func (r *R) pub(x *T) { p.Store(x) }

func (r *R) quiet(x *T) { _ = x.n }

func (r *R) keep(x *T) *T { return x }

func (r *R) self() *R { return r }

func (r *R) fresh() *T { return &T{} }

var kept *T

func (r *R) stash(a, b *T) { kept = b }

type V struct{ n int }

func (v V) pub(x *T) { p.Store(x) }

type E struct{ R }

type G[X any] struct{ x X }

func (g *G[X]) pub(x *T) { p.Store(x) }

func (g *G[X]) quiet(x *T) { _ = x.n }
`

// methodValuePublishDiags gives the diagnostics in use that contain substr. The methods of the
// head that publish their own parameter are told so whatever use does, and are left out.
func methodValuePublishDiags(t *testing.T, body, substr string) []string {
	t.Helper()
	headLines := strings.Count(methodValuePublishHead, "\n")
	var out []string
	for _, d := range advDiagLines(t, methodValuePublishHead+"\nfunc use(r *R, c bool) int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", substr) {
		line, _, _ := strings.Cut(d, ":")
		if n, err := strconv.Atoi(line); err != nil || n > headLines {
			out = append(out, d)
		}
	}
	return out
}

func TestPublishThroughAMethodValueIsSeenAtTheCall(t *testing.T) {
	const after = "is used after it was published"
	for name, body := range map[string]string{
		"a method value":                          "g := r.pub\n\tg(x)",
		"a method expression":                     "g := (*R).pub\n\tg(r, x)",
		"a method value of a value receiver":      "var v V\n\tg := v.pub\n\tg(x)",
		"a method expression of a value receiver": "var v V\n\tg := V.pub\n\tg(v, x)",
		"a promoted method":                       "e := &E{}\n\tg := e.pub\n\tg(x)",
		"a method value of a generic type":        "gv := &G[int]{}\n\tg := gv.pub\n\tg(x)",
		"a method expression of a generic type":   "g := (*G[int]).pub\n\tg(&G[int]{}, x)",
		"one of two method values":                "g := r.quiet\n\tif c {\n\t\tg = r.pub\n\t}\n\tg(x)",
		"control: the method called by name":      "r.pub(x)",
		// The result is the argument, so publishing the result publishes x.
		"the result of a method value that hands its argument back": "g := r.keep\n\ty := g(x)\n\tp.Store(y)",
		"control: the same by name":                                 "y := r.keep(x)\n\tp.Store(y)",
	} {
		if got := methodValuePublishDiags(t, body, after); len(got) == 0 {
			t.Errorf("%s: x is published by the call, and the use after it was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"a method value that only reads":                   "g := r.quiet\n\tg(x)",
		"a method expression that only reads":              "g := (*R).quiet\n\tg(r, x)",
		"a method value of a generic type that only reads": "gv := &G[int]{}\n\tg := gv.quiet\n\tg(x)",
		"control: the method called by name":               "r.quiet(x)",
	} {
		if got := methodValuePublishDiags(t, body, "publish"); len(got) != 0 {
			t.Errorf("%s: nothing publishes x, and this was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

// What a method value returns is what the method returns. A result that is the receiver is not a
// fresh value, and one the method allocates is.
func TestResultOfAMethodValue(t *testing.T) {
	const notOwned = "is not owned here"
	for name, body := range map[string]string{
		"the receiver, through a method value":      "g := r.self\n\ty := g()\n\tpr.Store(y)",
		"the receiver, through a method expression": "g := (*R).self\n\ty := g(r)\n\tpr.Store(y)",
		"control: the receiver, by name":            "y := r.self()\n\tpr.Store(y)",
	} {
		if got := methodValuePublishDiags(t, body, notOwned); len(got) == 0 {
			t.Errorf("%s: the result is the caller's r, and publishing it was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"an allocation, through a method value":      "g := r.fresh\n\ty := g()\n\tp.Store(y)",
		"an allocation, through a method expression": "g := (*R).fresh\n\ty := g(r)\n\tp.Store(y)",
		"control: an allocation, by name":            "y := r.fresh()\n\tp.Store(y)",
		// The result is the receiver, and the receiver is this function's own allocation.
		"its own receiver, through a method expression": "rr := &R{}\n\tg := (*R).self\n\ty := g(rr)\n\tpr.Store(y)",
		"control: its own receiver, by name":            "rr := &R{}\n\ty := rr.self()\n\tpr.Store(y)",
	} {
		if got := methodValuePublishDiags(t, body, notOwned); len(got) != 0 {
			t.Errorf("%s: the result is fresh, and publishing it was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

// What a method keeps of its arguments it keeps when called through a value, and of the same
// arguments: stash stores its second in a package variable and does nothing with its first.
// Called through a value it used to be a function nothing is known of, which may keep both.
func TestMethodValueKeepsWhatItsMethodKeeps(t *testing.T) {
	const escapes = "but also escapes"
	for name, body := range map[string]string{
		"a method value, the argument it stores":      "y := &T{}\n\tg := r.stash\n\tg(x, y)\n\tp.Store(y)",
		"a method expression, the argument it stores": "y := &T{}\n\tg := (*R).stash\n\tg(r, x, y)\n\tp.Store(y)",
		"control: by name, the argument it stores":    "y := &T{}\n\tr.stash(x, y)\n\tp.Store(y)",
	} {
		if got := methodValuePublishDiags(t, body, escapes); len(got) == 0 {
			t.Errorf("%s: y is stored in a package variable by the call and then published, and that was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"a method value, the argument it leaves":      "y := &T{}\n\tg := r.stash\n\tg(y, x)\n\tp.Store(y)",
		"a method expression, the argument it leaves": "y := &T{}\n\tg := (*R).stash\n\tg(r, y, x)\n\tp.Store(y)",
		"control: by name, the argument it leaves":    "y := &T{}\n\tr.stash(y, x)\n\tp.Store(y)",
	} {
		if got := methodValuePublishDiags(t, body, escapes); len(got) != 0 {
			t.Errorf("%s: the method does nothing with y, and its publish was reported as an escape: %s", name, strings.Join(got, "; "))
		}
	}
}
