package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestHandlerWriteInSyntheticWrapperHasPosition: a handler calls a method of a generic type of
// another package on memory every request shares. The call goes through a wrapper go/ssa
// synthesises, whose instructions have no source position, and the write check follows the handler
// into it. The finding used to be reported at no position at all. Findings of one rule at one
// position are one finding, so every such finding in a package collapsed into the first: the four
// calls marked below were reported as one finding, at no line. Each is now reported at the call in
// the handler's own source that leads into the wrapper, once per call, including two calls of one
// method, and a //gosree:unsafe on such a call mutes it.
func TestHandlerWriteInSyntheticWrapperHasPosition(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"nopos/dep/dep.go": `package dep

type Cache[V any] struct{ m map[string]V }

func New[V any]() *Cache[V] { return &Cache[V]{m: map[string]V{}} }

func (c *Cache[V]) Put(k string, v V) { c.m[k] = v }

func (c *Cache[V]) Drop(k string) { delete(c.m, k) }
`,
		"nopos/nopos.go": `package nopos

import (
	"net/http"

	"nopos/dep"
)

type handler struct {
	ints *dep.Cache[int]
	strs *dep.Cache[string]
	wrap promoted
}

// promoted reaches Put and Drop through a wrapper for the embedded field's methods.
type promoted struct{ *dep.Cache[int] }

// Three more ways into a wrapper: a method value, a promoted method, and a method value of a
// promoted method, which is a wrapper that calls a wrapper.
func (h *handler) third(http.ResponseWriter, *http.Request) {
	put := h.ints.Put
	put("k", 1)        // want "which may write it"
	h.wrap.Put("k", 2) // want "which may write it"
	drop := h.wrap.Drop
	drop("k") // want "which may write it"
}

func (h *handler) first(http.ResponseWriter, *http.Request) {
	h.ints.Put("k", 1) // want "which may write it"
}

func (h *handler) second(http.ResponseWriter, *http.Request) {
	h.strs.Put("k", "v") // want "which may write it"
	h.strs.Drop("k")     // want "which may write it"
	h.strs.Put("j", "w") // want "which may write it"
}

// A finding with a position can be muted where it is: the hatch on the call covers it.
func (h *handler) hatched(http.ResponseWriter, *http.Request) {
	//gosree:unsafe this cache is replaced before the server starts serving
	h.ints.Put("k", 2)
}

func register(mux *http.ServeMux) {
	mux.HandleFunc("/first", (&handler{ints: dep.New[int](), strs: dep.New[string]()}).first)
	mux.HandleFunc("/second", (&handler{ints: dep.New[int](), strs: dep.New[string]()}).second)
	mux.HandleFunc("/hatched", (&handler{ints: dep.New[int](), strs: dep.New[string]()}).hatched)
	mux.HandleFunc("/third", (&handler{ints: dep.New[int](), strs: dep.New[string]()}).third)
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "nopos")
}
