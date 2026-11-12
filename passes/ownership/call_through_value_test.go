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

type W struct{ t *T }

var sharedW = &W{}

// wrap returns fresh memory that holds its parameter; wrap2 the same as the first of two results.
func wrap(x *T) *W { return &W{t: x} }

func wrap2(x *T) (*W, bool) { return &W{t: x}, true }

func old(*T) *W { return sharedW }

// attach joins its parameters and keeps neither.
func attach(w *W, x *T) { w.t = x }

type maker struct {
	wrap   func(*T) *W
	wrap2  func(*T) (*W, bool)
	attach func(*W, *T)
}

// Open has callers this package does not show: what f may be is not known here.
func Open(f func(*T)) {
	x := &T{}
	f(x)
	go func() { x.n++ }()
}

type srv struct{ n int }

func register(mux *http.ServeMux, s, sArg, sFrame *srv) {
	setN := func(p *srv) { p.n = 1 }
	mux.HandleFunc("/arg", func(w http.ResponseWriter, r *http.Request) {
		setN(sArg)
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		var h struct{ f func() }
		h.f = func() { sFrame.n++ }
		h.f()
	})
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

var cfgRead, cfgWritten = &T{}, &T{}

func pokes() {
	r := holder{f: reads}
	r.f(cfgRead)
	w := holder{f: writesIt}
	w.f(cfgWritten)
}

func newTick() func() {
	n := 0
	return func() { n++ }
}

// tick holds a closure that writes what it captured each time it is called.
var tick = newTick()

func callF(f func()) { f() }

func ticks() { callF(tick) }

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
		case strings.Contains(d, "variable bump"), strings.Contains(d, "variable s is"):
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

// The function a handler calls through a variable is followed like one called by name: a write it
// makes through its parameter to what the handler shares is reported where it is made, and so is
// a write by a closure the handler itself made and keeps in a field.
func TestHandlerCallingAFunctionVariableIsFollowed(t *testing.T) {
	got := advDiagLines(t, callThroughValueHead, "is written here by a handler")
	for _, v := range []string{"sArg", "sFrame"} {
		n := 0
		for _, d := range got {
			if strings.Contains(d, "captured variable "+v+" is written here") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is written by a function its handler calls through a variable; the write must be reported once, got %d of: %s", v, n, strings.Join(got, "; "))
		}
	}
}

// The memory a target joins is joined, and a result is fresh when every target says so: memory
// it allocates, or memory of the arguments it is joined to.
func TestCallThroughAValueJoinsAndReturnsWhatItsTargetsDo(t *testing.T) {
	for name, body := range map[string]string{
		"joined by the function a field holds":         "x := &T{}\n\tw := &W{}\n\tm := maker{attach: attach}\n\tm.attach(w, x)\n\tgo func() { w.t.n++ }()\n\tx.n++",
		"control: joined by a function called by name": "x := &T{}\n\tw := &W{}\n\tattach(w, x)\n\tgo func() { w.t.n++ }()\n\tx.n++",
	} {
		if got := callThroughValueDiags(t, body, "is used after it was"); len(got) != 1 {
			t.Errorf("%s: x is part of what the goroutine was given, and its use after must be reported once, got %d: %s", name, len(got), strings.Join(got, "; "))
		}
	}
	for name, body := range map[string]string{
		"one result":              "x := &T{}\n\tm := maker{wrap: wrap}\n\tw := m.wrap(x)\n\tgo func() { w.t.n++ }()",
		"the first of two":        "x := &T{}\n\tm := maker{wrap2: wrap2}\n\tw, _ := m.wrap2(x)\n\tgo func() { w.t.n++ }()",
		"control: called by name": "x := &T{}\n\tw := wrap(x)\n\tgo func() { w.t.n++ }()",
	} {
		if got := callThroughValueDiags(t, body, "is not owned here"); len(got) != 0 {
			t.Errorf("%s: the result is fresh memory that holds x, which is owned; it was reported as not owned: %s", name, strings.Join(got, "; "))
		}
	}
	body := "x := &T{}\n\tmk := wrap\n\tif c {\n\t\tmk = old\n\t}\n\tw := mk(x)\n\tgo func() { w.t.n++ }()"
	if got := callThroughValueDiags(t, body, "is not owned here"); len(got) != 1 {
		t.Errorf("one of the two functions returns memory a package variable holds; the result is not owned, got %d: %s", len(got), strings.Join(got, "; "))
	}
}

// An argument is written when a target writes it, and the called value when a target is a closure
// that writes something it captured.
func TestCallThroughAValueWritesWhatItsTargetWrites(t *testing.T) {
	got := advDiagLines(t, callThroughValueHead, "is written outside init")
	count := func(v string) int {
		n := 0
		for _, d := range got {
			if strings.Contains(d, "package variable "+v+" ") {
				n++
			}
		}
		return n
	}
	if n := count("cfgRead"); n != 0 {
		t.Errorf("cfgRead is handed to a function that only reads it; it was reported as written: %s", strings.Join(got, "; "))
	}
	if n := count("cfgWritten"); n != 1 {
		t.Errorf("cfgWritten is handed to writesIt through a field; the write must be reported once, got %d of: %s", n, strings.Join(got, "; "))
	}
	if n := count("tick"); n != 1 {
		t.Errorf("tick holds a closure that writes what it captured; handing it to a function that calls it must be reported once, got %d of: %s", n, strings.Join(got, "; "))
	}
}

// Handing a function held in a package variable to a function that calls it is not a write of
// the variable: a function that is not a closure has captured nothing to write.
func TestCallOfAPackageFunctionVariableIsNotAWrite(t *testing.T) {
	if got := advDiagLines(t, callThroughValueHead, "package variable seam"); len(got) != 0 {
		t.Errorf("seam is assigned where it is declared and only called after, through callIt; got: %s", strings.Join(got, "; "))
	}
}
