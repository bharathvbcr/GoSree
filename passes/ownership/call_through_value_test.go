package ownership_test

import (
	"strings"
	"testing"
)

// A call through a function value whose targets are all known is the call of those targets. What a
// call by name takes from its callee's summary (the argument is kept, written, owned, started on a
// goroutine) a call through a value did not take at all: every argument escaped and was written,
// whatever the targets do, and a target that starts a goroutine with its argument did not move
// it. On the production code of the reference service the walk knows every target at 230 of the
// 434 calls through a value that hand over memory, and x/tools' whole-module call graph has the
// same targets at each of them (tests/stress/FINDINGS.md, section 5).
const callThroughValueHead = `package p

import "net/http"

type T struct{ n int }

type holder struct {
	f   func(*T)
	run func(func())
}

var kept *T

func reads(x *T) { _ = x.n }

func keeps(x *T) { kept = x }

func writesIt(x *T) { x.n = 1 }

func start(f func()) { go f() }

//gosree:iso j
func take(j *T) { _ = j.n }

func sink(*T) {}

// Open has callers this package does not show: what f may be is not known here.
func Open(f func(*T)) {
	x := &T{}
	f(x)
	go func() { x.n++ }()
}

type srv struct{ n int }

func register(mux *http.ServeMux, s *srv) {
	check := func(r *http.Request) bool { return s != nil && r != nil }
	bump := func() { s.n++ }
	mux.HandleFunc("/reads", func(w http.ResponseWriter, r *http.Request) {
		if check(r) {
			w.WriteHeader(200)
		}
	})
	mux.HandleFunc("/writes", func(w http.ResponseWriter, r *http.Request) {
		bump()
	})
}

var seam = func(x *T) { _ = x.n }

func callIt(f func(*T), x *T) { f(x) }

func viaSeam(x *T) { callIt(seam, x) }
`

func callThroughValueDiags(t *testing.T, body, match string) []string {
	t.Helper()
	return advDiagLinesAfter(t, callThroughValueHead, "\nfunc use(c bool) {\n\t"+body+"\n}\n", match)
}

func TestCallThroughAValueMovesWhatItsTargetMoves(t *testing.T) {
	for name, body := range map[string]string{
		"a callback started by the function a field holds":         "x := &T{}\n\th := holder{run: start}\n\th.run(func() { x.n++ })\n\tx.n++",
		"a callback started by one of two functions":               "x := &T{}\n\tr := func(f func()) { f() }\n\tif c {\n\t\tr = start\n\t}\n\tr(func() { x.n++ })\n\tx.n++",
		"an owned parameter of the function a field holds":         "x := &T{}\n\th := holder{f: take}\n\th.f(x)\n\tx.n++",
		"control: a callback started by a function called by name": "x := &T{}\n\tstart(func() { x.n++ })\n\tx.n++",
	} {
		if got := callThroughValueDiags(t, body, "is used after it was"); len(got) != 1 {
			t.Errorf("%s: the use after the call must be reported once, got %d: %s", name, len(got), strings.Join(got, "; "))
		}
	}
	for name, body := range map[string]string{
		"the function a field holds only reads":          "x := &T{}\n\th := holder{f: reads}\n\th.f(x)\n\tx.n++",
		"the callback is run in place by what is called": "x := &T{}\n\tr := func(f func()) { f() }\n\tr(func() { x.n++ })\n\tx.n++",
	} {
		if got := callThroughValueDiags(t, body, "is used after it was"); len(got) != 0 {
			t.Errorf("%s: nothing is moved, and a use was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

func TestCallThroughAValueKeepsWhatItsTargetKeeps(t *testing.T) {
	const then = "\n\tgo func() { x.n++ }()"
	for name, body := range map[string]string{
		"the function a field holds only reads":        "x := &T{}\n\th := holder{f: reads}\n\th.f(x)" + then,
		"the function a field holds only writes":       "x := &T{}\n\th := holder{f: writesIt}\n\th.f(x)" + then,
		"both functions a variable may hold only read": "x := &T{}\n\tf := reads\n\tif c {\n\t\tf = sink\n\t}\n\tf(x)" + then,
		"control: called by name":                      "x := &T{}\n\treads(x)" + then,
	} {
		if got := callThroughValueDiags(t, body, "also escapes"); len(got) != 0 {
			t.Errorf("%s: no target keeps the argument, and it was reported as escaping: %s", name, strings.Join(got, "; "))
		}
	}
	for name, body := range map[string]string{
		"the function a field holds keeps it": "x := &T{}\n\th := holder{f: keeps}\n\th.f(x)" + then,
		"one of two functions keeps it":       "x := &T{}\n\tf := reads\n\tif c {\n\t\tf = keeps\n\t}\n\tf(x)" + then,
		"control: called by name":             "x := &T{}\n\tkeeps(x)" + then,
	} {
		got := callThroughValueDiags(t, body, "also escapes")
		if len(got) != 1 || !strings.Contains(got[0], "keeps") {
			t.Errorf("%s: the argument is kept by keeps and must be reported as escaping there, got %d: %s", name, len(got), strings.Join(got, "; "))
		}
	}
}

// What is called is not all in sight: every argument is kept and written, as before.
func TestCallThroughAValueOutOfSightKeepsEverything(t *testing.T) {
	got := advDiagLines(t, callThroughValueHead, "also escapes: passed to a dynamic function call")
	if len(got) != 1 {
		t.Errorf("Open calls a parameter its callers supply; the argument escapes there, got %d: %s", len(got), strings.Join(got, "; "))
	}
}

// A handler that calls a function variable it captured writes what every request shares only if
// a function the variable may hold does.
func TestHandlerCallingACapturedFunctionVariable(t *testing.T) {
	got := advDiagLines(t, callThroughValueHead, "which shares it with every concurrent request")
	var onCheck, onBump []string
	for _, d := range got {
		switch {
		case strings.Contains(d, "variable check"):
			onCheck = append(onCheck, d)
		case strings.Contains(d, "variable bump"), strings.Contains(d, "variable s"):
			onBump = append(onBump, d)
		}
	}
	if len(onCheck) != 0 {
		t.Errorf("check reads what it captured and writes nothing; calling it was reported: %s", strings.Join(onCheck, "; "))
	}
	if len(onBump) != 1 {
		t.Errorf("bump writes s, which every request shares; the call must be reported once, got %d of %d: %s", len(onBump), len(got), strings.Join(got, "; "))
	}
}

// Handing a function held in a package variable to a function that calls it is not a write of
// the variable: a function that is not a closure has captured nothing to write.
func TestCallOfAPackageFunctionVariableIsNotAWrite(t *testing.T) {
	if got := advDiagLines(t, callThroughValueHead, "package variable seam"); len(got) != 0 {
		t.Errorf("seam is assigned where it is declared and only called after, through callIt; got: %s", strings.Join(got, "; "))
	}
}
