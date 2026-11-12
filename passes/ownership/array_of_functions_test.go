package ownership_test

import (
	"strconv"
	"strings"
	"testing"
)

const arrayOfFunctionsHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

func second(a [2]func(*T), x *T) { a[1](x) }

func mk() [2]func(*T) { return [2]func(*T){noop, p.Store} }

func mkQuiet() [2]func(*T) { return [2]func(*T){noop, noop} }

func mk2() ([2]func(*T), bool) { return [2]func(*T){noop, p.Store}, true }
`

// arrayOfFunctionsDiags gives the uses of x after a publish that are reported in use.
func arrayOfFunctionsDiags(t *testing.T, body string) []string {
	t.Helper()
	headLines := strings.Count(arrayOfFunctionsHead, "\n")
	var out []string
	for _, d := range advDiagLines(t, arrayOfFunctionsHead+"\nfunc use() int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", "is used after it was published") {
		line, _, _ := strings.Cut(d, ":")
		if n, err := strconv.Atoi(line); err != nil || n > headLines {
			out = append(out, d)
		}
	}
	return out
}

// A function called out of a local array publishes what the call hands it when an element of the
// array may be a publisher, however the array came to hold it.
func TestCallOfAnElementOfAnArrayOfFunctions(t *testing.T) {
	for name, body := range map[string]string{
		"built as a value":                  "arr := [2]func(*T){noop, p.Store}\n\tarr[1](x)",
		"an element assigned":               "var arr [2]func(*T)\n\tarr[0] = p.Store\n\tarr[0](x)",
		"a closure writes an element":       "var arr [2]func(*T)\n\tw := func() { arr[0] = p.Store }\n\tw()\n\tarr[0](x)",
		"ranged over":                       "arr := [2]func(*T){noop, p.Store}\n\tfor _, f := range arr {\n\t\tf(x)\n\t}",
		"copied from another array":         "a := [2]func(*T){noop, p.Store}\n\tb := a\n\tb[1](x)",
		"passed by value to a function":     "second([2]func(*T){noop, p.Store}, x)",
		"returned by value from a function": "arr := mk()\n\tarr[1](x)",
		"returned as one of two results":    "arr, _ := mk2()\n\tarr[1](x)",
		"control: a slice literal":          "fs := []func(*T){noop, p.Store}\n\tfs[1](x)",
		"control: a variable":               "f := p.Store\n\tf(x)",
	} {
		if got := arrayOfFunctionsDiags(t, body); len(got) == 0 {
			t.Errorf("%s: an element may publish x, and the use after the call was not reported", name)
		}
	}
	for name, body := range map[string]string{
		"built as a value, no publisher":            "arr := [2]func(*T){noop, noop}\n\tarr[1](x)",
		"a closure writes an element, no publisher": "var arr [2]func(*T)\n\tw := func() { arr[0] = noop }\n\tw()\n\tarr[0](x)",
		"ranged over, no publisher":                 "arr := [2]func(*T){noop, noop}\n\tfor _, f := range arr {\n\t\tf(x)\n\t}",
		"returned by value, no publisher":           "arr := mkQuiet()\n\tarr[1](x)",
		"another array holds the publisher":         "arr := [2]func(*T){noop, noop}\n\tother := [2]func(*T){noop, p.Store}\n\t_ = other\n\tarr[1](x)",
	} {
		if got := arrayOfFunctionsDiags(t, body); len(got) != 0 {
			t.Errorf("%s: no element of the array publishes, and a use after publish was reported: %s", name, strings.Join(got, "; "))
		}
	}
}
