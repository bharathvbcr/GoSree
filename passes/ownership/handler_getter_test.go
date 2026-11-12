package ownership_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestHandlerWriteThroughGetterOfAnotherPackage: a handler reaches the memory every request shares
// through a method of another package and passes it to a method that writes it. The walk back to
// the handler's shared memory stopped at a call result unless the callee's body was in this package
// and had one result, so every getter below hid the write. The callee's summary links its result to
// its receiver; the walk reads it now, at the result's own index.
func TestHandlerWriteThroughGetterOfAnotherPackage(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"getter/dep/dep.go": `package dep

type Loop struct{ runs int }

func (l *Loop) Run() { l.runs++ }

func NewLoop() *Loop { return &Loop{} }

type Gateway struct {
	Loop  *Loop
	plain *Loop
}

func New() *Gateway { return &Gateway{Loop: &Loop{}, plain: &Loop{}} }

func (g *Gateway) Plain() *Loop { return g.plain }

func (g *Gateway) Lookup() (*Loop, bool) { return g.plain, g.plain != nil }

// Pair returns a loop nothing else holds and the shared one, in that order.
func (g *Gateway) Pair() (*Loop, *Loop) { return &Loop{}, g.plain }

func (g *Gateway) Self() *Gateway { return g }
`,
		"getter/getter.go": `package getter

import (
	"net/http"

	"getter/dep"
)

type handler struct{ gw *dep.Gateway }

func (h *handler) viaField(http.ResponseWriter, *http.Request) {
	l := h.gw.Loop
	l.Run() // want "is passed to .*Loop..Run, which may write it"
}

func (h *handler) viaGetter(http.ResponseWriter, *http.Request) {
	l := h.gw.Plain()
	l.Run() // want "is passed to .*Loop..Run, which may write it"
}

func (h *handler) viaTuple(http.ResponseWriter, *http.Request) {
	l, ok := h.gw.Lookup()
	if !ok {
		return
	}
	l.Run() // want "is passed to .*Loop..Run, which may write it"
}

func (h *handler) viaPairFresh(http.ResponseWriter, *http.Request) {
	fresh, _ := h.gw.Pair()
	fresh.Run() // the first result is a new loop: not a finding
}

func (h *handler) viaPairShared(http.ResponseWriter, *http.Request) {
	_, shared := h.gw.Pair()
	shared.Run() // want "is passed to .*Loop..Run, which may write it"
}

func (h *handler) viaChain(http.ResponseWriter, *http.Request) {
	l := h.gw.Self().Plain()
	l.Run() // want "is passed to .*Loop..Run, which may write it"
}

func (h *handler) viaFresh(http.ResponseWriter, *http.Request) {
	l := dep.NewLoop()
	l.Run() // made for this request: not a finding
}

func register(mux *http.ServeMux) {
	mux.HandleFunc("/field", (&handler{gw: dep.New()}).viaField)
	mux.HandleFunc("/getter", (&handler{gw: dep.New()}).viaGetter)
	mux.HandleFunc("/tuple", (&handler{gw: dep.New()}).viaTuple)
	mux.HandleFunc("/pair-fresh", (&handler{gw: dep.New()}).viaPairFresh)
	mux.HandleFunc("/pair-shared", (&handler{gw: dep.New()}).viaPairShared)
	mux.HandleFunc("/chain", (&handler{gw: dep.New()}).viaChain)
	mux.HandleFunc("/fresh", (&handler{gw: dep.New()}).viaFresh)
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "getter")
}

// TestHandlerWriteThroughRecursiveGetter: getters of the handler's own package that recurse. A
// (value, ok) getter was not followed at all; a function's results are now solved as a fixpoint, so
// direct recursion, mutual recursion and a recursion that swaps its arguments all lead back to the
// shared loop. A new wrapper around the loop is not the loop: writing the wrapper's own field is
// clean. Writing through the wrapper to the loop is a write this walk still does not see.
func TestHandlerWriteThroughRecursiveGetter(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"getterrec/getterrec.go": `package getterrec

import "net/http"

type loop struct{ runs int }

type box struct {
	l *loop
	n int
}

type handler struct{ l *loop }

func (h *handler) rec(n int) (*loop, bool) {
	if n == 0 {
		return h.l, true
	}
	return h.rec(n - 1)
}

func (h *handler) a(n int) (*loop, bool) {
	if n == 0 {
		return h.l, true
	}
	return h.b(n - 1)
}

func (h *handler) b(n int) (*loop, bool) {
	if n == 0 {
		return nil, false
	}
	return h.a(n - 1)
}

func swap(x, y *loop, n int) *loop {
	if n == 0 {
		return x
	}
	return swap(y, x, n-1)
}

func (h *handler) boxed() *box { return &box{l: h.l} }

func (h *handler) serveRec(http.ResponseWriter, *http.Request) {
	l, _ := h.rec(3)
	l.runs++ // want "is written here by a handler"
}

func (h *handler) serveA(http.ResponseWriter, *http.Request) {
	l, _ := h.a(3)
	l.runs++ // want "is written here by a handler"
}

func (h *handler) serveB(http.ResponseWriter, *http.Request) {
	l, ok := h.b(3)
	if ok {
		l.runs++ // want "is written here by a handler"
	}
}

func (h *handler) serveSwap(http.ResponseWriter, *http.Request) {
	l := swap(&loop{}, h.l, 1)
	l.runs++ // want "is written here by a handler"
}

func (h *handler) serveBoxOwn(http.ResponseWriter, *http.Request) {
	b := h.boxed()
	b.n++ // the box is new: not a finding
}

func (h *handler) serveBoxContents(http.ResponseWriter, *http.Request) {
	b := h.boxed()
	b.l.runs++ // the box holds the shared loop; not reported (a known miss, lk_w_getter)
}

func register(mux *http.ServeMux) {
	mux.HandleFunc("/rec", (&handler{l: &loop{}}).serveRec)
	mux.HandleFunc("/a", (&handler{l: &loop{}}).serveA)
	mux.HandleFunc("/b", (&handler{l: &loop{}}).serveB)
	mux.HandleFunc("/swap", (&handler{l: &loop{}}).serveSwap)
	mux.HandleFunc("/box", (&handler{l: &loop{}}).serveBoxOwn)
	mux.HandleFunc("/boxc", (&handler{l: &loop{}}).serveBoxContents)
}
`})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "getterrec")
}

// getterChain is a package whose handler calls the last of depth getters, each with two results and
// two returns that both call the one before. leaf is what the first getter returns; want is the
// expectation comment on the handler's write.
func getterChain(depth int, leaf, want string) string {
	var b strings.Builder
	b.WriteString(`package getterchain

import (
	"errors"
	"net/http"
)

var errX = errors.New("x")

type loop struct{ runs int }

type handler struct {
	l    *loop
	fail bool
}

func (h *handler) g0() (*loop, error) {
	if h.fail {
		return nil, errX
	}
	return `)
	b.WriteString(leaf)
	b.WriteString(", nil\n}\n\n")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, "func (h *handler) g%d() (*loop, error) {\n\tif h.fail {\n\t\treturn h.g%d()\n\t}\n\tl, err := h.g%d()\n\treturn l, err\n}\n\n", i, i-1, i-1)
	}
	fmt.Fprintf(&b, "func (h *handler) serve(http.ResponseWriter, *http.Request) {\n\tl, err := h.g%d()\n\tif err != nil {\n\t\treturn\n\t}\n\tl.runs++%s\n}\n\n", depth, want)
	b.WriteString("func register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/\", (&handler{l: &loop{}}).serve)\n}\n")
	return b.String()
}

// TestHandlerWriteGetterChainTerminates: a handler calls the last of 24 getters, each with two
// results and two returns that both call the one before. Following each return once per path walks
// the chain 2^24 times when it leads nowhere, which took 66 s; each function's results are now
// computed once. The chain that ends in the handler's loop is still reported, and the one that ends
// in a new loop is still clean.
//
// The bound is against a chain of one getter analysed just before, not a number of seconds: most of
// either run is loading net/http, which takes as long as the machine is busy. The deep chain may
// take three times the shallow one plus ten seconds; walked once per path it takes a minute more
// than the shallow one on an idle machine.
func TestHandlerWriteGetterChainTerminates(t *testing.T) {
	const depth = 24
	analyse := func(t *testing.T, depth int, leaf, want string) time.Duration {
		t.Helper()
		dir, cleanup, err := analysistest.WriteFiles(map[string]string{"getterchain/getterchain.go": getterChain(depth, leaf, want)})
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		start := time.Now()
		analysistest.Run(t, dir, ownership.Analyzer, "getterchain")
		return time.Since(start)
	}
	for name, c := range map[string]struct{ leaf, want string }{
		"leads to the shared loop": {"h.l", ` // want "is written here by a handler"`},
		"leads to a new loop":      {"&loop{}", ""},
	} {
		t.Run(name, func(t *testing.T) {
			shallow := analyse(t, 1, c.leaf, c.want)
			deep := analyse(t, depth, c.leaf, c.want)
			if limit := 3*shallow + 10*time.Second; deep > limit {
				t.Fatalf("analysing a chain of %d two-return getters took %s; a chain of one took %s (want under %s)", depth, deep, shallow, limit)
			}
		})
	}
}
