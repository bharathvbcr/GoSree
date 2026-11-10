package stress_test

// Scale, complexity and determinism of the analyzers themselves. Programs are generated at test time
// into a temporary module (nothing generated is committed), loaded once, and analysed with the CLI's
// analyzer set through the same go/analysis checker the multichecker uses. Only checker.Analyze is
// timed, never loading; each size is the minimum of several runs, and each shape is measured at
// three sizes so its growth exponent can be read. Wall-time bounds flake on a loaded machine, so the
// assertions are on the RATIO between sizes, with a wide margin, and every timing test is skipped in
// -short mode.
//
//	go test ./tests/stress -run TestScale -v                       committed sizes
//	GOSREE_SCALE_EXPLORE=4 go test ./tests/stress -run TestScaleExplore -v -timeout 60m
//	                                                               sizes x4, logs the table only

import (
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/annot/stale"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

// scaleKnownBug records a measured blowup that is present today; once it is gone the test fails and
// asks for the assertion to be flipped.
func scaleKnownBug(t *testing.T, id, what string, present bool) {
	t.Helper()
	if present {
		t.Logf("KNOWN BUG %s (still present): %s", id, what)
		return
	}
	t.Errorf("%s appears fixed: flip this assertion (%s)", id, what)
}

// scaleModule writes a module named scaletest holding files and returns its directory.
func scaleModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module scaletest\n\ngo 1.27\n"
	vetWrite(t, dir, files)
	return dir
}

// scaleLoad loads every package of the module at dir, failing on any load or type error.
func scaleLoad(t *testing.T, dir string) []*packages.Package {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  dir,
		Env: append(os.Environ(),
			"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	bad := false
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			t.Errorf("%s: %v", p.PkgPath, e)
			bad = true
		}
	})
	if bad || len(pkgs) == 0 {
		t.FailNow()
	}
	return pkgs
}

// scaleRun is one analysis run.
type scaleRun struct {
	dur    time.Duration
	alloc  uint64 // bytes allocated during the run (MemStats.TotalAlloc delta): volume, not peak
	diags  int
	errors []string
	graph  *checker.Graph
}

func scaleAnalyze(t *testing.T, as []*analysis.Analyzer, pkgs []*packages.Package, opts *checker.Options) scaleRun {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	g, err := checker.Analyze(as, pkgs, opts)
	d := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("checker.Analyze: %v", err)
	}
	r := scaleRun{dur: d, alloc: after.TotalAlloc - before.TotalAlloc, graph: g}
	for act := range g.All() {
		if act.Err != nil {
			r.errors = append(r.errors, fmt.Sprintf("%s: %v", act, act.Err))
		}
	}
	for _, act := range g.Roots {
		if act.Err == nil {
			r.diags += len(act.Diagnostics)
		}
	}
	slices.Sort(r.errors)
	return r
}

// scaleMin runs the analysis reps times and keeps the fastest run (allocation volume of that run).
func scaleMin(t *testing.T, as []*analysis.Analyzer, pkgs []*packages.Package, reps int) scaleRun {
	t.Helper()
	var best scaleRun
	for i := range reps {
		r := scaleAnalyze(t, as, pkgs, nil)
		if len(r.errors) > 0 {
			t.Fatalf("analysis failed:\n%s", strings.Join(r.errors, "\n"))
		}
		if i == 0 || r.dur < best.dur {
			best = r
		}
	}
	return best
}

// scaleShape is a program family of size n.
type scaleShape struct {
	name  string
	sizes []int // committed sizes, increasing; the last two are compared
	gen   func(n int) map[string]string
	// maxRatio bounds t(last)/t(previous) (time above the empty-program baseline). Linear growth over a
	// doubling gives about 2; the margin absorbs noise.
	maxRatio float64
	// knownBug, when set, records that this shape grows super-linearly today: the test asserts the
	// exponent is at least knownExp instead of bounding the ratio.
	knownBug string
	knownExp float64
}

const scalePrelude = "package p\n\ntype Job struct {\n\tID   int\n\tNext *Job\n}\n\nvar sink *Job\n\n"

func scaleOne(body string) map[string]string {
	return map[string]string{"p/p.go": scalePrelude + body}
}

func scaleShapes() []scaleShape {
	return []scaleShape{
		{name: "send/use pairs in one function", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F(ch chan *Job) {\n")
			for i := range n {
				fmt.Fprintf(&b, "\tj%d := &Job{}\n\tch <- j%d\n\tj%d.ID = 1\n", i, i, i)
			}
			b.WriteString("}\n")
			return scaleOne(b.String())
		}},
		{name: "one value sent and used N times", sizes: []int{100, 200, 400}, maxRatio: 10, knownBug: "RB-RESEND", knownExp: 2.5, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F(ch chan *Job) {\n\tj := &Job{}\n")
			for i := range n {
				fmt.Fprintf(&b, "\tch <- j\n\tj.ID = %d\n", i)
			}
			b.WriteString("}\n")
			return scaleOne(b.String())
		}},
		{name: "alias chain a1->a0 ... aN->aN-1", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F(ch chan *Job) {\n\ta0 := &Job{}\n")
			for i := 1; i <= n; i++ {
				fmt.Fprintf(&b, "\ta%d := &Job{Next: a%d}\n", i, i-1)
			}
			fmt.Fprintf(&b, "\tch <- a%d\n\ta0.ID = 1\n}\n", n)
			return scaleOne(b.String())
		}},
		{name: "closures nested N deep", sizes: []int{50, 100, 200}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F(ch chan *Job) {\n\tj := &Job{}\n")
			for range n {
				b.WriteString("\tfunc() {\n")
			}
			b.WriteString("\tch <- j\n")
			for range n {
				b.WriteString("\t}()\n")
			}
			b.WriteString("\tj.ID = 1\n}\n")
			return scaleOne(b.String())
		}},
		{name: "N goroutines each capturing 10 variables", sizes: []int{50, 100, 200}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F() {\n")
			for i := range n {
				for k := range 10 {
					fmt.Fprintf(&b, "\tv%d_%d := &Job{}\n", i, k)
				}
				b.WriteString("\tgo func() {\n")
				for k := range 10 {
					fmt.Fprintf(&b, "\t\tv%d_%d.ID++\n", i, k)
				}
				b.WriteString("\t}()\n")
				fmt.Fprintf(&b, "\tv%d_0.ID = 1\n", i)
			}
			b.WriteString("}\n")
			return scaleOne(b.String())
		}},
		{name: "call chain, caller declared first", sizes: []int{100, 200, 400}, maxRatio: 10, knownBug: "RB-CHAIN", knownExp: 1.7, gen: func(n int) map[string]string {
			return scaleOne(scaleChain(n, false))
		}},
		{name: "call chain, callee declared first", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			return scaleOne(scaleChain(n, true))
		}},
		{name: "SCC of N mutually recursive functions", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			for i := range n {
				fmt.Fprintf(&b, "func F%d(j *Job, k int) {\n\tif k > 0 {\n\t\tF%d(j, k-1)\n\t}\n", i, (i+1)%n)
				if i == n-1 {
					b.WriteString("\tsink = j\n")
				}
				b.WriteString("}\n")
			}
			b.WriteString("func Use(ch chan *Job) {\n\tj := &Job{}\n\tF0(j, 3)\n\tch <- j\n}\n")
			return scaleOne(b.String())
		}},
		{name: "N live //gosree:mut borrows", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F() int {\n")
			for i := range n {
				fmt.Fprintf(&b, "\tx%d := %d\n\t//gosree:mut\n\tp%d := &x%d\n", i, i, i, i)
			}
			b.WriteString("\ts := 0\n")
			for i := range n {
				fmt.Fprintf(&b, "\ts += *p%d\n", i)
			}
			b.WriteString("\treturn s\n}\n")
			return scaleOne(b.String())
		}},
		{name: "struct with N pointer fields", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("type Big struct {\n")
			for i := range n {
				fmt.Fprintf(&b, "\tF%d *Job\n", i)
			}
			b.WriteString("}\n\nfunc F(ch chan *Big) {\n\tb := &Big{}\n")
			for i := range n {
				fmt.Fprintf(&b, "\tb.F%d = &Job{}\n", i)
			}
			b.WriteString("\tch <- b\n")
			for i := range n {
				fmt.Fprintf(&b, "\tb.F%d.ID = 1\n", i)
			}
			b.WriteString("}\n")
			return scaleOne(b.String())
		}},
		{name: "N sequential if/else diamonds", sizes: []int{25, 50, 100}, maxRatio: 10, knownBug: "RB-DIAMOND", knownExp: 2.5, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func F(ch chan *Job, c []bool) {\n\tj := &Job{}\n\tk := &Job{}\n")
			for i := range n {
				fmt.Fprintf(&b, "\tif c[%d] {\n\t\tch <- j\n\t} else {\n\t\tk.ID++\n\t\tk = &Job{Next: j}\n\t}\n", i)
			}
			b.WriteString("\tj.ID = 1\n\tk.ID = 2\n}\n")
			return scaleOne(b.String())
		}},
		{name: "N guarded fields with N lock/unlock pairs", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("import \"sync\"\n\ntype G struct {\n\tmu sync.Mutex\n")
			for i := range n {
				fmt.Fprintf(&b, "\t//gosree:guarded_by mu\n\tf%d int\n", i)
			}
			b.WriteString("}\n\nfunc (g *G) F() {\n")
			for i := range n {
				fmt.Fprintf(&b, "\tg.mu.Lock()\n\tg.f%d++\n\tg.mu.Unlock()\n", i)
			}
			b.WriteString("\tg.f0 = 1\n}\n")
			return map[string]string{"p/p.go": strings.Replace(scalePrelude, "\n\ntype Job", "\n\n"+b.String()+"\ntype Job", 1)}
		}},
		{name: "expression tree N deep", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			b.WriteString("func id(j *Job) *Job { return j }\n\nfunc F(ch chan *Job) {\n\tj := &Job{}\n\tk := ")
			b.WriteString(strings.Repeat("id(", n) + "j" + strings.Repeat(")", n))
			b.WriteString("\n\tch <- k\n\tj.ID = 1\n}\n")
			return scaleOne(b.String())
		}},
		{name: "N packages in an import chain", sizes: []int{20, 40, 80}, maxRatio: 10, gen: func(n int) map[string]string {
			files := map[string]string{}
			for i := range n {
				var b strings.Builder
				fmt.Fprintf(&b, "package p%d\n\n", i)
				if i > 0 {
					fmt.Fprintf(&b, "import \"scaletest/p%d\"\n\n", i-1)
					fmt.Fprintf(&b, "type Job = p%d.Job\n\nfunc Keep(j *Job) { p%d.Keep(j) }\n\n", i-1, i-1)
					b.WriteString("func Use(ch chan *Job) {\n\tj := &Job{}\n\tKeep(j)\n\tch <- j\n}\n")
				} else {
					b.WriteString("type Job struct{ ID int }\n\nvar kept *Job\n\nfunc Keep(j *Job) { kept = j }\n")
				}
				files[fmt.Sprintf("p%d/p.go", i)] = b.String()
			}
			return files
		}},
		{name: "N //gosree:unsafe scopes each muting a finding", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			for i := range n {
				fmt.Fprintf(&b, "func F%d(ch chan *Job) {\n\tj := &Job{}\n\tch <- j\n\t//gosree:unsafe scale test\n\tj.ID = 1\n}\n\n", i)
			}
			return scaleOne(b.String())
		}},
		{name: "slice literal of N pointer elements", sizes: []int{500, 1000, 2000}, maxRatio: 10, knownBug: "RB-LITERAL", knownExp: 1.7, gen: func(n int) map[string]string {
			return scaleOne("func Lit(ch chan []*Job) {\n\tjs := []*Job{" + strings.Repeat("{}, ", n) + "}\n\tch <- js\n\tjs[0].ID = 1\n}\n")
		}},
		{name: "N small functions (baseline)", sizes: []int{100, 200, 400}, maxRatio: 10, gen: func(n int) map[string]string {
			var b strings.Builder
			for i := range n {
				fmt.Fprintf(&b, "func F%d(ch chan *Job) {\n\tj := &Job{}\n\tch <- j\n\tj.ID = 1\n}\n\n", i)
			}
			return scaleOne(b.String())
		}},
	}
}

// scaleChain is a call chain F0 -> F1 -> ... -> F(n-1); the last one retains its argument, so the
// fact "retains" must travel n-1 calls back to F0. With calleeFirst the functions are declared from
// F(n-1) down.
func scaleChain(n int, calleeFirst bool) string {
	decl := make([]string, n)
	for i := range n {
		if i == n-1 {
			decl[i] = fmt.Sprintf("func F%d(j *Job) { sink = j }\n", i)
		} else {
			decl[i] = fmt.Sprintf("func F%d(j *Job) { F%d(j) }\n", i, i+1)
		}
	}
	if calleeFirst {
		slices.Reverse(decl)
	}
	return strings.Join(decl, "") + "\nfunc Use(ch chan *Job) {\n\tj := &Job{}\n\tF0(j)\n\tch <- j\n}\n"
}

// scaleRow is the measurement of one shape at one size.
type scaleRow struct {
	n             int
	gosree, ssa   time.Duration // gosree's analyzers; buildssa alone (its prerequisite) on the same packages
	alloc         uint64
	diags, rounds int
}

// scaleMeasureShape measures a shape at the given sizes, min of reps runs each.
func scaleMeasureShape(t *testing.T, sh scaleShape, sizes []int, reps int) []scaleRow {
	t.Helper()
	var rows []scaleRow
	for _, n := range sizes {
		pkgs := scaleLoad(t, scaleModule(t, sh.gen(n)))
		g := scaleMin(t, cliAnalyzers(), pkgs, reps)
		s := scaleMin(t, []*analysis.Analyzer{buildssa.Analyzer}, pkgs, reps)
		rows = append(rows, scaleRow{n: n, gosree: g.dur, ssa: s.dur, alloc: g.alloc, diags: g.diags})
	}
	return rows
}

// scaleExponent is the growth exponent between two rows, on time above the SSA baseline.
func scaleExponent(a, b scaleRow) float64 {
	ta, tb := float64(a.gosree-a.ssa), float64(b.gosree-b.ssa)
	if ta <= 0 || tb <= 0 {
		return math.NaN()
	}
	return math.Log(tb/ta) / math.Log(float64(b.n)/float64(a.n))
}

func scaleTable(name string, rows []scaleRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n  %8s %12s %12s %12s %8s %8s\n", name, "N", "gosree", "buildssa", "alloc MB", "diags", "exp")
	for i, r := range rows {
		exp := "-"
		if i > 0 {
			exp = strconv.FormatFloat(scaleExponent(rows[i-1], r), 'f', 2, 64)
		}
		fmt.Fprintf(&b, "  %8d %12s %12s %12.1f %8d %8s\n", r.n, r.gosree.Round(time.Millisecond), r.ssa.Round(time.Millisecond),
			float64(r.alloc)/(1<<20), r.diags, exp)
	}
	return b.String()
}

// TestScale measures every shape at its committed sizes and bounds the growth between the last two.
func TestScale(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test: skipped in -short mode")
	}
	for _, sh := range scaleShapes() {
		t.Run(sh.name, func(t *testing.T) {
			rows := scaleMeasureShape(t, sh, sh.sizes, 3)
			t.Log(scaleTable(sh.name+" (min of 3)", rows))
			if rows[0].diags == 0 && !strings.Contains(sh.name, "unsafe") && !strings.Contains(sh.name, "borrows") {
				t.Errorf("the generated program produced no findings: the shape does not exercise the checker")
			}
			a, b := rows[len(rows)-2], rows[len(rows)-1]
			// Allocation volume is deterministic, so the blowup assertions use its exponent; time is
			// reported alongside.
			allocExp := math.Log(float64(b.alloc)/float64(a.alloc)) / math.Log(float64(b.n)/float64(a.n))
			if sh.knownBug != "" {
				scaleKnownBug(t, sh.knownBug, fmt.Sprintf("%s: allocation grows with exponent %.2f, time above SSA with exponent %.2f (N %d -> %d)",
					sh.name, allocExp, scaleExponent(a, b), a.n, b.n), allocExp >= sh.knownExp)
				return
			}
			if allocExp > 2.6 {
				t.Errorf("%s: allocation grows with exponent %.2f from N=%d to N=%d (bound 2.6)", sh.name, allocExp, a.n, b.n)
			}
			// Bound the ratio of time above the SSA baseline; tiny absolute times are noise.
			above := func(r scaleRow) time.Duration { return max(r.gosree-r.ssa, 20*time.Millisecond) }
			ratio := float64(above(b)) / float64(above(a))
			if ratio > sh.maxRatio {
				// Wall time on a busy machine overshoots; a real blowup overshoots again. Measure the
				// two sizes once more, with more repetitions, before calling it one.
				again := scaleMeasureShape(t, sh, []int{a.n, b.n}, 7)
				t.Log(scaleTable(sh.name+" (re-measured, min of 7)", again))
				ratio = float64(above(again[1])) / float64(above(again[0]))
			}
			if ratio > sh.maxRatio {
				t.Errorf("%s: time above SSA grew %.1fx from N=%d to N=%d in two measurements (bound %.0fx)", sh.name, ratio, a.n, b.n, sh.maxRatio)
			}
		})
	}
}

// TestScaleExplore runs the shapes at larger sizes (multiplied by GOSREE_SCALE_EXPLORE) and only logs
// the table. It is the instrument for finding the size at which a shape blows up.
func TestScaleExplore(t *testing.T) {
	mult, _ := strconv.Atoi(os.Getenv("GOSREE_SCALE_EXPLORE"))
	if mult <= 0 {
		t.Skip("set GOSREE_SCALE_EXPLORE to a size multiplier")
	}
	only := os.Getenv("GOSREE_SCALE_ONLY")
	for _, sh := range scaleShapes() {
		if only != "" && !strings.Contains(sh.name, only) {
			continue
		}
		maxN, _ := strconv.Atoi(os.Getenv("GOSREE_SCALE_MAXN"))
		var sizes []int
		for _, n := range sh.sizes {
			if maxN > 0 && n*mult > maxN {
				continue
			}
			sizes = append(sizes, n*mult)
		}
		if len(sizes) == 0 {
			continue
		}
		rows := scaleMeasureShape(t, sh, sizes, 1)
		t.Log(scaleTable(fmt.Sprintf("%s (x%d, 1 run)", sh.name, mult), rows))
	}
}

// TestScaleOddShapes analyses programs that are legal but extreme in one dimension and requires every
// analyzer to finish without an error: 255 parameters and 255 results, a package of 500 tiny files, a
// composite literal of 20000 elements on one 200 KB line, identifiers 5000 bytes long, and a function
// of 3000 statements. Each must still report its planted finding.
func TestScaleOddShapes(t *testing.T) {
	if testing.Short() {
		t.Skip("large generated programs: skipped in -short mode")
	}
	shapes := map[string]func() map[string]string{
		"255 params and results": func() map[string]string {
			var ps, rs, rets []string
			for i := range 255 {
				ps = append(ps, fmt.Sprintf("p%d *Job", i))
				rs = append(rs, "*Job")
				rets = append(rets, fmt.Sprintf("p%d", i))
			}
			body := fmt.Sprintf("func Wide(ch chan *Job, %s) (%s) {\n\tch <- p254\n\treturn %s\n}\n\nfunc Use(ch chan *Job) {\n\tj := &Job{}\n\tch <- j\n\tj.ID = 1\n}\n",
				strings.Join(ps, ", "), strings.Join(rs, ", "), strings.Join(rets, ", "))
			return scaleOne(body)
		},
		"500 files": func() map[string]string {
			files := scaleOne("")
			for i := range 500 {
				files[fmt.Sprintf("p/f%03d.go", i)] = fmt.Sprintf("package p\n\nfunc F%d(ch chan *Job) {\n\tj := &Job{}\n\tch <- j\n\tj.ID = %d\n}\n", i, i)
			}
			return files
		},
		"20000-element literal on one line": func() map[string]string {
			elems := make([]string, 20000)
			for i := range elems {
				elems[i] = "{}"
			}
			return scaleOne("func Lit(ch chan []*Job) {\n\tjs := []*Job{" + strings.Join(elems, ", ") + "}\n\tch <- js\n\tjs[0].ID = 1\n}\n")
		},
		"5000-byte identifiers": func() map[string]string {
			id := "x" + strings.Repeat("ä", 2500)
			return scaleOne(fmt.Sprintf("func Long(ch chan *Job) {\n\t%[1]s := &Job{}\n\tch <- %[1]s\n\t%[1]s.ID = 1\n}\n", id))
		},
		"3000-statement function": func() map[string]string {
			var b strings.Builder
			b.WriteString("func Big(ch chan *Job) {\n\tj := &Job{}\n\tn := 0\n")
			for i := range 3000 {
				fmt.Fprintf(&b, "\tn += %d\n", i)
			}
			b.WriteString("\tch <- j\n\tj.ID = n\n}\n")
			return scaleOne(b.String())
		},
	}
	for _, name := range slices.Sorted(maps.Keys(shapes)) {
		t.Run(name, func(t *testing.T) {
			pkgs := scaleLoad(t, scaleModule(t, shapes[name]()))
			r := scaleAnalyze(t, cliAnalyzers(), pkgs, nil)
			t.Logf("%s: %s, %.1f MB allocated, %d findings", name, r.dur.Round(time.Millisecond), float64(r.alloc)/(1<<20), r.diags)
			if len(r.errors) > 0 {
				t.Fatalf("analyzer failed:\n%s", strings.Join(r.errors, "\n"))
			}
			if r.diags == 0 {
				t.Errorf("the planted finding was not reported")
			}
		})
	}
}

// scaleRichLib is a dependency whose facts the rich program uses: links, leaks, fresh results.
const scaleRichLib = `package lib

type Node struct {
	Next, Prev *Node
	Kids       []*Node
	Tag        map[string]*Node
	ID         int
}

var Kept *Node

func Keep(n *Node)          { Kept = n }
func Join(a, b *Node)       { a.Next = b; b.Prev = a }
func Adopt(p *Node, k ...*Node) { p.Kids = append(p.Kids, k...) }
func First(n *Node) *Node   { return n.Next }
func Fresh() *Node          { return &Node{} }
func Pair() (*Node, *Node)  { return &Node{}, &Node{} }
func Tagged(n *Node, k string, v *Node) { if n.Tag == nil { n.Tag = map[string]*Node{} }; n.Tag[k] = v }
`

// scaleRichFunc is one function of the rich program, instantiated n times: many transfers per
// function, several links and aliases per value, and several findings sharing positions.
const scaleRichFunc = `
func R%[1]d(ch chan *lib.Node, cs chan []*lib.Node, a, b *lib.Node, f func() *lib.Node, g func() *lib.Node) {
	x, y := lib.Pair()
	z := lib.Fresh()
	lib.Join(x, y)
	lib.Adopt(z, x, y, a)
	lib.Tagged(z, "k", b)
	w := &lib.Node{Next: x, Prev: y, Kids: []*lib.Node{z, f(), g()}}
	u := f()
	v := g()
	pair := &lib.Node{Next: u, Prev: v}
	ch <- pair
	ch <- w
	cs <- []*lib.Node{x, y, z, a, b}
	select {
	case ch <- z:
	case ch <- lib.First(a):
	case cs <- w.Kids:
	}
	go func() { x.ID++; y.ID++; a.ID++; b.ID++ }()
	func() { ch <- &lib.Node{Next: a, Prev: b, Kids: []*lib.Node{u, v}} }()
	for k := range seq%[1]d {
		ch <- &lib.Node{Next: x, Prev: k}
	}
	x.ID, y.ID, z.ID, w.ID, u.ID, v.ID, pair.ID = 1, 2, 3, 4, 5, 6, 7
	lib.Keep(w)
	defer lib.Keep(z)
}

func seq%[1]d(yield func(*lib.Node) bool) {
	_ = yield(lib.Fresh()) && yield(lib.Fresh())
}

type Imm%[1]d struct {
	a int
	b *Imm%[1]dB
}

//gosree:imm
type Imm%[1]dB struct {
	c *Imm%[1]d
	d []int
}
`

func scaleRich(n int) map[string]string {
	var b strings.Builder
	b.WriteString("package rich\n\nimport \"scaletest/lib\"\n")
	for i := range n {
		fmt.Fprintf(&b, scaleRichFunc, i)
	}
	return map[string]string{"lib/lib.go": scaleRichLib, "rich/rich.go": b.String()}
}

// scaleRender renders a run: the sorted finding set, and, per root action, the diagnostics in
// emission order, plus the ownership pass's Stats.
func scaleRender(g *checker.Graph) (set, order string) {
	var all []string
	var ord strings.Builder
	for _, act := range g.Roots {
		if act.Err != nil {
			all = append(all, fmt.Sprintf("ERROR %s: %v", act, act.Err))
			continue
		}
		fmt.Fprintf(&ord, "== %s\n", act)
		for _, d := range act.Diagnostics {
			p := act.Package.Fset.Position(d.Pos)
			line := fmt.Sprintf("%s:%d:%d: [%s] %s", filepath.Base(p.Filename), p.Line, p.Column, act.Analyzer.Name, d.Message)
			for _, r := range d.Related {
				rp := act.Package.Fset.Position(r.Pos)
				line += fmt.Sprintf(" {related %d:%d %s}", rp.Line, rp.Column, r.Message)
			}
			all = append(all, line)
			ord.WriteString(line + "\n")
		}
		if st, ok := act.Result.(*ownership.Stats); ok {
			s := fmt.Sprintf("STATS %s %+v", act.Package.PkgPath, *st)
			all = append(all, s)
			ord.WriteString(s + "\n")
		}
	}
	slices.Sort(all)
	return strings.Join(all, "\n"), ord.String()
}

// TestScaleDeterminism analyses a diagnostics-rich generated program in ten fresh driver invocations
// and requires the same finding set, the same emission order per analyzer, and the same Stats. One
// extra run has the checker's SanityCheck on, which gob-encodes every fact twice and requires the
// same bytes and a lossless round-trip (the path facts take under go vet).
func TestScaleDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("ten analysis runs: skipped in -short mode")
	}
	pkgs := scaleLoad(t, scaleModule(t, scaleRich(40)))
	const runs = 10
	var sets, orders []string
	for range runs {
		r := scaleAnalyze(t, cliAnalyzers(), pkgs, nil)
		s, o := scaleRender(r.graph)
		sets, orders = append(sets, s), append(orders, o)
	}
	n := strings.Count(sets[0], "\n") + 1
	if n < 200 {
		t.Fatalf("only %d findings: the program is not rich enough to test determinism", n)
	}
	t.Logf("%d findings per run, %d runs", n, runs)
	reordered := 0
	for i := 1; i < runs; i++ {
		if sets[i] != sets[0] {
			t.Errorf("run %d reports a different finding SET from run 0 (a build gate would flake):\n%s", i, scaleLineDiff(sets[0], sets[i]))
		}
		if orders[i] != orders[0] {
			reordered++
			if reordered == 1 {
				t.Logf("run %d emits the same findings in a different order from run 0:\n%s", i, scaleLineDiff(orders[0], orders[i]))
			}
		}
	}
	// SAFE005 findings are emitted by ranging a map (validateImm, passes/ownership/imm.go:46), so their
	// order within the safeownership action changes from run to run. The set does not.
	scaleKnownBug(t, "RB-ORDER", fmt.Sprintf("%d of %d runs emitted the findings in a different order from run 0", reordered, runs-1), reordered > 0)
	// The reordering is confined to SAFE005: without those lines every run's order is identical.
	stripImm := func(s string) string {
		var keep []string
		for l := range strings.SplitSeq(s, "\n") {
			if !strings.Contains(l, "SAFE005") {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "\n")
	}
	for i := 1; i < runs; i++ {
		if stripImm(orders[i]) != stripImm(orders[0]) {
			t.Errorf("run %d: findings other than SAFE005 are also reordered:\n%s", i, scaleLineDiff(stripImm(orders[0]), stripImm(orders[i])))
		}
	}
	sane := scaleAnalyze(t, cliAnalyzers(), pkgs, &checker.Options{SanityCheck: true})
	if s, _ := scaleRender(sane.graph); s != sets[0] {
		t.Errorf("with SanityCheck (facts gob round-tripped) the finding set differs:\n%s", scaleLineDiff(sets[0], s))
	}
}

// TestScaleDeterminismProcesses runs the built gosree binary ten times, each a fresh process, over the
// rich program and requires byte-identical output and exit codes.
func TestScaleDeterminismProcesses(t *testing.T) {
	bin := vetBuildGosree(t)
	dir := scaleModule(t, scaleRich(40))
	env := []string{"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local"}
	var first vetResult
	reordered := 0
	for i := range 10 {
		r := vetRun(t, dir, env, bin, "./...")
		if i == 0 {
			first = r
			if r.code != 3 || strings.Count(r.stderr, "\n") < 200 {
				t.Fatalf("expected exit 3 and many findings, got exit %d, %d lines", r.code, strings.Count(r.stderr, "\n"))
			}
			continue
		}
		if r.code != first.code {
			t.Errorf("process %d: exit %d, first run %d", i, r.code, first.code)
		}
		if scaleSortedLines(r.stderr) != scaleSortedLines(first.stderr) {
			t.Errorf("process %d reports a different finding SET from the first:\n%s", i, scaleLineDiff(scaleSortedLines(first.stderr), scaleSortedLines(r.stderr)))
		}
		if r.stderr != first.stderr {
			reordered++
		}
	}
	// The text driver prints each action's diagnostics in emission order, so the map-ordered SAFE005
	// findings make the CLI's output differ byte for byte between runs: a CI step that diffs gosree's
	// output against a baseline flakes even though the set is stable.
	scaleKnownBug(t, "RB-ORDER", fmt.Sprintf("%d of 9 fresh processes printed the same findings in a different order from the first", reordered), reordered > 0)
}

// scaleStaleBody declares three //gosree:unsafe scopes, each hiding one finding of a different pass
// (borrow, ownership, errcheck), and unmuted twins of the borrow and errcheck shapes.
const scaleStaleBody = `func Dangling() *int {
	x := 1
	//gosree:unsafe hides a BORROW004
	return &x
}

func Moved(ch chan *Job) {
	j := &Job{}
	ch <- j
	//gosree:unsafe hides a SAFE003
	j.ID = 1
}

func Dangling2() *int {
	y := 2
	return &y
}

func fail() error { return nil }

func Dropped() {
	//gosree:unsafe hides an ERR001
	fail()
}

func Dropped2() {
	fail()
}
`

// TestScaleUnsafeStaleBorrow checks the stale-hatch count when a //gosree:unsafe scope mutes only a
// borrow-pass or an errcheck finding: both passes record the mute (annot.Index.Mute), and the stale
// analyzer, which requires every pass that mutes, counts after all of them. A scope that hides a
// BORROW004 or an ERR001 is not stale. Regression lock for RB-STALE. A count taken before every muting
// pass has run depends on scheduling, so this runs the analysis several times.
func TestScaleUnsafeStaleBorrow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode with the other generated-program tests")
	}
	for run := range 10 {
		// Each run loads fresh packages: Mute records into the annot.Index of one analysis.
		pkgs := scaleLoad(t, scaleModule(t, scaleOne(scaleStaleBody)))
		r := scaleAnalyze(t, append(cliAnalyzers(), stale.Analyzer), pkgs, nil)
		if len(r.errors) > 0 {
			t.Fatalf("analyzer failed:\n%s", strings.Join(r.errors, "\n"))
		}
		var stats *ownership.Stats
		var hatches []annot.Hatch
		borrowDiags, errDiags := 0, 0
		for _, act := range r.graph.Roots {
			switch res := act.Result.(type) {
			case *ownership.Stats:
				stats = res
			case []annot.Hatch:
				hatches = res
			}
			for _, d := range act.Diagnostics {
				switch {
				case strings.HasPrefix(d.Message, "BORROW004"):
					borrowDiags++
				case strings.HasPrefix(d.Message, "ERR001"):
					errDiags++
				}
			}
		}
		if stats == nil {
			t.Fatal("no ownership Stats")
		}
		// The unmuted twins prove each pass reports its shape, so each first scope hid one finding.
		if borrowDiags != 1 || errDiags != 1 {
			t.Fatalf("run %d: expected exactly the unmuted BORROW004 of Dangling2 and ERR001 of Dropped2, got %d and %d", run, borrowDiags, errDiags)
		}
		if stats.UnsafeScope != 3 {
			t.Fatalf("run %d: UnsafeScope = %d, want 3", run, stats.UnsafeScope)
		}
		if len(hatches) != 0 {
			t.Fatalf("run %d: RB-STALE: //gosree:unsafe scopes that mute a BORROW004 or an ERR001 are counted stale (%d of %d scopes), want 0", run, len(hatches), stats.UnsafeScope)
		}
	}
}

// scaleSortedLines sorts the lines of s, so two outputs can be compared as sets of lines.
func scaleSortedLines(s string) string {
	lines := strings.Split(s, "\n")
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// scaleLineDiff shows the lines of b missing from a and of a missing from b (at most 20 each).
func scaleLineDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	inA, inB := map[string]int{}, map[string]int{}
	for _, l := range as {
		inA[l]++
	}
	for _, l := range bs {
		inB[l]++
	}
	var out []string
	for _, l := range as {
		if inB[l] < inA[l] {
			out = append(out, "- "+l)
			inA[l]--
		}
	}
	for _, l := range bs {
		if inA[l] < inB[l] {
			out = append(out, "+ "+l)
			inB[l]--
		}
	}
	if len(out) == 0 {
		// Same lines, different order: show the first differing line.
		for i := range min(len(as), len(bs)) {
			if as[i] != bs[i] {
				return fmt.Sprintf("same lines, order differs from line %d:\n  %s\n  %s", i, as[i], bs[i])
			}
		}
	}
	if len(out) > 40 {
		out = append(out[:40], fmt.Sprintf("... %d more", len(out)-40))
	}
	return strings.Join(out, "\n")
}
