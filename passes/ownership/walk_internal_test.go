package ownership

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// The walks tested here are reached through checks whose outcome depends on much else (whether a
// package variable counts as written only during initialisation), so they are called directly on
// the SSA of a small program.

// deepTypes declares struct types n0 .. n<depth-1>, each holding the next in a field named in, and
// the last holding an int x.
func deepTypes(depth int) string {
	var b strings.Builder
	for i := range depth - 1 {
		fmt.Fprintf(&b, "type n%d struct{ in n%d }\n\n", i, i+1)
	}
	fmt.Fprintf(&b, "type n%d struct{ x int }\n\n", depth-1)
	return b.String()
}

func buildWalk(t *testing.T, src string) *ssa.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset, types.NewPackage("p", "p"), []*ast.File{file}, ssa.BuilderMode(0))
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// result is the value the one return statement of fn returns.
func result(t *testing.T, fn *ssa.Function) ssa.Value {
	t.Helper()
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
				return ret.Results[0]
			}
		}
	}
	t.Fatalf("%s has no single-result return", fn.Name())
	return nil
}

// TestParamWalksRunToTheEnd: the address of a field 40 structs inside a parameter is still memory
// reached from that parameter. Both walks that answer that used to give up after 16 steps.
func TestParamWalksRunToTheEnd(t *testing.T) {
	pkg := buildWalk(t, "package p\n\n"+deepTypes(40)+"func deep(k int, p *n0) *int {\n\treturn &p"+strings.Repeat(".in", 39)+".x\n}\n")
	fn := pkg.Func("deep")
	v := result(t, fn)
	if got := paramRootIndex(v, fn); got != 1 {
		t.Errorf("paramRootIndex of a field 40 structs inside parameter 1: got %d, want 1", got)
	}
	if !rootsAtParam(v, fn) {
		t.Error("rootsAtParam of a field 40 structs inside a parameter: got false, want true")
	}
}

// TestDirectInitOnlyGlobalRunsToTheEnd: the address of a field 40 structs inside a package variable
// that nothing writes is a projection of that variable. The walk used to give up after 32 steps.
func TestDirectInitOnlyGlobalRunsToTheEnd(t *testing.T) {
	pkg := buildWalk(t, "package p\n\n"+deepTypes(40)+"var table n0\n\nfunc far() *int {\n\treturn &table"+strings.Repeat(".in", 39)+".x\n}\n\nfunc near() *n1 {\n\treturn &table.in\n}\n")
	// This state has no alias graphs, and the walk is what is under test, so the set of escaped
	// package variables is given as already worked out, and empty.
	st := &state{ssa: &buildssa.SSA{Pkg: pkg}, ix: &annot.Index{}, escapedGlobals: map[*ssa.Global]bool{}}
	for _, name := range []string{"near", "far"} {
		if !st.directInitOnlyGlobal(result(t, pkg.Func(name))) {
			t.Errorf("%s: directInitOnlyGlobal of a field of a package variable nothing writes: got false, want true", name)
		}
	}
}

// TestGlobalRootBudgetIsReported: with the walk budget at 8, the walk from a store 10 pointers away
// from a package variable stops before it reaches the variable. It used to stop without a word (at
// 64 values) and the store read as one to memory that is no package variable. It now reports where
// it stopped, and the write it could not attribute is not claimed either way. A hatch on the line
// mutes that report like any other finding there.
func TestGlobalRootBudgetIsReported(t *testing.T) {
	lower(t, &globalWalkBudget, 8)
	analyse(t, `package caps

type node struct {
	next *node
	n    int
}

var root = &node{}

func bump() {
	root`+strings.Repeat(".next", 10)+`.n = 1 // want "SAFE006: whether the value used here comes from a package variable could not be followed to the end .more than 8 values lead to it."
}

func hatched() {
	//gosree:unsafe the list is built before any goroutine starts
	root`+strings.Repeat(".next", 10)+`.n = 2
}
`)
}
