package ownership_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// advDiagLines runs the ownership analyzer over one package and returns "line: message" for every
// diagnostic that contains substr.
func advDiagLines(t *testing.T, src, substr string) []string {
	t.Helper()
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var out []string
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			if strings.Contains(d.Message, substr) {
				out = append(out, fmt.Sprintf("%d: %s", act.Package.Fset.Position(d.Pos).Line, d.Message))
			}
		}
	}
	sort.Strings(out)
	return out
}

// A write through a shared atomic-loaded value must be reported when the write sits inside a
// generic function, a bound-method wrapper or a method expression thunk, not only in a plain
// function.
func TestAdvSharedLoadWriteInSyntheticFunctions(t *testing.T) {
	cases := map[string]string{
		"generic function": `package p

import "sync/atomic"

type Cfg struct{ X any }

var live atomic.Pointer[Cfg]

func set[T any](c *Cfg, v T) { c.X = v }

func use() {
	c := live.Load()
	set(c, 1)
}
`,
		"bound method value": `package p

import "sync/atomic"

type Cfg struct{ X int }

func (c *Cfg) Mutate() { c.X++ }

var live atomic.Pointer[Cfg]

func use() {
	c := live.Load()
	f := c.Mutate
	f()
}
`,
		"method expression": `package p

import "sync/atomic"

type Cfg struct{ X int }

func (c *Cfg) Mutate() { c.X++ }

var live atomic.Pointer[Cfg]

func use() {
	c := live.Load()
	(*Cfg).Mutate(c)
}
`,
		"generic method on generic type": `package p

import "sync/atomic"

type Cfg[T any] struct{ X T }

func (c *Cfg[T]) Set(v T) { c.X = v }

var live atomic.Pointer[Cfg[int]]

func use() {
	c := live.Load()
	c.Set(1)
}
`,
		"direct write in loading function (control)": `package p

import "sync/atomic"

type Cfg struct{ X int }

var live atomic.Pointer[Cfg]

func use() {
	c := live.Load()
	c.X = 1
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got := advDiagLines(t, src, "published atomic values are read-only")
			t.Logf("%s: %v", name, got)
			if len(got) == 0 {
				t.Fatalf("%s: a write through a shared Load result was not reported", name)
			}
		})
	}
}
