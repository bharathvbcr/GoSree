package ownership

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/types"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gocap/internal/kinds"
)

// oraclePackages are the std packages the race corpus and the reference service call most. The
// differential runs over every exported function gocap summarises in them.
var oraclePackages = []string{"bytes", "strings", "strconv", "sort", "bufio", "io", "encoding/json", "net/http"}

// compilerLeak is one "leaking param" line of `go build -gcflags=-m`.
type compilerLeak struct {
	pos      string // file:line:col of the parameter
	content  bool   // "leaking param content": what the parameter points to escapes
	toResult bool   // "leaking param: x to result ~rN": the parameter flows to a result
	result   int    // N of ~rN when toResult
	line     string
}

var leakLine = regexp.MustCompile(`^(.+:\d+:\d+): leaking param( content)?: (\S+)(?: to result ~r(\d+))?`)

// parseCompilerLeaks reads the escape-analysis lines of `go build -gcflags=-m` output.
func parseCompilerLeaks(out []byte) []compilerLeak {
	var leaks []compilerLeak
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := leakLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		l := compilerLeak{pos: m[1], content: m[2] != "", line: sc.Text()}
		if m[4] != "" {
			l.toResult = true
			fmt.Sscan(m[4], &l.result)
		}
		leaks = append(leaks, l)
	}
	return leaks
}

// explains reports whether a gocap summary accounts for a compiler leak of parameter i of a function
// with np parameters. The compiler's escape analysis is the oracle for one direction only: whatever
// it says leaves the function must be retained (Leaks), joined to another argument or result (Links),
// or assumed (Unknown). A flow to a result may instead make the result not fresh, which callers treat
// as foreign. The converse is not checked: gocap's Leaks is wider than heap escape (a move to another
// goroutine leaks even when the compiler keeps it on the stack), and Fresh and Writes have no oracle.
func explains(f *FuncFact, i, np int, l compilerLeak) bool {
	if f.Unknown || (i < len(f.Leaks) && f.Leaks[i]) {
		return true
	}
	for _, ln := range f.Links {
		if ln.A == i || ln.B == i {
			if !l.toResult || ln.A == np+l.result || ln.B == np+l.result {
				return true
			}
		}
	}
	return l.toResult && !f.Fresh
}

type oracleParam struct {
	fn    *types.Func
	index int
	typ   types.Type
}

// paramPositions maps each named parameter of a function declaration to its SSA parameter index (the
// receiver is 0), keyed by file:line:col as the compiler prints it.
func paramPositions(p *packages.Package) map[string]oracleParam {
	out := map[string]oracleParam{}
	for _, file := range p.Syntax {
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func)
			if !ok {
				continue
			}
			var fields []*ast.Field
			if fd.Recv != nil {
				fields = append(fields, fd.Recv.List...)
			}
			fields = append(fields, fd.Type.Params.List...)
			i := 0
			for _, fld := range fields {
				if len(fld.Names) == 0 {
					i++
					continue
				}
				for _, name := range fld.Names {
					pos := p.Fset.Position(name.Pos())
					out[fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column)] = oracleParam{fn, i, p.TypesInfo.Defs[name].Type()}
					i++
				}
			}
		}
	}
	return out
}

func canonical(pos string) string {
	file, rest, ok := strings.Cut(pos, ":")
	if !ok {
		return pos
	}
	if r, err := filepath.EvalSymlinks(file); err == nil {
		file = r
	}
	return file + ":" + rest
}

// TestEscapeOracleNoFreshOrWritesOracle is the differential test of gocap's std summaries against
// the compiler's escape analysis. Every parameter the compiler reports as leaking must be leaked or
// linked in gocap's FuncFact, or the fact must be Unknown. Parameters of shareable types are skipped:
// gocap gives them no alias class because sharing them is safe. Fresh and Writes are not compared;
// the compiler has no notion of either (hence the name).
func TestEscapeOracleNoFreshOrWritesOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and analyses std packages")
	}
	args := []string{"build"}
	for _, p := range oraclePackages {
		args = append(args, "-gcflags="+p+"=-m")
	}
	out, err := exec.Command("go", append(args, oraclePackages...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go build -gcflags=-m: %v\n%s", err, out)
	}
	leaks := parseCompilerLeaks(out)
	if len(leaks) == 0 {
		t.Fatalf("no leaking-param lines in the compiler output; the format changed?\n%.2000s", out)
	}

	pkgs, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax}, oraclePackages...)
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("packages failed to load")
	}
	graph, err := checker.Analyze([]*analysis.Analyzer{Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	facts := map[*types.Func]*FuncFact{}
	params := map[string]oracleParam{}
	for _, act := range graph.Roots {
		if act.Err != nil {
			t.Fatalf("%s: %v", act, act.Err)
		}
		for _, of := range act.AllObjectFacts() {
			if f, ok := of.Fact.(*FuncFact); ok && of.Object.Pkg() == act.Package.Types {
				facts[of.Object.(*types.Func)] = f
			}
		}
		for pos, p := range paramPositions(act.Package) {
			params[canonical(pos)] = p
		}
	}

	cls := kinds.NewClassifier(func(*types.TypeName) bool { return false })
	var compared, unknown, violations int
	var bad []string
	for _, l := range leaks {
		p, ok := params[canonical(l.pos)]
		if !ok {
			continue // a closure's or an unexported function's parameter
		}
		f, ok := facts[p.fn]
		if !ok || cls.Shareable(p.typ) {
			continue
		}
		compared++
		if f.Unknown {
			unknown++
		}
		if !explains(f, p.index, p.fn.Signature().Params().Len()+btoi(p.fn.Signature().Recv() != nil), l) {
			violations++
			bad = append(bad, fmt.Sprintf("%s param %d: %s (gocap: leaks %v, links %v, fresh %v)", p.fn.FullName(), p.index, l.line, f.Leaks, f.Links, f.Fresh))
		}
	}
	slices.Sort(bad)
	t.Logf("compiler leak lines %d, compared %d (Unknown %d), violations %d, functions summarised %d", len(leaks), compared, unknown, violations, len(facts))
	if compared == 0 {
		t.Fatal("nothing compared; the position mapping is broken")
	}
	for _, b := range bad {
		t.Error(b)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestExplainsRejectsPlantedSummary checks the comparison itself: a summary that neither leaks nor
// links a parameter the compiler says escapes is a violation.
func TestExplainsRejectsPlantedSummary(t *testing.T) {
	heap := compilerLeak{}
	toR0 := compilerLeak{toResult: true}
	clean := &FuncFact{Leaks: []bool{false, false}, Fresh: true}
	cases := []struct {
		name string
		f    *FuncFact
		l    compilerLeak
		want bool
	}{
		{"planted: no leak, no link", clean, heap, false},
		{"planted: fresh result, no link", clean, toR0, false},
		{"leaked", &FuncFact{Leaks: []bool{true, false}, Fresh: true}, heap, true},
		{"unknown", &FuncFact{Leaks: []bool{false, false}, Unknown: true}, heap, true},
		{"linked to other param", &FuncFact{Leaks: []bool{false, false}, Fresh: true, Links: []Link{{0, 1, 1, 0}}}, heap, true},
		{"linked to result 0", &FuncFact{Leaks: []bool{false, false}, Fresh: true, Links: []Link{{0, 0, 2, 0}}}, toR0, true},
		{"linked to other param, not the result", &FuncFact{Leaks: []bool{false, false}, Fresh: true, Links: []Link{{0, 1, 1, 0}}}, toR0, false},
		{"result not fresh", &FuncFact{Leaks: []bool{false, false}}, toR0, true},
	}
	for _, c := range cases {
		if got := explains(c.f, 0, 2, c.l); got != c.want {
			t.Errorf("%s: explains = %v, want %v", c.name, got, c.want)
		}
	}
	got := parseCompilerLeaks([]byte("a.go:1:7: leaking param: b to result ~r1 level=1\na.go:2:3: leaking param content: x\na.go:3:3: x does not escape\n"))
	if len(got) != 2 || !got[0].toResult || got[0].result != 1 || !got[1].content || got[1].toResult {
		t.Errorf("parseCompilerLeaks = %+v", got)
	}
}
