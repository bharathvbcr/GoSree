package ownership_test

import (
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
