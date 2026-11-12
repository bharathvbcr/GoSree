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
		"called by name only":                   {"", false},
		"also called through an interface":      {"\ntype doer interface{ do(f func(*T), x *T) }\n\nfunc via(d doer, x *T) { d.do(keeps, x) }\n", true},
		"another method of that name is called": {"\ntype other struct{}\n\nfunc (other) do(f func(*T), x *T) {}\n\ntype doer interface{ do(f func(*T), x *T) }\n\nfunc via(d doer, x *T) { d.do(keeps, x) }\n", true},
	} {
		pkg := buildWalk(t, head+c.rest)
		var fns []*ssa.Function
		for _, m := range pkg.Members {
			if f, ok := m.(*ssa.Function); ok {
				fns = append(fns, f)
			}
		}
		do := pkg.Prog.LookupMethod(pkg.Type("impl").Type(), pkg.Pkg, "do")
		if do == nil {
			t.Fatalf("%s: no method do", name)
		}
		fns = append(fns, do)
		st := &state{
			pass:       &analysis.Pass{Pkg: pkg.Pkg, Fset: pkg.Prog.Fset},
			ssa:        &buildssa.SSA{Pkg: pkg, SrcFuncs: fns},
			ix:         &annot.Index{},
			sums:       map[*ssa.Function]*summary{},
			sumReaders: map[*ssa.Function]map[*ssa.Function]bool{},
		}
		st.contracts = st.buildContractIndex(fns)
		leaves, mayMiss := st.ask().origins(do, calledValue(t, do))
		if len(leaves) != 1 {
			t.Errorf("%s: the one call by name passes reads; the trace found %d functions", name, len(leaves))
		}
		if mayMiss != c.mayMiss {
			t.Errorf("%s: the trace says its answer may not be all: %v, want %v", name, mayMiss, c.mayMiss)
		}
	}
}
