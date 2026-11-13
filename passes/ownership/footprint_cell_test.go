package ownership_test

import (
	"strings"
	"testing"
)

// A value stored into a local variable's cell is followed through the cell's loads, but never into
// a goroutine that captures the cell: that goroutine's accesses are not the walk's own. Following it
// once hid the child's write in the race corpus's TestRaceCaseTypeBody (an *int boxed in an
// interface, written by the goroutine through a type switch, while the parent writes it).
func TestCellValueIsNotFollowedIntoAnotherGoroutine(t *testing.T) {
	racy := "package p\n\nfunc f() {\n\tvar x, y int\n\tvar i any = &x\n\tc := make(chan int, 1)\n\tgo func() {\n\t\tswitch i := i.(type) {\n\t\tcase *int:\n\t\t\t*i = y\n\t\t}\n\t\tc <- 1\n\t}()\n\tx = y\n\t<-c\n}\n"
	if got := advDiagLines(t, racy, "SAFE003"); len(got) == 0 {
		t.Fatal("the parent's write of x races with the goroutine's write through i, and it was not reported")
	}
}

// The slice a captured variable holds (make with a constant length is an array, a slice of it, and
// the variable's cell) designates the same memory as the cell's contents: disjoint constant
// indices on the two sides are not a race, and overlapping ones still are.
func TestCapturedSliceOfAnArrayIsWalkedThroughItsCell(t *testing.T) {
	src := func(parent string) string {
		return "package p\n\nfunc f() {\n\tch := make(chan bool, 1)\n\ta := make([]int, 2)\n\tgo func() {\n\t\ta[0] = 1\n\t\tch <- true\n\t}()\n\t" + parent + "\n\t<-ch\n}\n"
	}
	if got := advDiagLines(t, src("_ = a[1]"), "SAFE003"); len(got) != 0 {
		t.Errorf("a[0] in the goroutine and a[1] in the parent do not overlap, and it was reported: %s", strings.Join(got, "; "))
	}
	if got := advDiagLines(t, src("_ = a[0]"), "SAFE003"); len(got) == 0 {
		t.Error("the parent reads a[0] while the goroutine writes it, and it was not reported")
	}
	if got := advDiagLines(t, src("_ = len(a)\n\ta[1] = 2\n\t_ = a[0]"), "SAFE003"); len(got) == 0 {
		t.Error("the parent reads a[0] (after other accesses) while the goroutine writes it, and it was not reported")
	}
}
