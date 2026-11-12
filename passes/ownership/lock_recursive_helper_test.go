package ownership_test

import (
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

// TestLockHelperRecursingOnAFieldEnds: a helper that stores an unlock into the caller's function
// variable calls itself on a field of its parameter first. Bringing the helper's value back to the
// caller met a longer path at every self-call, so no path repeated and the walk did not end (a
// stack overflow, here within the 16 MB stack this test allows). The walk now gives the value up
// as lost, and the write after the call is reported: the lock may have been released.
func TestLockHelperRecursingOnAFieldEnds(t *testing.T) {
	old := debug.SetMaxStack(16 << 20)
	defer debug.SetMaxStack(old)
	const src = `package p

import "sync"

type S struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n    int
	next *S
}

func (s *S) unlockIt() { s.mu.Unlock() }

func setRecNext(p *func(), t *S, n int) {
	if n > 0 {
		setRecNext(p, t.next, n-1)
		return
	}
	*p = t.unlockIt
}

func (s *S) rec() {
	s.mu.Lock()
	f := func() {}
	setRecNext(&f, s, 3)
	f()
	s.n++ // REPORT
}
`
	want := 0
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "// REPORT") {
			want = i + 1
		}
	}
	got := advDiagLines(t, src, "SAFE009")
	for _, d := range got {
		if strings.HasPrefix(d, strconv.Itoa(want)+":") {
			return
		}
	}
	t.Errorf("the write at line %d after a call that may release the lock was not reported (got %q)", want, got)
}
