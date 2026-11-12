package ownership

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// The tests in this file lower the two bounds of the handler write check and the shared-load check,
// or call the reporting point directly, so they are inside the package. What they pin: a check that
// reached a bound says so where it stopped, and no finding is reported at no position.

func lower(t *testing.T, limit *int, to int) {
	t.Helper()
	was := *limit
	*limit = to
	t.Cleanup(func() { *limit = was })
}

func analyse(t *testing.T, src string) {
	t.Helper()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"caps/caps.go": src})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, Analyzer, "caps")
}

// TestHandlerStepLimitIsReported: with the scan limit at 2, the check of a handler whose write is
// three calls down stops before it gets there. It used to stop without a word, and the handler read
// as clean. It now reports at the registration that it stopped. That report is its own finding: the
// write it did find on the same line is still reported, and a hatch on a registration mutes it with
// the rest of that handler's findings.
func TestHandlerStepLimitIsReported(t *testing.T) {
	lower(t, &handlerStepLimit, 2)
	analyse(t, `package caps

import "net/http"

type handler struct{ runs, seen int }

func (h *handler) f1() { h.f2() }

func (h *handler) f2() { h.f3() }

func (h *handler) f3() { h.runs++ }

func register(mux *http.ServeMux) {
	h := &handler{}
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) { h.seen++; h.f1() }) // want "is written here by a handler" "SAFE008: the write check of the handler registered with .* stopped after scanning 2 function bodies"
}

func hatched(mux *http.ServeMux) {
	h := &handler{}
	//gosree:unsafe the handlers of this mux run one at a time
	mux.HandleFunc("/h", func(http.ResponseWriter, *http.Request) { h.seen++; h.f1() })
}
`)
}

// manyChoices is a function body that picks one of cases+1 new boxes in a switch and writes it.
func manyChoices(cases int) string {
	var b strings.Builder
	b.WriteString("\tvar b *box\n\tswitch k {\n")
	for i := range cases {
		fmt.Fprintf(&b, "\tcase %d:\n\t\tb = &box{}\n", i)
	}
	b.WriteString("\tdefault:\n\t\tb = &box{}\n\t}\n")
	return b.String()
}

// TestRootWalkBudgetIsReported: with the walk budget at 8, the walk back from a write through one of
// 21 pointers stops before it has looked at them all. None of them is shared here, but the check
// cannot know that: it used to answer "not shared", and now reports where it gave up, under the rule
// whose check it was. With the real budget the same code is clean.
func TestRootWalkBudgetIsReported(t *testing.T) {
	handler := func(want string) string {
		return "package caps\n\nimport \"net/http\"\n\ntype box struct{ n int }\n\ntype handler struct{ k int }\n\n" +
			"func (h *handler) serve(http.ResponseWriter, *http.Request) {\n\tk := h.k\n" + manyChoices(20) + "\tb.n++" + want + "\n}\n\n" +
			"func register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/\", (&handler{}).serve)\n}\n"
	}
	load := func(want string) string {
		return "package caps\n\nimport \"sync/atomic\"\n\ntype box struct{ n int }\n\nvar cur atomic.Pointer[box]\n\n" +
			"func f(k int) int {\n\tc := cur.Load()\n" + manyChoices(20) + "\tb.n++" + want + "\n\treturn c.n\n}\n"
	}
	// The walk that is cut is inside a getter here, and what a getter's result leads back to is
	// worked out once and kept. The second use of the result reads the kept answer, and must still
	// say that the answer came from a walk that was cut.
	getter := func(first, second string) string {
		return "package caps\n\nimport \"net/http\"\n\ntype box struct{ n int }\n\ntype handler struct{ k int }\n\n" +
			"func (h *handler) pick(k int) *box {\n" + manyChoices(20) + "\treturn b\n}\n\n" +
			"func (h *handler) serve(http.ResponseWriter, *http.Request) {\n\tb := h.pick(h.k)\n\tb.n++" + first + "\n\tb.n--" + second + "\n}\n\n" +
			"func register(mux *http.ServeMux) {\n\tmux.HandleFunc(\"/\", (&handler{}).serve)\n}\n"
	}
	t.Run("through a getter, real budget", func(t *testing.T) { analyse(t, getter("", "")) })
	t.Run("through a getter, budget 8", func(t *testing.T) {
		lower(t, &rootWalkBudget, 8)
		want := ` // want "SAFE008: where a value used here comes from could not be followed"`
		analyse(t, getter(want, want))
	})
	t.Run("handler write, real budget", func(t *testing.T) { analyse(t, handler("")) })
	t.Run("shared load, real budget", func(t *testing.T) { analyse(t, load("")) })
	t.Run("handler write, budget 8", func(t *testing.T) {
		lower(t, &rootWalkBudget, 8)
		analyse(t, handler(` // want "SAFE008: where a value used here comes from could not be followed to the end .more than 8 values"`))
	})
	t.Run("shared load, budget 8", func(t *testing.T) {
		lower(t, &rootWalkBudget, 8)
		analyse(t, load(` // want "SAFE005: where a value used here comes from could not be followed to the end .more than 8 values"`))
	})
}

// TestReportNeverWithoutPosition: the reporting point is handed a finding with no position. It used
// to report it there, and a second one of the same rule was dropped as a duplicate of the first
// whatever it said. Each is now reported, at the transfer being checked when there is one and at the
// package clause when there is not, and says that this is not where the code is. A repeat of the
// same finding is still one finding.
func TestReportNeverWithoutPosition(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n\nfunc f() {}\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []analysis.Diagnostic
	st := &state{
		pass:       &analysis.Pass{Fset: fset, Files: []*ast.File{file}, Report: func(d analysis.Diagnostic) { got = append(got, d) }},
		ix:         &annot.Index{},
		stats:      &Stats{},
		reports:    map[reportKey]bool{},
		suppressed: map[reportKey]bool{},
	}
	for _, cat := range []string{HandlerWrite, UseAfter, Imm} {
		got = nil
		st.transferAt = token.NoPos
		st.report(token.NoPos, cat, "first")
		st.report(token.NoPos, cat, "second")
		st.report(token.NoPos, cat, "first")
		st.transferAt = file.Decls[0].Pos()
		st.report(token.NoPos, cat, "third")
		if len(got) != 3 {
			t.Fatalf("%s: %d diagnostics for three distinct findings with no position, want 3: %v", cat, len(got), got)
		}
		for i, want := range []struct {
			pos   token.Pos
			where string
		}{
			{file.Name.Pos(), "first; the code this is about has no source position, so it is reported at the package clause"},
			{file.Name.Pos(), "second; the code this is about has no source position, so it is reported at the package clause"},
			{file.Decls[0].Pos(), "third; the code this is about has no source position, so it is reported at the transfer that causes it"},
		} {
			if got[i].Pos != want.pos || got[i].Message != cat+": "+want.where {
				t.Errorf("%s: diagnostic %d is %q at %s, want %q at %s", cat, i, got[i].Message, fset.Position(got[i].Pos), cat+": "+want.where, fset.Position(want.pos))
			}
		}
	}
}

// TestSharedLoadPosLastResort: an instruction of a synthesised wrapper has no position. The
// shared-load check reports it at the calls that enter the wrapper when it checked it there
// (forwardShared), and so not at the instruction; when it did not, it reports it at the Load, never
// at no position.
func TestSharedLoadPosLastResort(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n\ntype T struct{ n int }\n\nfunc (t *T) M() { t.n++ }\n\nfunc f(t *T) func() { return t.M }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _, err := ssautil.BuildPackage(&types.Config{Importer: importer.Default()}, fset, types.NewPackage("p", "p"), []*ast.File{file}, ssa.BuilderMode(0))
	if err != nil {
		t.Fatal(err)
	}
	var wrapper *ssa.Function
	var inSource ssa.Instruction
	for _, b := range pkg.Func("f").Blocks {
		for _, ins := range b.Instrs {
			if mc, ok := ins.(*ssa.MakeClosure); ok {
				wrapper, inSource = mc.Fn.(*ssa.Function), ins
			}
		}
	}
	if wrapper == nil || wrapper.Synthetic == "" {
		t.Fatalf("f does not build a bound-method wrapper: %v", wrapper)
	}
	fw, ok := forwardedCall(wrapper, nil, []ssa.Value{pkg.Func("f").Params[0]})
	if !ok || fw.callee.Name() != "M" {
		t.Fatalf("forwardedCall(%s) = %v, %v; want the call of M", wrapper, fw.callee, ok)
	}
	if fw.call.Pos().IsValid() {
		t.Fatalf("the wrapper's call has a position (%s); this test needs one that has none", fset.Position(fw.call.Pos()))
	}
	st := &state{pass: &analysis.Pass{Fset: fset}}
	orig := sharedLoadOrigin{pos: file.Decls[1].Pos()}
	if pos, located, report := st.sharedLoadPos(fw.call, orig); pos != orig.pos || located || !report {
		t.Errorf("an unforwarded call with no position: got %s, located=%v, report=%v; want the Load's position, not located, reported", fset.Position(pos), located, report)
	}
	st.forwardedCalls = map[ssa.Instruction]bool{fw.call: true}
	if _, _, report := st.sharedLoadPos(fw.call, orig); report {
		t.Error("a call checked at the calls that enter its wrapper is reported again inside the wrapper")
	}
	if pos, located, report := st.sharedLoadPos(inSource, orig); pos != inSource.Pos() || !located || !report {
		t.Errorf("an instruction with a position: got %s, located=%v, report=%v; want its own position", fset.Position(pos), located, report)
	}
}
