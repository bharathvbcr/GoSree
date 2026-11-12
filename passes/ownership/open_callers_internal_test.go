package ownership

import (
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// What a parameter of a function may be is all in sight only when every call of the function is.
// A method called through an interface has calls that name no function, so the calls listed for it
// are not all of them. An unexported method was read as if they were: with one direct call that
// passes reads, the function do calls was answered "reads, and that is all", while the interface
// call passes keeps. Since a call through a value whose targets are all known is given the
// targets' summaries (callOfKnown), that answer decides what is reported.
func TestParameterOfAMethodCalledThroughAnInterfaceIsNotAllInSight(t *testing.T) {
	const head = `package p

type T struct{ n int }

var kept *T

func reads(x *T) { _ = x.n }

func keeps(x *T) { kept = x }

type impl struct{}

func (impl) do(f func(*T), x *T) { f(x) }

func direct(x *T) { impl{}.do(reads, x) }
`
	for name, c := range map[string]struct {
		rest    string
		mayMiss bool
	}{
		"called by name only":                         {"", false},
		"also called through an interface":            {"\ntype doer interface{ do(f func(*T), x *T) }\n\nfunc via(d doer, x *T) { d.do(keeps, x) }\n", true},
		"another method of that name is called":       {"\ntype other struct{}\n\nfunc (other) do(f func(*T), x *T) {}\n\ntype doer interface{ do(f func(*T), x *T) }\n\nfunc via(d doer, x *T) { d.do(keeps, x) }\n", true},
		"a method value of the interface is called":   {"\ntype doer interface{ do(f func(*T), x *T) }\n\nfunc via(d doer, x *T) {\n\tg := d.do\n\tg(keeps, x)\n}\n", true},
		"called through an interface a struct embeds": {"\ntype doer interface{ do(f func(*T), x *T) }\n\ntype box struct{ doer }\n\nfunc via(b box, x *T) { b.do(keeps, x) }\n", true},
		"a method value through the embedding struct": {"\ntype doer interface{ do(f func(*T), x *T) }\n\ntype box struct{ doer }\n\nfunc via(b box, x *T) {\n\tg := b.do\n\tg(keeps, x)\n}\n", true},
	} {
		leaves, mayMiss := calledOrigins(t, head+c.rest, func(pkg *ssa.Package) *ssa.Function {
			return pkg.Prog.LookupMethod(pkg.Type("impl").Type(), pkg.Pkg, "do")
		})
		if leaves != 1 {
			t.Errorf("%s: the one call by name passes reads; the trace found %d functions", name, leaves)
		}
		if mayMiss != c.mayMiss {
			t.Errorf("%s: the trace says its answer may not be all: %v, want %v", name, mayMiss, c.mayMiss)
		}
	}
}

// What a package variable may hold is all in sight only when every store to it is. One whose
// address has been handed on (stored, returned, passed to a call) can be assigned through that
// address anywhere: var LstatP = &lstat in path/filepath lets its test package replace lstat.
// The stores by name were read as all of them.
func TestPackageVariableWhoseAddressIsHandedOnIsNotAllInSight(t *testing.T) {
	const head = `package p

type T struct{ n int }

var kept *T

func reads(x *T) { _ = x.n }

func keeps(x *T) { kept = x }

var hook = reads

func call(x *T) { hook(x) }

type conf struct{ hook func(*T) }

var cfg = conf{hook: reads}

func callField(x *T) { cfg.hook(x) }
`
	for name, c := range map[string]struct {
		rest    string
		fn      string
		leaves  int
		mayMiss bool
	}{
		"assigned where it is declared":                   {"", "call", 1, false},
		"assigned by name in another function":            {"\nfunc swap() { hook = keeps }\n", "call", 2, false},
		"its address is in an exported variable":          {"\nvar HookP = &hook\n", "call", 1, true},
		"its address is returned":                         {"\nfunc HookAddr() *func(*T) { return &hook }\n", "call", 1, true},
		"its address is handed to a call":                 {"\nfunc Hand(set func(*func(*T))) { set(&hook) }\n", "call", 1, true},
		"a field assigned where the struct is declared":   {"", "callField", 1, false},
		"the address of the field is in another variable": {"\nvar FieldP = &cfg.hook\n", "callField", 1, true},
		"the address of the struct is returned":           {"\nfunc CfgAddr() *conf { return &cfg }\n", "callField", 1, true},
	} {
		leaves, mayMiss := calledOrigins(t, head+c.rest, func(pkg *ssa.Package) *ssa.Function { return pkg.Func(c.fn) })
		if leaves != c.leaves {
			t.Errorf("%s: the trace found %d functions stored by name, want %d", name, leaves, c.leaves)
		}
		if mayMiss != c.mayMiss {
			t.Errorf("%s: the trace says its answer may not be all: %v, want %v", name, mayMiss, c.mayMiss)
		}
	}
}

// What is received from a channel is what is sent on it, when every send is in sight. A channel
// made here and handed to a call, captured by a closure or stored can be sent on where its uses
// here do not show; the sends among those uses were read as all of them.
func TestChannelHandedOnIsNotAllInSight(t *testing.T) {
	const head = `package p

type T struct{ n int }

var kept *T

func reads(x *T) { _ = x.n }

func keeps(x *T) { kept = x }

func fill(ch chan func(*T)) { ch <- keeps }

var chans []chan func(*T)
`
	for name, c := range map[string]struct {
		handOn  string
		leaves  int
		mayMiss bool
	}{
		"sent here and received here":         {"", 1, false},
		"sent here twice, once in a select":   {"select {\n\tcase ch <- keeps:\n\tdefault:\n\t}", 2, false},
		"its length is read and it is closed": {"_ = len(ch)\n\tdefer close(ch)", 1, false},
		"handed to a function":                {"fill(ch)", 1, true},
		"captured by a closure":               {"func() { ch <- keeps }()", 0, true},
		"stored":                              {"chans = append(chans, ch)", 1, true},
		"sent on another channel":             {"chch := make(chan chan func(*T), 1)\n\tchch <- ch", 1, true},
		"sent on another channel in a select": {"chch := make(chan chan func(*T), 1)\n\tselect {\n\tcase chch <- ch:\n\tdefault:\n\t}", 1, true},
	} {
		src := head + "\nfunc call(x *T) {\n\tch := make(chan func(*T), 2)\n\tch <- reads\n\t" + c.handOn + "\n\tf := <-ch\n\tf(x)\n}\n"
		leaves, mayMiss := calledOrigins(t, src, func(pkg *ssa.Package) *ssa.Function { return pkg.Func("call") })
		if leaves != c.leaves {
			t.Errorf("%s: the trace found %d functions sent in sight, want %d", name, leaves, c.leaves)
		}
		if mayMiss != c.mayMiss {
			t.Errorf("%s: the trace says its answer may not be all: %v, want %v", name, mayMiss, c.mayMiss)
		}
	}
}

// calledOrigins builds src, indexes every function of the package with the one pick returns, and
// asks the trace what the value that function calls may be: how many it found, and whether it
// says there may be more.
func calledOrigins(t *testing.T, src string, pick func(*ssa.Package) *ssa.Function) (leaves int, mayMiss bool) {
	t.Helper()
	pkg := buildWalk(t, src)
	fn := pick(pkg)
	if fn == nil {
		t.Fatal("the function to ask about is not in the package")
	}
	fns := []*ssa.Function{fn}
	for _, m := range pkg.Members {
		if f, ok := m.(*ssa.Function); ok && f != fn && f.Name() != "init" {
			fns = append(fns, f)
		}
	}
	st := &state{
		pass:       &analysis.Pass{Pkg: pkg.Pkg, Fset: pkg.Prog.Fset},
		ssa:        &buildssa.SSA{Pkg: pkg, SrcFuncs: fns},
		ix:         &annot.Index{},
		sums:       map[*ssa.Function]*summary{},
		sumReaders: map[*ssa.Function]map[*ssa.Function]bool{},
	}
	st.contracts = st.buildContractIndex(fns)
	found, mayMiss := st.ask().origins(fn, calledValue(t, fn))
	return len(found), mayMiss
}
