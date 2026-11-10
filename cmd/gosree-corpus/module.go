package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

// ModuleReport is the machine-readable result of -module, written to <out>/module-report.json.
type ModuleReport struct {
	GoVersion string `json:"go_version"`
	// GosreeCommit is `git describe --always --dirty` of the gosree checkout the tool was run from.
	GosreeCommit string   `json:"gosree_commit"`
	Dir          string   `json:"dir"`
	Patterns     []string `json:"patterns"`
	Tests        bool     `json:"tests"`
	// Repo, Rev and Subdir name the archived commit, for -git; Rev is the full SHA.
	Repo   string `json:"repo,omitempty"`
	Rev    string `json:"rev,omitempty"`
	Subdir string `json:"subdir,omitempty"`
	// Annotations counts the rows of the -annotate table added to the copy.
	Annotations int            `json:"annotations"`
	Total       PackageStats   `json:"total"`
	Packages    []PackageStats `json:"packages"`
}

// moduleOpts is what -module and -git measure.
type moduleOpts struct {
	dir      string
	git      *gitSource
	table    *annotationTable
	tests    bool
	patterns []string
	out      string
	list     bool
	census   bool
}

type gitSource struct{ repo, rev, subdir string }

// PackageStats is what gosree saw in one package (or, for the total, in all of them).
type PackageStats struct {
	Package string `json:"package"`
	ownership.Stats
	// Hatch is HatchRatio(), stored so the JSON carries it.
	Hatch float64 `json:"hatch_ratio"`
	// Findings counts the diagnostics still reported, by category.
	Findings map[string]int `json:"findings"`
	// Clean counts transfers with no finding at all: Transfers - FlaggedTransfers.
	Clean int `json:"clean_transfers"`
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

func runModule(o moduleOpts) {
	rep := ModuleReport{GoVersion: runtime.Version(), GosreeCommit: gosreeCommit(), Tests: o.tests}
	if o.git != nil {
		short := o.git.rev
		if len(short) > 12 {
			short = short[:12]
		}
		o.dir = filepath.Join(o.out, filepath.Base(filepath.Clean(o.git.subdir))+"@"+short)
		sha, err := archiveRev(o.git.repo, o.git.rev, o.git.subdir, o.dir)
		if err != nil {
			log.Fatal(err)
		}
		rep.Repo, rep.Rev, rep.Subdir = o.git.repo, sha, o.git.subdir
	}
	if o.table != nil {
		if o.table.rev == "" || !strings.HasPrefix(rep.Rev, o.table.rev) {
			log.Fatalf("-annotate: the table was written for #rev %q, not %s", o.table.rev, rep.Rev)
		}
		if err := applyAnnotations(o.dir, o.table.rows); err != nil {
			log.Fatalf("-annotate: %v", err)
		}
		rep.Annotations = len(o.table.rows)
	}
	dir, tests, patterns, out := o.dir, o.tests, o.patterns, o.out
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
	acts := analyze(pkgs)
	if o.census {
		runCensus(pkgs, acts, o.table.rows, abs, out)
	}
	for _, act := range acts {
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

	rep.Dir, rep.Patterns = abs, patterns
	rep.Total = PackageStats{Package: "total", Findings: map[string]int{}}
	for _, ps := range byID {
		ps.Hatch = ps.HatchRatio()
		ps.Clean = ps.Transfers - ps.FlaggedTransfers
		rep.Packages = append(rep.Packages, *ps)
		rep.Total.add(ps.Stats)
		for c, n := range ps.Findings {
			rep.Total.Findings[c] += n
		}
	}
	rep.Total.Hatch = rep.Total.HatchRatio()
	rep.Total.Clean = rep.Total.Transfers - rep.Total.FlaggedTransfers
	sort.Slice(rep.Packages, func(i, j int) bool { return rep.Packages[i].Package < rep.Packages[j].Package })
	if o.table != nil {
		if err := checkApplied(o.table.rows, rep.Total, tests); err != nil {
			for _, f := range listFindings(acts, abs) {
				if f.Category == annot.CategoryMalformed {
					log.Printf("%s: %s", f.Pos, f.Message)
				}
			}
			log.Fatalf("-annotate: %v", err)
		}
	}
	path := writeJSON(out, "module-report.json", rep)
	if o.list {
		writeJSON(out, "module-findings.json", listFindings(acts, abs))
	}
	printModuleSummary(rep, path)
}

// add sums s into the total. It names every Stats field, so a field added to Stats without a line
// here fails TestTotalAddsEveryField instead of totalling 0.
func (ps *PackageStats) add(s ownership.Stats) {
	t := &ps.Stats
	t.Transfers += s.Transfers
	t.FlaggedTransfers += s.FlaggedTransfers
	t.SuppressedTransfers += s.SuppressedTransfers
	t.Suppressed += s.Suppressed
	t.UnsafeScope += s.UnsafeScope
	t.UnsafeStale += s.UnsafeStale
	t.Guarded += s.Guarded
	t.Holds += s.Holds
	t.Imm += s.Imm
	t.Iso += s.Iso
}

// checkApplied fails unless every table row took effect: no directive is malformed (SAFE007), and the
// counts gosree parsed match the table's. A row that anchored but did not parse would otherwise leave
// the run measuring less than the table claims. Without tests, rows in _test.go files are written but
// not loaded, so they are not expected.
func checkApplied(rows []annotation, t PackageStats, tests bool) error {
	if !tests {
		rows = slices.DeleteFunc(slices.Clone(rows), func(a annotation) bool { return strings.HasSuffix(a.file, "_test.go") })
	}
	want := directiveCounts(rows)
	got := map[string]int{"unsafe": t.UnsafeScope, "guarded_by": t.Guarded, "holds": t.Holds, "iso": t.Iso, "imm": t.Imm}
	var bad []string
	for verb := range want {
		if _, ok := got[verb]; !ok {
			bad = append(bad, "unknown directive //safe:"+verb)
		}
	}
	for verb, n := range got {
		if n != want[verb] {
			bad = append(bad, fmt.Sprintf("%s: table has %d, gosree parsed %d", verb, want[verb], n))
		}
	}
	if n := t.Findings[annot.CategoryMalformed]; n > 0 {
		bad = append(bad, fmt.Sprintf("%d malformed directives (%s)", n, annot.CategoryMalformed))
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("the table did not apply as written: %s", strings.Join(bad, "; "))
	}
	return nil
}

// gosreeCommit describes the gosree checkout in the working directory, or "unknown" outside one.
func gosreeCommit() string {
	out, err := exec.Command("git", "describe", "--always", "--dirty").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
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
	fmt.Fprintf(&b, "gosree on %s (%s, %s, tests=%v)\n", rep.Dir, strings.Join(rep.Patterns, " "), rep.GoVersion, rep.Tests)
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
	fmt.Fprintf(&b, "         %d flagged (muted or not), %d clean\n", t.FlaggedTransfers, t.Clean)
	fmt.Fprintf(&b, "         %d findings muted, %d //safe:unsafe directives (%d muted nothing); remaining findings: %s\n",
		t.Suppressed, t.UnsafeScope, t.UnsafeStale, findings(t.Findings))
	fmt.Fprintf(&b, "         checked claims (not counted as escape hatches): %d guarded_by, %d holds, %d imm, %d iso\n",
		t.Guarded, t.Holds, t.Imm, t.Iso)
	if rep.Rev != "" {
		fmt.Fprintf(&b, "  archived %s at %s (%s); %d annotations; gosree %s\n", rep.Subdir, rep.Rev, rep.Repo, rep.Annotations, rep.GosreeCommit)
	}
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
