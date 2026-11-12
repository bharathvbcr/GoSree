package errcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

// shadowSource returns a function with n sequential scopes that each shadow the outer err, which
// is read after every one of them.
func shadowSource(n int) string {
	var b strings.Builder
	b.WriteString("package p\n\nfunc fail() error { return nil }\n\nfunc use(error) {}\n\nfunc f(c bool) error {\n\terr := fail()\n\tif err != nil {\n\t\treturn err\n\t}\n")
	for range n {
		b.WriteString("\tif c {\n\t\terr := fail()\n\t\tuse(err)\n\t}\n")
	}
	b.WriteString("\treturn err\n}\n")
	return b.String()
}

func scanShadowSource(tb testing.TB, src string) (*funcASTInfo, *analysis.Pass, *ast.FuncDecl) {
	tb.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		tb.Fatal(err)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	pkg, err := (&types.Config{}).Check("p", fset, []*ast.File{file}, info)
	if err != nil {
		tb.Fatal(err)
	}
	pass := &analysis.Pass{Fset: fset, Pkg: pkg, TypesInfo: info}
	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
			decl = fd
		}
	}
	return scanFuncAST(pass, decl), pass, decl
}

// TestShadowScanWorkIsLinear bounds the number of AST nodes the shadow scan visits. A wall-clock
// bound would be noise on a loaded machine; node visits are deterministic.
func TestShadowScanWorkIsLinear(t *testing.T) {
	visits := map[int]int{}
	for _, n := range []int{100, 400, 1600} {
		info, _, _ := scanShadowSource(t, shadowSource(n))
		if len(info.shadows) != n {
			t.Fatalf("n=%d: found %d shadows, want %d", n, len(info.shadows), n)
		}
		visits[n] = info.shadowVisits
	}
	// Linear work grows 4x per step (allow 2x slack for constants); quadratic work grows 16x.
	for _, step := range [][2]int{{100, 400}, {400, 1600}} {
		lo, hi := visits[step[0]], visits[step[1]]
		if hi > 8*lo {
			t.Fatalf("shadow scan visits grew %dx for %dx input (%d -> %d nodes at n=%d -> %d): not linear",
				hi/max(lo, 1), step[1]/step[0], lo, hi, step[0], step[1])
		}
	}
	t.Log(fmt.Sprint(visits))
}

func BenchmarkShadowScan(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		src := shadowSource(n)
		_, pass, decl := scanShadowSource(b, src)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			for b.Loop() {
				scanFuncAST(pass, decl)
			}
		})
	}
}
