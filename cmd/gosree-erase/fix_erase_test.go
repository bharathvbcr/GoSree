package main

// Tests added with the fixes for ER-01..ER-22.
//
// fixCorpus holds end-to-end programs (run through the five-check oracle of
// stress_erase_test.go: the original builds, the erased program builds and
// prints the same, no cap import is left, erasing again changes nothing).
// They are grouped by the mechanism they attack. The text-level and CLI tests
// below pin the choices the oracle cannot see: refusals, statement rewrites,
// atomic writes.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hdrMath = "package main\n\nimport (\n\t\"fmt\"\n\t\"math\"\n\t\"time\"\n\n\t\"github.com/bharathvbcr/gosree/cap\"\n)\n\n"

var fixCorpus = []e2eCase{
	// ======== mechanism: name resolution (what does this identifier refer to?) ========
	{name: "scope_type_param_named_cap", src: hdr + `type Box[cap any] struct{ v cap }

func (b Box[cap]) Get() cap { return b.v }

func Id[cap any](v cap) cap {
	var zero cap
	_ = zero
	return v
}

func main() {
	x := 4
	p := cap.BorrowMut(&x)
	*p = Id(*p) + cap.Move(1)
	fmt.Println(x, Box[string]{"b"}.Get())
}
`},
	{name: "scope_field_cap_holding_Mut_member", src: hdr + `type inner struct {
	Mut  []int
	Move func(int) int
}

type holder struct{ cap inner }

func use(h holder) int { return h.cap.Mut[0] + h.cap.Move(1) }

func main() {
	h := holder{inner{[]int{5}, func(v int) int { return v * 10 }}}
	x := 2
	p := cap.Borrow(&x)
	fmt.Println(use(h), *p)
}
`},
	{name: "scope_embedded_method_move_shadow_in_closure_in_loop", src: hdr + `type base struct{ n int }

func (b base) Move(d int) int {
	fmt.Println("base.Move", d)
	return b.n + d
}

type derived struct{ base }

func main() {
	d := derived{base{10}}
	total := 0
	for i := 0; i < 3; i++ {
		func() {
			cap := d
			total += cap.Move(i)
		}()
		x := i
		q := cap.BorrowMut(&x)
		total += *q
	}
	fmt.Println(total, d.Move(1))
}
`},
	{name: "scope_shadow_ends_before_marker_use", src: hdr + `func main() {
	x := 1
	{
		cap := struct{ Move func(int) int }{func(v int) int { return v + 100 }}
		fmt.Println(cap.Move(x))
	}
	p := cap.BorrowMut(&x)
	*p = cap.Move(7)
	fmt.Println(x)
}
`},
	{name: "scope_switch_range_typeswitch_shadows", src: hdr + `type S struct{}

func (S) String() string { return "S!" }

func describe(v any) string {
	switch cap := v.(type) {
	case fmt.Stringer:
		return cap.String()
	case int:
		return fmt.Sprint(cap + 1)
	}
	return "?"
}

func main() {
	for cap := range 2 {
		fmt.Println(cap)
	}
	for _, cap := range []struct{ Mut []int }{{[]int{7}}} {
		fmt.Println(cap.Mut[0])
	}
	x := 1
	var m cap.Mut[int] = &x
	fmt.Println(describe(S{}), describe(41), *m)
}
`},
	{name: "scope_param_and_result_named_cap_with_marker_types", src: hdr + `type named string

func (n named) String() string { return string(n) }

func pair(cap fmt.Stringer, p cap.Mut[int]) string { return cap.String() + fmt.Sprint(*p) }

func results() (cap fmt.Stringer, n cap.Mut[int]) {
	n = new(int)
	*n = 3
	return named("r"), n
}

func main() {
	x := 5
	var p cap.Mut[int] = &x
	s, n := results()
	fmt.Println(pair(named("p"), p), s, *n)
}
`},
	{name: "scope_receiver_named_cap", src: hdr + `type counter struct{ n int }

func (cap *counter) Move(d int) int { cap.n += d; return cap.n }

func (cap *counter) Ref() cap.Ref[int] { return &cap.n }

func main() {
	c := &counter{1}
	x := 3
	p := cap.BorrowMut(&x)
	*p += c.Move(2)
	fmt.Println(x, *c.Ref())
}
`},
	{name: "scope_dot_import_shadows_every_name", src: `package main

import (
	"fmt"

	. "github.com/bharathvbcr/gosree/cap"
)

func local(Mut []int, Borrow func(int) int) int {
	Move := func(v int) int { return v * 2 }
	var Ref = Move(Mut[0])
	return Ref + Borrow(1)
}

func real() {
	x := 3
	var p Mut[int] = &x
	var q Ref[int] = Borrow(p)
	fmt.Println(*Move(p), *q)
}

func main() {
	fmt.Println(local([]int{1}, func(i int) int { return i + 10 }))
	real()
}
`},
	{name: "scope_renamed_import_shadowed_then_used", src: `package main

import (
	"fmt"

	c "github.com/bharathvbcr/gosree/cap"
)

func main() {
	x := 1
	if c := (struct{ n int }{x + 4}); c.n > 0 {
		fmt.Println(c.n)
	}
	var p c.Mut[int] = c.BorrowMut(&x)
	*p = c.Move(9)
	fmt.Println(x)
}
`},

	// ======== mechanism: the value of cap.Move(arg), with and without a type ========
	{name: "move_explicit_type_arguments", src: `package main

import (
	"errors"
	"fmt"

	"github.com/bharathvbcr/gosree/cap"
)

type MyP *int

type Str struct{}

func (Str) String() string { return "str" }

func main() {
	fmt.Printf("%T\n", cap.Move[int64](5))
	fmt.Printf("%T\n", cap.Move[uint8](200))
	var e error = cap.Move[error](nil)
	fmt.Println(e == nil)
	var i any = cap.Move[*int](nil)
	fmt.Println(i == nil)
	var s any = cap.Move[fmt.Stringer](Str{})
	fmt.Printf("%T\n", s)
	fmt.Printf("%T\n", cap.Move[any](3))
	n := 3
	var q MyP = &n
	fmt.Printf("%T\n", cap.Move[*int](q))
	fmt.Println(cap.Move[[]int](nil) == nil, cap.Move[map[string]int](nil) == nil)
	ch := make(chan int, 1)
	fmt.Printf("%T\n", cap.Move[<-chan int](ch))
	fmt.Printf("%T\n", cap.Move[func(int) int](func(v int) int { return v }))
	fmt.Println(errors.Is(cap.Move[error](nil), nil))
	fmt.Println(cap.Move[float64](1)/2, cap.Move[float32](1)/4)
	fmt.Printf("%T\n", cap.Borrow[int](&n))
	fmt.Printf("%T %v\n", cap.BorrowMut[int](nil), cap.BorrowRob[int](nil) == nil)
}
`},
	{name: "move_untyped_constants_in_constant_sensitive_positions", src: `package main

import (
	"fmt"

	"github.com/bharathvbcr/gosree/cap"
)

type Weekday int

const (
	Sunday Weekday = iota
	Monday
)

const (
	big   = 1 << 40
	huge  = 1 << 62
	typed int8 = 100
)

func try(name string, f func()) {
	defer func() { fmt.Println(name, "panic:", recover() != nil) }()
	f()
}

func main() {
	fmt.Println(cap.Move(1)/2.0, cap.Move(1) == 1.0)
	fmt.Println(cap.Move(1)<<70>>70, cap.Move(255)+cap.Move(1))
	fmt.Printf("%T %v\n", cap.Move('a')+0.0, cap.Move('x')-'a')
	fmt.Println(cap.Move(huge)*4, cap.Move(big)*big*big, cap.Move(1<<62)*4)
	fmt.Println(cap.Move(typed)*2, cap.Move(int8(100))*2, cap.Move(Monday)+1)
	fmt.Println(^cap.Move(0), -cap.Move(1), !cap.Move(false), cap.Move(2.5)*2, cap.Move("a")+"b")
	x := 2
	fmt.Println(cap.Move(true) && x > 1)
	var arr [3]int
	try("index", func() { fmt.Println(arr[cap.Move(5)]) })
	try("div", func() { fmt.Println(10 / cap.Move(0)) })
	try("u8", func() { fmt.Println(uint8(cap.Move(300))) })
	switch x {
	case cap.Move(1):
		fmt.Println("one")
	case cap.Move(1):
		fmt.Println("dup")
	case cap.Move(2):
		fmt.Println("two")
	}
	m := map[int]string{cap.Move(1): "a", cap.Move(1): "b"}
	fmt.Println(m)
	fmt.Println(len(cap.Move("abc")), cap.Move(cap.Move(1))*2, cap.Move(cap.Move[int64](3))<<63>>63)
}
`},
	{name: "move_constants_where_the_default_type_is_enough", src: hdrMath + `func take(v any) { fmt.Printf("%T %v\n", v, v) }

func main() {
	x := cap.Move(1)
	var y = cap.Move(1.5)
	z := []any{cap.Move('r'), cap.Move("s"), cap.Move(true), cap.Move(2i)}
	take(cap.Move(7))
	take(cap.Move(math.MaxInt8))
	take(cap.Move(math.Pi))
	take(cap.Move(time.Second))
	fmt.Println(x, y, z)
	for i := range cap.Move(3) {
		fmt.Print(i)
	}
	fmt.Println()
}
`},
	{name: "move_nested_and_chained_helpers", src: hdr + `func main() {
	x := 5
	p := cap.Move(cap.BorrowMut(cap.Move(&x)))
	*p = cap.Move(cap.Move(cap.Move(6))) + cap.Move(cap.Move(1))
	fmt.Println(x, cap.Move(cap.Move(2))<<63>>63, cap.Move(cap.Move[int8](100))*2)
}
`},

	// ======== mechanism: helper calls as statements, defer and go ========
	{name: "stmt_expression_and_parenthesised_statements", src: hdr + `func f(n int) int {
	fmt.Println("f", n)
	return n
}

func main() {
	x := 1
	cap.Move(x)
	cap.BorrowMut(&x)
	(cap.Move(f(3)))
	cap.Move[int](f(4))
	cap.Borrow[int](&x)
	cap.Move(1)
	cap.Move[*int](nil)
	switch {
	case true:
		cap.Move(f(7))
	}
	select {
	default:
		cap.Move(f(8))
	}
	fmt.Println("end", x)
}
`},
	{name: "stmt_defer_evaluates_argument_at_the_statement", src: hdr + `func f(n int) int {
	fmt.Println("f", n)
	return n
}

func g() (r int) {
	defer cap.Move(f(1))
	defer cap.Move(1)
	defer cap.BorrowMut(&r)
	defer cap.Move[string]("s")
	fmt.Println("g body")
	return f(2)
}

func main() {
	fmt.Println(g())
	for i := 0; i < 2; i++ {
		defer cap.Move(f(10 + i))
	}
	defer cap.Move(f)(9)
	fmt.Println("main end")
}
`},
	{name: "stmt_go_evaluates_argument_in_the_caller", src: hdr + `func f(n int) int {
	fmt.Println("f", n)
	return n
}

func main() {
	x := 1
	go cap.Move(f(6))
	go cap.BorrowMut(&x)
	go cap.Move(1)
	done := make(chan int)
	go func() { done <- cap.Move(f(7)) }()
	fmt.Println(<-done)
}
`},

	// ======== mechanism: the replacement must parse where it lands ========
	{name: "paren_composite_literal_in_every_header", src: `package main

import (
	"fmt"
	"image"

	"github.com/bharathvbcr/gosree/cap"
)

type P struct{ n int }

type Pair[T any] struct{ A T }

func has(p P) bool { return p.n > 0 }

func main() {
	if cap.Move(P{1}) == (P{1}) {
		fmt.Println("if")
	}
	if v := cap.Move(P{3}); v.n == 3 {
		fmt.Println("init", v.n)
	}
	for i := 0; i < cap.Move(P{2}).n; i++ {
		fmt.Println("for", i)
	}
	switch cap.Move(P{1}).n {
	case 1:
		fmt.Println("switch")
	}
	switch v := cap.Move(P{5}); v.n {
	case 5:
		fmt.Println("switch init")
	}
	for range cap.Move([]P{{1}, {2}}) {
		fmt.Println("range")
	}
	if cap.Move(image.Point{1, 2}) == (image.Point{1, 2}) {
		fmt.Println("qualified")
	}
	if cap.Move(Pair[int]{1}).A == 1 {
		fmt.Println("generic")
	}
	if has(cap.Move(P{4})) {
		fmt.Println("in call")
	}
	if func() bool { return cap.Move(P{1}) == P{1} }() {
		fmt.Println("in func literal")
	}
	if cap.Move(&P{6}).n == 6 {
		fmt.Println("address")
	}
	if cap.Move(P{1}) == cap.Move(P{1}) {
		fmt.Println("both")
	}
}
`},

	// ======== mechanism: helpers and markers used as values or imported oddly ========
	{name: "value_helpers_with_explicit_type_arguments", src: hdr + `func apply[T any](f func(*T) *T, p *T) *T { return f(p) }

func twice(f func(int) int, v int) int { return f(f(v)) }

func main() {
	x := 3
	f := cap.Move[int]
	var g func(string) string = cap.Move[string]
	h := map[string]func(*int) *int{
		"mut": cap.BorrowMut[int],
		"ref": cap.Borrow[int],
		"r2":  cap.BorrowRef[int],
		"m2":  cap.BorrowMub[int],
		"r3":  cap.BorrowRob[int],
	}
	fmt.Println(f(4), g("s"), twice(cap.Move[int], 5), *apply(cap.BorrowMut[int], &x), len(h))
	fmt.Println(*h["mut"](&x), *h["r3"](&x))
	nested := cap.Move[cap.Mut[int]]
	slice := cap.Move[[]string]
	fmt.Println(*nested(&x), slice([]string{"a"}))
	defer fmt.Println(cap.Move[int](5))
}
`},
	{name: "value_helpers_with_dot_import_and_generic_parameters", src: `package main

import (
	"fmt"

	. "github.com/bharathvbcr/gosree/cap"
)

func pick[T any](v T) T {
	f := Move[T]
	return f(v)
}

func main() {
	x := 7
	g := BorrowMut[int]
	var p Mut[int] = g(&x)
	fmt.Println(pick("t"), *p, Move[int](2)*3)
}
`},
	{name: "import_blank_and_named_of_the_same_package", src: `package main

import (
	"fmt"

	_ "embed"
	_ "github.com/bharathvbcr/gosree/cap"
	c "github.com/bharathvbcr/gosree/cap"
)

func main() {
	x := 1
	var p c.Mut[int] = &x
	fmt.Println(*c.Move(p))
}
`},
	{name: "import_blank_only", src: `package main

import (
	_ "github.com/bharathvbcr/gosree/cap"
)

func main() { println("blank") }
`},
	{name: "interface_constraint_may_embed_a_marker", src: hdr + `type T struct{ A int }

type C interface{ cap.Mut[T] }

func get[P C](p P) int { return (*T)(p).A }

func main() {
	fmt.Println(get(&T{4}))
}
`},

	// ======== mechanism: embedded markers cannot be erased exactly ========
	{name: "refuse_embedded_marker_in_nested_anonymous_struct", refuse: "embedded field", src: hdr + `type T struct{ A int }

func main() {
	v := struct{ cap.Ref[T] }{&T{1}}
	fmt.Println(v.A)
}
`},
	{name: "refuse_embedded_marker_through_dot_import", refuse: "embedded field", src: `package main

import (
	"fmt"

	. "github.com/bharathvbcr/gosree/cap"
)

type T struct{ A int }

type W struct {
	N int
	Iso[T]
}

func main() { fmt.Println(W{1, &T{2}}.A) }
`},

	// ======== mechanism: forms whose exact erasure is unknown are refused ========
	{name: "refuse_helper_value_without_type_arguments", refuse: "cap.BorrowMut", src: hdr + `func apply(f func(*int) *int, p *int) *int { return f(p) }

func main() {
	x := 1
	fmt.Println(*apply(cap.BorrowMut, &x))
}
`},
	{name: "refuse_constant_of_unknown_kind_in_an_operator", refuse: "may be a constant", src: strings.Replace(hdrMath, "\t\"time\"\n", "", 1) + `func main() {
	fmt.Println(cap.Move(math.MaxInt8) + 1)
}
`},
	{name: "refuse_default_type_name_redeclared", refuse: "redeclared", src: hdr + `type rune = int32

func main() {
	fmt.Println(cap.Move('a') + 1)
}
`},
}

// ---------------------------------------------------------------------------
// Text-level choices
// ---------------------------------------------------------------------------

func TestFixErasedText(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string // substrings of the erased file
		wantNot   []string
	}{
		{
			name: "statement_forms",
			src: hdr + `func f() int { return 1 }

func main() {
	x := 1
	cap.Move(x)
	cap.BorrowMut(&x)
	defer cap.Move(f())
	go cap.Move[string]("s")
	fmt.Println(x)
}
`,
			want:    []string{"\t_ = x\n", "\t_ = &x\n", "\t_ = f()\n", "\t_ = string(\"s\")\n"},
			wantNot: []string{"defer", "go ", "cap."},
		},
		{
			name: "untyped_constant_sink_and_operand",
			src: hdr + `func main() {
	a := cap.Move(1)
	b := cap.Move(1) / 2.0
	fmt.Println(a, b, cap.Move[int64](5))
}
`,
			want:    []string{"a := 1\n", "b := func() int { return 1 }() / 2.0", "int64(5)"},
			wantNot: []string{"cap."},
		},
		{
			name: "type_arguments_are_parenthesised_where_needed",
			src: hdr + `func main() {
	var p *int
	var f func()
	var s []int
	fmt.Println(cap.Move[*int](p), cap.Move[func()](f) == nil, cap.Move[[]int](s) == nil, cap.Borrow[int](nil))
}
`,
			want: []string{"(*int)(p)", "(func())(f)", "[]int(s)", "(*int)(nil)"},
		},
		{
			name: "value_helpers",
			src: hdr + `func main() {
	f := cap.Move[int]
	g := cap.BorrowMut[string]
	fmt.Println(f, g)
}
`,
			want: []string{"f := func(x int) int { return x }", "g := func(x *string) *string { return x }"},
		},
		{
			name: "composite_literal_header",
			src: hdr + `type P struct{ v int }

func main() {
	if cap.Move(P{1}) == (P{1}) {
		fmt.Println(cap.Move(P{2}))
	}
}
`,
			want: []string{"if (P{1}) == (P{1}) {", "fmt.Println(P{2})"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := mustErase(t, tc.src)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q in:\n%s", w, out)
				}
			}
			if again := mustErase(t, out); again != out {
				t.Errorf("not idempotent:\n%s\n---\n%s", out, again)
			}
			lockPassed(t)
		})
	}
}

func TestFixRefusalsNameTheProblem(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"embedded", hdr + "type T struct{ A int }\n\ntype W struct{ cap.Mut[T] }\n", "embedded field cap.Mut[T]"},
		{"value_without_type_arguments", hdr + "var f = cap.Move\n", "cap.Move is not a form this tool can erase"},
		{"unknown_constant", hdrMath + "var v = cap.Move(math.Pi) * 2\n", "math.Pi"},
	} {
		_, err := safeErase([]byte(tc.src))
		if err == nil || errors.Is(err, errErasePanicked) || !strings.Contains(err.Error(), "cannot erase this file safely") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a refusal mentioning %q", tc.name, err, tc.want)
		}
	}
	lockPassed(t)
}

// ---------------------------------------------------------------------------
// CLI behaviour that needs control of the eraser or of the file system
// ---------------------------------------------------------------------------

type runResult struct {
	out, errOut string
	code        int
}

func runInProcess(t *testing.T, erase eraseFunc, args ...string) runResult {
	t.Helper()
	var out, errOut strings.Builder
	code := runWith(args, strings.NewReader(""), &out, &errOut, erase)
	return runResult{out.String(), errOut.String(), code}
}

func TestFixPanicBecomesAFileErrorAndNothingIsWritten(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	mid := filepath.Join(dir, "m.go")
	z := filepath.Join(dir, "z.go")
	for _, f := range []string{a, z} {
		mustWrite(t, f, markedFile)
	}
	mustWrite(t, mid, markedFile+"\n// PANIC\n")
	panicky := func(src []byte) ([]byte, error) {
		if strings.Contains(string(src), "// PANIC") {
			panic("boom")
		}
		return Erase(src)
	}
	r := runInProcess(t, panicky, "-w", dir)
	if r.code != 1 || !strings.Contains(r.errOut, "m.go") || !strings.Contains(r.errOut, "boom") || !strings.Contains(r.errOut, "nothing was written") {
		t.Errorf("exit=%d stderr=%q", r.code, r.errOut)
	}
	if readFile(t, a) != markedFile || readFile(t, z) != markedFile {
		t.Errorf("files were written although one file panicked")
	}
	// Read-only modes keep going past the failure.
	r = runInProcess(t, panicky, "-l", dir)
	if r.code != 1 || !strings.Contains(r.out, "a.go") || !strings.Contains(r.out, "z.go") || strings.Contains(r.out, "m.go") {
		t.Errorf("-l: exit=%d stdout=%q stderr=%q", r.code, r.out, r.errOut)
	}
	lockPassed(t)
}

func TestFixUntouchedFilesAreNeitherListedNorWritten(t *testing.T) {
	dir := t.TempDir()
	crlf := filepath.Join(dir, "crlf.go")
	marked := filepath.Join(dir, "marked.go")
	mustWrite(t, crlf, "package p\r\n\r\nfunc  F( )  {\r\n}\r\n")
	mustWrite(t, marked, markedFile)
	before, _ := os.Stat(crlf)

	r := runInProcess(t, Erase, "-l", dir)
	if r.code != 0 || r.out != marked+"\n" {
		t.Errorf("-l lists untouched files: %+v", r)
	}
	r = runInProcess(t, Erase, "-d", dir)
	if r.code != 0 || strings.Contains(r.out, "crlf.go") {
		t.Errorf("-d shows untouched files: %+v", r)
	}
	r = runInProcess(t, Erase, "-w", dir, filepath.Join(dir, "..."))
	if r.code != 0 {
		t.Errorf("-w: %+v", r)
	}
	if readFile(t, crlf) != "package p\r\n\r\nfunc  F( )  {\r\n}\r\n" {
		t.Errorf("untouched file rewritten")
	}
	after, _ := os.Stat(crlf)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("untouched file was written (mtime changed)")
	}
	// The same file named twice is processed once.
	if readFile(t, marked) != "package p\n\nfunc F(p *int) int { return *p }\n" {
		t.Errorf("marked file not erased: %q", readFile(t, marked))
	}
	lockPassed(t)
}

func TestFixCommitIsAtomicAndRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	mustWrite(t, a, markedFile)
	good := result{target: target{display: "a.go", real: a}, src: []byte(markedFile), out: []byte("package p\n"), mode: 0o644}
	missing := result{target: target{display: "gone.go", real: filepath.Join(dir, "gone", "b.go")}, src: []byte("x"), out: []byte("y"), mode: 0o644}

	o := &options{stdout: &strings.Builder{}, stderr: &strings.Builder{}}
	err := o.commit([]result{good, missing})
	if err == nil {
		t.Fatal("commit succeeded for a file that does not exist")
	}
	if readFile(t, a) != markedFile {
		t.Errorf("first file not restored after a later write failed: %q", readFile(t, a))
	}

	// A file that changed between reading and writing is not overwritten.
	mustWrite(t, a, markedFile+"// edited meanwhile\n")
	if err := o.commit([]result{good}); err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Errorf("concurrent edit not detected: %v", err)
	}
	if readFile(t, a) != markedFile+"// edited meanwhile\n" {
		t.Errorf("concurrent edit overwritten")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gosree-erase") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
	lockPassed(t)
}

func TestFixDuplicateAndOverlappingArguments(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.go")
	mustWrite(t, f, markedFile)
	r := runInProcess(t, Erase, "-l", dir, f, filepath.Join(dir, "..."))
	if r.code != 0 || strings.Count(r.out, "a.go") != 1 {
		t.Errorf("a file reached by three arguments was listed %d times: %+v", strings.Count(r.out, "a.go"), r)
	}
	r = runInProcess(t, Erase, "-w", "-o", f)
	if r.code != 1 || !strings.Contains(r.errOut, "cannot use both -w and -o") {
		t.Errorf("-w with -o: %+v", r)
	}
	r = runInProcess(t, Erase, "-badflag")
	if r.code != 2 {
		t.Errorf("invalid flag exit %d, want 2", r.code)
	}
	r = runInProcess(t, Erase, filepath.Join(dir, "missing.go"))
	if r.code != 1 || !strings.Contains(r.errOut, "missing.go") {
		t.Errorf("missing file: %+v", r)
	}
	lockPassed(t)
}
