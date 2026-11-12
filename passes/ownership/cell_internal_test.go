package ownership

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

const cellHead = `package p

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type T struct{ n int }

func noop(*T) {}

func pub(*T) {}

type S struct {
	mu sync.Mutex
	fn func(*T)
}

var keep *func(*T)

var keepS *S

func set(q *func(*T)) { *q = pub }

func setS(q *S) { q.fn = pub }

func (s *S) arm() { s.fn = pub }

func set2(q *func(*T)) { set(q) }

func rec(q *func(*T), n int) {
	if n > 0 {
		rec(q, n-1)
	}
	*q = pub
}

func back(q *func(*T)) *func(*T) { return q }

func same(q *S) *S { return q }

func pick(p *S, h **S) *S { return *h }

func back2(q *func(*T)) (*func(*T), bool) { return q, true }

type PS *S

type P *P

var keepQ **S

type setter interface{ set(q *func(*T)) }

var other setter

var _ = fmt.Sprint

var held atomic.Pointer[func(*T)]
`

// calledValue is what the last call in fn calls that is not a function named where it is called.
func calledValue(t *testing.T, fn *ssa.Function) ssa.Value {
	t.Helper()
	var v ssa.Value
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if c, ok := ins.(*ssa.Call); ok && !c.Call.IsInvoke() && c.Call.StaticCallee() == nil {
				v = c.Call.Value
			}
		}
	}
	if v == nil {
		t.Fatalf("%s calls nothing through a value", fn.Name())
	}
	return v
}

// TestWhatACellMayHold asks the trace what a called local variable may be, for a variable that
// lives in memory because a closure captures it or its address is taken. The answer has two
// parts, and both are checked: the functions found, and whether they may not be all.
//
// The trace read the stores made through the variable in its own function and answered that
// those were all. A store made by a closure that captures the variable was not read, and a
// variable whose address had gone somewhere that could write it was still "all". The address is
// now followed into a function of this package it is passed to, and where it goes out of sight
// the answer says so.
func TestWhatACellMayHold(t *testing.T) {
	for name, c := range map[string]struct {
		body    string
		want    string
		mayMiss bool
	}{
		"only its own function writes it, a closure reads it": {"f := noop\n\tpeek := func() bool { return f != nil }\n\t_ = peek()\n\tf(x)", "noop", false},
		"a closure writes it":                                              {"f := noop\n\tw := func() { f = pub }\n\tw()\n\tf(x)", "noop pub", false},
		"a closure inside a closure writes it":                             {"f := noop\n\tw := func() {\n\t\tin := func() { f = pub }\n\t\tin()\n\t}\n\tw()\n\tf(x)", "noop pub", false},
		"two closures write it":                                            {"f := noop\n\tw := func() { f = pub }\n\tv := func() { f = noop }\n\tw()\n\tv()\n\tf(x)", "noop pub", false},
		"its address is passed to a function that writes it":               {"f := noop\n\tset(&f)\n\tf(x)", "noop pub", false},
		"its address is passed on twice":                                   {"f := noop\n\tset2(&f)\n\tf(x)", "noop pub", false},
		"its address is passed to a function that calls itself":            {"f := noop\n\trec(&f, 3)\n\tf(x)", "noop pub", false},
		"its address is passed to a function that returns it, and dropped": {"f := noop\n\t_ = back(&f)\n\tf(x)", "noop", false},
		"its address is passed to a function that returns it, and kept":    {"f := noop\n\tkeep = back(&f)\n\tf(x)", "noop", true},
		"its address comes back and is written through":                    {"f := noop\n\tr := back(&f)\n\t*r = pub\n\tf(x)", "noop pub", false},
		"its address comes back as one of two results":                     {"f := noop\n\tr, _ := back2(&f)\n\t*r = pub\n\tf(x)", "noop pub", false},
		"its address comes back from the second of two calls":              {"f := noop\n\t_ = back(&f)\n\tr := back(&f)\n\t*r = pub\n\tf(x)", "noop pub", false},
		"a closure that captures it hands its address to any caller":       {"f := noop\n\tw := func(q *func(*T)) *func(*T) { return &f }\n\t_ = w(&f)\n\tr := w(nil)\n\t*r = pub\n\tf(x)", "noop", true},
		// The closure is entered through r, a name read before the closure is, so when its return
		// is read there is a call to take it back to. It captures the variable all the same, and
		// the call that passes nothing gets the address too.
		"a closure that captures it, entered through another name first": {"f := noop\n\tr := back(&f)\n\tw := func(q *func(*T)) *func(*T) { return &f }\n\t_ = w(r)\n\tu := w(nil)\n\t*u = pub\n\tf(x)", "noop", true},
		"its address is passed to another package":                       {"f := noop\n\tfmt.Sscan(\"\", &f)\n\tf(x)", "noop", true},
		"its address is passed to a method of an interface":              {"f := noop\n\tother.set(&f)\n\tf(x)", "noop", true},
		"its address is passed to a function of another package":         {"f := noop\n\theld.Store(&f)\n\tf(x)", "noop", true},
		"its address is passed to a function held in a variable":         {"f := noop\n\tg := set\n\tif x == nil {\n\t\tg = set2\n\t}\n\tg(&f)\n\tf(x)", "noop", true},
		"its address is stored":                                          {"f := noop\n\tkeep = &f\n\tf(x)", "noop", true},
		"a closure passes its address to a function":                     {"f := noop\n\tw := func() { set(&f) }\n\tw()\n\tf(x)", "noop pub", false},
		"a struct: a closure writes the field":                           {"s := S{fn: noop}\n\tw := func() { s.fn = pub }\n\tw()\n\ts.fn(x)", "noop pub", false},
		"a struct: a closure writes it whole":                            {"s := S{fn: noop}\n\tw := func() { s = S{fn: pub} }\n\tw()\n\ts.fn(x)", "noop pub", false},
		"a struct: another field's address is passed on":                 {"s := S{fn: noop}\n\ts.mu.Lock()\n\ts.fn(x)\n\ts.mu.Unlock()", "noop", false},
		"a struct: a closure passes another field's address":             {"s := S{fn: noop}\n\tw := func() { s.mu.Lock() }\n\tw()\n\ts.fn(x)", "noop", false},
		"a struct: its address is passed to a function":                  {"s := S{fn: noop}\n\tsetS(&s)\n\ts.fn(x)", "noop pub", false},
		"a struct: a method with a pointer receiver writes it":           {"s := S{fn: noop}\n\ts.arm()\n\ts.fn(x)", "noop pub", false},
		"a struct made with &: passed to a function":                     {"q := &S{fn: noop}\n\tsetS(q)\n\tq.fn(x)", "noop pub", false},
		"a struct made with &: a method writes it":                       {"q := &S{fn: noop}\n\tq.arm()\n\tq.fn(x)", "noop pub", false},
		"a struct: its address is stored":                                {"s := S{fn: noop}\n\tkeepS = &s\n\ts.fn(x)", "noop", true},
		"a struct: the field's address is passed to a function":          {"s := S{fn: noop}\n\tset(&s.fn)\n\ts.fn(x)", "noop pub", false},
		"a struct: the field's address is stored":                        {"s := S{fn: noop}\n\tkeep = &s.fn\n\ts.fn(x)", "noop", true},
		// The variable holds a pointer: each load of it is one more name of the struct it points to.
		"a pointer: a closure points it at another struct":        {"q := &S{fn: noop}\n\tw := func() { q = &S{fn: pub} }\n\tw()\n\tq.fn(x)", "noop pub", false},
		"a pointer: a closure writes the field through it":        {"q := &S{fn: noop}\n\tw := func() { q.fn = pub }\n\tw()\n\tq.fn(x)", "noop pub", false},
		"a pointer: the field is written through a branch":        {"q := &S{fn: noop}\n\tu := &S{fn: noop}\n\tr := q\n\tif x == nil {\n\t\tr = u\n\t}\n\tr.fn = pub\n\tq.fn(x)", "noop pub", false},
		"a pointer: handed back by the function it is passed to":  {"q := same(&S{fn: noop})\n\tq.fn = pub\n\tq.fn(x)", "noop pub", false},
		"a pointer: converted to a named pointer type":            {"q := &S{fn: noop}\n\tr := PS(q)\n\tr.fn = pub\n\tq.fn(x)", "noop pub", false},
		"a pointer: compared with nil":                            {"q := &S{fn: noop}\n\tif q == nil {\n\t\treturn\n\t}\n\tq.fn(x)", "noop", false},
		"a pointer: kept in a package variable":                   {"q := &S{fn: noop}\n\tkeepS = q\n\tq.fn(x)", "noop", true},
		"a pointer: kept in a pointer variable that is handed on": {"q := &S{fn: noop}\n\tw := func() { q.fn = pub }\n\tw()\n\tkeepQ = &q\n\tq.fn(x)", "noop", true},
		// A function that reads the pointer variable can hand the struct to any call of it, not only
		// to the calls that passed the struct in.
		"a pointer: a function handed the variable returns what it holds": {"q := &S{fn: noop}\n\t_ = pick(q, &q)\n\tr := pick(nil, &q)\n\tr.fn = pub\n\tq.fn(x)", "noop", true},
		"a pointer: a closure that captures the variable returns it":      {"q := &S{fn: noop}\n\tget := func(p *S) *S { return q }\n\t_ = get(q)\n\tr := get(nil)\n\tr.fn = pub\n\tq.fn(x)", "noop", true},
	} {
		pkg := buildWalk(t, cellHead+"\nfunc use(x *T) {\n\t"+c.body+"\n}\n")
		fn := pkg.Func("use")
		st := &state{ssa: &buildssa.SSA{Pkg: pkg}, ix: &annot.Index{}}
		q := st.ask()
		leaves, mayMiss := q.origins(fn, calledValue(t, fn))
		var names []string
		for _, lf := range leaves {
			if f, ok := lf.v.(*ssa.Function); ok && !slices.Contains(names, f.Name()) {
				names = append(names, f.Name())
			}
		}
		sort.Strings(names)
		if got := strings.Join(names, " "); got != c.want {
			t.Errorf("%s: the variable may be [%s], the trace found [%s]", name, c.want, got)
		}
		if mayMiss != c.mayMiss {
			t.Errorf("%s: the trace says its answer may not be all: %v, want %v", name, mayMiss, c.mayMiss)
		}
		if q.cut {
			t.Errorf("%s: the trace was cut at a limit", name)
		}
	}
}

// TestUsesOfCell reads the uses of a cell directly: every store through the variable and through
// each closure that captures it is listed, and the cell is open exactly when an address of the
// whole of it goes somewhere that is not one of those. A struct made with a literal is stored
// whole once, and each s.fn is one address of that field.
func TestUsesOfCell(t *testing.T) {
	for name, c := range map[string]struct {
		body   string
		stores int
		fields int
		open   bool
	}{
		"written here and in a closure":                {"f := noop\n\tw := func() { f = pub }\n\tw()\n\tf(x)", 2, 0, false},
		"written in a closure inside a closure":        {"f := noop\n\tw := func() {\n\t\tin := func() { f = pub }\n\t\tin()\n\t}\n\tw()\n\tf(x)", 2, 0, false},
		"captured by two closures, written by one":     {"f := noop\n\tw := func() { f = pub }\n\tr := func() { f(x) }\n\tw()\n\tr()", 2, 0, false},
		"address passed to a function that writes it":  {"f := noop\n\tset(&f)\n\tf(x)", 2, 0, false},
		"address passed to a function that returns it": {"f := noop\n\tkeep = back(&f)\n\tf(x)", 1, 0, true},
		"address comes back and is written through":    {"f := noop\n\tr := back(&f)\n\t*r = pub\n\tf(x)", 2, 0, false},
		// type P *P: each of two variables holds the other's address. Reading one reads the other,
		// which comes back to the first, and the reading stops there with the variable open.
		"two variables that hold each other's address": {"var s, o P\n\ts = &o\n\to = &s\n\t_ = x", 1, 0, true},
		"address passed to a function by a closure":    {"f := noop\n\tw := func() { set(&f) }\n\tw()\n\tf(x)", 2, 0, false},
		"address stored": {"f := noop\n\tkeep = &f\n\tf(x)", 1, 0, true},
		"a struct, a field written here and in closure": {"s := S{fn: noop}\n\tw := func() { s.fn = pub }\n\tw()\n\ts.fn(x)", 1, 2, false},
		"a struct, another field's address passed on":   {"s := S{fn: noop}\n\ts.mu.Lock()\n\ts.fn(x)", 1, 2, false},
		"a struct, its address passed to a function":    {"s := S{fn: noop}\n\tsetS(&s)\n\ts.fn(x)", 1, 2, false},
	} {
		pkg := buildWalk(t, cellHead+"\nfunc use(x *T) {\n\t"+c.body+"\n}\n")
		var cell *ssa.Alloc
		for _, ins := range pkg.Func("use").Blocks[0].Instrs {
			if a, ok := ins.(*ssa.Alloc); ok && (a.Comment == "f" || a.Comment == "s") {
				cell = a
			}
		}
		if cell == nil {
			t.Fatalf("%s: no cell named f or s in use", name)
		}
		u := usesOfCell(cell)
		if len(u.stores) != c.stores || len(u.fields) != c.fields || u.open != c.open {
			t.Errorf("%s: got %d stores, %d field addresses, open %v; want %d, %d, %v", name, len(u.stores), len(u.fields), u.open, c.stores, c.fields, c.open)
		}
	}
}

// TestCellViewLimit: an address passed down a chain of functions is followed through each of
// them, up to cellViewLimit addresses. Past that the memory is taken as open, so the answer may
// not be all, and it says so. The store at the end of a chain shorter than the limit is found.
func TestCellViewLimit(t *testing.T) {
	chain := func(n int) string {
		var b strings.Builder
		b.WriteString("package p\n\ntype T struct{ n int }\n\nfunc noop(*T) {}\n\nfunc pub(*T) {}\n\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "func hop%d(q *func(*T)) { hop%d(q) }\n\n", i, i+1)
		}
		fmt.Fprintf(&b, "func hop%d(q *func(*T)) { *q = pub }\n\nfunc use(x *T) {\n\tf := noop\n\thop0(&f)\n\tf(x)\n}\n", n)
		return b.String()
	}
	for _, c := range []struct {
		hops    int
		pub     bool
		mayMiss bool
	}{
		{cellViewLimit - 8, true, false},
		{cellViewLimit + 8, false, true},
	} {
		pkg := buildWalk(t, chain(c.hops))
		fn := pkg.Func("use")
		st := &state{ssa: &buildssa.SSA{Pkg: pkg}, ix: &annot.Index{}}
		leaves, mayMiss := st.ask().origins(fn, calledValue(t, fn))
		found := slices.ContainsFunc(leaves, func(lf leafValue) bool {
			f, ok := lf.v.(*ssa.Function)
			return ok && f.Name() == "pub"
		})
		if found != c.pub || mayMiss != c.mayMiss {
			t.Errorf("an address passed down %d functions: the store at the end found %v, may not be all %v; want %v, %v", c.hops, found, mayMiss, c.pub, c.mayMiss)
		}
	}
}
