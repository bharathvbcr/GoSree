package ownership_test

import (
	"strconv"
	"strings"
	"testing"
)

// TestHandedBackAddressIndependentOfOrder: a closure get returns either its parameter or the
// pointer variable keep, which holds &f. The address of f handed to get comes back from it, but
// get also reads keep, so what it returns is not only what it was handed: f may be written through
// r, and f() may run unlock. Whether that was seen depended on whether `keep = &f` came before or
// after the first call of get (968c8a7): handedBack decided once, before keptIn found that get
// reads keep. The write after f(s) is reported in both orders.
func TestHandedBackAddressIndependentOfOrder(t *testing.T) {
	const head = `package p

import "sync"

type S struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n int
}

func nop(*S) {}

func unlock(t *S) { t.mu.Unlock() }
`
	for name, body := range map[string]string{
		"call before keep": `
func (s *S) m(c bool) {
	var keep *func(*S)
	get := func(q *func(*S)) *func(*S) {
		r := q
		if !c {
			r = keep
		}
		return r
	}
	f := nop
	g := nop
	_ = get(&f)
	keep = &f
	r := get(&g)
	*r = unlock
	s.mu.Lock()
	f(s)
	s.n++ // REPORT
}
`,
		"keep before call": `
func (s *S) m(c bool) {
	var keep *func(*S)
	get := func(q *func(*S)) *func(*S) {
		r := q
		if !c {
			r = keep
		}
		return r
	}
	f := nop
	g := nop
	keep = &f
	_ = get(&f)
	r := get(&g)
	*r = unlock
	s.mu.Lock()
	f(s)
	s.n++ // REPORT
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
		got := advDiagLines(t, src, "SAFE009")
		found := false
		for _, d := range got {
			if strings.HasPrefix(d, strconv.Itoa(want)+":") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: f may run unlock, and the write at line %d was not reported (got %q)", name, want, got)
		}
	}
}
