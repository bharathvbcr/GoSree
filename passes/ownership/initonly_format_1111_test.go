package ownership_test

import (
	"strings"
	"testing"
)

const formatHead = `package p

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync/atomic"
)

var _, _, _, _, _ = errors.New, fmt.Sprint, io.Discard, log.Printf, os.Stdout

type Config struct {
	Limit int
	Table any
}

var liveCfg atomic.Pointer[Config]

var sink map[string]int

func freeze() {
	liveCfg.Store(&Config{Table: g})
}
`

func formatHits(t *testing.T, src string) []string {
	t.Helper()
	var hits []string
	for _, d := range advAllDiags(t, formatHead+src) {
		if strings.Contains(d, "SAFE002") || strings.Contains(d, "SAFE004") || strings.Contains(d, "SAFE006") {
			hits = append(hits, d)
		}
	}
	return hits
}

// A plain-data package table that is only formatted stays init-only: fmt and log read their
// operands and keep none of them.
func TestInitOnlyGlobalFormattedStaysInitOnly(t *testing.T) {
	plain := `
var g = map[string]int{"a": 1}
type row struct{ n int; tags []string }
var h = []row{{1, nil}}
func read() {
	fmt.Println(g)
	_ = fmt.Sprintf("%v", g)
	log.Printf("%v", g)
	fmt.Fprintln(os.Stderr, g)
	_ = fmt.Errorf("bad table %v", g)
	log.Print(g)
}
`
	if hits := formatHits(t, plain); len(hits) != 0 {
		t.Fatalf("formatting a plain table made it escape: %v", hits)
	}
}

// A formatted operand whose type, or a type inside it, has a method fmt calls (String, Error,
// Format, GoString, Write) can leak through that method, so it keeps escaping.
func TestInitOnlyGlobalFormattedWithMethodStillEscapes(t *testing.T) {
	cases := map[string]string{
		"String method storing its receiver": `
type Named map[string]int
func (n Named) String() string { sink = n; return "" }
var g = Named{"a": 1}
func read() { fmt.Println(g) }
`,
		"String on an element type": `
type Elem struct{ m map[string]int }
func (e *Elem) String() string { sink = e.m; return "" }
var g = map[string]*Elem{"a": {}}
func read() { fmt.Println(g) }
`,
		"interface element": `
var g = map[string]any{"a": 1}
func read() { fmt.Println(g) }
`,
		"Write method used as the writer": `
type W map[string]int
func (w W) Write(p []byte) (int, error) { sink = w; return len(p), nil }
var g = W{"a": 1}
func read() { fmt.Fprintln(g, "x") }
`,
		"Error method": `
type E map[string]int
func (e E) Error() string { sink = e; return "" }
var g = E{"a": 1}
func read() { _ = fmt.Errorf("x: %w", g) }
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if hits := formatHits(t, src); len(hits) == 0 {
				t.Fatalf("a formatted operand with a callable method must still escape; got no finding")
			}
		})
	}
}
