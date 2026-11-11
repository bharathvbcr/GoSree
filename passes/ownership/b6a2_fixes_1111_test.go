package ownership_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// A closure bound to a same-named variable elsewhere in the function must not contribute its
// borrows to an unrelated goroutine capture, and the spurious CAP003 must not hide SAFE002.
func TestShadowedClosureNameIsNotABorrowCapture(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

type B struct{ n int }

type File struct{}

func (*File) Close() {} // want Close:"ownership summary"

type Job struct{ n int }

func sink(j *Job) {}

// The inner f is a different variable from the ranged f.
//
//gosree:mub b
func shadowed(b *B, files []*File) {
	{
		f := func() { b.n++ }
		f()
	}
	for _, f := range files { // want "SAFE003"
		go func() { f.Close() }() // want "SAFE002"
	}
}

// A field selector named f is not a variable named f.
//
//gosree:mub b
func selectorLHS(b *B, files []*File) {
	var h struct{ f func() }
	h.f = func() { b.n++ }
	h.f()
	for _, f := range files { // want "SAFE003"
		go func() { f.Close() }() // want "SAFE002"
	}
}

// The spurious CAP003 used to hide the real ownership finding for the same transfer.
//
//gosree:mub b
func hidesSafe002(b *B, j *Job) {
	{
		j := func() { b.n++ }
		j()
	}
	go func() { sink(j) }() // want "SAFE002"
}

//gosree:mub b
func hidesSafe003(b *B) {
	{
		j := func() { b.n++ }
		j()
	}
	j := &Job{}
	go func() { sink(j) }()
	j.n = 1 // want "SAFE003"
}

var registry []*Job // want "SAFE006"

func retain(j *Job) { registry = append(registry, j) }

//gosree:mub b
func hidesSafe004(b *B) {
	{
		j := func() { b.n++ }
		j()
	}
	j := &Job{}
	retain(j)
	go func() { sink(j) }() // want "SAFE004"
}

// The genuine shape still reports: f is the closure that captures the borrow.
//
//gosree:mub b
func genuine(b *B) {
	f := func() { b.n++ }
	go f() // want "CAP003: stack-scoped borrow 'b'"
}

// A closure that reaches the borrow through another closure variable still reports.
//
//gosree:mub b
func nested(b *B) {
	inner := func() { b.n++ }
	outer := func() { inner() }
	go outer() // want "CAP003: stack-scoped borrow 'b'"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

// immInValueType used to give up after 16 distinct types and report nothing, so an imm value
// buried after enough other field types was silently writable.
func TestImmStoreFoundBehindManyFieldTypes(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\n//gosree:imm\ntype Config struct { // want Config:\"imm\"\n\tport int\n}\n\n")
	for i := range 24 {
		fmt.Fprintf(&b, "type A%d struct{ x int }\n\n", i)
	}
	b.WriteString("type Wrapper struct {\n")
	for i := range 24 {
		fmt.Fprintf(&b, "\tf%d A%d\n", i, i)
	}
	b.WriteString("\tcfg Config\n}\n\n")
	b.WriteString("func overwrite(w *Wrapper) { *w = Wrapper{} } // want `SAFE005: store through a \\*Wrapper overwrites an immutable value`\n")
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"p/p.go": b.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

// A handler factory with an early return that gives no handler still builds one further down; the
// roots come from every return, not the first.
func TestHandlerFactoryWithEarlyNilReturn(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

import "net/http"

func makeGuarded(enabled bool) http.HandlerFunc {
	if !enabled {
		return nil
	}
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		n++ // want "SAFE008: captured variable n is written here"
	}
}

func makeUnguarded() http.HandlerFunc {
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		n++ // want "SAFE008: captured variable n is written here"
	}
}

func makeClean(enabled bool) http.HandlerFunc {
	if !enabled {
		return nil
	}
	return func(w http.ResponseWriter, r *http.Request) {
		local := 0
		local++
		_ = local
	}
}

func register(mux *http.ServeMux) {
	mux.HandleFunc("/g", makeGuarded(true))
	mux.HandleFunc("/u", makeUnguarded())
	mux.HandleFunc("/c", makeClean(true))
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}
