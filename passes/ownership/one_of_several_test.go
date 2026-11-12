package ownership_test

import (
	"strings"
	"testing"
)

// Memory loaded from a shared container is handed to a variable that holds one of two closures.
// If either of them writes it, that is a write of shared memory. The call used to be followed
// into the first closure the variable was seen to hold, as if it were the only one.
const oneOfSeveralHead = `package p

import "sync/atomic"

type T struct{ n int }

var cfg atomic.Pointer[T]
`

func oneOfSeveralDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, oneOfSeveralHead+"\nfunc use(c bool, k int) {\n\tv := cfg.Load()\n\t"+body+"\n}\n", "SAFE005")
}

func TestSharedLoadHandedToOneOfSeveralClosures(t *testing.T) {
	for name, body := range map[string]string{
		"reads, or writes": "f := func(t *T) { _ = t.n + k }\n\tif c {\n\t\tf = func(t *T) { t.n += k }\n\t}\n\tf(v)",
		"writes, or reads": "f := func(t *T) { t.n += k }\n\tif c {\n\t\tf = func(t *T) { _ = t.n + k }\n\t}\n\tf(v)",
		"one that writes":  "f := func(t *T) { t.n += k }\n\tf(v)",
	} {
		got := oneOfSeveralDiags(t, body)
		if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "write through shared value") {
			t.Errorf("%s: a closure the variable may hold writes the loaded memory, and it was not reported; got %v", name, got)
		}
	}
	for name, body := range map[string]string{
		"reads, or reads": "f := func(t *T) { _ = t.n + k }\n\tif c {\n\t\tf = func(t *T) { _ = t.n - k }\n\t}\n\tf(v)",
		"one that reads":  "f := func(t *T) { _ = t.n + k }\n\tf(v)",
	} {
		if got := oneOfSeveralDiags(t, body); len(got) != 0 {
			t.Errorf("%s: no closure the variable may hold writes the loaded memory, and it was reported: %v", name, got)
		}
	}
}

// A goroutine started from a variable that may hold one of two closures takes what both capture:
// the check transfers one closure, and a phi or a cell joins what the variable may hold into one
// class, so the other's captures move with it. Pinned because the goroutine check leans on that.
func TestGoOfVariableThatMayBeTwoClosures(t *testing.T) {
	const head = "package p\n\ntype B struct{ n int }\n\nfunc two(c bool) {\n\ta, b := &B{}, &B{}\n\t"
	const tail = "\n\tgo f()\n\ta.n = 1\n\tb.n = 1\n}\n"
	for name, bind := range map[string]string{
		"bound on two paths":             "f := func() { a.n++ }\n\tif c {\n\t\tf = func() { b.n++ }\n\t}",
		"in a cell a closure overwrites": "f := func() { a.n++ }\n\tset := func() { f = func() { b.n++ } }\n\tif c {\n\t\tset()\n\t}",
	} {
		got := strings.Join(advDiagLines(t, head+bind+tail, "SAFE003"), "\n")
		for _, v := range []string{"variable a is used after it was captured by a goroutine", "variable b is used after it was captured by a goroutine"} {
			if !strings.Contains(got, v) {
				t.Errorf("%s: missing %q; got %s", name, v, got)
			}
		}
	}
}
