package ownership_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

func advAllDiags(t *testing.T, src string) []string {
	t.Helper()
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var out []string
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			out = append(out, fmt.Sprintf("%d: %s", act.Package.Fset.Position(d.Pos).Line, d.Message))
		}
	}
	sort.Strings(out)
	return out
}

const advInitOnlyHead = `package p

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"reflect"
	"sync/atomic"
)

var _, _, _, _, _ = json.Marshal, fmt.Sprint, log.Printf, maps.Clone[map[string]int], reflect.DeepEqual

type Config struct {
	Limit int
	Table map[string]int
}

var liveCfg atomic.Pointer[Config]

var g = map[string]int{"a": 1}

func count(m map[string]int) int {
	n := 0
	for range m {
		n++
	}
	return n
}

func freeze() {
	liveCfg.Store(&Config{Table: g})
}

func read() {
`

// A package map that is only read (length, index, range, a read-only callee, formatting) and is
// then published inside an atomic pointer's composite literal. The init-only exemption must stay
// on for a read-only use, so these rows must give no SAFE002/SAFE004/SAFE006.
//
// BREAK: the fmt/log rows fail on the fix and pass on 9161d81. markEscapedGlobals treats a map
// boxed into a ...any parameter of fmt.Sprintf, fmt.Println or log.Printf as escaping, although
// those calls only read it, so a table that is logged anywhere in the package loses its init-only
// status and the Store of a literal holding it is reported.
func TestAdvInitOnlyGlobalReadOnlyUses(t *testing.T) {
	shapes := []struct{ name, body string }{
		{"len", `_ = len(g)`},
		{"index", `_ = g["a"]`},
		{"range", `for k, v := range g { _, _ = k, v }`},
		{"readonly callee", `_ = count(g)`},
		{"local alias", `m := g; _ = m["a"]`},
		{"maps.Keys", `for k := range maps.Keys(g) { _ = k }`},
		{"maps.Equal", `_ = maps.Equal(g, g)`},
		{"Sprintf", `_ = fmt.Sprintf("%v", g)`},
		{"Println", `fmt.Println(g)`},
		{"log.Printf", `log.Printf("%v", g)`},
	}
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			hits := advSafeHits(t, advInitOnlyHead+"\t"+s.body+"\n}\n")
			if len(hits) != 0 {
				t.Fatalf("read-only use %q made the init-only table escape: %v", s.body, hits)
			}
		})
	}
}

// PRE-EXISTING: maps.Clone, json.Marshal and reflect.DeepEqual of a package map are reported as
// a write to it (SAFE006) and the later Store as aliasing it, on 9161d81 and on the fix alike.
// The calls only read the map; the model treats an unknown external callee that takes the map as
// mutating it. This asserts the current behaviour so a change either way is noticed.
func TestAdvInitOnlyGlobalExternalReadersPreExisting(t *testing.T) {
	for _, body := range []string{
		`_ = maps.Clone(g)`,
		`_, _ = json.Marshal(g)`,
		`_ = reflect.DeepEqual(g, g)`,
	} {
		hits := advSafeHits(t, advInitOnlyHead+"\t"+body+"\n}\n")
		if len(hits) == 0 {
			t.Errorf("%q: the pre-existing external-callee report is gone; update this test if that is intended", body)
		}
	}
}

func advSafeHits(t *testing.T, src string) []string {
	t.Helper()
	var hits []string
	for _, d := range advAllDiags(t, src) {
		if strings.Contains(d, "SAFE002") || strings.Contains(d, "SAFE004") || strings.Contains(d, "SAFE006") {
			hits = append(hits, d)
		}
	}
	return hits
}
