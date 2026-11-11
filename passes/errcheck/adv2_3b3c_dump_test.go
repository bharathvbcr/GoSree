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

type adv2RecT struct{}

func (r *adv2RecT) Errorf(string, ...any) {}

// TestAdv23b3cDump prints diagnostics of the packages in ADV2_PKGS as pkg:file:line: message.
func TestAdv23b3cDump(t *testing.T) {
	pkgs := os.Getenv("ADV2_PKGS")
	if pkgs == "" {
		t.Skip("ADV2_PKGS not set")
	}
	for _, p := range strings.Split(pkgs, ",") {
		res := analysistest.Run(&adv2RecT{}, analysistest.TestData(), errcheck.Analyzer, p)
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
