package ownership_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// The handler write check used to stop quietly at five fixed sizes, and a handler past any of them
// was reported clean: its write was never looked at. Each test below is a handler just past one of
// them, with the write it must still find, and beside it the same shape with nothing shared, which
// must still be clean.

func analyseHandler(t *testing.T, src string) {
	t.Helper()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"caps/caps.go": src,
		// A wrapper of another package, whose body the check does not see.
		"caps/dep/dep.go": "package dep\n\nimport \"net/http\"\n\nfunc Wrap(f func(http.ResponseWriter, *http.Request)) http.Handler { return http.HandlerFunc(f) }\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "caps")
}

// TestHandlerWriteManySharedParams: the handler passes 100 shared pointers down a chain of 105
// methods and the last one writes through one of them. Every newly shared value used to put its
// function on the worklist again, so each method was scanned 101 times, and the check stopped at
// 10000 scans before it reached the last method. A function is now on the worklist once however many
// values are added to it.
func TestHandlerWriteManySharedParams(t *testing.T) {
	const fns, params = 105, 100
	var names, fields, args []string
	for i := range params {
		names = append(names, fmt.Sprintf("p%d", i))
		fields = append(fields, fmt.Sprintf("c%d", i))
		args = append(args, fmt.Sprintf("h.c%d", i))
	}
	var b strings.Builder
	b.WriteString("package caps\n\nimport \"net/http\"\n\n")
	fmt.Fprintf(&b, "type handler struct{ %s *int }\n\n", strings.Join(fields, ", "))
	for i := 1; i < fns; i++ {
		fmt.Fprintf(&b, "func (h *handler) f%d(%s *int) { h.f%d(%s) }\n\n", i, strings.Join(names, ", "), i+1, strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "func (h *handler) f%d(%s *int) {\n\t*p%d = 1 // want \"is written here by a handler\"\n}\n\n", fns, strings.Join(names, ", "), params-1)
	fmt.Fprintf(&b, "func (h *handler) serve(http.ResponseWriter, *http.Request) { h.f1(%s) }\n\n", strings.Join(args, ", "))
	b.WriteString("func register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/\", (&handler{}).serve)\n}\n")
	analyseHandler(t, b.String())
}

// TestHandlerWriteRequeuesItsOwnFrame: a method that calls itself with its two pointers swapped shares
// the second only on the second pass. The function is being scanned when that value is added to it,
// and must be scanned again: it waits in the worklist once at a time, not once ever.
func TestHandlerWriteRequeuesItsOwnFrame(t *testing.T) {
	analyseHandler(t, `package caps

import "net/http"

type handler struct{ a int }

func (h *handler) swap(p, q *int, n int) {
	if n == 0 {
		*q = 1 // want "is written here by a handler"
		return
	}
	h.swap(q, p, n-1)
}

func (h *handler) serve(http.ResponseWriter, *http.Request) {
	local := 0
	h.swap(&h.a, &local, 3)
}

func register(mux *http.ServeMux) {
	mux.HandleFunc("/", (&handler{}).serve)
}
`)
}

// widePhi is a handler that picks a loop in a switch of the given number of cases and writes it. Every
// case makes a new loop; the default takes what last names.
func widePhi(cases int, last, want string) string {
	var b strings.Builder
	b.WriteString("package caps\n\nimport \"net/http\"\n\ntype loop struct{ runs int }\n\ntype handler struct{ l *loop }\n\n")
	b.WriteString("func (h *handler) serve(_ http.ResponseWriter, r *http.Request) {\n\tvar l *loop\n\tswitch r.URL.Path {\n")
	for i := range cases {
		fmt.Fprintf(&b, "\tcase \"/%d\":\n\t\tl = &loop{}\n", i)
	}
	fmt.Fprintf(&b, "\tdefault:\n\t\tl = %s\n\t}\n\tl.runs++%s\n}\n\n", last, want)
	b.WriteString("func register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/\", (&handler{l: &loop{}}).serve)\n}\n")
	return b.String()
}

// TestHandlerWriteThroughWidePhi: the pointer the handler writes through is one of 71 a switch
// chooses between, and only the last is the loop every request shares. The walk back from the write
// used to give up after 64 values, before it reached that one.
func TestHandlerWriteThroughWidePhi(t *testing.T) {
	t.Run("the last choice is shared", func(t *testing.T) {
		analyseHandler(t, widePhi(70, "h.l", ` // want "is written here by a handler"`))
	})
	t.Run("every choice is new", func(t *testing.T) {
		analyseHandler(t, widePhi(70, "&loop{}", ""))
	})
}

// TestHandlerWriteThroughLongChain: the write is 70 pointers away from the handler's receiver. The walk
// back used to give up after 64 steps.
func TestHandlerWriteThroughLongChain(t *testing.T) {
	src := func(start, want string) string {
		return "package caps\n\nimport \"net/http\"\n\ntype node struct {\n\tnext *node\n\truns int\n}\n\ntype handler struct{ n *node }\n\n" +
			"func (h *handler) serve(http.ResponseWriter, *http.Request) {\n\tn := " + start + "\n\tn" + strings.Repeat(".next", 70) + ".runs++" + want + "\n}\n\n" +
			"func register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/\", (&handler{n: &node{}}).serve)\n}\n"
	}
	t.Run("from the receiver", func(t *testing.T) {
		analyseHandler(t, src("h.n", ` // want "is written here by a handler"`))
	})
	t.Run("from a new node", func(t *testing.T) {
		analyseHandler(t, src("&node{}", ""))
	})
}

// TestHandlerWriteThroughLongConstructorChain: the handler is built by the last of 20 constructors,
// each returning what the one before returns. Finding the handler's body used to give up after 16
// steps, and a handler whose body is not found is not checked at all.
func TestHandlerWriteThroughLongConstructorChain(t *testing.T) {
	const depth = 20
	var b strings.Builder
	b.WriteString("package caps\n\nimport \"net/http\"\n\ntype handler struct{ runs int }\n\n")
	b.WriteString("func (h *handler) serve(http.ResponseWriter, *http.Request) {\n\th.runs++ // want \"is written here by a handler\"\n}\n\n")
	b.WriteString("func (h *handler) c0() http.Handler { return http.HandlerFunc(h.serve) }\n\n")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, "func (h *handler) c%d() http.Handler { return h.c%d() }\n\n", i, i-1)
	}
	fmt.Fprintf(&b, "func register(mux *http.ServeMux) {\n\tmux.Handle(\"/\", (&handler{}).c%d())\n}\n", depth)
	analyseHandler(t, b.String())
}

// TestHandlerWriteThroughRecursiveConstructor: a constructor that only ever returns its own result
// builds no handler. Finding the body ends there, where it used to count 16 turns of the cycle.
func TestHandlerWriteThroughRecursiveConstructor(t *testing.T) {
	analyseHandler(t, `package caps

import "net/http"

type handler struct{ runs int }

func (h *handler) forever() http.Handler { return h.forever() }

func register(mux *http.ServeMux) {
	mux.Handle("/", (&handler{}).forever())
}
`)
}

// TestHandlerWriteThroughManyConversions: the handler function is converted between two named
// function types 20 times before it is registered. Seeing through the conversions used to give up
// after 16.
//
// Three loops each counted to 16: the one that finds what a registration runs (spawnedFunc), the one
// that finds the handler's body (handlerRoots), and the one that tells a converted function boxed as
// an http.Handler from a handler value (unwrapToClosure). Registering the function itself reaches
// the second; boxing it reaches the third, which used to take it for a handler value with a
// ServeHTTP method and so missed the receiver it is bound to; handing it to a wrapper of another
// package reaches the first, which used to find no function in the argument and give up.
func TestHandlerWriteThroughManyConversions(t *testing.T) {
	const depth = 20
	conv := "h.serve"
	for i := range depth {
		conv = fmt.Sprintf("%c(%s)", "ab"[i%2], conv)
	}
	for name, register := range map[string]string{
		"registered as a function": `mux.HandleFunc("/", ` + conv + `)`,
		"boxed as a handler":       `mux.Handle("/", http.HandlerFunc(` + conv + `))`,
		"handed to a wrapper":      `mux.Handle("/", dep.Wrap(` + conv + `))`,
	} {
		t.Run(name, func(t *testing.T) { analyseHandler(t, manyConversions(register)) })
	}
}

func manyConversions(register string) string {
	imports := `import "net/http"`
	if strings.Contains(register, "dep.") {
		imports = "import (\n\t\"net/http\"\n\n\t\"caps/dep\"\n)"
	}
	return `package caps

` + imports + `

type a func(http.ResponseWriter, *http.Request)

type b func(http.ResponseWriter, *http.Request)

type handler struct{ runs int }

func (h *handler) serve(http.ResponseWriter, *http.Request) {
	h.runs++ // want "is written here by a handler"
}

func register(mux *http.ServeMux) {
	h := &handler{}
	` + register + `
}
`
}
