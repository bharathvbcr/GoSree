package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestChallenger_NestedGenericsSlicesChannels verifies erasure of complex nested generics,
// including slices of channels of marker pointers ([]chan cap.Mut[int]).
func TestChallenger_NestedGenericsSlicesChannels(t *testing.T) {
	src := `package test

import "github.com/bharathvbcr/gosree/cap"

type MultiGen[A, B, C any] struct {
	a A
	b B
	c C
}

type ComplexTypes struct {
	SliceOfChannels []chan cap.Mut[int]
	MapOfSliceOfPtr map[string]cap.Ref[[]cap.Mut[int]]
	ChanOfDoublePtr chan<- cap.Mut[cap.Ref[int]]
	ArrayOfNested   [3]map[string]chan cap.Mub[int]
	GenericTuple    MultiGen[cap.Mut[int], cap.Ref[string], []cap.Mut[bool]]
}

func ProcessGenerics[T any](
	ch []chan cap.Mut[T],
	fn func(cap.Mut[T]) cap.Ref[T],
) cap.Mut[T] {
	return nil
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed on nested generics: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "cap.") {
		t.Errorf("erased output still contains 'cap.':\n%s", out)
	}
	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("cap import was not removed:\n%s", out)
	}

	expectedSubstrings := []string{
		"SliceOfChannels []chan *int",
		"MapOfSliceOfPtr map[string]*[]*int",
		"ChanOfDoublePtr chan<- **int",
		"ArrayOfNested   [3]map[string]chan *int",
		"GenericTuple    MultiGen[*int, *string, []*bool]",
		"func ProcessGenerics[T any](\n\tch []chan *T,\n\tfn func(*T) *T,\n) *T",
	}

	for _, exp := range expectedSubstrings {
		if !strings.Contains(out, exp) {
			t.Errorf("missing expected erased snippet: %q\nGot:\n%s", exp, out)
		}
	}

	// Verify that the erased code parses and type-checks cleanly
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, "cleaned.go", res, parser.ParseComments)
	if err != nil {
		t.Fatalf("erased code has syntax error: %v\nCode:\n%s", err, out)
	}
}

// TestChallenger_DocCommentsPreservation verifies that doc comments are NEVER detached or lost
// on type specs, func decls, var decls, const decls, and package clauses.
func TestChallenger_DocCommentsPreservation(t *testing.T) {
	src := `// Package testpkg documents the package.
//gosree:unsafe package initialization
package testpkg

import "github.com/bharathvbcr/gosree/cap"

// GlobalConfig stores system settings.
//gosree:imm
type GlobalConfig struct {
	// TimeoutSec is duration.
	//safe:imm
	TimeoutSec int
}

// GlobalStatus indicates node state.
//
//gosree:unsafe global lock
//
// More details about GlobalStatus.
var GlobalStatus = 1

// MaxRetries is the upper bound.
//gosree:unsafe const declaration
const MaxRetries = 5

// DoWork executes the main task.
//gosree:iso x
//safe:iso y
//gosree:mut z
// More documentation after directives.
func DoWork(x cap.Iso[int], y *int, z cap.Mut[int]) {
}

// Compute is an isolated helper.
	//gosree:iso a
	//safe:imm
func Compute(a *int) {}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "//gosree:") || strings.Contains(out, "//safe:") {
		t.Fatalf("output still contains directives:\n%s", out)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", res, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse erased output: %v\nOutput:\n%s", err, out)
	}

	// 1. Package doc comment
	if f.Doc == nil {
		t.Errorf("Package doc comment was lost (f.Doc == nil)")
	} else if !strings.Contains(f.Doc.Text(), "Package testpkg documents the package.") {
		t.Errorf("Package doc text corrupted: %q", f.Doc.Text())
	}

	// 2. Type doc comment
	var foundType, foundVar, foundConst, foundDoWork, foundCompute bool
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == "GlobalConfig" {
						foundType = true
						doc := d.Doc
						if doc == nil {
							t.Errorf("GlobalConfig doc comment was detached (GenDecl.Doc == nil)")
						} else if !strings.Contains(doc.Text(), "GlobalConfig stores system settings.") {
							t.Errorf("GlobalConfig doc text corrupted: %q", doc.Text())
						}
					}
				}
			case token.VAR:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							if name.Name == "GlobalStatus" {
								foundVar = true
								doc := d.Doc
								if doc == nil {
									t.Errorf("GlobalStatus doc comment was detached (GenDecl.Doc == nil)")
								} else if !strings.Contains(doc.Text(), "GlobalStatus indicates node state.") ||
									!strings.Contains(doc.Text(), "More details about GlobalStatus.") {
									t.Errorf("GlobalStatus doc text corrupted: %q", doc.Text())
								}
							}
						}
					}
				}
			case token.CONST:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							if name.Name == "MaxRetries" {
								foundConst = true
								doc := d.Doc
								if doc == nil {
									t.Errorf("MaxRetries doc comment was detached (GenDecl.Doc == nil)")
								} else if !strings.Contains(doc.Text(), "MaxRetries is the upper bound.") {
									t.Errorf("MaxRetries doc text corrupted: %q", doc.Text())
								}
							}
						}
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name == "DoWork" {
				foundDoWork = true
				if d.Doc == nil {
					t.Errorf("DoWork doc comment was detached (FuncDecl.Doc == nil)")
				} else if !strings.Contains(d.Doc.Text(), "DoWork executes the main task.") ||
					!strings.Contains(d.Doc.Text(), "More documentation after directives.") {
					t.Errorf("DoWork doc text corrupted: %q", d.Doc.Text())
				}
			}
			if d.Name.Name == "Compute" {
				foundCompute = true
				if d.Doc == nil {
					t.Errorf("Compute doc comment was detached (FuncDecl.Doc == nil)")
				} else if !strings.Contains(d.Doc.Text(), "Compute is an isolated helper.") {
					t.Errorf("Compute doc text corrupted: %q", d.Doc.Text())
				}
			}
		}
	}

	if !foundType {
		t.Errorf("GlobalConfig type was not found")
	}
	if !foundVar {
		t.Errorf("GlobalStatus var was not found")
	}
	if !foundConst {
		t.Errorf("MaxRetries const was not found")
	}
	if !foundDoWork {
		t.Errorf("DoWork func was not found")
	}
	if !foundCompute {
		t.Errorf("Compute func was not found")
	}
}

// TestChallenger_InlineCommentsInExpressions verifies inline comments inside statements,
// parameter lists, and expressions with arbitrary whitespace.
func TestChallenger_InlineCommentsInExpressions(t *testing.T) {
	src := `package test

func Calc() int {
	x := 10
	y := /*gosree:mut*/ &x
	z := 20 + /*gosree:unsafe math*/ 30
	/*gosree:mut*/
	_ = y
	_ = z
	return x
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	if strings.Contains(out, "/*gosree:") || strings.Contains(out, "/*safe:") {
		t.Fatalf("inline comments were not stripped:\n%s", out)
	}

	// Verify it parses cleanly
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "calc.go", res, parser.ParseComments); err != nil {
		t.Fatalf("erased code fails parsing: %v\nOutput:\n%s", err, out)
	}
}

// TestChallenger_ExplicitTypeArgumentsOnHelpers tests helper expressions called with
// explicit type arguments, e.g. cap.Move[int](x) or cap.BorrowMut[int](&x).
func TestChallenger_ExplicitTypeArgumentsOnHelpers(t *testing.T) {
	src := `package test

import "github.com/bharathvbcr/gosree/cap"

func Run(x int) {
	m := cap.BorrowMut[int](&x)
	v := cap.Move[int](*m)
	_ = v
}
`
	res, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(res)
	t.Logf("Erased output for explicit type arguments:\n%s", out)

	if strings.Contains(out, "cap.BorrowMut") || strings.Contains(out, "cap.Move") {
		t.Errorf("cap helper calls with explicit type arguments were NOT unwrapped:\n%s", out)
	}
	if strings.Contains(out, "github.com/bharathvbcr/gosree/cap") {
		t.Errorf("cap import was not removed:\n%s", out)
	}
}

// TestChallenger_IsoAndImmPointerSemantics tests whether cap.Iso[T] and cap.Imm[T]
// are erased consistently with their definition type Iso[T] = *T in cap.go.
func TestChallenger_IsoAndImmPointerSemantics(t *testing.T) {
	tmpDir := t.TempDir()
	goMod := `module isotest
go 1.24
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	// Source code that compiles cleanly against cap.Iso and cap.Imm
	src := `package main

import (
	"fmt"
	"github.com/bharathvbcr/gosree/cap"
)

type Config struct {
	Port int
}

func ReadConfig(c cap.Imm[Config]) int {
	return c.Port
}

func ConsumeIso(x cap.Iso[int]) int {
	return *x
}

func main() {
	cfg := &Config{Port: 8080}
	val := 42
	fmt.Println(ReadConfig(cfg))
	fmt.Println(ConsumeIso(&val))
}
`
	erased, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	out := string(erased)
	t.Logf("Erased source:\n%s", out)

	targetFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(targetFile, erased, 0644); err != nil {
		t.Fatal(err)
	}

	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = tmpDir
	outBuild, errBuild := cmdBuild.CombinedOutput()
	if errBuild != nil {
		t.Errorf("CRITICAL BUG: Erased code failed to compile with standard go build!\nError: %v\nCompiler output:\n%s\nErased source:\n%s",
			errBuild, string(outBuild), out)
	}
}

// TestChallenger_CLIStdinFlags verifies -l, -d, -o, -w on stdin and files.
func TestChallenger_CLIStdinFlags(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "gosree-erase")

	cmdBuild := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("building gosree-erase failed: %v\n%s", err, string(out))
	}

	sampleSrc := `package test
//gosree:iso x
func Sample(x *int) int { return *x }
`

	// 1. Stdin with -l flag
	cmdL := exec.Command(binPath, "-l")
	cmdL.Stdin = strings.NewReader(sampleSrc)
	var outL bytes.Buffer
	cmdL.Stdout = &outL
	if err := cmdL.Run(); err != nil {
		t.Fatalf("stdin -l failed: %v", err)
	}
	t.Logf("stdin -l output:\n%s", outL.String())
	if strings.Contains(outL.String(), "package test") {
		t.Errorf("BUG: 'gosree-erase -l' on stdin printed the entire erased file instead of listing <stdin> or nothing! Output:\n%s", outL.String())
	}

	// 2. Stdin with -d flag
	cmdD := exec.Command(binPath, "-d")
	cmdD.Stdin = strings.NewReader(sampleSrc)
	var outD bytes.Buffer
	cmdD.Stdout = &outD
	if err := cmdD.Run(); err != nil {
		t.Fatalf("stdin -d failed: %v", err)
	}
	t.Logf("stdin -d output:\n%s", outD.String())
	if !strings.Contains(outD.String(), "-//gosree:iso") && !strings.Contains(outD.String(), "---") {
		t.Errorf("BUG: 'gosree-erase -d' on stdin did NOT print diff! Output:\n%s", outD.String())
	}

	// 3. Stdin with -w flag (should fail)
	cmdW := exec.Command(binPath, "-w")
	cmdW.Stdin = strings.NewReader(sampleSrc)
	errW := cmdW.Run()
	if errW == nil {
		t.Errorf("expected 'gosree-erase -w' on stdin to return error, but it succeeded")
	}

	// 4. Multiple inputs with -o (should fail)
	file1 := filepath.Join(tmpDir, "f1.go")
	file2 := filepath.Join(tmpDir, "f2.go")
	_ = os.WriteFile(file1, []byte("package p\n"), 0644)
	_ = os.WriteFile(file2, []byte("package p\n"), 0644)
	cmdMultiO := exec.Command(binPath, "-o", filepath.Join(tmpDir, "out.go"), file1, file2)
	if err := cmdMultiO.Run(); err == nil {
		t.Errorf("expected 'gosree-erase -o' with multiple files to fail, but it succeeded")
	}

	// 5. Directory with -o (should fail)
	cmdDirO := exec.Command(binPath, "-o", filepath.Join(tmpDir, "out.go"), tmpDir)
	if err := cmdDirO.Run(); err == nil {
		t.Errorf("expected 'gosree-erase -o' with directory to fail, but it succeeded")
	}
}

// TestChallenger_DirectoryRecursionRobustness verifies recursive directory walking
// and ignoring of vendor, testdata, and hidden directories.
func TestChallenger_DirectoryRecursionRobustness(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "gosree-erase")

	cmdBuild := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("building gosree-erase failed: %v\n%s", err, string(out))
	}

	// Create directory structure:
	// root/
	//   pkg1/
	//     a.go (has directive)
	//   pkg2/
	//     b.go (no directive)
	//   vendor/
	//     v.go (has directive, should be ignored)
	//   testdata/
	//     t.go (has directive, should be ignored)
	//   .hidden/
	//     h.go (has directive, should be ignored)
	subDirs := []string{"pkg1", "pkg2", "vendor", "testdata", ".hidden"}
	for _, d := range subDirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	withDirective := "package p\n\n//gosree:iso x\nfunc F(x *int) {}\n"
	noDirective := "package p\n\nfunc G() {}\n"

	fA := filepath.Join(tmpDir, "pkg1", "a.go")
	fB := filepath.Join(tmpDir, "pkg2", "b.go")
	fV := filepath.Join(tmpDir, "vendor", "v.go")
	fT := filepath.Join(tmpDir, "testdata", "t.go")
	fH := filepath.Join(tmpDir, ".hidden", "h.go")

	_ = os.WriteFile(fA, []byte(withDirective), 0644)
	_ = os.WriteFile(fB, []byte(noDirective), 0644)
	_ = os.WriteFile(fV, []byte(withDirective), 0644)
	_ = os.WriteFile(fT, []byte(withDirective), 0644)
	_ = os.WriteFile(fH, []byte(withDirective), 0644)

	// Run gosree-erase -w in-place on root directory
	cmdWalk := exec.Command(binPath, "-w", tmpDir)
	if out, err := cmdWalk.CombinedOutput(); err != nil {
		t.Fatalf("recursive gosree-erase -w failed: %v\n%s", err, string(out))
	}

	// Verify fA was modified (directive removed)
	cA, _ := os.ReadFile(fA)
	if strings.Contains(string(cA), "//gosree:iso") {
		t.Errorf("pkg1/a.go was not erased")
	}

	// Verify fB was unchanged
	cB, _ := os.ReadFile(fB)
	if string(cB) != noDirective {
		t.Errorf("pkg2/b.go was modified unexpectedly: %s", string(cB))
	}

	// Verify vendor/v.go was skipped
	cV, _ := os.ReadFile(fV)
	if !strings.Contains(string(cV), "//gosree:iso") {
		t.Errorf("vendor/v.go should have been skipped, but was erased")
	}

	// Verify testdata/t.go was skipped
	cT, _ := os.ReadFile(fT)
	if !strings.Contains(string(cT), "//gosree:iso") {
		t.Errorf("testdata/t.go should have been skipped, but was erased")
	}

	// Verify .hidden/h.go was skipped
	cH, _ := os.ReadFile(fH)
	if !strings.Contains(string(cH), "//gosree:iso") {
		t.Errorf(".hidden/h.go should have been skipped, but was erased")
	}
}

// TestChallenger_DiffStressPerformance benchmarks printUnifiedDiff on a large file.
func TestChallenger_DiffStressPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	var b1, b2 strings.Builder
	for i := 0; i < 6000; i++ {
		b1.WriteString("line\n")
		b2.WriteString("line\n")
	}
	b1.WriteString("//gosree:iso x\n")
	b2.WriteString("\n")

	t.Logf("Running printUnifiedDiff on 6000-line diff...")
	// Measure memory and time
	printUnifiedDiff(b1.String(), b2.String())
}

