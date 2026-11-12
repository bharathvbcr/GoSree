package ownership_test

import (
	"strings"
	"testing"
)

// A variable that holds one of two closures is started as a goroutine. Whichever closure it holds
// at that point, what that closure borrows crosses to the goroutine. The lookup used to stop at
// the first closure bound to the variable that borrows anything, so what only the other closure
// borrows was not reported.
func TestGoOfVariableBoundToTwoClosuresReportsEachBorrow(t *testing.T) {
	got := advDiagLines(t, advCapHead+`
//gosree:mub a
//gosree:mub b
func two(a, b *B, c bool) {
	f := func() { a.n++ }
	if c {
		f = func() { b.n++ }
	}
	go f()
}
`, "CAP003")
	joined := strings.Join(got, "\n")
	for _, name := range []string{"'a'", "'b'"} {
		if !strings.Contains(joined, "stack-scoped borrow "+name) {
			t.Errorf("no CAP003 for the borrow %s of a closure the started variable may hold; got %v", name, got)
		}
	}
}

// A borrowed parameter handed to a publishing container (atomic.Pointer.Store) leaves the call it
// was lent for, like a borrowed local does. The check for that used to skip every parameter.
func TestBorrowedParameterPublishedThroughContainer(t *testing.T) {
	got := advDiagLines(t, `package p

import "sync/atomic"

type B struct{ n int }

//gosree:mub b
func pub(p *atomic.Pointer[B], b *B) {
	p.Store(b)
}
`, "CAP003")
	if len(got) != 1 || !strings.Contains(got[0], "stack-scoped borrow 'b'") {
		t.Fatalf("want one CAP003 for the borrowed parameter b published by Store, got %v", got)
	}
}
