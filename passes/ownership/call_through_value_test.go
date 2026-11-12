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

func newPeek() func() {
	n := 0
	return func() { _ = n }
}

// peek holds a closure that only reads what it captured.
var peek = newPeek()

func callG(f func()) { f() }

func peeks() { callG(peek) }

var cfgVia, cfgViaRead = &T{}, &T{}

func writeVia(x *T) {
	h := holder{f: writesIt}
	h.f(x)
}

func readVia(x *T) {
	h := holder{f: reads}
	h.f(x)
}

func pokesVia() {
	writeVia(cfgVia)
	readVia(cfgViaRead)
}

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
	if n := count("peek"); n != 0 {
		t.Errorf("peek holds a closure that only reads what it captured; handing it to a function that calls it was reported as a write: %s", strings.Join(got, "; "))
	}
	if n := count("cfgVia"); n != 1 {
		t.Errorf("cfgVia is handed to a function that calls writesIt through a field; the write must be reported once, got %d of: %s", n, strings.Join(got, "; "))
	}
	if n := count("cfgViaRead"); n != 0 {
		t.Errorf("cfgViaRead is handed to a function that calls reads through a field; it was reported as written: %s", strings.Join(got, "; "))
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

// A package variable whose address has been handed on can be assigned where this package does
// not show: the functions stored to it by name are not all it may hold, and a call of it keeps
// and writes what it is handed, as a call of something unknown does. The standard library does
// this in every export_test.go (var LstatP = &lstat, hookSetter(&testHookEnterRoundTrip)).
func TestCallThroughAPackageVariableWhoseAddressIsHandedOn(t *testing.T) {
	const use = "\nfunc useProbe() {\n\tx := &T{}\n\tprobe(x)\n\tgo func() { x.n++ }()\n}\n"
	const match = "also escapes: passed to a dynamic function call"
	if got := advDiagLinesAfter(t, callThroughValueHead, "\nvar probe = reads\n"+use, match); len(got) != 0 {
		t.Errorf("probe is assigned where it is declared and nowhere else, and reads keeps nothing; got %d reports: %s", len(got), strings.Join(got, "; "))
	}
	for name, decl := range map[string]string{
		"its address is in an exported variable": "\nvar probe = reads\n\nvar ProbeP = &probe\n",
		"its address is returned":                "\nvar probe = reads\n\nfunc ProbeAddr() *func(*T) { return &probe }\n",
		"its address is handed to a function":    "\nvar probe = reads\n\nfunc Hand(set func(*func(*T))) { set(&probe) }\n",
	} {
		if got := advDiagLinesAfter(t, callThroughValueHead, decl+use, match); len(got) != 1 {
			t.Errorf("%s: probe may be assigned out of sight, so x escapes into the call; it must be reported once, got %d: %s", name, len(got), strings.Join(got, "; "))
		}
	}
}

// A builtin that only reads what it is handed is not a call of something unknown: len and cap of
// memory every request shares write nothing. The builtins that write are writes, as before.
func TestHandlerCallingAReadOnlyBuiltin(t *testing.T) {
	const body = `
type store struct {
	items map[string]int
	list  []int
}

func registerStore(mux *http.ServeMux, sLen, sDel, sDefer *store) {
	mux.HandleFunc("/len", func(w http.ResponseWriter, r *http.Request) {
		_ = len(sLen.items) + cap(sLen.list) + len(sLen.list)
	})
	mux.HandleFunc("/del", func(w http.ResponseWriter, r *http.Request) {
		delete(sDel.items, "k")
	})
	mux.HandleFunc("/defer", func(w http.ResponseWriter, r *http.Request) {
		defer delete(sDefer.items, "k")
	})
}
`
	got := advDiagLinesAfter(t, callThroughValueHead, body, "which shares it with every concurrent request")
	count := func(v string) int {
		n := 0
		for _, d := range got {
			if strings.Contains(d, "variable "+v+" ") {
				n++
			}
		}
		return n
	}
	if n := count("sLen"); n != 0 {
		t.Errorf("len and cap read; sLen was reported %d times: %s", n, strings.Join(got, "; "))
	}
	if n := count("sDel"); n != 1 {
		t.Errorf("delete writes the map sDel holds; it must be reported once, got %d: %s", n, strings.Join(got, "; "))
	}
	if n := count("sDefer"); n != 1 {
		t.Errorf("a deferred delete writes the map sDefer holds; it must be reported once, got %d: %s", n, strings.Join(got, "; "))
	}
}

// A target that publishes its argument publishes it at the call, and one that keeps it besides
// is reported for that, as when it is called by name. A publish is not a write of what is
// published: the function that hands its parameter to a method value of sync.Map writes it no
// more than the one that calls the method.
func TestCallThroughAValuePublishesWhatItsTargetPublishes(t *testing.T) {
	const decl = `
var reg sync.Map

func pubAndKeep(x *T) {
	reg.Store(1, x)
	kept = x
}

func pubOnly(x *T) { reg.Store(1, x) }

func useKeep() {
	x := &T{}
	h := holder{f: pubAndKeep}
	h.f(x)
}

func useOnly() {
	x := &T{}
	h := holder{f: pubOnly}
	h.f(x)
}

var cfgStore, cfgDirect = &T{}, &T{}

func storeVia(x *T) {
	store := reg.Store
	store(1, x)
}

func storeDirect(x *T) { reg.Store(1, x) }

func pokesStore() {
	storeVia(cfgStore)
	storeDirect(cfgDirect)
}
`
	src := strings.Replace(callThroughValueHead, "import \"net/http\"", "import (\n\t\"net/http\"\n\t\"sync\"\n)", 1) + decl
	all := advDiagLines(t, src, "")
	var besides, stored, direct []string
	for _, d := range all {
		switch {
		case strings.Contains(d, "composite literal is published via (*sync.Map).Store but also escapes"):
			besides = append(besides, d)
		case strings.Contains(d, "package variable cfgStore is written"):
			stored = append(stored, d)
		case strings.Contains(d, "package variable cfgDirect is written"):
			direct = append(direct, d)
		}
	}
	if len(besides) != 1 {
		t.Errorf("pubAndKeep publishes its argument and keeps it; the call through the field must say so once (and not for pubOnly), got %d: %s", len(besides), strings.Join(besides, "; "))
	}
	if len(stored) != len(direct) {
		t.Errorf("handing a parameter to a method value of sync.Map is the call of the method: written %d times through the value, %d by name: %s", len(stored), len(direct), strings.Join(append(stored, direct...), "; "))
	}
}
