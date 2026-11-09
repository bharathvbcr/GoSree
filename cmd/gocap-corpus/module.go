package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gocap/passes/ownership"
)

// ModuleReport is the machine-readable result of -module, written to <out>/module-report.json.
type ModuleReport struct {
	GoVersion string         `json:"go_version"`
	Dir       string         `json:"dir"`
	Patterns  []string       `json:"patterns"`
	Tests     bool           `json:"tests"`
	Total     PackageStats   `json:"total"`
	Packages  []PackageStats `json:"packages"`
}

// PackageStats is what gocap saw in one package (or, for the total, in all of them).
type PackageStats struct {
	Package string `json:"package"`
	ownership.Stats
	// Hatch is HatchRatio(), stored so the JSON carries it.
	Hatch float64 `json:"hatch_ratio"`
	// Findings counts the diagnostics still reported, by category.
	Findings map[string]int `json:"findings"`
}

// HatchRatio is SuppressedTransfers / Transfers: how often memory crossing goroutines needed the
// escape hatch. It is 0 when there are no transfers.
func (s PackageStats) HatchRatio() float64 { return ratio(s.SuppressedTransfers, s.Transfers) }

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func runModule(dir string, tests bool, patterns []string, out string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		log.Fatal(err)
	}
	// go/packages runs `go list` in this directory; a missing one surfaces as "fork/exec .../go: no
	// such file or directory", which blames the toolchain. Say what is actually wrong.
	if fi, err := os.Stat(abs); err != nil {
		log.Fatalf("-module %s: %v", dir, err)
	} else if !fi.IsDir() {
		log.Fatalf("-module %s: not a directory", dir)
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Tests: tests, Dir: abs}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		log.Fatalf("load %s: %v", abs, err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		log.Fatalf("%s did not type-check", abs)
	}
	pkgs = distinctPackages(pkgs)
	if len(pkgs) == 0 {
		log.Fatalf("no packages matched %q in %s", patterns, abs)
	}

	byID := map[string]*PackageStats{}
	for _, act := range analyze(pkgs) {
		ps := byID[act.Package.ID]
		if ps == nil {
			ps = &PackageStats{Package: act.Package.ID, Findings: map[string]int{}}
			byID[act.Package.ID] = ps
		}
		for _, d := range act.Diagnostics {
			ps.Findings[d.Category]++
		}
		if act.Analyzer == ownership.Analyzer {
			s, ok := act.Result.(*ownership.Stats)
			if !ok || s == nil {
				log.Fatalf("%s returned no stats for %s", act.Analyzer.Name, act.Package.ID)
			}
			ps.Stats = *s
		}
	}

	rep := ModuleReport{GoVersion: runtime.Version(), Dir: abs, Patterns: patterns, Tests: tests}
	rep.Total = PackageStats{Package: "total", Findings: map[string]int{}}
	for _, ps := range byID {
		ps.Hatch = ps.HatchRatio()
		rep.Packages = append(rep.Packages, *ps)
		rep.Total.Transfers += ps.Transfers
		rep.Total.SuppressedTransfers += ps.SuppressedTransfers
		rep.Total.Suppressed += ps.Suppressed
		rep.Total.UnsafeScope += ps.UnsafeScope
		rep.Total.Guarded += ps.Guarded
		rep.Total.Holds += ps.Holds
		for c, n := range ps.Findings {
			rep.Total.Findings[c] += n
		}
	}
	rep.Total.Hatch = rep.Total.HatchRatio()
	sort.Slice(rep.Packages, func(i, j int) bool { return rep.Packages[i].Package < rep.Packages[j].Package })
	path := writeJSON(out, "module-report.json", rep)
	printModuleSummary(rep, path)
}

// distinctPackages drops the copies go/packages makes when tests are loaded, so no file is counted
// twice: package p also loads as "p [p.test]" (p plus its in-package tests), and "p.test" is the
// generated test main. Keep the test variant over the plain package, keep external "p_test [p.test]"
// packages, and drop the generated mains.
func distinctPackages(pkgs []*packages.Package) []*packages.Package {
	hasTestVariant := map[string]bool{}
	for _, p := range pkgs {
		if strings.HasSuffix(p.ID, ".test]") && p.PkgPath+" ["+p.PkgPath+".test]" == p.ID {
			hasTestVariant[p.PkgPath] = true
		}
	}
	var keep []*packages.Package
	for _, p := range pkgs {
		switch {
		case strings.HasSuffix(p.ID, ".test]"):
			keep = append(keep, p)
		case p.Name == "main" && strings.HasSuffix(p.ID, ".test"):
			// generated test main
		case hasTestVariant[p.PkgPath]:
			// counted through its test variant
		default:
			keep = append(keep, p)
		}
	}
	return keep
}

func printModuleSummary(rep ModuleReport, path string) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "gocap on %s (%s, %s, tests=%v)\n", rep.Dir, strings.Join(rep.Patterns, " "), rep.GoVersion, rep.Tests)
	var shown []PackageStats
	width := len("package")
	quiet := 0
	for _, ps := range rep.Packages {
		if ps.Transfers == 0 && ps.Suppressed == 0 && len(ps.Findings) == 0 {
			quiet++
			continue
		}
		shown = append(shown, ps)
		width = max(width, len(displayName(ps.Package)))
	}
	fmt.Fprintf(&b, "  %-*s %9s %10s %9s  %s\n", width, "package", "transfers", "suppressed", "hatch", "findings")
	for _, ps := range shown {
		fmt.Fprintf(&b, "  %-*s %9d %10d %8.1f%%  %s\n", width, displayName(ps.Package), ps.Transfers,
			ps.SuppressedTransfers, 100*ps.HatchRatio(), findings(ps.Findings))
	}
	if quiet > 0 {
		fmt.Fprintf(&b, "  (%d packages with no transfers, suppressions or findings)\n", quiet)
	}
	t := rep.Total
	fmt.Fprintf(&b, "  total: %d transfers, %d needed //safe:unsafe = %.1f%% (abandon threshold 20%%)\n",
		t.Transfers, t.SuppressedTransfers, 100*t.HatchRatio())
	fmt.Fprintf(&b, "         %d findings muted, %d //safe:unsafe directives; remaining findings: %s\n",
		t.Suppressed, t.UnsafeScope, findings(t.Findings))
	fmt.Fprintf(&b, "         checked claims (not counted as escape hatches): %d guarded_by, %d holds\n",
		t.Guarded, t.Holds)
	b.WriteString("  transfers are go statements, callbacks run on another goroutine (time.AfterFunc, WaitGroup.Go,\n" +
		"  errgroup, net/http handlers), channel sends and //safe:iso arguments\n")
	fmt.Fprintf(&b, "  report: %s\n", path)
	if _, err := os.Stdout.Write(b.Bytes()); err != nil {
		log.Fatalf("write summary: %v", err)
	}
}

// displayName shortens a test variant's ID, "p [p.test]", to "p +tests" for the table; the JSON
// keeps the full ID.
func displayName(id string) string {
	if base, rest, ok := strings.Cut(id, " ["); ok && rest == base+".test]" {
		return base + " +tests"
	}
	return id
}

func findings(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	var cats []string
	for c := range m {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	var parts []string
	for _, c := range cats {
		parts = append(parts, fmt.Sprintf("%s=%d", c, m[c]))
	}
	return strings.Join(parts, " ")
}
