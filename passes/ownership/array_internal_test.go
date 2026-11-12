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

// TestWhatAnElementMayHold asks the trace what a called element of a local array may be.
//
// The step read the stores made through the array's own element addresses in its own function.
// An array built as a value (arr := [2]func(*T){noop, pub}) has none: the literal is built in
// memory of its own and stored whole, so the element was "nothing found". A closure that writes
// an element writes through its own name for the array, and an element read out of an array value
// (a range over the array) was not a step at all. The array is read like any other local memory
// now (usesOfCell): through every name it has, with what is stored to it whole read element by
// element. Which element is read is not followed, so the answer may never be all.
func TestWhatAnElementMayHold(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		want string
	}{
		"built as a value":                         {"arr := [2]func(*T){noop, pub}\n\tarr[1](x)", "noop pub"},
		"built as a value, read through a pointer": {"arr := [2]func(*T){noop, pub}\n\tq := &arr\n\tq[1](x)", "noop pub"},
		"an element assigned":                      {"var arr [2]func(*T)\n\tarr[0] = pub\n\tarr[0](x)", "pub"},
		"a closure writes an element":              {"var arr [2]func(*T)\n\tw := func() { arr[0] = pub }\n\tw()\n\tarr[0](x)", "pub"},
		"a closure reads an element":               {"arr := [2]func(*T){noop, pub}\n\tr := func() { arr[1](x) }\n\tr()\n\tarr[0](x)", "noop pub"},
		"ranged over":                              {"arr := [2]func(*T){noop, pub}\n\tfor _, f := range arr {\n\t\tf(x)\n\t}", "noop pub"},
		"copied from another array":                {"a := [2]func(*T){noop, pub}\n\tb := a\n\tb[0](x)", "noop pub"},
		"an element's address is passed to a function that writes it": {"var arr [2]func(*T)\n\tset(&arr[0])\n\tarr[0](x)", "pub"},
		"a slice of it is written":                                    {"var arr [2]func(*T)\n\ts := arr[:]\n\ts[0] = pub\n\tarr[0](x)", "pub"},
		"a slice literal":                                             {"fs := []func(*T){noop, pub}\n\tfs[1](x)", "noop pub"},
		// Each array is stored whole into the other: the walk comes round, and has to end there
		// and not at the depth limit.
		"two arrays assigned to each other": {"a := [2]func(*T){noop, noop}\n\tb := [2]func(*T){pub, pub}\n\tfor i := 0; i < 2; i++ {\n\t\ta, b = b, a\n\t}\n\ta[0](x)", "noop pub"},
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
			t.Errorf("%s: the element may be [%s], the trace found [%s]", name, c.want, got)
		}
		if !mayMiss {
			t.Errorf("%s: the trace says its answer is all; which element is read is not followed, so it never is", name)
		}
		if q.cut {
			t.Errorf("%s: the trace was cut at a limit", name)
		}
	}
}
