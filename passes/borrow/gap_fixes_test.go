package borrow_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/passes/borrow"
)

func loadBorrowTempModule(t *testing.T, files map[string]string) []*packages.Package {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gomod := fmt.Sprintf("module testmod\n\ngo 1.27\n\nrequire github.com/bharathvbcr/gosree v0.0.0\nreplace github.com/bharathvbcr/gosree => %s\n", repoRoot)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  dir,
		Env: append(os.Environ(),
			"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Fatalf("%s: %v", p.PkgPath, e)
		}
	}
	return pkgs
}

func TestDanglingReturnNoPosAndDedup(t *testing.T) {
	src := `package p

type Job struct{ ID int }

func NamedDefer() (out *Job) {
	defer func() {}()
	out = &Job{}
	return out
}

func DanglingRecoverOnly() (out *int) {
	x := 42
	defer func() { _ = recover() }()
	out = &x
	panic("boom")
}
`
	pkgs := loadBorrowTempModule(t, map[string]string{"p/p.go": src})
	g, err := checker.Analyze([]*analysis.Analyzer{borrow.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var lines []int
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			if !d.Pos.IsValid() {
				t.Fatalf("diagnostic emitted with token.NoPos: %s", d.Message)
			}
			posn := act.Package.Fset.Position(d.Pos)
			if posn.Line <= 0 {
				t.Fatalf("diagnostic emitted with invalid line %d: %s", posn.Line, d.Message)
			}
			if strings.HasPrefix(d.Message, "BORROW004:") {
				lines = append(lines, posn.Line)
			}
		}
	}
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 positioned BORROW004 diagnostics (NamedDefer + DanglingRecoverOnly), got lines %v", lines)
	}
}

func TestPackageLevelFuncLitBorrows(t *testing.T) {
	src := `package p

import "github.com/bharathvbcr/gosree/cap"

var global *int

var lit = func(b *int /*gosree:mub*/) {
	global = b
}

var litTyped = func(b cap.Mut[int]) {
	global = b
}

var litConflict = func() {
	x := 0
	b1 := &x //gosree:mut
	b2 := &x //gosree:mut
	*b2 = 1
	*b1 = 2
}
`
	pkgs := loadBorrowTempModule(t, map[string]string{"p/p.go": src})
	g, err := checker.Analyze([]*analysis.Analyzer{borrow.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	byLine := map[int][]string{}
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			ln := act.Package.Fset.Position(d.Pos).Line
			byLine[ln] = append(byLine[ln], d.Message)
		}
	}
	if len(byLine[8]) == 0 || !strings.HasPrefix(byLine[8][0], "BORROW001:") {
		t.Errorf("expected BORROW001 on line 8 (lit), got %v", byLine)
	}
	if len(byLine[12]) == 0 || !strings.HasPrefix(byLine[12][0], "BORROW001:") {
		t.Errorf("expected BORROW001 on line 12 (litTyped), got %v", byLine)
	}
	if len(byLine[18]) == 0 || !strings.HasPrefix(byLine[18][0], "BORROW002:") {
		t.Errorf("expected BORROW002 on line 18 (litConflict), got %v", byLine)
	}
}

func TestNLLBackEdgeAndStoreLaunder(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

func loopBackEdgeSameBlock(n int) {
	x := 0
	b1 := &x //gosree:mut
	for i := 0; i < n; i++ {
		*b1 = i
		b2 := &x //gosree:mut // want "BORROW002"
		*b2 = i
	}
}

func gotoLoop(n int) {
	x := 0
	b1 := &x //gosree:mut
	i := 0
again:
	*b1 = i
	b2 := &x //gosree:mut // want "BORROW002"
	*b2 = i
	i++
	if i < n {
		goto again
	}
}

func storeLaunder() {
	x := 0
	b1 := &x //gosree:mut
	var c *int
	pc := &c
	*pc = b1 // want "BORROW001"
	b2 := &x //gosree:mut // want "BORROW002"
	*b2 = 1
	*c = 2
}

func sequentialInLoop(n int) {
	x := 0
	for i := 0; i < n; i++ {
		b1 := &x //gosree:mut
		*b1 = i
		b2 := &x //gosree:mut
		*b2 = i
	}
}

func sequentialGotoLoop(n int) {
	x := 0
	i := 0
again:
	b1 := &x //gosree:mut
	*b1 = i
	b2 := &x //gosree:mut
	*b2 = i
	i++
	if i < n {
		goto again
	}
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, borrow.Analyzer, "p")
}

func TestRecursiveTypeStorageNoStackOverflow(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

type SliceRec []SliceRec

type Tree struct {
	Kids []Tree
}

func useRec() {
	var s SliceRec
	var tr Tree
	f := func() {
		_ = s
		_ = tr
	}
	f()
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, borrow.Analyzer, "p")
}

func TestNLLSameLineSameBlockAndBackwardGoto(t *testing.T) {
	pkgs := loadBorrowTempModule(t, map[string]string{
		"p/p.go": `package p

import "github.com/bharathvbcr/gosree/cap"

func sameLineLoopConflict(n int) {
	x := 0; var b1 cap.Mut[int] = &x; for i := 0; i < n; i++ { *b1 = i; var b2 cap.Mut[int] = &x; *b2 = i }
}

func sameLineLoopOwnerWrite(n int) int {
	x := 0; var b cap.Ref[int] = &x; s := 0; for i := 0; i < n; i++ { s += *b; x = i }; return s
}

func sameBlockLoopCarriedReadAfterWrite(n int) {
	x := 0
	c := &x
	for i := 0; i < n; i++ {
		x = i
		println(*c)
		b := cap.Borrow(&x)
		c = b
	}
}

func sameBlockLoopCleanReborrow(n int) {
	x := 0
	for i := 0; i < n; i++ {
		x = i
		b := cap.Borrow(&x)
		println(*b)
	}
	for i := 0; i < n; i++ {
		x = i
		b2 := &x //gosree:mut
		*b2 = i
	}
}

func backwardGotoUse(cond bool) {
	x := 1
	var c *int
	if !cond {
		goto makeBorrow
	}
useIt:
	println(*c)
	return
makeBorrow:
	b := &x //gosree:rob
	c = b
	x = 2
	goto useIt
}

func sameLineUnannotatedAddrTake() {
	x := 1
	var b cap.Ref[int] = &x; p := &x; println(*b, *p)
}
`,
	})
	g, err := checker.Analyze([]*analysis.Analyzer{borrow.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	byLine := map[int][]string{}
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			ln := act.Package.Fset.Position(d.Pos).Line
			byLine[ln] = append(byLine[ln], d.Message)
		}
	}
	hasRule := func(ln int, prefix string) bool {
		for _, m := range byLine[ln] {
			if strings.HasPrefix(m, prefix) {
				return true
			}
		}
		return false
	}
	if !hasRule(6, "BORROW002:") {
		t.Errorf("sameLineLoopConflict (line 6): expected BORROW002, got %v", byLine[6])
	}
	if !hasRule(10, "BORROW003:") {
		t.Errorf("sameLineLoopOwnerWrite (line 10): expected BORROW003, got %v", byLine[10])
	}
	if !hasRule(17, "BORROW003:") {
		t.Errorf("sameBlockLoopCarriedReadAfterWrite (line 17): expected BORROW003, got %v", byLine[17])
	}
	for ln := 24; ln <= 37; ln++ {
		if len(byLine[ln]) > 0 {
			t.Errorf("sameBlockLoopCleanReborrow (line %d): unexpected false positive %v", ln, byLine[ln])
		}
	}
	if !hasRule(50, "BORROW003:") {
		t.Errorf("backwardGotoUse (line 50): expected BORROW003, got %v", byLine[50])
	}
	if !hasRule(56, "BORROW003:") {
		t.Errorf("sameLineUnannotatedAddrTake (line 56): expected BORROW003, got %v", byLine[56])
	}
}
