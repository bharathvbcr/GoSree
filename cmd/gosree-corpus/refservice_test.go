package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestTotalAddsEveryField checks that the module total sums every Stats field: add names them one by
// one, and a field it misses would total 0 in every report.
func TestTotalAddsEveryField(t *testing.T) {
	var s ownership.Stats
	v := reflect.ValueOf(&s).Elem()
	for i := range v.NumField() {
		v.Field(i).SetInt(int64(i + 1))
	}
	var total PackageStats
	total.add(s)
	total.add(s)
	got := reflect.ValueOf(total.Stats)
	for i := range got.NumField() {
		if want := int64(2 * (i + 1)); got.Field(i).Int() != want {
			t.Errorf("total %s = %d, want %d", got.Type().Field(i).Name, got.Field(i).Int(), want)
		}
	}
}

func TestLoadAnnotationTable(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "t.tsv")
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	header := strings.Join(tableColumns, "\t") + "\n"
	tab, err := loadAnnotationTable(write("# a comment\n#rev abc123\n" + header +
		"a/b.go\tfunc (s *S) M(\t\\t\\tmu.Lock()\t2\tbefore\t//gosree:unsafe reason\tSAFE002\tPOLICY:x\tc\tnote\n" +
		"a/b.go\ttype S struct {\t\\tn int\t\ttrail\t//gosree:guarded_by mu\tSAFE008\tGUARDED\t\t\n"))
	if err != nil {
		t.Fatal(err)
	}
	if tab.rev != "abc123" || len(tab.rows) != 2 {
		t.Fatalf("got rev %q and %d rows, want abc123 and 2", tab.rev, len(tab.rows))
	}
	r := tab.rows[0]
	if r.line != "\t\tmu.Lock()" || r.occurrence != 2 || !r.before || r.verdict != "POLICY:x" {
		t.Errorf("row 1 = %+v", r)
	}
	if tab.rows[1].before || tab.rows[1].occurrence != 0 {
		t.Errorf("row 2 = %+v", tab.rows[1])
	}
	if got := directiveCounts(tab.rows); got["unsafe"] != 1 || got["guarded_by"] != 1 {
		t.Errorf("directiveCounts = %v", got)
	}
	for _, tc := range []struct{ body, want string }{
		{"file\tscope\n", "header must be"},
		{header + "a.go\tfunc F(\t\t\tbefore\t//gosree:unsafe r\tX\n", "columns"},
		{header + "a.go\tfunc F(\t\t0\tbefore\t//gosree:unsafe r\tX\tV\t\t\n", "occurrence"},
		{header + "a.go\tfunc F(\t\t\tabove\t//gosree:unsafe r\tX\tV\t\t\n", "mode"},
		{header + "a.go\tfunc F(\t\t\tbefore\t// not a directive\tX\tV\t\t\n", "required"},
		{header + "a.go\tfunc F(\t\t\tbefore\t//gosree:unsafe r\tX\t\t\t\n", "required"},
		{"# only comments\n", "no header"},
	} {
		if _, err := loadAnnotationTable(write(tc.body)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("table %q: got %v, want an error containing %q", tc.body, err, tc.want)
		}
	}
}

// TestCheckApplied checks that a run whose parsed directives differ from the table fails.
func TestCheckApplied(t *testing.T) {
	rows := []annotation{
		{directive: "//gosree:unsafe r"},
		{directive: "//gosree:guarded_by mu"},
		{directive: "//gosree:iso fn, onPanic"},
	}
	ok := PackageStats{Findings: map[string]int{}}
	ok.UnsafeScope, ok.Guarded, ok.Iso = 1, 1, 2
	if err := checkApplied(rows, ok, true); err != nil {
		t.Errorf("matching counts: %v", err)
	}
	short := ok
	short.Iso = 1
	if err := checkApplied(rows, short, true); err == nil || !strings.Contains(err.Error(), "iso: table has 2, gosree parsed 1") {
		t.Errorf("an iso name that did not parse: got %v", err)
	}
	bad := ok
	bad.Findings = map[string]int{"SAFE007": 1}
	if err := checkApplied(rows, bad, true); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("a malformed directive: got %v", err)
	}
	if err := checkApplied([]annotation{{directive: "//gosree:bogus"}}, PackageStats{}, true); err == nil || !strings.Contains(err.Error(), "unknown directive") {
		t.Errorf("an unknown directive: got %v", err)
	}
	// A row in a test file is written into the copy either way, but only loaded with -tests.
	withTest := append(rows, annotation{file: "p/x_test.go", directive: "//gosree:unsafe r"})
	if err := checkApplied(withTest, ok, false); err != nil {
		t.Errorf("a test-file row without -tests: %v", err)
	}
	if err := checkApplied(withTest, ok, true); err == nil || !strings.Contains(err.Error(), "unsafe: table has 2, gosree parsed 1") {
		t.Errorf("a test-file row that did not parse with -tests: got %v", err)
	}
}

// TestArchiveRev archives a subdirectory of a scratch repository at a commit and checks that later
// commits, files outside the subdirectory and the checkout itself play no part.
func TestArchiveRev(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	put := func(name, body string) {
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	put("backend/svc/go.mod", "module svc\n")
	put("backend/svc/pkg/a.go", "package pkg // v1\n")
	put("other/x.go", "package other\n")
	git("add", ".")
	git("commit", "-q", "-m", "one")
	first := git("rev-parse", "HEAD")
	put("backend/svc/pkg/a.go", "package pkg // v2\n")
	git("commit", "-q", "-am", "two")
	put("backend/svc/pkg/a.go", "package pkg // uncommitted\n")

	dst := filepath.Join(t.TempDir(), "out")
	sha, err := archiveRev(repo, first[:7], "backend/svc", dst)
	if err != nil {
		t.Fatal(err)
	}
	if sha != first {
		t.Errorf("sha = %s, want %s", sha, first)
	}
	got, err := os.ReadFile(filepath.Join(dst, "pkg", "a.go"))
	if err != nil || string(got) != "package pkg // v1\n" {
		t.Errorf("pkg/a.go = %q, %v; want the first commit's content", got, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "go.mod")); err != nil {
		t.Errorf("go.mod not at the top: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "other")); err == nil {
		t.Error("a directory outside -subdir was extracted")
	}
	if _, err := archiveRev(repo, first, "missing", dst); err == nil {
		t.Error("archiving a missing subdirectory succeeded")
	}
	if _, err := archiveRev(repo, "no-such-rev", "backend/svc", dst); err == nil {
		t.Error("archiving a missing commit succeeded")
	}
}

func TestEnclosingFunc(t *testing.T) {
	src := "package p\n\nvar v = 1\n\nfunc F() {}\n\nfunc (s *S) M() {}\n\nfunc (g G[T]) N() {}\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pos := func(substr string) token.Pos { return f.Pos() + token.Pos(strings.Index(src, substr)) }
	for _, tc := range []struct{ at, want string }{
		{"v = 1", ""},
		{"F() {}", "F"},
		{"M() {}", "(*S).M"},
		{"N() {}", "G.N"},
	} {
		if got := enclosingFunc([]*ast.File{f}, pos(tc.at)); got != tc.want {
			t.Errorf("at %q: got %q, want %q", tc.at, got, tc.want)
		}
	}
}
