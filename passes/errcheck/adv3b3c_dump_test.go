package errcheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// recT records analysistest failures instead of failing: the dump wants the raw diagnostics.
type recT struct{}

func (r *recT) Errorf(string, ...any) {}

// TestAdv3b3cDump prints every diagnostic of the packages named in ADV3B3C_PKGS (comma separated)
// as pkg:file:line: message, sorted, so the pre-fix and post-fix trees can be diffed per engine.
func TestAdv3b3cDump(t *testing.T) {
	pkgs := os.Getenv("ADV3B3C_PKGS")
	if pkgs == "" {
		t.Skip("ADV3B3C_PKGS not set")
	}
	for _, p := range strings.Split(pkgs, ",") {
		rt := &recT{}
		res := analysistest.Run(rt, analysistest.TestData(), errcheck.Analyzer, p)
		var lines []string
		for _, r := range res {
			for _, d := range r.Diagnostics {
				pos := r.Pass.Fset.Position(d.Pos)
				lines = append(lines, fmt.Sprintf("%s:%s:%d: %s", p, filepath.Base(pos.Filename), pos.Line, d.Message))
			}
		}
		sort.Strings(lines)
		for _, l := range lines {
			fmt.Println("DIAG", l)
		}
	}
}
