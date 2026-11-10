// Package stress_test is gocap's adversarial suite: programs written to get past the checker.
//
// Every fixture under testdata/src is real, compiling Go. Each line states what the checker does
// with it today and whether that is right, with a marker in a trailing comment:
//
//	HIT(CODE)           the checker reports CODE here, and should. A regression lock.
//	MISS(CODE): why     the checker reports nothing here, and should report CODE. A known hole.
//	FP(CODE): why       the checker reports CODE here, and should not. A known false alarm.
//
// A line with no marker must be clean, so the accepted controls in a fixture are assertions too.
// A file may also carry, on a line of its own:
//
//	// CRASH: why         an analyzer fails on this package (panic or error) instead of reporting.
//	// WITNESS: race      the file is a runnable program in which `go run -race` finds a data race.
//	// WITNESS: panic     the file is a runnable program that `go run` ends in a panic or fatal error.
//
// CODE is a rule id (SAFE003, BORROW002, CAP003), or NORULE for a hazard no rule claims to cover:
// those record the distance to the guarantee the project aims at, not a broken rule.
//
// The suite stays green while the holes are open, and turns red in both directions: a HIT that
// stops firing is a regression, and a MISS that starts firing is a fix whose marker must be
// promoted to HIT. Every run logs the ledger (hits, misses, false positives, crashes), so a green
// run never reads as "no known holes".
//
// The analyzers run exactly as cmd/gocap runs them (annot, unsafeban, ownership, borrow), through
// the same driver package multichecker uses.
package stress_test

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gocap/internal/annot"
	"github.com/bharathvbcr/gocap/passes/borrow"
	"github.com/bharathvbcr/gocap/passes/ownership"
	"github.com/bharathvbcr/gocap/passes/unsafeban"
)

// cliAnalyzers is the analyzer set of cmd/gocap/main.go, in its order.
func cliAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{annot.Analyzer, unsafeban.Analyzer, ownership.Analyzer, borrow.Analyzer}
}

var (
	markerRE  = regexp.MustCompile(`\b(HIT|MISS|FP)\(([A-Z]+[0-9]+|NORULE)\)`)
	codeRE    = regexp.MustCompile(`^([A-Z]+[0-9]+): `)
	crashRE   = regexp.MustCompile(`^//\s*CRASH: \S`)
	witnessRE = regexp.MustCompile(`^//\s*WITNESS: (race|panic)\b`)
)

// rootsEnv restricts a run to the fixture roots with one of the listed prefixes (comma separated).
// It exists so one area can be worked on while another's fixtures are mid-edit; a filtered run says
// so, and is not a run of the suite.
const rootsEnv = "GOCAP_STRESS_ROOTS"

func selected(root string) bool {
	filter := os.Getenv(rootsEnv)
	if filter == "" {
		return true
	}
	for p := range strings.SplitSeq(filter, ",") {
		if p != "" && strings.HasPrefix(root, p) {
			return true
		}
	}
	return false
}

type lineKey struct {
	file string
	line int
}

// finding is one diagnostic of one analyzer.
type finding struct {
	lineKey
	col      int
	code     string
	analyzer string
	message  string
}

// fixture is one package of testdata/src with what its markers claim and what the analyzers said.
type fixture struct {
	path     string // import path under testdata/src
	hit      map[lineKey]map[string]bool
	miss     map[lineKey]map[string]bool
	fp       map[lineKey]map[string]bool
	crash    bool     // a file carries a CRASH marker
	witness  []string // files carrying a WITNESS marker
	findings []finding
	failures []string // analyzer errors (a panic surfaces as one)
}

// fixtureRoots lists the top-level fixture directories the analyzers can be run over in this
// process. The vendored import roots (github.com, golang.org) hold support packages, not fixtures,
// and a root with a CRASH marker is left to crashRoots: a panicking analyzer takes the whole
// process with it, so those are probed in a child process.
func fixtureRoots(t *testing.T) []string {
	t.Helper()
	roots, _ := splitRoots(t)
	if len(roots) == 0 {
		t.Fatal("no fixtures selected under testdata/src: the suite would pass having checked nothing")
	}
	if f := os.Getenv(rootsEnv); f != "" {
		t.Logf("PARTIAL RUN: %s=%s selects %d fixture roots; this is not a run of the suite", rootsEnv, f, len(roots))
	}
	return roots
}

// crashRoots lists the selected fixture roots that carry a CRASH marker.
func crashRoots(t *testing.T) []string {
	t.Helper()
	_, crash := splitRoots(t)
	return crash
}

func splitRoots(t *testing.T) (plain, crash []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "src"))
	if err != nil {
		t.Fatalf("reading fixtures: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.Contains(e.Name(), ".") || !selected(e.Name()) {
			continue
		}
		if rootHasLine(t, e.Name(), crashRE) {
			crash = append(crash, e.Name())
		} else {
			plain = append(plain, e.Name())
		}
	}
	sort.Strings(plain)
	sort.Strings(crash)
	return plain, crash
}

// rootHasLine reports whether a Go file directly in the fixture root has a line matching re. It
// reads the text, not the syntax tree, so it can be asked about a package no analyzer survives.
func rootHasLine(t *testing.T, root string, re *regexp.Regexp) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "src", root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(src), "\n") {
			if re.MatchString(strings.TrimSpace(line)) {
				return true
			}
		}
	}
	return false
}

// gopath builds the GOPATH the fixtures load under: testdata, plus a generated root holding the
// real cap package, so fixtures import the cap that ships and not a copy that can drift.
func gopath(t *testing.T) string {
	t.Helper()
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "cap", "cap.go"))
	if err != nil {
		t.Fatalf("reading the cap package: %v", err)
	}
	gen := t.TempDir()
	dir := filepath.Join(gen, "src", "github.com", "bharathvbcr", "gocap", "cap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cap.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	return testdata + string(os.PathListSeparator) + gen
}

// analyze loads the named fixture roots (each with its sub-packages) and runs the CLI's analyzers
// over them in one driver invocation. It fails the test on anything that stops a fixture from
// being checked: a load error, a type error, or a root that matched no package.
func analyze(t *testing.T, roots []string) []*fixture {
	t.Helper()
	var patterns []string
	for _, r := range roots {
		patterns = append(patterns, r+"/...")
	}
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  filepath.Join("testdata", "src"),
		Env: append(os.Environ(),
			"GOPATH="+gopath(t), "GO111MODULE=off", "GOPROXY=off", "GOWORK=off", "GOFLAGS="),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatalf("loading fixtures: %v", err)
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Errorf("fixture %s does not compile: %v", p.PkgPath, e)
		}
		seen[strings.SplitN(p.PkgPath, "/", 2)[0]] = true
	}
	for _, r := range roots {
		if !seen[r] {
			t.Errorf("fixture root %s matched no package", r)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	graph, err := checker.Analyze(cliAnalyzers(), pkgs, nil)
	if err != nil {
		t.Fatalf("running the analyzers: %v", err)
	}
	byPath := map[string]*fixture{}
	var out []*fixture
	for _, p := range pkgs {
		fx := readMarkers(t, p)
		byPath[p.PkgPath] = fx
		out = append(out, fx)
	}
	for _, act := range graph.Roots {
		fx := byPath[act.Package.PkgPath]
		if fx == nil {
			continue
		}
		if act.Err != nil {
			fx.failures = append(fx.failures, fmt.Sprintf("%s: %v", act.Analyzer.Name, act.Err))
			continue
		}
		for _, d := range act.Diagnostics {
			// Positions are the physical ones, ignoring //line directives, so a marker sits on the
			// line of text it describes whatever that line claims to be.
			pos := act.Package.Fset.PositionFor(d.Pos, false)
			code := "?"
			if m := codeRE.FindStringSubmatch(d.Message); m != nil {
				code = m[1]
			}
			fx.findings = append(fx.findings, finding{
				lineKey:  lineKey{pos.Filename, pos.Line},
				col:      pos.Column,
				code:     code,
				analyzer: act.Analyzer.Name,
				message:  d.Message,
			})
		}
	}
	for _, fx := range out {
		sort.Slice(fx.findings, func(i, j int) bool {
			a, b := fx.findings[i], fx.findings[j]
			if a.file != b.file {
				return a.file < b.file
			}
			if a.line != b.line {
				return a.line < b.line
			}
			if a.col != b.col {
				return a.col < b.col
			}
			return a.message < b.message
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func readMarkers(t *testing.T, p *packages.Package) *fixture {
	t.Helper()
	fx := &fixture{
		path: p.PkgPath,
		hit:  map[lineKey]map[string]bool{},
		miss: map[lineKey]map[string]bool{},
		fp:   map[lineKey]map[string]bool{},
	}
	add := func(m map[lineKey]map[string]bool, k lineKey, code string) {
		if m[k] == nil {
			m[k] = map[string]bool{}
		}
		m[k][code] = true
	}
	for _, f := range p.Syntax {
		name := p.Fset.PositionFor(f.Pos(), false).Filename
		witnesses := len(fx.witness)
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				text := c.Text
				switch {
				case crashRE.MatchString(text):
					fx.crash = true
				case witnessRE.MatchString(text):
					fx.witness = append(fx.witness, name)
				}
				k := lineKey{name, p.Fset.PositionFor(c.Pos(), false).Line}
				for _, m := range markerRE.FindAllStringSubmatch(text, -1) {
					switch m[1] {
					case "HIT":
						add(fx.hit, k, m[2])
					case "MISS":
						if !markerHasReason(text, m[0]) {
							t.Errorf("%s:%d: %s needs a reason after a colon", name, k.line, m[0])
						}
						add(fx.miss, k, m[2])
					case "FP":
						if !markerHasReason(text, m[0]) {
							t.Errorf("%s:%d: %s needs a reason after a colon", name, k.line, m[0])
						}
						add(fx.fp, k, m[2])
					}
				}
			}
		}
		if len(fx.witness) > witnesses && (f.Name.Name != "main" || !hasMain(f)) {
			t.Errorf("%s: a WITNESS file must be package main and declare func main", name)
		}
	}
	return fx
}

// markerHasReason reports whether a MISS or FP marker is followed, somewhere on its line, by a colon
// and text. A hole recorded without its reason cannot be reviewed.
func markerHasReason(comment, marker string) bool {
	rest := comment[strings.Index(comment, marker)+len(marker):]
	i := strings.Index(rest, ":")
	return i >= 0 && strings.TrimSpace(rest[i+1:]) != ""
}

func hasMain(f *ast.File) bool {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "main" {
			return true
		}
	}
	return false
}

// ledger counts what a set of fixtures records.
type ledger struct {
	hits, misses, falsePositives, crashes int
	byCode                                map[string][3]int // hit, miss, fp
}

// verify checks one fixture's markers against its findings and returns the problems.
func (fx *fixture) verify(l *ledger) []string {
	var problems []string
	rel := func(k lineKey) string { return fmt.Sprintf("%s:%d", shortPath(k.file), k.line) }

	if len(fx.failures) > 0 != fx.crash {
		if fx.crash {
			problems = append(problems, fmt.Sprintf("%s: carries a CRASH marker but every analyzer ran: remove the marker", fx.path))
		}
		for _, f := range fx.failures {
			problems = append(problems, fmt.Sprintf("%s: analyzer failed: %s", fx.path, f))
		}
	}
	if fx.crash {
		l.crashes++
	}

	got := map[lineKey]map[string][]string{}
	for _, f := range fx.findings {
		if got[f.lineKey] == nil {
			got[f.lineKey] = map[string][]string{}
		}
		got[f.lineKey][f.code] = append(got[f.lineKey][f.code], f.message)
	}
	bump := func(code string, i int) {
		c := l.byCode[code]
		c[i]++
		l.byCode[code] = c
	}
	for k, codes := range fx.hit {
		for code := range codes {
			l.hits++
			bump(code, 0)
			if len(got[k][code]) == 0 {
				problems = append(problems, fmt.Sprintf("%s: HIT(%s) but nothing was reported: a regression", rel(k), code))
			}
		}
	}
	for k, codes := range fx.fp {
		for code := range codes {
			l.falsePositives++
			bump(code, 2)
			if len(got[k][code]) == 0 {
				problems = append(problems, fmt.Sprintf("%s: FP(%s) but nothing is reported any more: remove the marker", rel(k), code))
			}
		}
	}
	for k, codes := range fx.miss {
		for code := range codes {
			l.misses++
			bump(code, 1)
			if len(got[k][code]) > 0 {
				problems = append(problems, fmt.Sprintf("%s: MISS(%s) but it is reported now: promote the marker to HIT", rel(k), code))
			}
		}
	}
	for k, codes := range got {
		for code, msgs := range codes {
			if !fx.hit[k][code] && !fx.fp[k][code] {
				problems = append(problems, fmt.Sprintf("%s: unexpected %s: %s", rel(k), code, msgs[0]))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// shortPath trims a fixture file name to the part under testdata/src.
func shortPath(file string) string {
	const marker = "testdata" + string(filepath.Separator) + "src" + string(filepath.Separator)
	if _, after, ok := strings.CutLast(file, marker); ok {
		return after
	}
	return file
}

func (l *ledger) String() string {
	var codes []string
	for c := range l.byCode {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	var b strings.Builder
	fmt.Fprintf(&b, "stress ledger: %d hits locked, %d KNOWN MISSES, %d known false positives, %d known crashes\n",
		l.hits, l.misses, l.falsePositives, l.crashes)
	fmt.Fprintf(&b, "  %-10s %5s %5s %5s\n", "rule", "hit", "miss", "fp")
	for _, c := range codes {
		v := l.byCode[c]
		fmt.Fprintf(&b, "  %-10s %5d %5d %5d\n", c, v[0], v[1], v[2])
	}
	return b.String()
}
