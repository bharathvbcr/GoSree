package ownership_test

import (
	"strconv"
	"strings"
	"testing"
)

// A closure that publishes a variable it captures publishes it when it is called, in the function
// that owns the variable. The closure was told that the variable is not its own to publish
// (SAFE002), and the function that calls it was told nothing: the call was a use of the variable
// by a closure that may keep it, and a use of the variable after the call was not a use after
// publish.
const closurePublishHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func run(f func()) { f() }

func pubVia(y *T) {
	f := func() { p.Store(y) }
	f()
}

func readVia(y *T) {
	f := func() { _ = y.n }
	f()
}

func pubDeferred(y *T) {
	defer func() { p.Store(y) }()
	_ = y.n
}

func pubEither(y *T, c bool) {
	z := &T{}
	if c {
		z = y
	}
	w := func() { z.n++ }
	w()
	f := func() { p.Store(z) }
	f()
}
`

// closurePublishDiags gives the diagnostics in use that contain substr.
func closurePublishDiags(t *testing.T, body, substr string) []string {
	t.Helper()
	headLines := strings.Count(closurePublishHead, "\n")
	var out []string
	for _, d := range advDiagLines(t, closurePublishHead+"\nfunc use(c bool) int {\n\tx := &T{}\n\t"+body+"\n}\n", substr) {
		line, _, _ := strings.Cut(d, ":")
		if n, err := strconv.Atoi(line); err != nil || n > headLines {
			out = append(out, d)
		}
	}
	return out
}

func TestUseAfterAClosurePublishesWhatItCaptures(t *testing.T) {
	const after = "is used after it was published"
	for name, body := range map[string]string{
		"called through its variable":                                               "f := func() { p.Store(x) }\n\tf()\n\treturn x.n",
		"called where it is made":                                                   "func() { p.Store(x) }()\n\treturn x.n",
		"the variable is assigned again before":                                     "if c {\n\t\tx = &T{n: 2}\n\t}\n\tf := func() { p.Store(x) }\n\tf()\n\treturn x.n",
		"written after the call":                                                    "f := func() { p.Store(x) }\n\tf()\n\tx.n = 1\n\treturn 0",
		"a closure inside the closure":                                              "f := func() {\n\t\tg := func() { p.Store(x) }\n\t\tg()\n\t}\n\tf()\n\treturn x.n",
		"called twice":                                                              "f := func() { p.Store(x) }\n\tf()\n\tf()\n\treturn 0",
		"a function whose closure publishes its parameter":                          "pubVia(x)\n\treturn x.n",
		"a function whose deferred closure publishes its parameter":                 "pubDeferred(x)\n\treturn x.n",
		"a function whose closure publishes a variable that may hold its parameter": "pubEither(x, c)\n\treturn x.n",
		"control: published where it is used":                                       "p.Store(x)\n\treturn x.n",
	} {
		if got := closurePublishDiags(t, body, after); len(got) == 0 {
			t.Errorf("%s: the call publishes x, and the use after it was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"the closure is not called":           "f := func() { p.Store(x) }\n\t_ = f\n\treturn x.n",
		"used before the call only":           "f := func() { p.Store(x) }\n\tn := x.n\n\tf()\n\treturn n",
		"the closure only reads":              "f := func() { _ = x.n }\n\tf()\n\treturn x.n",
		"another variable is published":       "y := &T{}\n\tf := func() { p.Store(y) }\n\tf()\n\treturn x.n",
		"a function whose closure only reads": "readVia(x)\n\treturn x.n",
		// A deferred closure runs when the function returns: x is read before that.
		"deferred": "defer func() { p.Store(x) }()\n\treturn x.n",
	} {
		if got := closurePublishDiags(t, body, after); len(got) != 0 {
			t.Errorf("%s: x is not published before it is used, and a use after publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
	// The publish stands for what the closure does with the variable: the call is not also an
	// escape of it, and x is this function's own.
	for name, body := range map[string]string{
		"a closure that publishes its own function's x, called once": "f := func() { p.Store(x) }\n\tf()\n\treturn 0",
		"a function whose closure publishes its parameter":           "pubVia(x)\n\treturn 0",
		"a function whose deferred closure publishes its parameter":  "pubDeferred(x)\n\treturn 0",
	} {
		if got := closurePublishDiags(t, body, "but also escapes"); len(got) != 0 {
			t.Errorf("%s: the publish was reported as an escape as well: %s", name, strings.Join(got, "; "))
		}
	}
	// Whether the variable was the closure's to publish is said once, in the closure.
	all := advDiagLines(t, closurePublishHead, "is not owned here")
	if len(all) != 3 {
		t.Errorf("the three closures of the head that publish a parameter's variable are each told once that it is not theirs; got %d: %s", len(all), strings.Join(all, "; "))
	}
}

// Pinned, not wanted: the closure is not called by name where the variable's owner can see it. A
// function it is handed to calls it, or it is one of several a variable may hold. The owner is
// then told nothing about a use after the call, as before; the closure is still told (SAFE002).
func TestUseAfterAClosureCalledOutOfSightIsAKnownMiss(t *testing.T) {
	for name, body := range map[string]string{
		"handed to a function that calls it": "f := func() { p.Store(x) }\n\trun(f)\n\treturn x.n",
		"one of two a variable may hold":     "f := func() {}\n\tif c {\n\t\tf = func() { p.Store(x) }\n\t}\n\tf()\n\treturn x.n",
	} {
		if got := closurePublishDiags(t, body, "is used after it was published"); len(got) != 0 {
			t.Errorf("%s: this is reported now (%s): move the case to the test above", name, strings.Join(got, "; "))
		}
		if got := closurePublishDiags(t, body, "captured variable x is published"); len(got) == 0 {
			t.Errorf("%s: the closure is no longer told that x is not its own to publish", name)
		}
	}
}
