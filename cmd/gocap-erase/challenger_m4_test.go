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

// TestChallengerM4_StructTagsAndCommentsErasure verifies that comments and directives
// around struct tags, field declarations, and block comments are erased cleanly without
// corrupting struct tags or standard Go AST structure.
func TestChallengerM4_StructTagsAndCommentsErasure(t *testing.T) {
	src := `package main

import (
	"fmt"
	"github.com/bharathvbcr/gocap/cap"
)

type Config struct {
	// Primary worker pool configuration
	Workers cap.Mut[int] ` + "`json:\"workers\" db:\"workers\"`" + ` //gocap:mub
	Timeout /*gocap:rob*/ cap.Ref[int] /* inline */ ` + "`json:\"timeout\"`" + `
	Secret  cap.Iso[string] ` + "`yaml:\"secret,omitempty\"`" + ` //safe:iso
	// Doc comment for metadata
	Metadata cap.Imm[string] ` + "`json:\"metadata\"`" + `
}

func main() {
	w := 8
	to := 30
	sec := "secret-key"
	meta := "v1.0.0"

	cfg := Config{
		Workers:  cap.BorrowMut(&w),
		Timeout:  cap.Borrow(&to),
		Secret:   cap.Move(&sec),
		Metadata: &meta,
	}

	fmt.Println(*cfg.Workers, *cfg.Timeout, *cfg.Secret, *cfg.Metadata)
}
`

	erased, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	erasedStr := string(erased)

	// Verify directives are completely gone
	if strings.Contains(erasedStr, "//gocap:") || strings.Contains(erasedStr, "/*gocap:") || strings.Contains(erasedStr, "//safe:") {
		t.Errorf("Directives survived erasure:\n%s", erasedStr)
	}

	// Verify cap import was cleaned up
	if strings.Contains(erasedStr, "github.com/bharathvbcr/gocap/cap") {
		t.Errorf("cap import was not cleaned up:\n%s", erasedStr)
	}

	// Verify marker types became standard pointer types
	if !strings.Contains(erasedStr, "Workers") || !strings.Contains(erasedStr, "*int `json:\"workers\" db:\"workers\"`") {
		t.Errorf("Workers field not erased cleanly:\n%s", erasedStr)
	}
	if !strings.Contains(erasedStr, "Timeout") || !strings.Contains(erasedStr, "*int/* inline */ `json:\"timeout\"`") {
		t.Errorf("Timeout field not erased cleanly:\n%s", erasedStr)
	}
	if !strings.Contains(erasedStr, "Secret") || !strings.Contains(erasedStr, "*string `yaml:\"secret,omitempty\"`") {
		t.Errorf("Secret field not erased cleanly:\n%s", erasedStr)
	}

	// Verify AST can be parsed cleanly
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, "erased.go", erased, parser.ParseComments)
	if err != nil {
		t.Fatalf("Erased code fails to parse: %v\nErased content:\n%s", err, erasedStr)
	}

	// Verify it compiles with go build
	tmpDir := t.TempDir()
	goMod := "module testm4\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), erased, 0644); err != nil {
		t.Fatalf("Writing main.go: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(tmpDir, "main.out"), filepath.Join(tmpDir, "main.go"))
	cmd.Dir = tmpDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build failed on erased code:\n%s\nOutput:\n%s", erasedStr, out.String())
	}
}

// TestChallengerM4_ComplexGenericsErasure verifies that generic structs and generic functions
// with multiple type parameters, type constraints, and nested marker types erase cleanly.
func TestChallengerM4_ComplexGenericsErasure(t *testing.T) {
	src := `package main

import (
	"fmt"
	"github.com/bharathvbcr/gocap/cap"
)

type Result[T any, E error] struct {
	Value cap.Iso[T]
	Err   E
}

type Cache[K comparable, V any] struct {
	entries map[K]cap.Mut[V]
	backup  cap.Imm[V]
}

func NewCache[K comparable, V any](initial cap.Imm[V]) Cache[K, V] {
	return Cache[K, V]{
		entries: make(map[K]cap.Mut[V]),
		backup:  initial,
	}
}

func (c *Cache[K, V]) Put(k K, v cap.Mut[V]) {
	c.entries[k] = v
}

func (c *Cache[K, V]) Get(k K) cap.Ref[V] {
	if val, ok := c.entries[k]; ok {
		return cap.BorrowRef[V](val)
	}
	return cap.BorrowRef[V](c.backup)
}

func ProcessResult[T any](res Result[T, error]) cap.Iso[T] {
	return cap.Move[T](res.Value)
}

func main() {
	val := 100
	cache := NewCache[string, int](&val)
	cache.Put("a", cap.BorrowMut[int](&val))
	got := cache.Get("a")

	res := Result[int, error]{
		Value: cap.Move[int](&val),
		Err:   nil,
	}
	unwrapped := ProcessResult(res)

	fmt.Println(*got, *unwrapped)
}
`

	erased, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	erasedStr := string(erased)

	if strings.Contains(erasedStr, "github.com/bharathvbcr/gocap/cap") {
		t.Errorf("cap import survived in erased code:\n%s", erasedStr)
	}

	// Verify standard compilation
	tmpDir := t.TempDir()
	goMod := "module testgenerics\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), erased, 0644); err != nil {
		t.Fatalf("Writing main.go: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(tmpDir, "main.out"), filepath.Join(tmpDir, "main.go"))
	cmd.Dir = tmpDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build failed on erased generic code:\n%s\nOutput:\n%s", erasedStr, out.String())
	}
}

// TestChallengerM4_ControlFlowErasure verifies that helper calls inside select, go, defer,
// and switch statements erase cleanly to standard Go syntax.
func TestChallengerM4_ControlFlowErasure(t *testing.T) {
	src := `package main

import (
	"fmt"
	"sync"
	"github.com/bharathvbcr/gocap/cap"
)

type Task struct {
	ID int
}

func worker(t cap.Mut[Task], wg *sync.WaitGroup) {
	defer wg.Done()
	t.ID += 10
}

func cleanup(t cap.Ref[Task]) {
	_ = t.ID
}

func main() {
	var wg sync.WaitGroup
	item := Task{ID: 1}
	ch := make(chan *Task, 1)

	// go statement with helper
	wg.Add(1)
	go worker(cap.BorrowMut(&item), &wg)

	// defer with helper
	defer cleanup(cap.Borrow(&item))

	// select with helper
	select {
	case ch <- cap.Move(&item):
	default:
	}

	// switch with helper
	switch cap.Move(&item) {
	default:
	}

	wg.Wait()
	fmt.Println("done")
}
`

	erased, err := Erase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v", err)
	}

	erasedStr := string(erased)

	if strings.Contains(erasedStr, "cap.BorrowMut") || strings.Contains(erasedStr, "cap.Move") || strings.Contains(erasedStr, "cap.Borrow") {
		t.Errorf("Helpers survived erasure:\n%s", erasedStr)
	}

	tmpDir := t.TempDir()
	goMod := "module testctrlflow\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Writing go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), erased, 0644); err != nil {
		t.Fatalf("Writing main.go: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(tmpDir, "main.out"), filepath.Join(tmpDir, "main.go"))
	cmd.Dir = tmpDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build failed on erased control flow code:\n%s\nOutput:\n%s", erasedStr, out.String())
	}
}
