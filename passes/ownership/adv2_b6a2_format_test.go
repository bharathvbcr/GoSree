package ownership_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/internal/annot/stale"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

const adv2Head = `package p

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"sync/atomic"
)

var _, _, _, _, _, _ = fmt.Sprint, io.Discard, log.Printf, os.Stdout, slog.Info, 0

type Config struct {
	Limit int
	Table any
}

var liveCfg atomic.Pointer[Config]

var sink any

func freeze() {
	liveCfg.Store(&Config{Table: g})
}
`

func adv2Hits(t *testing.T, src string) []string {
	t.Helper()
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": adv2Head + src})
	gr, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer, stale.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var out []string
	for _, act := range gr.Roots {
		for _, d := range act.Diagnostics {
			if (strings.Contains(d.Message, "SAFE002") || strings.Contains(d.Message, "SAFE004")) && strings.Contains(d.Message, "variable g ") {
				out = append(out, fmt.Sprintf("%02d %s", act.Package.Fset.Position(d.Pos).Line, d.Message))
			}
		}
	}
	sort.Strings(out)
	return out
}

type adv2Case struct {
	name string
	src  string
	leak bool // a finding is expected (the table escapes)
}

var adv2Cases = []adv2Case{
	// Leaking shapes: must report.
	{"wrapper with leaking String", `
type wrap struct{ m map[string]int }
func (w wrap) String() string { sink = w.m; return "" }
var g = map[string]int{"a": 1}
func read() { fmt.Println(wrap{g}) }
`, true},
	{"wrapper pointer with leaking String", `
type wrap struct{ m map[string]int }
func (w *wrap) String() string { sink = w.m; return "" }
var g = map[string]int{"a": 1}
func read() { fmt.Println(&wrap{g}) }
`, true},
	{"address of named map with String", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { fmt.Println(&g) }
`, true},
	{"through any variable", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { var x any = g; fmt.Println(x) }
`, true},
	{"slice of named with String", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { fmt.Println([]N{g}) }
`, true},
	{"printf two operands", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
var other = 3
func read() { fmt.Printf("%v %v", g, other) }
`, true},
	{"printf other first", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { fmt.Printf("%v %v", 1, g) }
`, true},
	{"through helper", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func show(v any) { fmt.Println(v) }
func read() { show(g) }
`, true},
	{"through helper variadic", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func show(v ...any) { fmt.Println(v...) }
func read() { show(g) }
`, true},
	{"spread local slice", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { a := []any{g}; fmt.Println(a...) }
`, true},
	{"spread local slice appended", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { var a []any; a = append(a, g); fmt.Println(a...) }
`, true},
	{"fprintln to package writer", `
type keep struct{ b []byte }
func (k *keep) Write(p []byte) (int, error) { sink = k; return len(p), nil }
var w io.Writer = &keep{}
var g = map[string]int{"a": 1}
func read() { fmt.Fprintln(w, g) }
`, false}, // g itself never reaches the writer; w is a different variable
	{"fprintln table as writer field", `
type tw struct{ t map[string]int }
func (k tw) Write(p []byte) (int, error) { sink = k.t; return len(p), nil }
var g = map[string]int{"a": 1}
func read() { fmt.Fprintln(tw{g}, "x") }
`, true},
	{"module function named Println", `
func Println(v any) { sink = v }
var g = map[string]int{"a": 1}
func read() { Println(g) }
`, true},
	{"module method named Println", `
type L struct{}
func (L) Println(v any) { sink = v }
var g = map[string]int{"a": 1}
func read() { L{}.Println(g) }
`, true},
	{"method value fmt.Println", `
var g = map[string]int{"a": 1}
func read() { f := fmt.Println; f(g) }
`, false}, // fmt keeps nothing; precision only
	{"func var shadowing fmt.Println", `
var Pr = func(a ...any) (int, error) { sink = a; return 0, nil }
var g = map[string]int{"a": 1}
func read() { Pr(g) }
`, true},
	{"slog keeps escaping", `
var g = map[string]int{"a": 1}
func read() { slog.Info("x", "t", g) }
`, true},
	{"log.New Printf plain", `
var g = map[string]int{"a": 1}
func read() { l := log.New(os.Stderr, "", 0); l.Printf("%v", g) }
`, false},
	{"logger.Print plain", `
var lg = log.New(os.Stderr, "", 0)
var g = map[string]int{"a": 1}
func read() { lg.Print(g) }
`, false},
	{"Sscan writes into arg", `
var g = map[string]int{"a": 1}
func read() { fmt.Sscan("1", &g) }
`, true},
	{"Sscan to elem pointer", `
var g = new(int)
func read() { fmt.Sscan("1", g) }
`, true},
	{"fmt.Append storing", `
var g = map[string]int{"a": 1}
var out []byte
func read() { out = fmt.Append(out, g) }
`, false}, // out is a byte slice; g itself not kept
	{"Errorf %w plain", `
var g = map[string]int{"a": 1}
var kept error
func read() { kept = fmt.Errorf("x %v", g) }
`, false},
	{"Errorf %w error-typed table", `
type E map[string]int
func (e E) Error() string { return "" }
var g = E{"a": 1}
var kept error
func read() { kept = fmt.Errorf("x %w", g) }
`, true},
	{"phi any", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read(c bool) { var x any = 1; if c { x = g }; fmt.Println(x) }
`, true},
	{"convert to named with String", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = map[string]int{"a": 1}
func read() { fmt.Println(N(g)) }
`, true},
	{"wrapper through local", `
type wrap struct{ m map[string]int }
func (w wrap) String() string { sink = w.m; return "" }
var g = map[string]int{"a": 1}
func read() { w := wrap{g}; fmt.Println(w) }
`, true},
	{"generic helper", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func show[T any](v T) { fmt.Println(v) }
func read() { show(g) }
`, true},
	{"generic helper plain", `
var g = map[string]int{"a": 1}
func show[T any](v T) { fmt.Println(v) }
func read() { show(g) }
`, true},
	{"defer print with String", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { defer fmt.Println(g) }
`, true},
	{"go print with String", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { go fmt.Println(g) }
`, true},
	{"embedded iface in wrapper", `
type wrap struct{ m map[string]int; s fmt.Stringer }
var g = map[string]int{"a": 1}
func read() { fmt.Println(wrap{m: g}) }
`, true},
	{"print field of wrapper plain", `
type wrap struct{ m map[string]int }
var g = map[string]int{"a": 1}
func read() { fmt.Println(wrap{g}.m) }
`, false},
	{"closure printing named", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { f := func() { fmt.Println(g) }; f() }
`, true},
	{"sprint passed on then stored", `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func read() { _ = fmt.Sprint(g) }
`, true},
	{"Fprint to os.Stdout with plain wrapper of writer", `
var g = map[string]int{"a": 1}
func read() { fmt.Fprint(os.Stdout, g) }
`, false},
	{"Fprintf writer is table-holding Write iface", `
type tw struct{ t map[string]int }
func (k *tw) Write(p []byte) (int, error) { sink = k.t; return len(p), nil }
var g = map[string]int{"a": 1}
func read() { var w io.Writer = &tw{g}; fmt.Fprintf(w, "x") }
`, true},
	// Precision shapes: must stay clean.
	{"plain map printed", `
var g = map[string]int{"a": 1}
func read() { fmt.Println(g) }
`, false},
	{"struct literal no method", `
var g = map[string]int{"a": 1}
func read() { fmt.Println(struct{ M map[string]int }{g}) }
`, false},
	{"slice of maps", `
var g = map[string]int{"a": 1}
func read() { fmt.Println([]map[string]int{g}) }
`, false},
	{"address of plain", `
var g = map[string]int{"a": 1}
func read() { fmt.Println(&g) }
`, false},
	{"any var plain", `
var g = map[string]int{"a": 1}
func read() { var x any = g; fmt.Println(x) }
`, false},
	{"spread local plain", `
var g = map[string]int{"a": 1}
func read() { fmt.Println([]any{g}...) }
`, false},
	{"slice of plain structs", `
type row struct{ n int; tags []string }
var g = []row{{1, nil}}
func read() { fmt.Println(g) }
`, false},
	// Original card escapes.
	{"card New returns config", `
type Cfg struct{ Table map[string]int }
var g = map[string]int{"a": 1}
func New() *Cfg { return &Cfg{Table: g} }
`, true},
	{"card closure returns &cfg", `
var g = map[string]int{"a": 1}
func Get() func() *map[string]int { cfg := g; return func() *map[string]int { return &cfg } }
`, true},
	{"card expose", `
var other map[string]int
func expose(m map[string]int) { other = m }
var g = map[string]int{"a": 1}
func Setup() { expose(g) }
`, true},
	{"printed in format then returned", `
var g = map[string]int{"a": 1}
func Get() map[string]int { fmt.Println(g); return g }
`, true},
	{"printed in log.Fatal", `
var g = map[string]int{"a": 1}
func read() { log.Fatalf("%v", g) }
`, false},
}

// The table is stored into the backing array of a []any through the slice variable, then the slice
// is spread into a printing call. formatOperands walked only the stores made through the array
// Alloc's own IndexAddr referrers, so a store made through the Slice value was not seen, the
// operand g was dropped, and the call was judged formatsOnly although fmt runs g's String, which
// keeps it. The correct answer is a finding (aa6d62f reports it). This test failed on b1c8585 and
// on 404bb00, which added it. formatOperands now takes a slice for built in place only when the
// slice goes nowhere but to the printing call, so the last case, where the element arrives through
// a second slice of the array, is reported as well. (An element stored by a helper the slice is
// handed to is still missed, for another reason: see TestAdv2DirectStoreToOtherGlobalPreExisting.)
func TestAdv2SliceStoreThenSpreadBreak(t *testing.T) {
	named := `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
`
	cases := map[string]string{
		"store through slice var": named + `
func read() { s := []any{1, 2}; s[0] = g; fmt.Println(s...) }
`,
		"store through helper, array literal": named + `
func read() { var a [2]any; s := a[:]; s[1] = g; fmt.Println(s...) }
`,
		"stored through a second slice of the array": named + `
func read() { var a [2]any; w := a[:1]; w[0] = g; fmt.Println(a[:]...) }
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if hits := adv2Hits(t, src); len(hits) == 0 {
				t.Fatalf("BREAK: a table stored into a spread []any whose String method keeps it is not reported")
			}
		})
	}
}

// PRE-EXISTING: assigning a table to another package variable inside a function is not an escape
// event (the store is tied to the variable, so markEscapedGlobals skips it), so it is unreported
// with or without a print. Before b1c8585 a sibling fmt.Println(g) happened to hide this miss by
// marking g not init-only; now that the print is ignored the same function is silent. Pinned.
func TestAdv2DirectStoreToOtherGlobalPreExisting(t *testing.T) {
	for name, src := range map[string]string{
		"no print": `
var g = map[string]int{"a": 1}
var keep map[string]int
func read() { keep = g }
`,
		"helper stores into spread slice (silent on aa6d62f too)": `
type N map[string]int
func (n N) String() string { sink = n; return "" }
var g = N{"a": 1}
func fill(s []any) { s[0] = g }
func read() { s := []any{1, 2}; fill(s); fmt.Println(s...) }
`,
		"with print": `
var g = map[string]int{"a": 1}
var keep map[string]int
func read() { fmt.Println(g); keep = g }
`,
	} {
		t.Run(name, func(t *testing.T) {
			if hits := adv2Hits(t, src); len(hits) != 0 {
				t.Fatalf("behaviour changed (improved?): %v", hits)
			}
		})
	}
}

func TestAdv2FormatProbes(t *testing.T) {
	for _, c := range adv2Cases {
		t.Run(c.name, func(t *testing.T) {
			hits := adv2Hits(t, c.src)
			t.Logf("NHITS=%d", len(hits))
			if c.leak && len(hits) == 0 {
				t.Errorf("BREAK/MISS: expected a finding, got none")
			}
			if !c.leak && len(hits) != 0 {
				t.Errorf("unexpected finding: %v", hits)
			}
		})
	}
}
