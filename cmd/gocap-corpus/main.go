// Command gocap-corpus runs gocap over the Go race detector's own test corpus
// ($GOROOT/src/runtime/race/testdata) and reports how many TestRace* functions it flags and how many
// TestNoRace* functions it leaves clean.
//
// The corpus is copied into <out>/racecorpus on every run and never committed: it is GOROOT content,
// and copying it fresh ties the numbers to the installed Go release. Files that cannot build outside
// GOROOT (cgo, GOROOT-internal imports) are skipped and counted.
//
//	go run ./cmd/gocap-corpus -out build
package main

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gocap/internal/annot"
	"github.com/bharathvbcr/gocap/passes/ownership"
	"github.com/bharathvbcr/gocap/passes/unsafeban"
)

// Report is the machine-readable result, written to <out>/corpus-report.json.
type Report struct {
	GoVersion string   `json:"go_version"`
	Files     int      `json:"files"`
	Skipped   []string `json:"skipped_files"`
	Race      Side     `json:"race"`
	NoRace    Side     `json:"norace"`
}

// Side summarises one half of the corpus.
type Side struct {
	Total int `json:"total"`
	// Flagged counts functions with at least one gocap diagnostic in the function or in a
	// same-package function it references.
	Flagged int `json:"flagged"`
	// ByCategory counts functions per diagnostic category (a function can count in several).
	ByCategory map[string]int `json:"by_category"`
	// ForkJoinOnly counts flagged functions whose every diagnostic is SAFE003-forkjoin.
	ForkJoinOnly int      `json:"forkjoin_only"`
	Unflagged    []string `json:"unflagged"`
	Flags        []Flag   `json:"flags"`
}

// Flag is one flagged function with the categories it was flagged for.
type Flag struct {
	Test       string   `json:"test"`
	Categories []string `json:"categories"`
}

func main() {
	out := flag.String("out", "build", "directory for the corpus copy and the report")
	flag.Parse()
	log.SetFlags(0)

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		log.Fatalf("go env GOROOT: %v", err)
	}
	src := filepath.Join(strings.TrimSpace(string(goroot)), "src", "runtime", "race", "testdata")
	dst, err := filepath.Abs(filepath.Join(*out, "racecorpus"))
	if err != nil {
		log.Fatal(err)
	}
	copied, skipped, err := copyCorpus(src, dst)
	if err != nil {
		log.Fatalf("copy corpus: %v", err)
	}

	// The corpus calls runtime.RaceDisable, which only exists in race builds.
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Tests: true, Dir: dst, BuildFlags: []string{"-race"}}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		log.Fatalf("load corpus: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		log.Fatal("corpus did not type-check")
	}
	var test *packages.Package
	for _, p := range pkgs {
		if p.Name == "race_test" && strings.HasSuffix(p.ID, ".test]") {
			test = p
		}
	}
	if test == nil {
		log.Fatal("no external test package race_test was loaded")
	}

	analyzers := []*analysis.Analyzer{annot.Analyzer, unsafeban.Analyzer, ownership.Analyzer}
	graph, err := checker.Analyze(analyzers, []*packages.Package{test}, nil)
	if err != nil {
		log.Fatalf("analyze: %v", err)
	}
	var diags []analysis.Diagnostic
	for _, act := range graph.Roots {
		if act.Err != nil {
			log.Fatalf("%s failed on %s: %v", act.Analyzer.Name, act.Package.ID, act.Err)
		}
		diags = append(diags, act.Diagnostics...)
	}

	rep := score(test, diags)
	rep.GoVersion = runtime.Version()
	rep.Files = copied
	rep.Skipped = skipped
	data, err := json.Marshal(rep, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(*out, "corpus-report.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	printSummary(rep, path)
}

func copyCorpus(src, dst string) (copied int, skipped []string, err error) {
	if err := os.RemoveAll(dst); err != nil {
		return 0, nil, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, nil, err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, nil, err
	}
	raceCtx := build.Default
	raceCtx.BuildTags = append(append([]string(nil), raceCtx.BuildTags...), "race")
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if ok, err := raceCtx.MatchFile(src, name); err != nil || !ok {
			skipped = append(skipped, name+" (build constraints)")
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return 0, nil, err
		}
		f, err := parser.ParseFile(fset, name, data, parser.ImportsOnly)
		if err != nil {
			return 0, nil, err
		}
		if f.Name.Name != "race_test" {
			skipped = append(skipped, name+" (package "+f.Name.Name+")")
			continue
		}
		if why := unbuildable(f); why != "" {
			skipped = append(skipped, name+" ("+why+")")
			continue
		}
		if err := os.WriteFile(filepath.Join(dst, name), data, 0o644); err != nil {
			return 0, nil, err
		}
		copied++
	}
	ver := strings.TrimPrefix(runtime.Version(), "go")
	if parts := strings.SplitN(ver, ".", 3); len(parts) >= 2 {
		ver = parts[0] + "." + parts[1]
	}
	files := map[string]string{
		"go.mod": "module racecorpus\n\ngo " + ver + "\n",
		"doc.go": "// Package race holds a copy of the Go race detector's test corpus as its external tests.\npackage race\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dst, name), []byte(content), 0o644); err != nil {
			return 0, nil, err
		}
	}
	return copied, skipped, nil
}

// unbuildable says why a corpus file cannot build outside GOROOT, or returns "".
func unbuildable(f *ast.File) string {
	for _, imp := range f.Imports {
		switch {
		case imp.Path.Value == `"C"`:
			return "cgo"
		case strings.HasPrefix(imp.Path.Value, `"internal/`):
			return "imports " + imp.Path.Value
		}
	}
	return ""
}

type funcInfo struct {
	decl  *ast.FuncDecl
	cats  map[string]bool
	calls map[string]bool
}

func score(p *packages.Package, diags []analysis.Diagnostic) Report {
	funcs := map[string]*funcInfo{}
	var order []*funcInfo
	for _, f := range p.Syntax {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			fi := &funcInfo{decl: fd, cats: map[string]bool{}, calls: map[string]bool{}}
			ast.Inspect(fd, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if fn, ok := p.TypesInfo.Uses[id].(*types.Func); ok && fn.Pkg() == p.Types && fn.Parent() == p.Types.Scope() {
						fi.calls[fn.Name()] = true
					}
				}
				return true
			})
			funcs[fd.Name.Name] = fi
			order = append(order, fi)
		}
	}
	for _, d := range diags {
		for _, fi := range order {
			if d.Pos >= fi.decl.Pos() && d.Pos < fi.decl.End() {
				fi.cats[d.Category] = true
				break
			}
		}
	}
	rep := Report{
		Race:   Side{ByCategory: map[string]int{}},
		NoRace: Side{ByCategory: map[string]int{}},
	}
	for _, fi := range order {
		name := fi.decl.Name.Name
		var side *Side
		switch {
		case strings.HasPrefix(name, "TestRace"):
			side = &rep.Race
		case strings.HasPrefix(name, "TestNoRace"):
			side = &rep.NoRace
		default:
			continue
		}
		side.Total++
		cats := reachableCategories(fi, funcs)
		if len(cats) == 0 {
			side.Unflagged = append(side.Unflagged, name)
			continue
		}
		side.Flagged++
		var list []string
		forkOnly := true
		for c := range cats {
			list = append(list, c)
			side.ByCategory[c]++
			if c != ownership.UseAfterSynced {
				forkOnly = false
			}
		}
		if forkOnly {
			side.ForkJoinOnly++
		}
		sort.Strings(list)
		side.Flags = append(side.Flags, Flag{Test: name, Categories: list})
	}
	sort.Strings(rep.Race.Unflagged)
	sort.Strings(rep.NoRace.Unflagged)
	return rep
}

func reachableCategories(root *funcInfo, funcs map[string]*funcInfo) map[string]bool {
	cats := map[string]bool{}
	seen := map[*funcInfo]bool{}
	stack := []*funcInfo{root}
	for len(stack) > 0 {
		fi := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[fi] {
			continue
		}
		seen[fi] = true
		for c := range fi.cats {
			cats[c] = true
		}
		for name := range fi.calls {
			if callee := funcs[name]; callee != nil {
				stack = append(stack, callee)
			}
		}
	}
	return cats
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func printSummary(rep Report, path string) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "gocap race corpus (%s, %d files copied, %d skipped: %s)\n",
		rep.GoVersion, rep.Files, len(rep.Skipped), strings.Join(rep.Skipped, ", "))
	fmt.Fprintf(&b, "  TestRace*   flagged %3d / %3d  (%.1f%%)\n", rep.Race.Flagged, rep.Race.Total, pct(rep.Race.Flagged, rep.Race.Total))
	clean := rep.NoRace.Total - rep.NoRace.Flagged
	fmt.Fprintf(&b, "  TestNoRace* clean   %3d / %3d  (%.1f%%); flagged only for fork-join: %d\n",
		clean, rep.NoRace.Total, pct(clean, rep.NoRace.Total), rep.NoRace.ForkJoinOnly)
	for _, side := range []struct {
		name string
		s    Side
	}{{"TestRace*", rep.Race}, {"TestNoRace*", rep.NoRace}} {
		var cats []string
		for c := range side.s.ByCategory {
			cats = append(cats, c)
		}
		sort.Strings(cats)
		fmt.Fprintf(&b, "  %s by category:", side.name)
		for _, c := range cats {
			fmt.Fprintf(&b, " %s=%d", c, side.s.ByCategory[c])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  missed races (%d): %s\n", len(rep.Race.Unflagged), strings.Join(rep.Race.Unflagged, ", "))
	fmt.Fprintf(&b, "  report: %s\n", path)
	os.Stdout.Write(b.Bytes())
}
