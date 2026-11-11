package stress_test

// Driver parity: the same analyzers run under the standalone multichecker (`gosree pkgs`, every
// dependency analysed in one process, facts passed in memory) and under `go vet -vettool=gosree`
// (unitchecker: one process per package, facts serialised with gob between them, standard-library
// facts from vet's own VetxOnly runs). A build gate must not depend on which driver runs it.
//
// The tests here build cmd/gosree into a temporary directory and run both drivers over a temporary
// GOPATH workspace holding copies of the repository's own cross-package fixtures plus generated
// packages, then compare the findings as (file, line, message) multisets.

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// vetRepoRoot is the repository root.
func vetRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// vetBuildGosree builds cmd/gosree into a test temporary directory and returns its path.
func vetBuildGosree(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("needs the gosree binary built from cmd/gosree: skipped in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "gosree")
	r := vetRun(t, vetRepoRoot(t), []string{"GOFLAGS=", "GOWORK=off"}, "go", "build", "-o", bin, "./cmd/gosree")
	if r.code != 0 {
		t.Fatalf("building cmd/gosree: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	return bin
}

// vetResult is one finished command.
type vetResult struct {
	stdout, stderr string
	code           int
}

// vetRun runs a command with a bounded time and returns its output and exit code. A command that
// cannot start or times out fails the test.
func vetRun(t *testing.T, dir string, env []string, name string, args ...string) vetResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s %s: timed out after 5m", name, strings.Join(args, " "))
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
		}
		code = ee.ExitCode()
	}
	return vetResult{out.String(), errb.String(), code}
}

// vetCopyTree copies the directory tree src to dst.
func vetCopyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
}

// vetWrite writes files (path relative to root -> contents) under root.
func vetWrite(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// vetFactsA exports every kind of fact the ownership pass produces: leaking, pure, fresh, linking
// and iso functions, and an imm type.
const vetFactsA = `package a

type Job struct{ ID int }

type Node struct {
	Next *Node
	ID   int
}

var kept *Job

// Keep retains its argument.
func Keep(j *Job) { kept = j }

// Pure reads its argument.
func Pure(j *Job) int { return j.ID }

// New returns a fresh value.
func New() *Job { return &Job{} }

// Link joins c into r.
func Link(r, c *Node) { r.Next = c }

// First returns memory of its argument.
func First(r *Node) *Node { return r.Next }

// Take owns its argument.
//
//gosree:iso j
func Take(j *Job) { j.ID = 1 }

// Frozen is deeply immutable.
//
//gosree:imm
type Frozen struct{ n int }

func NewFrozen(n int) *Frozen { return &Frozen{n: n} }

// Holder is generic.
type Holder[T any] struct{ V T }

func (h *Holder[T]) Put(v T) { h.V = v }

func (h *Holder[T]) Get() T { return h.V }
`

// vetFactsB uses every fact of package a across the package boundary.
const vetFactsB = `package b

import "vetfacts/a"

func afterKeep(ch chan *a.Job) {
	j := &a.Job{}
	a.Keep(j)
	ch <- j
}

func afterPure(ch chan *a.Job) {
	j := &a.Job{}
	_ = a.Pure(j)
	ch <- j
}

func fresh(ch chan *a.Job) {
	j := a.New()
	ch <- j
	j.ID = 1
}

func linked(ch chan *a.Node) {
	r, c := &a.Node{}, &a.Node{}
	a.Link(r, c)
	ch <- r
	c.ID = 1
}

func first(ch chan *a.Node, r *a.Node) {
	ch <- a.First(r)
}

func taken() {
	j := &a.Job{}
	a.Take(j)
	j.ID = 2
}

func frozen(ch chan *a.Frozen) {
	f := a.NewFrozen(1)
	ch <- f
	_ = f
}

func generic(ch chan *a.Job) {
	h := &a.Holder[*a.Job]{}
	j := &a.Job{}
	h.Put(j)
	ch <- j
	_ = h.Get()
}
`

// vetFactsBTest is an in-package test file: go vet analyses package b again with it.
const vetFactsBTest = `package b

import (
	"testing"

	"vetfacts/a"
)

func TestX(t *testing.T) {
	ch := make(chan *a.Job, 1)
	j := &a.Job{}
	ch <- j
	j.ID = 3
}
`

// vetFactsBXTest is an external test package.
const vetFactsBXTest = `package b_test

import (
	"testing"

	"vetfacts/a"
)

func TestY(t *testing.T) {
	ch := make(chan *a.Job, 1)
	j := a.New()
	ch <- j
	j.ID = 4
}
`

// vetNoPos holds a function for which the borrow pass reports a diagnostic without a position
// (the Return of the recover block SSA adds to a function with a defer).
const vetNoPos = `package vetnopos

type Job struct{ ID int }

func NamedDefer() (out *Job) {
	defer func() { _ = out }()
	out = &Job{}
	return out
}
`

// vetBad does not type-check.
const vetBad = `package vetbad

func f() int { return "not an int" }

func g(ch chan *int) {
	x := new(int)
	ch <- x
	*x = 1
}
`

// vetWorkspace builds the GOPATH workspace and returns (GOPATH, its src directory).
func vetWorkspace(t *testing.T) (string, string) {
	t.Helper()
	gp := t.TempDir()
	src := filepath.Join(gp, "src")
	fixtures := filepath.Join(vetRepoRoot(t), "passes", "ownership", "testdata", "src")
	for _, p := range []string{"user", "lib", "store"} {
		vetCopyTree(t, filepath.Join(fixtures, p), filepath.Join(src, p))
	}
	for _, p := range vetOwnRoots {
		vetCopyTree(t, filepath.Join("testdata", "src", p), filepath.Join(src, p))
	}
	vetWrite(t, src, map[string]string{
		"vetfacts/a/a.go":       vetFactsA,
		"vetfacts/b/b.go":       vetFactsB,
		"vetfacts/b/b_test.go":  vetFactsBTest,
		"vetfacts/b/bx_test.go": vetFactsBXTest,
		"vetnopos/nopos.go":     vetNoPos,
		"vetbad/bad.go":         vetBad,
	})
	// The cap package, so a fixture that imports it resolves the shipped one.
	capSrc, err := os.ReadFile(filepath.Join(vetRepoRoot(t), "cap", "cap.go"))
	if err != nil {
		t.Fatal(err)
	}
	vetWrite(t, src, map[string]string{"github.com/bharathvbcr/gosree/cap/cap.go": string(capSrc)})
	return gp, src
}

func vetGopathEnv(gp string) []string {
	return []string{"GOPATH=" + gp, "GO111MODULE=off", "GOPROXY=off", "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local"}
}

// vetFinding is one diagnostic, located relative to the workspace.
type vetFinding struct {
	file string
	line int
	msg  string
}

func (f vetFinding) String() string { return fmt.Sprintf("%s:%d: %s", f.file, f.line, f.msg) }

// vetJSONDiag is one diagnostic of the analysisflags JSON tree.
type vetJSONDiag struct {
	Posn    string `json:"posn"`
	Message string `json:"message"`
}

var vetPosnRE = regexp.MustCompile(`^(.*):(\d+):(\d+)$`)

// vetParseJSON decodes a stream of analysisflags JSON trees ({pkg: {analyzer: [diag] | {error}}}),
// skipping the "# pkg" lines go vet interleaves, and returns the findings with their multiplicity,
// the analyzer errors, and the package IDs seen.
func vetParseJSON(t *testing.T, root, data string) (map[vetFinding]int, []string, []string) {
	t.Helper()
	var keep []string
	for line := range strings.SplitSeq(data, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		keep = append(keep, line)
	}
	dec := jsontext.NewDecoder(strings.NewReader(strings.Join(keep, "\n")))
	findings := map[vetFinding]int{}
	var errs, pkgs []string
	for {
		var tree map[string]map[string]jsontext.Value
		if err := json.UnmarshalDecode(dec, &tree); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decoding JSON output: %v\n%s", err, data)
		}
		for pkg, byAnalyzer := range tree {
			pkgs = append(pkgs, pkg)
			for analyzer, raw := range byAnalyzer {
				var diags []vetJSONDiag
				if err := json.Unmarshal(raw, &diags); err != nil {
					errs = append(errs, fmt.Sprintf("%s %s: %s", pkg, analyzer, raw))
					continue
				}
				for _, d := range diags {
					findings[vetLocate(root, d.Posn, d.Message)]++
				}
			}
		}
	}
	slices.Sort(pkgs)
	slices.Sort(errs)
	return findings, errs, pkgs
}

// vetLocate turns a "file:line:col" position into a finding relative to root.
func vetLocate(root, posn, msg string) vetFinding {
	m := vetPosnRE.FindStringSubmatch(posn)
	if m == nil {
		return vetFinding{file: posn, msg: msg}
	}
	line, _ := strconv.Atoi(m[2])
	file := m[1]
	if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
		file = filepath.ToSlash(rel)
	}
	return vetFinding{file: file, line: line, msg: msg}
}

var vetTextRE = regexp.MustCompile(`^(\S+?\.go):(\d+):(\d+): (.*)$`)

// vetParseText reads "file:line:col: message" lines; relative file names are resolved against dir.
func vetParseText(root, dir, data string) map[vetFinding]int {
	out := map[vetFinding]int{}
	for line := range strings.SplitSeq(data, "\n") {
		m := vetTextRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		file := m[1]
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		out[vetLocate(root, file+":"+m[2]+":"+m[3], m[4])]++
	}
	return out
}

// vetDiff lists the findings in a but not b, and in b but not a, counting multiplicity.
func vetDiff(a, b map[vetFinding]int) (onlyA, onlyB []string) {
	for k, n := range a {
		if d := n - b[k]; d > 0 {
			onlyA = append(onlyA, fmt.Sprintf("%dx %s", d, k))
		}
	}
	for k, n := range b {
		if d := n - a[k]; d > 0 {
			onlyB = append(onlyB, fmt.Sprintf("%dx %s", d, k))
		}
	}
	slices.Sort(onlyA)
	slices.Sort(onlyB)
	return onlyA, onlyB
}

func vetFilter(m map[vetFinding]int, keep func(vetFinding) bool) map[vetFinding]int {
	out := map[vetFinding]int{}
	for k, n := range m {
		if keep(k) {
			out[k] = n
		}
	}
	return out
}

func vetDump(m map[vetFinding]int) string {
	keys := slices.SortedFunc(maps.Keys(m), func(a, b vetFinding) int { return strings.Compare(a.String(), b.String()) })
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %dx %s\n", m[k], k)
	}
	return b.String()
}

// vetOwnRoots are this area's marker fixtures, also compared across drivers.
var vetOwnRoots = []string{"rb_closures", "rb_controlflow", "rb_deadcode", "rb_directives", "rb_generics", "rb_misc", "rb_rangefunc"}

var vetGoodPkgs = []string{"user", "lib", "store", "store/inner", "vetfacts/a", "vetfacts/b", "vetnopos", "rb_closures", "rb_controlflow", "rb_deadcode", "rb_directives", "rb_generics", "rb_misc", "rb_rangefunc"}

// TestVetParity runs the standalone driver and go vet over the same packages and requires the same
// findings. The packages carry test files, which go vet always analyses and the standalone driver
// analyses with -test (its default).
func TestVetParity(t *testing.T) {
	bin := vetBuildGosree(t)
	gp, src := vetWorkspace(t)
	env := vetGopathEnv(gp)

	alone := vetRun(t, src, env, bin, append([]string{"-json"}, vetGoodPkgs...)...)
	vet := vetRun(t, src, env, "go", append([]string{"vet", "-vettool=" + bin, "-json"}, vetGoodPkgs...)...)

	aFind, aErrs, aPkgs := vetParseJSON(t, src, alone.stdout)
	// go vet writes the JSON of each package to stderr.
	vFind, vErrs, vPkgs := vetParseJSON(t, src, vet.stderr+"\n"+vet.stdout)
	t.Logf("standalone: exit %d, %d distinct findings, packages %v, errors %v", alone.code, len(aFind), aPkgs, aErrs)
	t.Logf("go vet:     exit %d, %d distinct findings, packages %v, errors %v", vet.code, len(vFind), vPkgs, vErrs)
	if len(aErrs) > 0 || len(vErrs) > 0 {
		t.Errorf("analyzer errors: standalone %v, vet %v", aErrs, vErrs)
	}
	if len(aFind) == 0 {
		t.Fatalf("standalone driver reported nothing: the comparison would be vacuous\nstdout:\n%s\nstderr:\n%s", alone.stdout, alone.stderr)
	}

	// Sets first: the same findings in both drivers.
	once := func(m map[vetFinding]int) map[vetFinding]int {
		out := map[vetFinding]int{}
		for k := range m {
			out[k] = 1
		}
		return out
	}
	onlyAlone, onlyVet := vetDiff(once(aFind), once(vFind))
	if len(onlyAlone) > 0 || len(onlyVet) > 0 {
		t.Errorf("drivers disagree\nonly standalone:\n  %s\nonly go vet:\n  %s",
			strings.Join(onlyAlone, "\n  "), strings.Join(onlyVet, "\n  "))
	}

	// A diagnostic must carry a position: one without names no file, cannot be suppressed by a
	// //gosree:unsafe scope, and cannot be reviewed. Regression lock for RB-NOPOS.
	noPos := vetFilter(aFind, func(f vetFinding) bool { return f.line == 0 })
	if len(noPos) > 0 {
		t.Errorf("RB-NOPOS regression: diagnostics reported with no position:\n%s", vetDump(noPos))
	}

	// Multiplicity: standalone -json and go vet -json must each report every finding once (de-duplicating
	// a package against its in-package test variant). Regression lock for RB-JSONDUP.
	aDup := vetFilter(aFind, func(f vetFinding) bool { return aFind[f] > 1 })
	vDup := vetFilter(vFind, func(f vetFinding) bool { return vFind[f] > 1 })
	if len(vDup) > 0 {
		t.Errorf("go vet reports findings more than once:\n%s", vetDump(vDup))
	}
	if len(aDup) > 0 {
		t.Errorf("RB-JSONDUP regression: standalone -json reports %d findings more than once:\n%s", len(aDup), vetDump(aDup))
	}
	t.Logf("standalone findings:\n%s", vetDump(aFind))
}

// vetClean is a package with no findings.
const vetClean = `package vetclean

func Add(a, b int) int { return a + b }
`

// vetWindows only builds for windows; its finding exists only for that target.
const vetWindows = `//go:build windows

package vettarget

func onWindows(ch chan *int) {
	x := new(int)
	ch <- x
	*x = 1
}
`

// vetTagged only builds with -tags vettag.
const vetTagged = `//go:build vettag

package vettarget

func tagged(ch chan *int) {
	x := new(int)
	ch <- x
	*x = 2
}
`

const vetTargetCommon = `package vettarget

func Common() {}
`

// TestVetCLIContract records the exit code and output of the CLI for the cases a build gate depends
// on: findings, none, a package that does not type-check, a missing package, no arguments, -fix,
// -c, -test=false, cross targets and -tags, in both drivers.
func TestVetCLIContract(t *testing.T) {
	bin := vetBuildGosree(t)
	gp, src := vetWorkspace(t)
	vetWrite(t, src, map[string]string{
		"vetclean/clean.go":     vetClean,
		"vettarget/common.go":   vetTargetCommon,
		"vettarget/windows.go":  vetWindows,
		"vettarget/tagged.go":   vetTagged,
		"vetfacts/c/c.go":       vetFactsB[:0] + "package c\n\nimport \"vetbad\"\n\nvar _ = vetbad.X\n",
		"vetbad/more.go":        "package vetbad\n\nvar X = 1\n",
		"vetnotest/n.go":        "package vetnotest\n\nfunc F(ch chan *int) {\n\tx := new(int)\n\tch <- x\n\t*x = 1\n}\n",
		"vetnotest/n_test.go":   "package vetnotest\n\nimport \"testing\"\n\nfunc TestN(t *testing.T) {}\n",
		"vetsyntax/syntax.go":   "package vetsyntax\n\nfunc f( {\n",
		"vetmixed/ok.go":        "package vetmixed\n\nfunc G(ch chan *int) {\n\tx := new(int)\n\tch <- x\n\t*x = 1\n}\n",
		"vetmixed/broken.go":    "package vetmixed\n\nvar Y int = \"s\"\n",
		"vetimportmissing/m.go": "package vetimportmissing\n\nimport \"does/not/exist\"\n\nvar _ = exist.X\n",
		"vetnopos2/n.go":        vetNoPos,
	})
	env := vetGopathEnv(gp)
	run := func(extra []string, name string, args ...string) vetResult {
		return vetRun(t, src, append(slices.Clone(env), extra...), name, args...)
	}
	count := func(r vetResult) int {
		n := 0
		for _, c := range vetParseText(src, src, r.stdout+r.stderr) {
			n += c
		}
		return n
	}
	type tc struct {
		name string
		env  []string
		cmd  string
		args []string
	}
	vet := func(args ...string) []string { return append([]string{"vet", "-vettool=" + bin}, args...) }
	cases := []tc{
		{"gosree findings", nil, bin, []string{"vetnotest"}},
		{"gosree clean", nil, bin, []string{"vetclean"}},
		{"gosree type error", nil, bin, []string{"vetbad"}},
		{"gosree syntax error", nil, bin, []string{"vetsyntax"}},
		{"gosree one broken file", nil, bin, []string{"vetmixed"}},
		{"gosree missing import", nil, bin, []string{"vetimportmissing"}},
		{"gosree dependency broken", nil, bin, []string{"vetfacts/c"}},
		{"gosree type error -json", nil, bin, []string{"-json", "vetbad"}},
		{"gosree nonexistent", nil, bin, []string{"./nonexistent"}},
		{"gosree no args", nil, bin, nil},
		{"gosree -fix", nil, bin, []string{"-fix", "vetnotest"}},
		{"gosree -c=1", nil, bin, []string{"-c=1", "vetnotest"}},
		{"gosree -test=false", nil, bin, []string{"-test=false", "vetfacts/b"}},
		{"gosree -test=true", nil, bin, []string{"vetfacts/b"}},
		{"gosree GOOS=windows", []string{"GOOS=windows", "GOARCH=amd64"}, bin, []string{"vettarget"}},
		{"gosree host target", nil, bin, []string{"vettarget"}},
		{"gosree -tags vettag", nil, bin, []string{"-tags=vettag", "vettarget"}},
		{"gosree GOFLAGS=-tags", []string{"GOFLAGS=-tags=vettag"}, bin, []string{"vettarget"}},
		{"gosree nopos text", nil, bin, []string{"vetnopos2"}},
		{"vet findings", nil, "go", vet("vetnotest")},
		{"vet clean", nil, "go", vet("vetclean")},
		{"vet type error", nil, "go", vet("vetbad")},
		{"vet syntax error", nil, "go", vet("vetsyntax")},
		{"vet one broken file", nil, "go", vet("vetmixed")},
		{"vet nonexistent", nil, "go", vet("./nonexistent")},
		{"vet GOOS=windows", []string{"GOOS=windows", "GOARCH=amd64"}, "go", vet("vettarget")},
		{"vet -tags vettag", nil, "go", vet("-tags=vettag", "vettarget")},
		{"vet test variant", nil, "go", vet("vetfacts/b")},
		{"vet nopos text", nil, "go", vet("vetnopos2")},
	}
	got := map[string]vetResult{}
	for _, c := range cases {
		r := run(c.env, c.cmd, c.args...)
		got[c.name] = r
		t.Logf("%-26s exit=%d findings=%d\n%s%s", c.name, r.code, count(r), vetIndent(r.stdout), vetIndent(r.stderr))
	}

	// Findings, clean, and the no-findings contract.
	vetExpectCode(t, got, "gosree findings", 3)
	vetExpectCode(t, got, "gosree clean", 0)
	vetExpectCode(t, got, "vet findings", 1)
	vetExpectCode(t, got, "vet clean", 0)
	vetExpectCode(t, got, "gosree no args", 1)

	// Fail-closed on input it cannot analyse: a non-zero exit and no claim of a clean result.
	for _, name := range []string{"gosree type error", "gosree syntax error", "gosree one broken file", "gosree missing import",
		"gosree dependency broken", "gosree nonexistent", "vet type error", "vet syntax error", "vet one broken file", "vet nonexistent"} {
		if got[name].code == 0 {
			t.Errorf("%s: exit 0 on input that could not be analysed: a gate would pass it", name)
		}
	}
	// A load error under -json still exits non-zero (the error is not a diagnostic).
	vetExpectCode(t, got, "gosree type error -json", 1)

	// -fix: gosree offers no suggested fixes, and the x/tools driver in -fix mode prints no diagnostics
	// and exits 0 (go/analysis/internal/checker/checker.go:194-219). cmd/gosree therefore refuses the
	// flag (RB-FIX, fixed): exit 2 and a message, instead of a pass for a package with findings.
	if r := got["gosree -fix"]; r.code != 2 || !strings.Contains(r.stderr, "-fix is not supported") {
		t.Errorf("RB-FIX: gosree -fix exits %d with stderr %q; want 2 and a refusal", r.code, r.stderr)
	}

	// -c prints context and keeps the findings exit code.
	vetExpectCode(t, got, "gosree -c=1", 3)

	// Cross targets reach the build-constrained files in both drivers.
	if count(got["gosree GOOS=windows"]) == 0 || count(got["gosree host target"]) != 0 {
		t.Errorf("GOOS not honoured: windows %d findings, host %d", count(got["gosree GOOS=windows"]), count(got["gosree host target"]))
	}
	if count(got["vet GOOS=windows"]) != count(got["gosree GOOS=windows"]) {
		t.Errorf("drivers disagree under GOOS=windows: vet %d, gosree %d", count(got["vet GOOS=windows"]), count(got["gosree GOOS=windows"]))
	}
	// -tags: go vet honours it; the driver of gosree accepts it and ignores it ("no effect
	// (deprecated)", go/analysis/internal/analysisflags/flags.go:86). cmd/gosree hands it to the go
	// command through GOFLAGS (RB-TAGS, fixed), so both drivers analyse the file it guards.
	// GOFLAGS=-tags=... works for both.
	if a, v := count(got["gosree -tags vettag"]), count(got["vet -tags vettag"]); a != v || a == 0 || got["gosree -tags vettag"].code == 0 {
		t.Errorf("RB-TAGS: gosree -tags=vettag: exit %d, %d findings; go vet -tags=vettag: exit %d, %d findings",
			got["gosree -tags vettag"].code, a, got["vet -tags vettag"].code, v)
	}
	if count(got["gosree GOFLAGS=-tags"]) != count(got["vet -tags vettag"]) {
		t.Errorf("GOFLAGS=-tags: gosree %d findings, go vet -tags %d", count(got["gosree GOFLAGS=-tags"]), count(got["vet -tags vettag"]))
	}

	// Test variants: the text driver de-duplicates the package analysed with and without its tests,
	// and -test=false drops only the test files' findings.
	if a, b := count(got["gosree -test=true"]), count(got["vet test variant"]); a != b {
		t.Errorf("standalone (text, -test) reports %d findings for vetfacts/b, go vet %d", a, b)
	}
	if a, b := count(got["gosree -test=false"]), count(got["gosree -test=true"]); a >= b {
		t.Errorf("-test=false (%d findings) should drop the test files' findings (-test=true: %d)", a, b)
	}

	// Regression lock for RB-NOPOS: neither driver may print a position-less "-: BORROW004" line.
	for _, name := range []string{"gosree nopos text", "vet nopos text"} {
		if strings.Contains(got[name].stderr, "\n-: BORROW004") || strings.HasPrefix(got[name].stderr, "-: BORROW004") {
			t.Errorf("RB-NOPOS regression: %s prints a diagnostic with no file position:\n%s", name, got[name].stderr)
		}
	}
}

// vetLoopvar captures a for-loop variable in goroutines. Before go1.22 the variable is shared by
// every iteration, so the goroutines race with the loop's increment; from go1.22 each iteration has
// its own. The go line of go.mod selects the semantics.
const vetLoopvar = `package loopvar

import "sync"

type Job struct{ ID int }

func Spawn(jobs []Job) {
	var wg sync.WaitGroup
	for i := 0; i < len(jobs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs[i].ID++
		}()
	}
	wg.Wait()
}

func SpawnPtr() {
	for _, j := range []*Job{{}, {}} {
		go func() { j.ID++ }()
	}
}
`

// TestVetModuleGoVersion runs both drivers on modules whose go.mod names an older language version
// (go1.21: per-loop variables), the current one, and a newer one than the toolchain.
func TestVetModuleGoVersion(t *testing.T) {
	bin := vetBuildGosree(t)
	findings := map[string]map[vetFinding]int{}
	for _, ver := range []string{"1.21", "1.27", "1.99"} {
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		vetWrite(t, dir, map[string]string{
			"go.mod":             "module example.com/loopvar\n\ngo " + ver + "\n",
			"loopvar/loopvar.go": vetLoopvar,
		})
		env := []string{"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local"}
		alone := vetRun(t, dir, env, bin, "./...")
		vet := vetRun(t, dir, env, "go", "vet", "-vettool="+bin, "./...")
		a := vetParseText(dir, dir, alone.stderr)
		v := vetParseText(dir, dir, vet.stderr)
		n := 0
		for _, c := range a {
			n += c
		}
		findings[ver] = a
		t.Logf("go %s: gosree exit %d, %d findings; go vet exit %d\n%s%s", ver, alone.code, n, vet.code, vetIndent(alone.stderr), vetIndent(vet.stderr))
		if onlyA, onlyV := vetDiff(a, v); len(onlyA)+len(onlyV) > 0 {
			t.Errorf("go %s: drivers disagree\nonly gosree: %v\nonly vet: %v", ver, onlyA, onlyV)
		}
		if ver == "1.99" && (alone.code == 0 || vet.code == 0) {
			t.Errorf("go 1.99 module under go1.27.3 with GOTOOLCHAIN=local: gosree exit %d, vet exit %d; a gate would pass code it never analysed", alone.code, vet.code)
		}
	}
	has := func(ver string, line int, prefix string) bool {
		for f := range findings[ver] {
			if f.line == line && strings.HasPrefix(f.msg, prefix) {
				return true
			}
		}
		return false
	}
	// go1.21: the shared loop variables race with the loop's own increment and advance.
	if !has("1.21", 9, "SAFE003: variable i is used after") || !has("1.21", 20, "SAFE003: variable j is used after") {
		t.Errorf("go1.21 module: the per-loop variables i and j shared with goroutines are not reported:\n%s", vetDump(findings["1.21"]))
	}
	// go1.27: each iteration has its own i and j, so neither is reported.
	if has("1.27", 9, "SAFE003: variable i") || has("1.27", 20, "SAFE003: variable j") {
		t.Errorf("go1.27 module: per-iteration loop variables reported as shared:\n%s", vetDump(findings["1.27"]))
	}
	// Regression lock for RB-LOOPCOPY: the go1.22+ per-iteration copy of a 3-clause loop variable
	// must not be reported as a use-after-capture at the loop header.
	if has("1.27", 9, "SAFE003: this value is used after it was captured by a goroutine") {
		t.Errorf("RB-LOOPCOPY regression: go1.22+ per-iteration copy of a 3-clause loop variable reported as SAFE003:\n%s", vetDump(findings["1.27"]))
	}
}

func vetExpectCode(t *testing.T, got map[string]vetResult, name string, want int) {
	t.Helper()
	if got[name].code != want {
		t.Errorf("%s: exit %d, want %d", name, got[name].code, want)
	}
}

func vetIndent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return "    | " + strings.ReplaceAll(s, "\n", "\n    | ") + "\n"
}
