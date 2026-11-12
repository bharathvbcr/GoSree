package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// TestListMatchesReport: -list and the report must agree on what a finding is. The stale
// analyzer's SAFE007 rows are counted as UnsafeStale in the report and used to be listed as
// findings, and a failed table named them as malformed directives.
func TestListMatchesReport(t *testing.T) {
	dir, err := filepath.Abs("testdata/listfix")
	if err != nil {
		t.Fatal(err)
	}
	pkgs := loadCensusModule(t, dir)
	acts := analyze(pkgs)

	_, total := moduleStats(acts)
	listed := map[string]int{}
	for _, f := range listFindings(acts, dir) {
		listed[f.Category]++
	}
	if total.UnsafeStale != 1 {
		t.Fatalf("fixture: want one stale hatch, got %d", total.UnsafeStale)
	}
	for cat, n := range total.Findings {
		if listed[cat] != n {
			t.Errorf("category %s: report counts %d, -list has %d", cat, n, listed[cat])
		}
	}
	for cat, n := range listed {
		if total.Findings[cat] != n {
			t.Errorf("category %s: -list has %d, report counts %d", cat, n, total.Findings[cat])
		}
	}

	bad := malformedFindings(acts, dir)
	if len(bad) != 1 || !strings.Contains(bad[0].Message, "unknown directive //gosree:bogus") || bad[0].Category != annot.CategoryMalformed {
		t.Errorf("malformed directives named: %+v; want only the bogus one", bad)
	}
}
