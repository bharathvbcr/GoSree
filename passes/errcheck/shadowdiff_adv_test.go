package errcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

type shadowGen struct {
	r      *rand.Rand
	b      strings.Builder
	depth  int
	named  bool
	labels int
}

func (g *shadowGen) w(indent int, s string) {
	g.b.WriteString(strings.Repeat("\t", indent))
	g.b.WriteString(s)
	g.b.WriteString("\n")
}

func (g *shadowGen) pick(opts ...string) string { return opts[g.r.Intn(len(opts))] }

func (g *shadowGen) stmts(ind, n int) {
	for range n {
		g.stmt(ind)
	}
}

func (g *shadowGen) stmt(ind int) {
	k := g.r.Intn(26)
	if g.depth > 3 && k >= 14 {
		k = g.r.Intn(14)
	}
	switch k {
	case 0, 1:
		g.w(ind, "err := fail()")
		g.w(ind, "_ = err")
	case 2:
		g.w(ind, "err = fail()")
	case 3:
		g.w(ind, "x, err := two()")
		g.w(ind, "_ = x")
	case 4:
		g.w(ind, "var err = fail()")
		g.w(ind, "_ = err")
	case 5:
		g.w(ind, "var err error")
		g.w(ind, "_ = err")
	case 6:
		g.w(ind, "use(err)")
	case 7:
		g.w(ind, "return err")
	case 8:
		g.w(ind, "return")
	case 9:
		g.w(ind, "return nil")
	case 10:
		g.w(ind, "err = wrap(err)")
	case 11:
		g.w(ind, g.pick("e2 := fail()", "e2, err := fail(), fail()", "err, e2 := fail(), fail()"))
		g.w(ind, "_ = e2")
	case 12:
		g.w(ind, "if err != nil {")
		g.w(ind+1, "return err")
		g.w(ind, "}")
	case 13:
		g.w(ind, "if err != nil { use(err) }")
	case 14, 15:
		g.depth++
		g.w(ind, g.pick("if c {", "if err := fail(); err != nil {", "if err := fail(); c {", "if x, err := two(); x > 0 {", "if err = fail(); c {"))
		g.stmts(ind+1, 1+g.r.Intn(3))
		if g.r.Intn(3) == 0 {
			g.w(ind, "} else if err := fail(); c {")
			g.stmts(ind+1, 1+g.r.Intn(2))
			g.w(ind, "} else {")
			g.stmts(ind+1, 1+g.r.Intn(2))
		} else if g.r.Intn(3) == 0 {
			g.w(ind, "} else {")
			g.stmts(ind+1, 1+g.r.Intn(2))
		}
		g.w(ind, "}")
		g.depth--
	case 16:
		g.depth++
		g.w(ind, g.pick("for c {", "for err := fail(); c; {", "for i := 0; i < 2; i++ {", "for range 2 {"))
		g.stmts(ind+1, 1+g.r.Intn(3))
		g.w(ind, "}")
		g.depth--
	case 17:
		g.depth++
		g.w(ind, g.pick("switch {", "switch err := fail(); {", "switch err := fail(); err {"))
		g.w(ind, "case c:")
		g.stmts(ind+1, 1+g.r.Intn(3))
		g.w(ind, "default:")
		g.stmts(ind+1, g.r.Intn(3))
		g.w(ind, "}")
		g.depth--
	case 18:
		g.depth++
		g.w(ind, "select {")
		g.w(ind, "case <-ch:")
		g.stmts(ind+1, 1+g.r.Intn(3))
		g.w(ind, "default:")
		g.stmts(ind+1, g.r.Intn(2))
		g.w(ind, "}")
		g.depth--
	case 19:
		g.depth++
		g.w(ind, "func() {")
		g.stmts(ind+1, 1+g.r.Intn(3))
		g.w(ind, "}()")
		g.depth--
	case 20:
		g.depth++
		g.w(ind, "{")
		g.stmts(ind+1, 1+g.r.Intn(3))
		g.w(ind, "}")
		g.depth--
	case 21:
		g.depth++
		g.w(ind, "switch v := any(nil).(type) {")
		g.w(ind, "case int:")
		g.w(ind+1, "_ = v")
		g.stmts(ind+1, 1+g.r.Intn(2))
		g.w(ind, "}")
		g.depth--
	case 22:
		g.labels++
		l := fmt.Sprintf("L%d", g.labels)
		g.depth++
		g.w(ind, l+":")
		g.w(ind, "for c {")
		g.stmts(ind+1, 1+g.r.Intn(2))
		g.w(ind+1, "break "+l)
		g.w(ind, "}")
		g.depth--
	case 23:
		g.w(ind, "defer func() { use(err) }()")
	case 24:
		g.w(ind, "go func() { err := fail(); _ = err }()")
	case 25:
		g.w(ind, "fail()")
	}
}

func genShadowSrc(seed int64) string {
	g := &shadowGen{r: rand.New(rand.NewSource(seed))}
	g.b.WriteString("package p\n\nfunc fail() error { return nil }\nfunc two() (int, error) { return 0, nil }\nfunc wrap(e error) error { return e }\nfunc use(error) {}\n\n")
	g.named = g.r.Intn(2) == 0
	if g.named {
		g.b.WriteString("func f(c bool, ch chan int) (err error) {\n")
	} else {
		g.b.WriteString("func f(c bool, ch chan int) error {\n")
		if g.r.Intn(2) == 0 {
			g.b.WriteString("\terr := fail()\n")
		} else {
			g.b.WriteString("\tvar err error\n")
		}
	}
	g.stmts(1, 2+g.r.Intn(8))
	if g.named {
		g.b.WriteString("\treturn\n")
	} else {
		g.b.WriteString("\treturn err\n")
	}
	g.b.WriteString("}\n")
	return g.b.String()
}

func typecheckForDiff(src string) (*analysis.Pass, *ast.FuncDecl, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		return nil, nil, err
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	// Type errors such as unused variables leave Defs/Uses populated; only a missing decl is fatal.
	pkg, _ := (&types.Config{Error: func(error) {}}).Check("p", fset, []*ast.File{file}, info)
	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
			decl = fd
		}
	}
	if decl == nil {
		return nil, nil, fmt.Errorf("no f")
	}
	return &analysis.Pass{Fset: fset, Pkg: pkg, TypesInfo: info}, decl, nil
}

// TestShadowScanMatchesOldAlgorithm holds the linear shadow scan to the pre-rewrite results over
// generated functions.
func TestShadowScanMatchesOldAlgorithm(t *testing.T) {
	checked, withShadow := 0, 0
	for seed := int64(0); seed < 20000; seed++ {
		src := genShadowSrc(seed)
		pass, decl, err := typecheckForDiff(src)
		if err != nil {
			continue // generator produced an ill-typed program (unused var, missing return...)
		}
		checked++
		oldInfo := &funcASTInfo{}
		old_scanShadowedErrors(pass, decl.Type, decl.Body, oldInfo)
		newInfo := &funcASTInfo{}
		scanShadowedErrors(pass, decl.Type, decl.Body, newInfo)
		if len(oldInfo.shadows) > 0 {
			withShadow++
		}
		if !reflect.DeepEqual(oldInfo.shadows, newInfo.shadows) {
			t.Errorf("seed %d: old %v new %v\n%s", seed, oldInfo.shadows, newInfo.shadows, src)
			if t.Failed() && seed > 0 {
				return
			}
		}
	}
	t.Logf("checked %d well-typed functions, %d with shadows", checked, withShadow)
	if checked < 5000 || withShadow < 100 {
		t.Fatalf("generator too weak: checked=%d withShadow=%d", checked, withShadow)
	}
}
