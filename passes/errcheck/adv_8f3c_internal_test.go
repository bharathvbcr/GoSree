package errcheck

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// chainFunc builds `func F(c []bool)` with n sequential `if c[i] { x++ }` and the given tail, and
// returns its SSA function. Each `if` is two blocks.
func chainFunc(t *testing.T, n int, tail string) *ssa.Function {
	t.Helper()
	var b strings.Builder
	b.WriteString("package p\n\nimport \"os\"\n\nvar _ = os.Exit\n\nfunc F(c []bool) int {\n\tx := 0\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\tif c[%d] {\n\t\tx++\n\t}\n", i)
	}
	b.WriteString(tail + "\n}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", b.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.ForCompiler(fset, "source", nil)}, fset, types.NewPackage("p", "p"), []*ast.File{file}, ssa.BuilderMode(0))
	if err != nil {
		t.Fatal(err)
	}
	return pkg.Func("F")
}

// ft-8f3c adversary: blocksWhereDefersRun at 20,000+ blocks. A tail that returns marks every block;
// a tail that calls os.Exit marks none. The allocations of a call do not grow with the function,
// which is the deterministic stand-in for a counter the production code does not carry: a pass
// that revisited blocks per edge would allocate or loop per visit.
func TestAdv8f3cBlocksWhereDefersRunScale(t *testing.T) {
	ret := chainFunc(t, 10500, "return x")
	exit := chainFunc(t, 10500, "os.Exit(1)\n\treturn x")
	if len(ret.Blocks) < 20000 {
		t.Fatalf("%d blocks, want at least 20,000", len(ret.Blocks))
	}
	for i, r := range blocksWhereDefersRun(ret) {
		if !r {
			t.Fatalf("returning tail: block %d is marked as never running defers", i)
		}
	}
	runs := blocksWhereDefersRun(exit)
	for i, b := range exit.Blocks {
		hasExit := false
		for _, ins := range b.Instrs {
			if c, ok := ins.(*ssa.Call); ok && noReturnKindOf(&c.Call) == callExitsProcess {
				hasExit = true
			}
		}
		if hasExit && runs[i] {
			t.Fatalf("exit tail: the block with os.Exit (index %d) is marked as running defers", i)
		}
	}
	small := chainFunc(t, 700, "return x")
	aSmall := testing.AllocsPerRun(5, func() { blocksWhereDefersRun(small) })
	aBig := testing.AllocsPerRun(5, func() { blocksWhereDefersRun(ret) })
	t.Logf("allocs per call: %.0f at %d blocks, %.0f at %d blocks", aSmall, len(small.Blocks), aBig, len(ret.Blocks))
	if aBig > aSmall+16 {
		t.Fatalf("allocations grow with the function: %.0f at %d blocks, %.0f at %d", aSmall, len(small.Blocks), aBig, len(ret.Blocks))
	}
}
