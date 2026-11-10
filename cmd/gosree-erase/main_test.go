package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentDirectiveErasure(t *testing.T) {
	src := `package test

// Foo does something useful.
//gosree:iso p
//gosree:mut q
// It returns computed total.
func Foo(p *int, q *int) int {
	//gosree:unsafe testing hardware
	//gosree:mut
	b := q
	return *p + *b
}

// Config stores port.
//gosree:imm
type Config struct {
	Port int
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase returned error: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "//gosree:") || strings.Contains(out, "//gosree:") {
		t.Fatalf("output still contains directive comments:\n%s", out)
	}

	// Verify doc comments are intact and attached
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", res, parser.ParseComments)
	if err != nil {
		t.Fatalf("re-parsing erased source failed: %v", err)
	}

	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "Foo" {
			if fd.Doc == nil {
				t.Fatalf("Foo doc comment was disconnected (fd.Doc == nil)")
			}
			docText := fd.Doc.Text()
			if !strings.Contains(docText, "Foo does something useful.") || !strings.Contains(docText, "It returns computed total.") {
				t.Fatalf("Foo doc comment text corrupted: %q", docText)
			}
		}
	}
}

func TestInlineCommentErasure(t *testing.T) {
	src := `package test

func Bar() {
	x := 10
	b1 := &x /*gosree:mut*/
	b2 := &x /*gosree:rob*/
	_ = b1
	_ = b2
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase returned error: %v", err)
	}
	out := string(res)
	if strings.Contains(out, "/*gosree:") || strings.Contains(out, "/*gosree:") {
		t.Fatalf("output still contains inline block comments:\n%s", out)
	}
}

func TestMarkerTypeErasure(t *testing.T) {
	src := `package test

import (
	"github.com/bharathvbcr/gosree/cap"
)

type Item struct {
	Name string
}

func Transform(
	m cap.Mut[int],
	r cap.Ref[string],
	i cap.Iso[Item],
	imm cap.Imm[Item],
) (cap.Mut[int], cap.Iso[Item]) {
	return m, i
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase returned error: %v", err)
	}

	out := string(res)
	expectedSubstrings := []string{
		"func Transform(\n\tm *int,\n\tr *string,\n\ti *Item,\n\timm *Item,\n) (*int, *Item)",
	}
	for _, exp := range expectedSubstrings {
		if !strings.Contains(out, exp) {
			t.Errorf("expected output to contain %q, got:\n%s", exp, out)
		}
	}

	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("import of gosree/cap was not removed")
	}
}

func TestComplexNestedTypes(t *testing.T) {
	src := `package test

import "github.com/bharathvbcr/gosree/cap"

type Container struct {
	Items  []cap.Iso[string]
	Lookup map[string]cap.Imm[int]
	Queue  chan cap.Iso[[]byte]
	Nested cap.Mut[cap.Ref[int]]
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	expectedLines := []string{
		"Items  []*string",
		"Lookup map[string]*int",
		"Queue  chan *[]byte",
		"Nested **int",
	}
	for _, line := range expectedLines {
		if !strings.Contains(out, line) {
			t.Errorf("missing expected line %q in:\n%s", line, out)
		}
	}
}

func TestHelperExpressionsErasure(t *testing.T) {
	src := `package test

import "github.com/bharathvbcr/gosree/cap"

func Run(x int, payload []byte) {
	b := cap.BorrowMut(&x)
	r := cap.Borrow(&x)
	p := cap.Move(payload)
	b2 := cap.BorrowMub(&x)
	r2 := cap.BorrowRob(&x)
	r3 := cap.BorrowRef(&x)
	_ = b
	_ = r
	_ = p
	_ = b2
	_ = r2
	_ = r3
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "cap.Borrow") || strings.Contains(out, "cap.Move") {
		t.Fatalf("output still contains helper expressions:\n%s", out)
	}
	if !strings.Contains(out, "b := &x") || !strings.Contains(out, "r := &x") || !strings.Contains(out, "p := payload") {
		t.Fatalf("helper expressions were not unwrapped properly:\n%s", out)
	}
	if !strings.Contains(out, "b2 := &x") || !strings.Contains(out, "r2 := &x") || !strings.Contains(out, "r3 := &x") {
		t.Fatalf("aliased helper expressions were not unwrapped properly:\n%s", out)
	}
}

func TestImportCleanupPreservesOthers(t *testing.T) {
	src := `package test

import (
	"fmt"
	"sync"

	"github.com/bharathvbcr/gosree/cap"
)

var mu sync.Mutex

func Echo(v cap.Iso[string]) {
	fmt.Println(v)
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("expected gosree/cap import to be deleted")
	}
	if !strings.Contains(out, "\"fmt\"") || !strings.Contains(out, "\"sync\"") {
		t.Errorf("expected standard imports to be preserved, got:\n%s", out)
	}
}

func TestRenamedAndDotImports(t *testing.T) {
	srcRenamed := `package test

import c "github.com/bharathvbcr/gosree/cap"

func Step(m c.Mut[int]) *int {
	return c.Move(m)
}
`
	res, err := Erase([]byte(srcRenamed))
	if err != nil {
		t.Fatalf("Erase failed for renamed import: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("named import was not deleted:\n%s", out)
	}
	if strings.Contains(out, "c.Mut") || strings.Contains(out, "c.Move") {
		t.Errorf("selector expressions with renamed import not rewritten:\n%s", out)
	}

	srcDot := `package test

import . "github.com/bharathvbcr/gosree/cap"

func StepDot(m Mut[int]) *int {
	return Move(m)
}
`
	resDot, err := Erase([]byte(srcDot))
	if err != nil {
		t.Fatalf("Erase failed for dot import: %v", err)
	}

	outDot := string(resDot)
	if strings.Contains(outDot, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("dot import was not deleted:\n%s", outDot)
	}
	if strings.Contains(outDot, "Mut[int]") || strings.Contains(outDot, "Move(m)") {
		t.Errorf("dot import expressions not rewritten:\n%s", outDot)
	}
}

func TestCLIInPlaceAndOutputFlags(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "sample.go")
	src := `package sample

//gosree:iso x
func Sample(x *int) int { return *x }
`
	if err := os.WriteFile(testFile, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Test -o flag
	outFile := filepath.Join(tmpDir, "sample_clean.go")
	if err := processFile(testFile, false, outFile); err != nil {
		t.Fatalf("processFile with -o failed: %v", err)
	}

	outContent, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if strings.Contains(string(outContent), "//gosree:iso") {
		t.Errorf("output file still contains directive")
	}

	// Test -w in-place flag
	if err := processFile(testFile, true, ""); err != nil {
		t.Fatalf("processFile with inPlace failed: %v", err)
	}
	updatedContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("reading updated file: %v", err)
	}
	if strings.Contains(string(updatedContent), "//gosree:iso") {
		t.Errorf("in-place file still contains directive")
	}

	// Test -l and -d flags using CLI binary execution
	binPath := filepath.Join(tmpDir, "gosree-erase-testbin")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build gosree-erase: %v\nOutput: %s", err, string(out))
	}

	// Create a modified file
	diffFile := filepath.Join(tmpDir, "diff_sample.go")
	if err := os.WriteFile(diffFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	// Test -l lists differing file
	cmdL := exec.Command(binPath, "-l", diffFile)
	outL, err := cmdL.CombinedOutput()
	if err != nil {
		t.Fatalf("gosree-erase -l failed: %v\nOutput: %s", err, string(outL))
	}
	if !strings.Contains(string(outL), diffFile) {
		t.Errorf("gosree-erase -l did not list differing file: %s", string(outL))
	}

	// Test -d prints diff
	cmdD := exec.Command(binPath, "-d", diffFile)
	outD, err := cmdD.CombinedOutput()
	if err != nil {
		t.Fatalf("gosree-erase -d failed: %v\nOutput: %s", err, string(outD))
	}
	if !strings.Contains(string(outD), "//gosree:iso") {
		t.Errorf("gosree-erase -d did not show diff of removed directive: %s", string(outD))
	}
}

func TestE2ECompilationCheck(t *testing.T) {
	tmpDir := t.TempDir()
	goMod := `module testpkg
go 1.24
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}

	rawSrc := `package testpkg

import (
	"fmt"
	"github.com/bharathvbcr/gosree/cap"
)

// Item holds data.
//gosree:imm
type Item struct {
	Count int
}

// Compute performs calculations.
//gosree:iso item
func Compute(item cap.Iso[Item], multiplier cap.Mut[int]) (int, *Item) {
	m := cap.BorrowMut(multiplier)
	*m *= 2
	it := cap.Move(item)
	fmt.Println("Done")
	return *multiplier, it
}
`
	erased, err := Erase([]byte(rawSrc))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	targetFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(targetFile, erased, 0644); err != nil {
		t.Fatalf("writing erased file: %v", err)
	}

	// Run go vet and go build to mathematically prove standard Go toolchain compatibility
	cmdVet := exec.Command("go", "vet", "./...")
	cmdVet.Dir = tmpDir
	if out, err := cmdVet.CombinedOutput(); err != nil {
		t.Fatalf("go vet failed on erased package: %v\nOutput:\n%s", err, string(out))
	}

	cmdBuild := exec.Command("go", "build", "./...")
	cmdBuild.Dir = tmpDir
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build failed on erased package: %v\nOutput:\n%s", err, string(out))
	}
}

func TestGoSreeDirectiveAndImportErasure(t *testing.T) {
	src := `package test

import "github.com/bharathvbcr/gosree/cap"

//gosree:imm
type Config struct{ Port int }

//gosree:iso item
func Process(item cap.Iso[Config], count cap.Mut[int]) *Config {
	//gosree:unsafe intentional
	b := cap.BorrowMut(count)
	*b++
	return cap.Move(item)
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase returned error: %v", err)
	}
	out := string(res)
	for _, forbidden := range []string{"//gosree:", "/*gosree:", "github.com/bharathvbcr/gosree/cap", "cap.Iso", "cap.Mut", "cap.BorrowMut", "cap.Move"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("erased output still contains %q:\n%s", forbidden, out)
		}
	}
}
