// Package rb_misc feeds the analyzers odd but legal Go: dot imports, init functions, package-level
// initialisation through functions, unicode and very long identifiers, arrays of arrays, complex
// numbers, shifts, blank identifiers everywhere, and min/max/clear.
package rb_misc

import (
	. "strings"
	_ "unsafe"
)

type Job struct{ ID int }

var (
	_      = initA()
	a1     = initB()
	b1     = 2
	_, _   = 1, 2
	global = &Job{}
)

func initA() int { return a1 + 1 }
func initB() int { return b1 * 2 }

func init() { global.ID = 1 }
func init() { global.ID = 2 }

type Ünïcødé struct{ Ωmega *Job }

func 送信(ch chan *Job) {
	ジョブ := &Job{}
	ch <- ジョブ
	ジョブ.ID = 1 // HIT(SAFE003)
}

type Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa struct {
	Bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb *Job
}

func longNames(ch chan *Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) {
	v := &Aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa{}
	ch <- v
	v.Bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb = nil // HIT(SAFE003)
}

func arrays(ch chan [3][4]*Job) {
	var grid [3][4]*Job
	j := &Job{}
	grid[1][2] = j
	ch <- grid
	j.ID = 1 // HIT(SAFE003)
}

func numbers() complex128 {
	c := complex(1, 2) * 3i
	var s uint = 63
	x := 1 << s >> (s - 1)
	_ = x &^ 5
	return c + complex(real(c), imag(c))
}

func builtins(ch chan []int) {
	xs := []int{3, 1, 2}
	m := map[int]*Job{1: {}}
	_ = min(xs[0], xs[1], 7)
	_ = max(1.5, 2)
	ch <- xs
	clear(xs) // HIT(SAFE003)
	clear(m)
}

func blanks(_ chan *Job, _ int) (_ int, _ error) {
	var _ = 1
	_, _, _ = Cut("a=b", "=")
	for range 2 {
	}
	return
}

func dotImport(ch chan *Builder) {
	b := &Builder{}
	ch <- b
	b.WriteString("x") // HIT(SAFE003)
}

// shadowedBuiltins: local names that shadow builtins and predeclared identifiers.
func shadowedBuiltins(ch chan *Job) {
	new := func() *Job { return &Job{} }
	len := 3
	j := new()
	_ = len
	ch <- j
	j.ID = 1 // HIT(SAFE003)
}

// structKeyPromoted uses a Go 1.27 promoted-field key in a struct literal.
type In struct{ P *Job }
type Out struct{ In }

func structKeyPromoted(ch chan Out) {
	j := &Job{}
	o := Out{P: j}
	ch <- o
	j.ID = 2 // HIT(SAFE003)
}
