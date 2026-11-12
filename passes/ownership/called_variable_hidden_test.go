package ownership_test

import (
	"strconv"
	"strings"
	"testing"
)

// A call of a variable whose functions the trace says it has all of, when it has not. The trace's
// "that is all" misses a package variable set through its address, a field of a package variable
// set through a method's receiver, a parameter of a method also reached through an interface, and a
// channel handed to another function. In each a function that returns shared memory may run, so the
// publish of the result must be reported. Taking "every function it may hold returns a fresh value"
// as an answer (5bfec07) silenced all four, and fde27e1 stopped taking it. The trace now says "may
// not be all" in each of the four (globalAddrTaken for the first two, the methods called through
// an interface, a channel with a use that is not a send or a receive), and this test holds that.
func TestCallOfAVariableWithHiddenTargetsIsNotFresh(t *testing.T) {
	const head = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

var shared = &T{}

func old() *T { return shared }

func newA() *T { return &T{} }
`
	for name, body := range map[string]string{
		"package variable set through its address": `
var mk = newA

func set(q *func() *T) { *q = old }

func Use() {
	set(&mk)
	x := mk()
	p.Store(x) // REPORT
}
`,
		"field of a package variable set through a receiver": `
type S struct{ mk func() *T }

var cfg S

func init() { cfg.mk = newA }

func (s *S) arm() { s.mk = old }

func Use() {
	cfg.arm()
	x := cfg.mk()
	p.Store(x) // REPORT
}
`,
		"parameter of a method also called through an interface": `
type I interface{ run(m func() *T) }

type R struct{}

func (R) run(m func() *T) {
	x := m()
	p.Store(x) // REPORT
}

func Use() {
	R{}.run(newA)
	var i I = R{}
	i.run(old)
}
`,
		"channel handed to another function": `
func fill(ch chan func() *T) { ch <- old }

func Use() {
	ch := make(chan func() *T, 2)
	ch <- newA
	fill(ch)
	m := <-ch
	x := m()
	p.Store(x) // REPORT
}
`,
	} {
		src := head + body
		want := 0
		for i, l := range strings.Split(src, "\n") {
			if strings.Contains(l, "// REPORT") {
				want = i + 1
			}
		}
		got := advDiagLines(t, src, "is not owned here")
		found := false
		for _, d := range got {
			if strings.HasPrefix(d, strconv.Itoa(want)+":") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: a function that returns shared memory may run, and the publish at line %d was not reported (got %q)", name, want, got)
		}
	}
}
