package ownership_test

import (
	"strings"
	"testing"
)

// A handler calls a variable that holds one of several functions. If any of them writes memory
// the handler shares with every concurrent request, that is a handler write. The check used to
// follow the first closure the variable was seen to hold.
const handlerTargetsHead = `package p

import "net/http"

type H struct {
	n      int
	inject func(k int)
	take   func(x *H)
}

var hook func(x *H)

var hook2 func(k int)

func reg(mux *http.ServeMux, h *H) { mux.HandleFunc("/", h.serve) }

func run(f func()) { f() }

func (h *H) bump(k int) { h.n += k }

func (h *H) peek(k int) { _ = h.n + k }
`

func handlerTargetsDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, handlerTargetsHead+"\nfunc (h *H) serve(w http.ResponseWriter, r *http.Request) {\n\tc := r.Method == \"POST\"\n\t_ = c\n\t"+body+"\n}\n", "SAFE008")
}

func TestHandlerCallsOneOfSeveralFunctions(t *testing.T) {
	for name, body := range map[string]string{
		"reads, or writes": "k := 1\n\tf := func(x *H) { _ = x.n + k }\n\tif c {\n\t\tf = func(x *H) { x.n += k }\n\t}\n\tf(h)",
		"writes, or reads": "k := 1\n\tf := func(x *H) { x.n += k }\n\tif c {\n\t\tf = func(x *H) { _ = x.n + k }\n\t}\n\tf(h)",
		"an injected function, or a method that writes": "f := h.inject\n\tif f == nil {\n\t\tf = h.bump\n\t}\n\tf(1)",
		// A function read from the handler may be a closure over the handler, whatever it is handed.
		"an injected function, or a method that reads":      "f := h.inject\n\tif f == nil {\n\t\tf = h.peek\n\t}\n\tf(1)",
		"a hook that is handed h, or a function that reads": "g := hook\n\tif g == nil {\n\t\tg = func(x *H) { _ = x.n }\n\t}\n\tg(h)",
		"an injected function that is handed h":             "g := h.take\n\tif g == nil {\n\t\tg = func(x *H) { _ = x.n }\n\t}\n\tg(h)",
	} {
		got := handlerTargetsDiags(t, body)
		if len(got) == 0 {
			t.Errorf("%s: a function the handler may call writes h, and it was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"reads, or reads": "k := 1\n\tf := func(x *H) { _ = x.n + k }\n\tif c {\n\t\tf = func(x *H) { _ = x.n - k }\n\t}\n\tf(h)",
		"a hook that is handed a number, or a function that reads": "g := hook2\n\tif g == nil {\n\t\tg = func(k int) { _ = k }\n\t}\n\tg(1)",
	} {
		if got := handlerTargetsDiags(t, body); len(got) != 0 {
			t.Errorf("%s: no function the handler may call writes h, is handed it or comes from it, and it was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
