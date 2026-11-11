package ownership_test

import (
	"fmt"
	"strings"
	"testing"
)

// An imm value buried 24 type-levels deep (a chain, not a wide struct) is still found: a store
// that overwrites the outermost struct overwrites the immutable value.
func TestAdvImmBuriedInDeepChain(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\n//gosree:imm\ntype Config struct{ port int }\n\n")
	for i := range 23 {
		fmt.Fprintf(&b, "type A%d struct{ x A%d }\n\n", i, i+1)
	}
	b.WriteString("type A23 struct{ c Config }\n\n")
	b.WriteString("func overwrite(a *A0) { *a = A0{} }\n")
	b.WriteString("\n//gosree:imm\ntype Holder struct{ s []A0; m map[string]A0; p *A0; arr [2]A0 }\n")
	b.WriteString("func overwriteSlice(h *Holder) { h.s[0] = A0{} }\n")
	got := advDiagLines(t, b.String(), "overwrites an immutable value")
	t.Logf("%v", got)
	if len(got) == 0 {
		t.Fatalf("no SAFE005 for a store that overwrites an imm value 24 levels down")
	}
}

// A handler factory with many returns: the handler is built behind nine early returns that give
// nothing, one that gives a non-closure, and a tuple-returning variant. Each real closure must be
// followed. The tuple variant is a pre-existing miss (only single-result returns are followed) and
// is asserted as such.
func TestAdvHandlerFactoryManyReturns(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"net/http\"\n\n")
	b.WriteString("func many(n int) http.HandlerFunc {\n")
	for i := range 9 {
		fmt.Fprintf(&b, "\tif n == %d {\n\t\treturn nil\n\t}\n", i)
	}
	b.WriteString("\tif n == 100 {\n\t\treturn http.NotFound\n\t}\n")
	b.WriteString("\tk := 0\n\treturn func(w http.ResponseWriter, r *http.Request) {\n\t\tk++\n\t}\n}\n\n")
	b.WriteString("func tuple() (http.HandlerFunc, error) {\n\tt := 0\n\treturn func(w http.ResponseWriter, r *http.Request) { t++ }, nil\n}\n\n")
	b.WriteString("func reg(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/m\", many(1))\n\th, _ := tuple()\n\tmux.HandleFunc(\"/t\", h)\n}\n")
	got := advDiagLines(t, b.String(), "SAFE008")
	t.Logf("%v", got)
	var sawK, sawT bool
	for _, d := range got {
		sawK = sawK || strings.Contains(d, "captured variable k")
		sawT = sawT || strings.Contains(d, "captured variable t")
	}
	if !sawK {
		t.Errorf("the closure behind 9 early nil returns and a non-closure return was not followed: %v", got)
	}
	if sawT {
		t.Errorf("PRE-EXISTING miss is gone (tuple factories now followed); update this test: %v", got)
	}
}
