package ownership

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

const cellHead = `package p

import "sync"

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
// variable whose address had gone somewhere that could write it was still "all".
func TestWhatACellMayHold(t *testing.T) {
	for name, c := range map[string]struct {
		body    string
		want    string
		mayMiss bool
	}{
		"only its own function writes it, a closure reads it": {"f := noop\n\tpeek := func() bool { return f != nil }\n\t_ = peek()\n\tf(x)", "noop", false},
		"a closure writes it":                                {"f := noop\n\tw := func() { f = pub }\n\tw()\n\tf(x)", "noop pub", false},
		"a closure inside a closure writes it":               {"f := noop\n\tw := func() {\n\t\tin := func() { f = pub }\n\t\tin()\n\t}\n\tw()\n\tf(x)", "noop pub", false},
		"two closures write it":                              {"f := noop\n\tw := func() { f = pub }\n\tv := func() { f = noop }\n\tw()\n\tv()\n\tf(x)", "noop pub", false},
		"its address is passed to a call":                    {"f := noop\n\tset(&f)\n\tf(x)", "noop", true},
		"its address is stored":                              {"f := noop\n\tkeep = &f\n\tf(x)", "noop", true},
		"a closure passes its address to a call":             {"f := noop\n\tw := func() { set(&f) }\n\tw()\n\tf(x)", "noop", true},
		"a struct: a closure writes the field":               {"s := S{fn: noop}\n\tw := func() { s.fn = pub }\n\tw()\n\ts.fn(x)", "noop pub", false},
		"a struct: a closure writes it whole":                {"s := S{fn: noop}\n\tw := func() { s = S{fn: pub} }\n\tw()\n\ts.fn(x)", "noop pub", false},
		"a struct: another field's address is passed on":     {"s := S{fn: noop}\n\ts.mu.Lock()\n\ts.fn(x)\n\ts.mu.Unlock()", "noop", false},
		"a struct: a closure passes another field's address": {"s := S{fn: noop}\n\tw := func() { s.mu.Lock() }\n\tw()\n\ts.fn(x)", "noop", false},
		"a struct: its address is passed to a call":          {"s := S{fn: noop}\n\tsetS(&s)\n\ts.fn(x)", "noop", true},
		"a struct: its address is stored":                    {"s := S{fn: noop}\n\tkeepS = &s\n\ts.fn(x)", "noop", true},
		"a struct: the field's address is passed to a call":  {"s := S{fn: noop}\n\tset(&s.fn)\n\ts.fn(x)", "noop", true},
		"a struct: the field's address is stored":            {"s := S{fn: noop}\n\tkeep = &s.fn\n\ts.fn(x)", "noop", true},
		// The variable holds a pointer. Both structs it may point to are found. The address of each
		// is stored in the variable, and what is written through a load of the variable is not read
		// as a write to the struct (the last case: pub is not found), so the answer may not be all.
		"a pointer: a closure points it at another struct":            {"q := &S{fn: noop}\n\tw := func() { q = &S{fn: pub} }\n\tw()\n\tq.fn(x)", "noop pub", true},
		"known: a pointer, the field is written through the variable": {"q := &S{fn: noop}\n\tw := func() { q.fn = pub }\n\tw()\n\tq.fn(x)", "noop", true},
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
		"written here and in a closure":                 {"f := noop\n\tw := func() { f = pub }\n\tw()\n\tf(x)", 2, 0, false},
		"written in a closure inside a closure":         {"f := noop\n\tw := func() {\n\t\tin := func() { f = pub }\n\t\tin()\n\t}\n\tw()\n\tf(x)", 2, 0, false},
		"captured by two closures, written by one":      {"f := noop\n\tw := func() { f = pub }\n\tr := func() { f(x) }\n\tw()\n\tr()", 2, 0, false},
		"address passed to a call":                      {"f := noop\n\tset(&f)\n\tf(x)", 1, 0, true},
		"address passed to a call by a closure":         {"f := noop\n\tw := func() { set(&f) }\n\tw()\n\tf(x)", 1, 0, true},
		"address stored":                                {"f := noop\n\tkeep = &f\n\tf(x)", 1, 0, true},
		"a struct, a field written here and in closure": {"s := S{fn: noop}\n\tw := func() { s.fn = pub }\n\tw()\n\ts.fn(x)", 1, 2, false},
		"a struct, another field's address passed on":   {"s := S{fn: noop}\n\ts.mu.Lock()\n\ts.fn(x)", 1, 2, false},
		"a struct, its address passed to a call":        {"s := S{fn: noop}\n\tsetS(&s)\n\ts.fn(x)", 1, 1, true},
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
