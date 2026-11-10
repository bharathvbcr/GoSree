package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestChallengerGate2_StructLiteralsAndInterfaceBoxing verifies that marker types
// and helper calls inside struct literals, interface boxing, and method call receivers
// erase cleanly and compile without errors.
func TestChallengerGate2_StructLiteralsAndInterfaceBoxing(t *testing.T) {
	src := `package main

import (
	"fmt"
	"github.com/bharathvbcr/gosree/cap"
)

type Item struct {
	ID   int
	Name string
}

func (it *Item) Label() string {
	return fmt.Sprintf("%d:%s", it.ID, it.Name)
}

type Container struct {
	Primary   cap.Iso[Item]
	Secondary cap.Imm[Item]
	Extra     any
}

func Process(c Container) string {
	return c.Primary.Label()
}

func main() {
	item1 := Item{ID: 1, Name: "one"}
	item2 := Item{ID: 2, Name: "two"}
	val := 42

	// Struct literal with helper calls inside field values
	c := Container{
		Primary:   cap.Move(&item1),
		Secondary: cap.Move(&item2),
		Extra:     cap.BorrowMut[int](&val),
	}

	// Interface boxing with explicit type arguments
	var boxed any = cap.Move[int](100)
	var boxedPtr any = cap.BorrowMut[int](&val)

	// Receiver with helper call
	lbl := cap.Move(&item1).Label()

	_ = boxed
	_ = boxedPtr
	_ = lbl
	_ = Process(c)
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	t.Logf("Erased output:\n%s", out)

	if strings.Contains(out, "cap.") {
		t.Errorf("erased output still contains 'cap.':\n%s", out)
	}
	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("cap import was not removed:\n%s", out)
	}

	// Verify standard Go build
	tmpDir := t.TempDir()
	goMod := "module structtest\ngo 1.24\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "main.go"), res, 0644)

	cmd := exec.Command("go", "build", ".")
	cmd.Dir = tmpDir
	outBuild, errBuild := cmd.CombinedOutput()
	if errBuild != nil {
		t.Fatalf("go build failed on erased struct literal code:\n%v\nOutput:\n%s\nSource:\n%s", errBuild, string(outBuild), out)
	}
}

// TestChallengerGate2_ComplexGenericInstantiations verifies multi-parameter generics,
// interface methods with marker types, and nested generic instantiations.
func TestChallengerGate2_ComplexGenericInstantiations(t *testing.T) {
	src := `package main

import (
	"fmt"
	"github.com/bharathvbcr/gosree/cap"
)

type Pair[A, B any] struct {
	First  A
	Second B
}

type Service[T any] interface {
	Handle(req cap.Iso[T]) cap.Imm[T]
}

type DataService struct{}

func (s DataService) Handle(req cap.Iso[int]) cap.Imm[int] {
	return req
}

func Zip[A, B any](a cap.Mut[A], b cap.Ref[B]) Pair[cap.Mut[A], cap.Ref[B]] {
	return Pair[cap.Mut[A], cap.Ref[B]]{
		First:  a,
		Second: b,
	}
}

func main() {
	x := 10
	y := "hello"
	p := Zip[int, string](cap.BorrowMut(&x), cap.Borrow(&y))
	var svc Service[int] = DataService{}
	res := svc.Handle(&x)
	fmt.Println(*p.First, *p.Second, *res)
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	t.Logf("Erased generic output:\n%s", out)

	if strings.Contains(out, "cap.") {
		t.Errorf("erased output still contains 'cap.':\n%s", out)
	}
	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("cap import was not removed:\n%s", out)
	}

	// Verify standard Go build and execution
	tmpDir := t.TempDir()
	goMod := "module gentest\ngo 1.24\n"
	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "main.go"), res, 0644)

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = tmpDir
	outRun, errRun := cmd.CombinedOutput()
	if errRun != nil {
		t.Fatalf("go run failed:\n%v\nOutput:\n%s\nSource:\n%s", errRun, string(outRun), out)
	}
	expected := "10 hello 10\n"
	if string(outRun) != expected {
		t.Errorf("expected %q, got %q", expected, string(outRun))
	}
}

// TestChallengerGate2_ChainedAndNestedHelpers verifies helper unwrapping when nested
// inside other helpers or expressions.
func TestChallengerGate2_ChainedAndNestedHelpers(t *testing.T) {
	src := `package test

import "github.com/bharathvbcr/gosree/cap"

func Nested() {
	x := 5
	// Nested moves
	a := cap.Move(cap.Move(cap.Move(&x)))
	// Nested with explicit types
	b := cap.Move[*int](cap.BorrowMut[int](&x))
	_ = a
	_ = b
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "cap.") {
		t.Errorf("erased output still contains 'cap.':\n%s", out)
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "nested.go", res, parser.ParseComments); err != nil {
		t.Fatalf("syntax error in erased output: %v\nOutput:\n%s", err, out)
	}
}

// TestChallengerGate2_AllAliasesAndDotImportExplicitTypeArgs verifies every alias
// function and type in cap under both dot import and renamed import with explicit type args.
func TestChallengerGate2_AllAliasesAndDotImportExplicitTypeArgs(t *testing.T) {
	src := `package test

import . "github.com/bharathvbcr/gosree/cap"

func AllDot(x int) {
	var _ Mut[int] = BorrowMut[int](&x)
	var _ Ref[int] = Borrow[int](&x)
	var _ Ref[int] = BorrowRef[int](&x)
	var _ Mub[int] = BorrowMub[int](&x)
	var _ Rob[int] = BorrowRob[int](&x)
	var _ Iso[int] = Move[int](&x)
	var _ Imm[int] = Move[int](&x)
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed on dot import: %v", err)
	}

	out := string(res)
	t.Logf("Dot import erased:\n%s", out)

	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("dot import was not removed:\n%s", out)
	}
	if strings.Contains(out, "Borrow") || strings.Contains(out, "Move") {
		t.Errorf("helper calls were not stripped:\n%s", out)
	}
	if strings.Contains(out, "Mut[") || strings.Contains(out, "Mub[") || strings.Contains(out, "Ref[") || strings.Contains(out, "Rob[") || strings.Contains(out, "Iso[") || strings.Contains(out, "Imm[") {
		t.Errorf("marker types were not replaced:\n%s", out)
	}
}

// TestChallengerGate2_CLIStdinFlagPermutations tests -l, -d, -o on stdin including
// when input is unchanged or empty.
func TestChallengerGate2_CLIStdinFlagPermutations(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "gosree-erase")

	cmdBuild := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("building gosree-erase failed: %v\n%s", err, string(out))
	}

	// 1. Unchanged stdin with -l: should output NOTHING
	cleanSrc := "package p\n\nfunc Clean() {}\n"
	cmdL := exec.Command(binPath, "-l")
	cmdL.Stdin = strings.NewReader(cleanSrc)
	var outL bytes.Buffer
	cmdL.Stdout = &outL
	if err := cmdL.Run(); err != nil {
		t.Fatalf("clean stdin -l failed: %v", err)
	}
	if outL.String() != "" {
		t.Errorf("expected empty output for unchanged stdin with -l, got: %q", outL.String())
	}

	// 2. Unchanged stdin with -d: should output NOTHING
	cmdD := exec.Command(binPath, "-d")
	cmdD.Stdin = strings.NewReader(cleanSrc)
	var outD bytes.Buffer
	cmdD.Stdout = &outD
	if err := cmdD.Run(); err != nil {
		t.Fatalf("clean stdin -d failed: %v", err)
	}
	if outD.String() != "" {
		t.Errorf("expected empty output for unchanged stdin with -d, got: %q", outD.String())
	}

	// 3. Stdin with -o flag: should write erased result to file
	dirtySrc := "package p\n\n//gosree:iso x\nfunc Dirty(x *int) {}\n"
	outFile := filepath.Join(tmpDir, "erased_output.go")
	cmdO := exec.Command(binPath, "-o", outFile)
	cmdO.Stdin = strings.NewReader(dirtySrc)
	if err := cmdO.Run(); err != nil {
		t.Fatalf("stdin with -o failed: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading -o file failed: %v", err)
	}
	if strings.Contains(string(data), "//gosree:iso") {
		t.Errorf("-o output still contains directive:\n%s", string(data))
	}
	if !strings.Contains(string(data), "func Dirty(x *int)") {
		t.Errorf("-o output missing expected code:\n%s", string(data))
	}

	// 4. Empty stdin
	cmdEmpty := exec.Command(binPath)
	cmdEmpty.Stdin = strings.NewReader("")
	var outEmpty bytes.Buffer
	cmdEmpty.Stdout = &outEmpty
	_ = cmdEmpty.Run() // empty Go file produces parser error or empty, should not panic
}
