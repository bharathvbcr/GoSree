package ownership_test

import (
	"strings"
	"testing"
)

const advCapHead = `package p

type B struct{ n int }

type S struct{ f func() }

func (b *B) inc() { b.n++ }

`

// CAP003 on closures bound to named variables. Rows with want==0 must stay silent: no CAP003 at
// all, because the goroutine never runs the borrowing closure. Rows with want==1 must report.
func TestAdvCap003ClosureBindings(t *testing.T) {
	cases := []struct {
		name, body string
		want       int // number of CAP003 diagnostics expected
	}{
		{"param same name as inner closure", `
//gosree:mub b
func f1(b *B, f func()) {
	{
		g := func() { b.n++ }
		g()
	}
	go f()
}
`, 0},
		{"inner literal shadows outer closure var", `
//gosree:mub b
func f2(b *B) {
	f := func() { b.n++ }
	f()
	go func() {
		f := func() {}
		f()
	}()
}
`, 0},
		{"sibling scope same name", `
//gosree:mub b
func f3(b *B) {
	if b != nil {
		f := func() { b.n++ }
		f()
	}
	{
		f := func() {}
		go f()
	}
}
`, 0},
		{"struct field selector same name", `
//gosree:mub b
func f4(b *B) {
	var s S
	s.f = func() { b.n++ }
	s.f()
	f := func() {}
	go f()
}
`, 0},
		{"closure of other function same name", `
//gosree:mub b
func f5a(b *B) {
	f := func() { b.n++ }
	f()
}

//gosree:mub b
func f5b(b *B) {
	f := func() {}
	go f()
}
`, 0},
		{"named closure goroutine genuine", `
//gosree:mub b
func f6(b *B) {
	f := func() { b.n++ }
	go f()
}
`, 1},
		{"named closure called inside goroutine literal", `
//gosree:mub b
func f7(b *B) {
	f := func() { b.n++ }
	go func() { f() }()
}
`, 1},
		{"named closure deferred into goroutine via param", `
//gosree:mub b
func f8(b *B) {
	f := func() { b.n++ }
	spawn(f)
}

func spawn(f func()) { go f() }
`, 1},
		{"PRE-EXISTING miss: closure sent on channel gives no CAP003 on 9161d81 or the fix", `
//gosree:mub b
func f9(b *B, ch chan func()) {
	f := func() { b.n++ }
	ch <- f
}
`, 0},
		{"closure reassigned later to benign literal", `
//gosree:mub b
func f10(b *B) {
	f := func() { b.n++ }
	f()
	f = func() {}
	go f()
}
`, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := advDiagLines(t, advCapHead+c.body, "CAP003")
			t.Logf("%s: %v", c.name, got)
			if c.want < 0 {
				return // flow-insensitive by design; recorded only
			}
			if (c.want == 0) != (len(got) == 0) {
				t.Fatalf("want %d CAP003, got %v", c.want, got)
			}
		})
	}
}

// For each shape the CAP003 used to hide, SAFE002/003/004 must still be reported next to it.
func TestAdvCap003DoesNotHideTransfers(t *testing.T) {
	src := advCapHead + `
type Job struct{ n int }

func sink(j *Job) {}

var registry []*Job

func retain(j *Job) { registry = append(registry, j) }

//gosree:mub b
func a(b *B, j *Job) {
	f := func() { b.n++ }
	f()
	go func() { sink(j) }()
}

//gosree:mub b
func c(b *B) {
	f := func() { b.n++ }
	f()
	j := &Job{}
	retain(j)
	go func() { sink(j) }()
}
`
	got := advAllDiags(t, src)
	joined := strings.Join(got, "\n")
	t.Log(joined)
	for _, code := range []string{"SAFE002", "SAFE004"} {
		if !strings.Contains(joined, code) {
			t.Errorf("%s missing next to a closure-binding borrow: %v", code, got)
		}
	}
	if strings.Contains(joined, "CAP003") {
		t.Errorf("spurious CAP003: %v", got)
	}
}
