package ownership_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/internal/annot/stale"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

func advAll(t *testing.T, src string) []string {
	t.Helper()
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer, stale.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var out []string
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			out = append(out, fmt.Sprintf("%02d %s", act.Package.Fset.Position(d.Pos).Line, d.Message))
		}
	}
	sort.Strings(out)
	return out
}

// TestAdvProbe prints diagnostics for scratch fixtures named in ADV_PROBE_DIR (one .go file each).
func TestAdvProbe(t *testing.T) {
	dir := os.Getenv("ADV_PROBE_DIR")
	if dir == "" {
		t.Skip("ADV_PROBE_DIR not set")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".go.txt") {
			continue
		}
		b, _ := os.ReadFile(dir + "/" + e.Name())
		fmt.Printf("== %s\n", e.Name())
		for _, l := range advAll(t, string(b)) {
			fmt.Println(l)
		}
	}
}
