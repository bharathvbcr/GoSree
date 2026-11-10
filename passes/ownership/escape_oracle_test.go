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
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/internal/kinds"
)

// oraclePackages are the std packages the race corpus and the reference service call most. The
// differential runs over every exported function gosree summarises in them.
var oraclePackages = []string{"bytes", "strings", "strconv", "sort", "bufio", "io", "encoding/json", "net/http"}

// compilerLeak is one "leaking param" line of `go build -gcflags=-m=2`.
type compilerLeak struct {
	pos      string // file:line:col of the parameter
	name     string // the parameter's name
	content  bool   // "leaking param content": what the parameter points to escapes
	toResult bool   // "leaking param: x to result ~rN": the parameter flows to a result
	result   int    // N of ~rN when toResult
	line     string
	// heapFlow holds the lines of the compiler's explanation of how the parameter reaches the heap
	// (-m=2 only): "flow:" steps and their "from" sources.
	heapFlow []string
}

var (
	leakLine = regexp.MustCompile(`^(.+:\d+:\d+): leaking param( content)?: (\S+)(?: to result ~r(\d+))?`)
	// heapHead opens a -m=2 explanation block; the lines after it that start with the same position
	// and an indent belong to it.
	heapHead = regexp.MustCompile(`^(.+:\d+:\d+): parameter \S+ leaks to \{heap\}`)
)

// parseCompilerLeaks reads the escape-analysis lines of `go build -gcflags=-m=2` output.
func parseCompilerLeaks(out []byte) []compilerLeak {
	var leaks []compilerLeak
	flows := map[string][]string{}
	block := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for sc.Scan() {
		text := sc.Text()
		if m := heapHead.FindStringSubmatch(text); m != nil {
			block = m[1]
			continue
		}
		if block != "" && strings.HasPrefix(text, block+":   ") {
			flows[block] = append(flows[block], strings.TrimSpace(strings.TrimPrefix(text, block+":")))
			continue
		}
		block = ""
		m := leakLine.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		l := compilerLeak{pos: m[1], name: m[3], content: m[2] != "", line: text}
		if m[4] != "" {
			l.toResult = true
			l.result, _ = strconv.Atoi(m[4]) // the pattern admits only digits
		}
		leaks = append(leaks, l)
	}
	for i := range leaks {
		leaks[i].heapFlow = flows[leaks[i].pos]
	}
	return leaks
}

var (
	// syncFlow matches a flow into an inlined sync or sync/atomic function or a call of one: the
	// compiler cannot see past the runtime semaphore a lock parks on or the assembly of an atomic, so
	// every locked or atomically accessed parameter "leaks".
	syncFlow = regexp.MustCompile(`flow: (sync|atomic)\.\w+ ←|\(\*?sync\.\w+\)\.\w+\(|\batomic\.\w+\(`)
	// fieldFlow matches the compiler loading a field of the parameter on its way to the heap.
	fieldFlow = regexp.MustCompile(`^from (\w+)\.(\w+) \(dot(?: of pointer)?\)`)
)

// excusedLeak says why a heap leak the compiler reports is not one gosree tracks, or "" if it is:
//   - "lock": the parameter reaches the heap through a sync method. gosree counts lock operations as
//     synchronisation, not as retention (lockOps, and the sync types are shareable).
//   - "shareable field": what escapes is a field of a shareable type (a string, an error) loaded
//     from the parameter, which a caller can share freely.
//   - "self-store": the content is stored back into the parameter's own memory (b.buf =
//     append(b.buf, c)); the compiler models a store through a parameter as the heap, gosree as a
//     write to the parameter, which Writes records.
//
// -m=2 explains only the first path the compiler found, so a second path that gosree should see can
// hide behind an excused one; that is this oracle's limit, not a proof.
func excusedLeak(l compilerLeak, ptype types.Type, cls *kinds.Classifier) string {
	for _, f := range l.heapFlow {
		if syncFlow.MatchString(f) {
			return "lock"
		}
	}
	selfStore := regexp.MustCompile(`^from \*?` + regexp.QuoteMeta(l.name) + `(\.\w+|\[[^\]]*\])* = .*\(assign\)( at \S+)?$`)
	for _, f := range l.heapFlow {
		if selfStore.MatchString(f) {
			return "self-store"
		}
	}
	for _, f := range l.heapFlow {
		m := fieldFlow.FindStringSubmatch(f)
		if m == nil || m[1] != l.name {
			continue
		}
		t := ptype
		if p, ok := t.Underlying().(*types.Pointer); ok {
			t = p.Elem()
		}
		s, ok := t.Underlying().(*types.Struct)
		if !ok {
			return ""
		}
		for fld := range s.Fields() {
			if fld.Name() == m[2] && cls.Shareable(fld.Type()) {
				return "shareable field"
			}
		}
		return ""
	}
	return ""
}

// explains reports whether a gosree summary accounts for a compiler leak of parameter i of a function
// with np parameters. The compiler's escape analysis is the oracle for one direction only: whatever
// it says leaves the function must be retained (Leaks), joined to another argument or result (Links),
// or assumed (Unknown). A flow to a result may instead make the result not fresh, which callers treat
// as foreign. The converse is not checked: gosree's Leaks is wider than heap escape (a move to another
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

// TestEscapeOracleNoFreshOrWritesOracle is the differential test of gosree's std summaries against
// the compiler's escape analysis. Every parameter the compiler reports as leaking must be leaked or
// linked in gosree's FuncFact, or the fact must be Unknown. Parameters of shareable types are skipped:
// gosree gives them no alias class because sharing them is safe. A heap leak the compiler explains by
// a lock or by a shareable field is excused (excusedLeak) and counted, not compared. Fresh and Writes
// are not compared; the compiler has no notion of either (hence the name).
func TestEscapeOracleNoFreshOrWritesOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and analyses std packages")
	}
	args := []string{"build"}
	for _, p := range oraclePackages {
		args = append(args, "-gcflags="+p+"=-m=2")
	}
	out, err := exec.Command("go", append(args, oraclePackages...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go build -gcflags=-m=2: %v\n%s", err, out)
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
	excused := map[string]int{}
	seen := map[string]bool{}
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
		// A flow into a result of a shareable type (an error, a string, a context) hands the caller
		// nothing it could race on, so gosree rightly keeps no link for it.
		if res := p.fn.Signature().Results(); l.toResult && l.result < res.Len() && cls.Shareable(res.At(l.result).Type()) {
			continue
		}
		if !l.toResult {
			if why := excusedLeak(l, p.typ, cls); why != "" {
				excused[why]++
				continue
			}
		}
		compared++
		if f.Unknown {
			unknown++
		}
		if !explains(f, p.index, p.fn.Signature().Params().Len()+btoi(p.fn.Signature().Recv() != nil), l) {
			key := fmt.Sprintf("%s#%d", p.fn.FullName(), p.index)
			if _, ok := reviewedLeaks[key]; ok {
				seen[key] = true
				continue
			}
			violations++
			bad = append(bad, fmt.Sprintf("%s: %s (gosree: leaks %v, links %v, fresh %v)", key, l.line, f.Leaks, f.Links, f.Fresh))
		}
	}
	slices.Sort(bad)
	t.Logf("compiler leak lines %d, compared %d (Unknown %d), excused %v, reviewed %d, violations %d, functions summarised %d",
		len(leaks), compared, unknown, excused, len(seen), violations, len(facts))
	if compared == 0 {
		t.Fatal("nothing compared; the position mapping is broken")
	}
	for _, b := range bad {
		t.Error(b)
	}
	for key := range reviewedLeaks {
		if !seen[key] {
			t.Errorf("reviewed leak %s no longer disagrees with gosree; remove it from reviewedLeaks", key)
		}
	}
}

// reviewedLeaks are compiler leaks gosree's summaries do not account for, each read by hand (item 4,
// go1.27.3): the compiler's -m=2 explanation and the source of the callee it names (pathAddrs,
// isRequestExempt, findHandler and matchOrRedirect, serveFile, genericReplacer.lookup,
// url.EscapedPath, Cookie.String). What escapes is string data reached through the callee, or the
// leak comes from an atomic or a lock inside it. excusedLeak cannot see through
// the callee from the text. A new disagreement fails the test until it is read the same way, and an
// entry that stops disagreeing fails it too, so the list cannot go stale.
var reviewedLeaks = map[string]string{
	"(*net/http.CrossOriginProtection).Check#0": "isRequestExempt loads c.bypass atomically",
	"(*net/http.CrossOriginProtection).Check#1": "isRequestExempt matches req's URL and host strings",
	"(*net/http.ServeMux).Handler#1":            "findHandler matches r's host and path strings",
	"(*net/http.fileHandler).ServeHTTP#2":       "serveFile reads r's header and URL strings",
	"(*net/http.redirectHandler).ServeHTTP#2":   "Redirect reads r.URL's path strings",
	"(*net/http.socksDialer).Dial#0":            "pathAddrs reads d's proxy address strings",
	"(*net/http.socksDialer).DialContext#0":     "pathAddrs reads d's proxy address strings",
	"(*net/http.socksDialer).DialWithConn#0":    "pathAddrs reads d's proxy address strings",
	"(*strings.genericReplacer).Replace#0":      "WriteString writes lookup's replacement strings",
	"(*strings.genericReplacer).WriteString#0":  "lookup's replacement strings go to the writer",
	"(net/http.Header).Clone#0":                 "copy copies the header's value strings",
	"net/http.Redirect#1":                       "(*url.URL).EscapedPath reads r.URL's path strings",
	"net/http.ServeFile#1":                      "serveFile reads r's header and URL strings",
	"net/http.ServeFileFS#1":                    "serveFile reads r's header and URL strings",
	"net/http.SetCookie#1":                      "(*Cookie).String formats the cookie's strings",
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
