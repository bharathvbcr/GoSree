package errcheck_test

import (
	"fmt"
	"sort"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/errcheck"
)

// messages runs the analyzer over one source file and returns "line: message" for every diagnostic,
// sorted by line.
func messages6d5a(t *testing.T, src string) []string {
	t.Helper()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"m/m.go": src})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	results := analysistest.Run(&recordT{}, dir, errcheck.Analyzer, "m")
	type row struct {
		line int
		text string
	}
	var rows []row
	for _, r := range results {
		for _, d := range r.Diagnostics {
			rows = append(rows, row{r.Pass.Fset.Position(d.Pos).Line, d.Message})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].line < rows[j].line })
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%d: %s", r.line, r.text)
	}
	return out
}

const hdr6d5a = `package m

import "errors"

type T struct{ n int }

var errNo = errors.New("no")

func a() error { return errNo }
func b() error { return errNo }

func setup() error { return errNo }

func workV() (*T, error) { return nil, errNo }

func use(t *T) int { return t.n }
`

// ft-6d5a: every ERR003 message names the variable once, in single quotes. The overwrite and shadow
// cases, a parameter, and the closure pair next to its no-closure twin are pinned to the exact text.
func TestMsg6d5aERR003Quoting(t *testing.T) {
	const overwrite = "ERR003: assignment to 'err' overwrites an earlier unchecked error"
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "local cell overwrite",
			src: hdr6d5a + `
func Local() error {
	var err error
	err = a()
	err = b()
	return err
}
`,
			want: []string{"21: " + overwrite},
		},
		{
			name: "captured cell overwrite",
			src: hdr6d5a + `
func Captured() error {
	var err error
	f := func() error { return err }
	err = a()
	err = b()
	return f()
}
`,
			want: []string{"22: " + overwrite},
		},
		{
			name: "captured parameter overwrite",
			src: hdr6d5a + `
func CapturedParam(err error) error {
	f := func() error { return err }
	err = a()
	err = b()
	return f()
}
`,
			want: []string{"21: " + overwrite},
		},
		{
			name: "parameter overwrite",
			src: hdr6d5a + `
func Param(err error) error {
	err = a()
	err = b()
	return err
}
`,
			want: []string{"20: " + overwrite},
		},
		{
			name: "shadow",
			src: hdr6d5a + `
func Shadow() error {
	err := a()
	if true {
		err := b()
		_ = err
	}
	return err
}
`,
			want: []string{"21: ERR003: inner 'err' declaration shadows outer 'err', losing the inner error on fallthrough"},
		},
		{
			name: "closure pair",
			src: hdr6d5a + `
func Closure() int {
	var v *T
	var err error
	err = setup()
	func() { v, err = workV() }()
	if err != nil {
		return 0
	}
	return use(v)
}
`,
			want: []string{"22: " + overwrite},
		},
		{
			name: "closure pair twin",
			src: hdr6d5a + `
func Twin() int {
	var v *T
	var err error
	err = setup()
	v, err = workV()
	if err != nil {
		return 0
	}
	return use(v)
}
`,
			want: []string{"22: " + overwrite},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := messages6d5a(t, tc.src)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("messages:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
