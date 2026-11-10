package main

// Stress suite for gosree-erase.
//
// Every subtest is either a regression LOCK (asserts correct behaviour) or a
// KNOWN BUG (asserts that the bug is still present and fails with
// "<id> appears fixed: flip this test to assert the correct behaviour" once
// the tool starts behaving). TestStressEraseLedger prints the bug registry so
// that a green run never reads as "no known bugs".
//
// The end-to-end oracle builds every corpus program twice, offline, inside a
// throwaway module whose path is github.com/bharathvbcr/gosree and which holds
// a byte-for-byte copy of ../../cap/cap.go, so the import path the eraser
// matches is the real one and no network or go.sum is needed.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Ledger
// ---------------------------------------------------------------------------

type bugInfo struct {
	severity string
	summary  string
}

// stressBugRegistry is the static list of every known bug this file encodes.
// It is static on purpose: the ledger must report open bugs even when only a
// subset of the stress tests ran.
var stressBugRegistry = map[string]bugInfo{
	"ER-23": {"contract-drift", "erasure parity holds for machine code (the README/docs wording), not for the whole binary: with -trimpath and line numbers aligned, the annotated build's __gopclntab is 8 bytes larger (inlined-helper metadata, inferred), so the files differ"},
}

var (
	ledgerMu        sync.Mutex
	ledgerConfirmed = map[string]int{}
	ledgerLocks     = map[string]bool{}
)

// knownBug records that bug id was observed. If present is false the bug no
// longer reproduces and the test fails so that somebody flips it to a lock.
func knownBug(t *testing.T, id string, present bool, detail string) {
	t.Helper()
	info, ok := stressBugRegistry[id]
	if !ok {
		t.Fatalf("knownBug: %s is not in stressBugRegistry", id)
	}
	if !present {
		t.Fatalf("%s appears fixed: flip this test to assert the correct behaviour (%s)", id, info.summary)
	}
	ledgerMu.Lock()
	ledgerConfirmed[id]++
	ledgerMu.Unlock()
	t.Logf("KNOWN BUG %s [%s]: %s\n%s", id, info.severity, info.summary, detail)
}

func lockPassed(t *testing.T) {
	ledgerMu.Lock()
	ledgerLocks[t.Name()] = true
	ledgerMu.Unlock()
}

// ---------------------------------------------------------------------------
// End-to-end harness
// ---------------------------------------------------------------------------

const capImportPath = "github.com/bharathvbcr/gosree/cap"

type stressModule struct {
	dir string
}

// otherCapSrc is a DIFFERENT package that is also named cap.
const otherCapSrc = `package cap

// Move is not an identity: it must never be erased.
func Move(x int) int { return x + 100 }
`

func goEnv() []string {
	env := os.Environ()
	return append(env, "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOSUMDB=off")
}

// newStressModule creates a throwaway module under t.TempDir() holding a copy
// of the real cap package and a second, unrelated package also named cap.
func newStressModule(t *testing.T) *stressModule {
	t.Helper()
	dir := t.TempDir()
	capSrc, err := os.ReadFile(filepath.Join("..", "..", "cap", "cap.go"))
	if err != nil {
		t.Fatalf("reading real cap/cap.go: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "go.mod"), "module github.com/bharathvbcr/gosree\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(dir, "cap", "cap.go"), string(capSrc))
	mustWrite(t, filepath.Join(dir, "other", "cap", "cap.go"), otherCapSrc)
	return &stressModule{dir: dir}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type e2eResult struct {
	origBuildErr   string
	origOut        string
	eraseErr       error
	erased         string
	erasedBuildErr string
	erasedOut      string
	reEraseErr     error
	reErased       string
}

func (r *e2eResult) residualCap() bool {
	return strings.Contains(r.erased, `"`+capImportPath+`"`)
}

func (r *e2eResult) idempotent() bool {
	return r.reEraseErr == nil && r.reErased == r.erased
}

// sound reports whether all five oracle properties hold.
func (r *e2eResult) sound() (bool, string) {
	switch {
	case r.origBuildErr != "":
		return false, "original does not build: " + r.origBuildErr
	case r.eraseErr != nil:
		return false, "Erase refused valid input: " + r.eraseErr.Error()
	case r.erasedBuildErr != "":
		return false, "erased output does not build: " + r.erasedBuildErr
	case r.origOut != r.erasedOut:
		return false, fmt.Sprintf("behaviour changed:\n--- original output\n%s--- erased output\n%s", r.origOut, r.erasedOut)
	case r.residualCap():
		return false, "erased output still imports " + capImportPath
	case !r.idempotent():
		return false, fmt.Sprintf("not idempotent (err=%v):\n--- first\n%s--- second\n%s", r.reEraseErr, r.erased, r.reErased)
	}
	return true, ""
}

func (r *e2eResult) dump() string {
	return fmt.Sprintf("erased source:\n%s\norig build err: %s\norig out: %q\nerase err: %v\nerased build err: %s\nerased out: %q",
		r.erased, r.origBuildErr, r.origOut, r.eraseErr, r.erasedBuildErr, r.erasedOut)
}

func (m *stressModule) buildRun(t *testing.T, rel string) (out string, buildErr string) {
	t.Helper()
	bin := filepath.Join(m.dir, "bin", strings.ReplaceAll(rel, "/", "_"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	b := exec.CommandContext(ctx, "go", "build", "-o", bin, "./"+rel)
	b.Dir = m.dir
	b.Env = goEnv()
	if o, err := b.CombinedOutput(); err != nil {
		return "", strings.TrimSpace(string(o)) + " (" + err.Error() + ")"
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer rcancel()
	r := exec.CommandContext(rctx, bin)
	o, err := r.CombinedOutput()
	if err != nil {
		return string(o) + "\nRUN ERROR: " + err.Error() + "\n", ""
	}
	return string(o), ""
}

func (m *stressModule) run(t *testing.T, name, src string) *e2eResult {
	t.Helper()
	res := &e2eResult{}
	mustWrite(t, filepath.Join(m.dir, "o", name, "main.go"), src)
	res.origOut, res.origBuildErr = m.buildRun(t, "o/"+name)

	erased, err := safeErase([]byte(src))
	res.eraseErr = err
	if err != nil {
		return res
	}
	res.erased = string(erased)
	again, err := safeErase(erased)
	res.reEraseErr = err
	res.reErased = string(again)

	mustWrite(t, filepath.Join(m.dir, "e", name, "main.go"), res.erased)
	res.erasedOut, res.erasedBuildErr = m.buildRun(t, "e/"+name)
	return res
}

// errErasePanicked marks an Erase call that panicked instead of returning.
var errErasePanicked = errors.New("Erase panicked")

// safeErase converts a panic inside Erase into an error wrapping
// errErasePanicked, so one crashing input cannot take the suite (and the
// shared temp module) down with it.
func safeErase(src []byte) (out []byte, err error) {
	defer func() {
		if p := recover(); p != nil {
			out, err = nil, fmt.Errorf("%w: %v", errErasePanicked, p)
		}
	}()
	return Erase(src)
}

type e2eCase struct {
	name string
	src  string
	// refuse is non-empty for a program that cannot be erased exactly: Erase
	// must fail (not panic) with an error containing this text, and must not
	// produce output.
	refuse string
	// extra runs additional assertions on an erasable program.
	extra func(t *testing.T, r *e2eResult)
}

const hdr = "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/bharathvbcr/gosree/cap\"\n)\n\n"

var stressCorpus = []e2eCase{
	// ---------------- locks: name resolution ----------------
	{name: "basic_markers", src: hdr + `type Item struct{ N int }

func bump(it cap.Mut[Item], by cap.Ref[int]) { it.N += *by }

func main() {
	it := &Item{1}
	n := 4
	bump(cap.BorrowMut(it), cap.Borrow(&n))
	var iso cap.Iso[Item] = cap.Move(it)
	var imm cap.Imm[Item] = iso
	var mub cap.Mub[Item] = cap.BorrowMub(it)
	var rob cap.Rob[Item] = cap.BorrowRob(it)
	var rr cap.Ref[Item] = cap.BorrowRef(it)
	fmt.Println(iso.N, imm.N, mub.N, rob.N, rr.N)
}
`},
	{name: "renamed_import_and_builtin_cap", src: `package main

import (
	"fmt"

	c "github.com/bharathvbcr/gosree/cap"
)

func main() {
	x := 5
	p := c.BorrowMut(&x)
	*p = 6
	var r c.Ref[int] = c.Borrow(&x)
	s := make([]int, 2, 7)
	fmt.Println(*r, cap(s), len(s))
}
`},
	{name: "dot_import", src: `package main

import (
	"fmt"

	. "github.com/bharathvbcr/gosree/cap"
)

func main() {
	x := 2
	var p Mut[int] = BorrowMut(&x)
	*p = Move(9)
	var r Ref[int] = Borrow[int](&x)
	fmt.Println(x, *r)
}
`},
	{name: "imported_twice", src: `package main

import (
	"fmt"

	"github.com/bharathvbcr/gosree/cap"
	gc "github.com/bharathvbcr/gosree/cap"
)

func main() {
	x := 1
	var p cap.Mut[int] = gc.BorrowMut(&x)
	*p = cap.Move(3)
	fmt.Println(x)
}
`},
	{name: "other_package_named_cap", src: `package main

import (
	"fmt"

	"github.com/bharathvbcr/gosree/other/cap"
)

func main() {
	fmt.Println(cap.Move(1))
}
`},
	{name: "gosree_renamed_other_named_cap", src: `package main

import (
	"fmt"

	gc "github.com/bharathvbcr/gosree/cap"
	"github.com/bharathvbcr/gosree/other/cap"
)

func main() {
	x := 1
	p := gc.BorrowMut(&x)
	fmt.Println(cap.Move(*p))
}
`},
	{name: "field_named_cap", src: hdr + `type mover struct{}

func (mover) Move(x int) int { return x * 10 }

type holder struct{ cap mover }

func main() {
	h := holder{}
	x := 3
	p := cap.Borrow(&x)
	fmt.Println(h.cap.Move(*p))
}
`},
	{name: "label_named_cap", src: hdr + `func main() {
	i := 0
cap:
	for {
		i = cap.Move(i + 1)
		if i > 2 {
			break cap
		}
	}
	fmt.Println(i)
}
`},

	// ---------------- locks: expression semantics ----------------
	{name: "conversion_to_marker", src: hdr + `func main() {
	x := 4
	var q any = &x
	p := cap.Mut[int](q.(*int))
	*p = 5
	r := cap.Ref[int](p)
	fmt.Println(x, *r)
}
`, extra: func(t *testing.T, r *e2eResult) {
		if !strings.Contains(r.erased, "(*int)(q.(*int))") {
			t.Errorf("conversion not parenthesised:\n%s", r.erased)
		}
	}},
	{name: "operator_precedence", src: hdr + `type P struct{ v int }

func (p *P) Get() int { return p.v }

func main() {
	a, b, c := 1, 2, 3
	fmt.Println(cap.Move(a+b)*c, -cap.Move(a-b), cap.Move(a<<1)|1, cap.Move(a+b) == 3, ^cap.Move(a|b))
	pp := &P{9}
	ppp := &pp
	fmt.Println(cap.Move(*ppp).Get(), (*cap.BorrowMut(&a))+1)
	*cap.BorrowMut(&a) = 10
	fmt.Println(a)
	fmt.Println(cap.Move(func() int { return 7 })())
	s := []int{4, 5, 6}
	fmt.Println(cap.Move(s)[1], cap.Move(s[1:])[0], cap.Move(s[:])[2])
	ch := make(chan int, 1)
	ch <- 8
	fmt.Println(cap.Move(<-ch) + 1)
	var i any = 3
	fmt.Println(cap.Move(i).(int) * 2)
	fmt.Println(float64(cap.Move(a)) / 4)
}
`},
	{name: "side_effects_once", src: hdr + `var n int

func next() int {
	n++
	fmt.Println("next", n)
	return n
}

func main() {
	arr := [4]int{}
	p := cap.BorrowMut(&arr[next()])
	*p = 9
	fmt.Println(arr, cap.Move(next()), cap.Move(cap.Move(next())))
	x := 1
	fmt.Println(cap.Move(x, ), cap.Move(
		x,
	))
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	fmt.Println(cap.Move(<-ch), cap.Move(<-ch))
	m := map[int]int{cap.Move(1): cap.Move(next())}
	fmt.Println(m)
}
`},
	{name: "composite_literal_contexts", src: hdr + `type P struct{ v int }

func main() {
	xs := []P{cap.Move(P{1}), cap.Move(P{v: 2})}
	ps := []*P{cap.BorrowMut(&xs[0])}
	for _, v := range cap.Move([]int{1, 2}) {
		fmt.Println(v)
	}
	fmt.Println(xs, ps[0].v, cap.Move(P{3}).v)
}
`},

	// ---------------- locks: type semantics ----------------
	{name: "type_positions", src: `package main

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/bharathvbcr/gosree/cap"
)

type T struct{ A int }

type M = cap.Mut[T]

type D cap.Ref[T]

func mk() cap.Mut[T] { return &T{1} }

func G[P interface{ ~cap.Mut[T] }](p P) int { return (*T)(p).A }

func H[E any](p cap.Mut[E]) E { return *p }

func main() {
	var s []cap.Mut[T] = []cap.Mut[T]{mk()}
	m := map[string]cap.Ref[T]{"a": s[0]}
	ch := make(chan cap.Mut[T], 1)
	ch <- s[0]
	var rc <-chan cap.Mut[T] = ch
	var fn func() cap.Imm[T] = mk
	var nested cap.Mut[cap.Ref[T]] = &s[0]
	var ps cap.Mut[[]int] = &[]int{1}
	var pm cap.Mut[map[string]int] = &map[string]int{"k": 2}
	var pf cap.Mut[func() int] = new(func() int)
	*pf = func() int { return 3 }
	var pc cap.Mut[chan int] = new(chan int)
	var pst cap.Mut[struct{ X int }] = &struct{ X int }{4}
	var d D = D(mk())
	var al M = mk()
	var sb cap.Mut[strings.Builder] = new(strings.Builder)
	sb.WriteString("sb")
	fmt.Println((<-rc).A, m["a"].A, fn().A, (*nested).A, (*ps)[0], (*pm)["k"], (*pf)(), *pc == nil, pst.X, d.A, al.A, sb.String())
	fmt.Println(reflect.TypeOf(al).String(), reflect.TypeOf(nested).String(), reflect.TypeOf(d).String(), reflect.TypeOf(ps).String())
	var x any = mk()
	switch v := x.(type) {
	case cap.Ref[int]:
		fmt.Println("int", v)
	case cap.Ref[T]:
		fmt.Println("T", v.A)
	}
	_, ok := x.(cap.Mut[T])
	fmt.Println(ok, G(mk()), G[cap.Mut[T]](mk()), H(cap.Borrow(&T{7})).A, H[int](new(int)))
}
`},

	// ---------------- former known bugs, now locks ----------------
	{name: "shadow_local_method_call", src: hdr + `type S struct{}

func (S) Move(x int) int {
	fmt.Println("S.Move called")
	return x + 1
}

func main() {
	x := 1
	p := cap.BorrowMut(&x)
	*p = 2
	{
		cap := S{}
		_ = cap
		fmt.Println(cap.Move(x))
	}
}
`},
	{name: "generic_type_param_marker_constraint", src: hdr + `type L[P cap.Mut[int]] struct{ v P }

type K[P cap.Ref[int], Q any] struct {
	v P
	q Q
}

func main() {
	x := 3
	l := L[*int]{&x}
	k := K[*int, string]{&x, "q"}
	fmt.Println(*l.v, *k.v, k.q)
}
`},
	{name: "shadow_param_method_call", src: hdr + `type Counter struct{ n *int }

func (c Counter) Borrow(p *int) *int { *c.n++; return p }

func use(cap Counter, v *int) *int { return cap.Borrow(v) }

func main() {
	calls := 0
	x := 1
	var r cap.Ref[int] = &x
	use(Counter{&calls}, r)
	fmt.Println("calls", calls)
}
`},
	{name: "explicit_type_args_dropped", src: hdr + `func main() {
	fmt.Printf("%T\n", cap.Move[int64](5))
	var i any = cap.Move[*int](nil)
	fmt.Println(i == nil)
	var j any = cap.Borrow[int](nil)
	fmt.Println(j == nil)
	fmt.Println(cap.Move[float64](1) / 2)
}
`},
	{name: "untyped_constant_retyped", src: hdr + `func main() {
	fmt.Println(cap.Move(1) / 2.0)
	fmt.Println(cap.Move(1) << 70 >> 70)
	fmt.Printf("%T\n", cap.Move('a')+0.0)
}
`},
	{name: "defer_moves_evaluation", src: hdr + `func f() int {
	fmt.Println("f evaluated")
	return 1
}

func main() {
	func() {
		defer cap.Move(f())
		fmt.Println("body")
	}()
}
`},
	{name: "dot_import_local_shadow", src: `package main

import (
	"fmt"

	. "github.com/bharathvbcr/gosree/cap"
)

func main() {
	x := 1
	var p Mut[int] = &x
	_ = p
	Move := func(v int) int { return v * 2 }
	_ = Move
	fmt.Println(Move(21))
}
`},
	{name: "expression_statement", src: hdr + `func main() {
	x := 1
	cap.Move(x)
	cap.BorrowMut(&x)
	fmt.Println(x)
}
`},
	{name: "defer_non_call_arg_panics", src: hdr + `func main() {
	defer cap.Move(1)
	fmt.Println("ok")
}
`},
	{name: "go_non_call_arg_panics", src: hdr + `func main() {
	x := 1
	go cap.BorrowMut(&x)
	fmt.Println("ok")
}
`},
	{name: "shadow_keeps_unused_import", src: hdr + `func main() {
	x := 1
	p := cap.BorrowMut(&x)
	*p = 2
	{
		cap := struct{ n int }{x}
		fmt.Println(cap.n)
	}
}
`},
	{name: "dot_import_value_use", src: `package main

import (
	"fmt"

	. "github.com/bharathvbcr/gosree/cap"
)

func main() {
	f := Move[int]
	fmt.Println(f(7))
}
`},
	{name: "constant_overflow", src: hdr + `func main() {
	fmt.Println(cap.Move(1<<62) * 4)
}
`},
	{name: "shadow_param_index", src: hdr + `func show(cap struct{ Mut []int }) { fmt.Println(cap.Mut[0]) }

func main() {
	x := 1
	var p cap.Mut[int] = &x
	show(struct{ Mut []int }{[]int{*p + 6}})
}
`},
	{name: "helper_as_value", src: hdr + `func apply(f func(*int) *int, p *int) *int { return f(p) }

func main() {
	f := cap.Move[int]
	x := 3
	fmt.Println(f(4), *apply(cap.BorrowMut[int], &x))
}
`},
	{name: "blank_import", src: `package main

import (
	"fmt"

	_ "github.com/bharathvbcr/gosree/cap"
)

func main() { fmt.Println("blank") }
`},
	{name: "composite_literal_in_if_header", src: hdr + `type P struct{ v int }

func main() {
	if cap.Move(P{1}) == (P{1}) {
		fmt.Println("eq")
	}
	switch cap.Move(P{2}) {
	case P{2}:
		fmt.Println("two")
	}
}
`},
	{name: "embedded_marker_field", refuse: "embedded field", src: `package main

import (
	"fmt"
	"reflect"

	"github.com/bharathvbcr/gosree/cap"
)

type T struct{ A int }

type W struct{ cap.Mut[T] }

func main() {
	w := W{&T{1}}
	fmt.Println(w.A, reflect.TypeOf(w).Field(0).Name)
}
`},
}

func TestStressE2ECorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs programs")
	}
	m := newStressModule(t)
	seen := map[string]bool{}
	t.Run("corpus", func(t *testing.T) {
		for _, tc := range allCorpus() {
			if seen[tc.name] {
				t.Fatalf("duplicate corpus name %s", tc.name)
			}
			seen[tc.name] = true
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				r := m.run(t, tc.name, tc.src)
				if r.origBuildErr != "" {
					t.Fatalf("corpus program is invalid Go (fix the fixture): %s", r.origBuildErr)
				}
				if tc.refuse != "" {
					if r.eraseErr == nil || errors.Is(r.eraseErr, errErasePanicked) || !strings.Contains(r.eraseErr.Error(), tc.refuse) || r.erased != "" {
						t.Fatalf("expected a refusal containing %q, got err=%v erased=%q", tc.refuse, r.eraseErr, r.erased)
					}
					lockPassed(t)
					return
				}
				if ok, why := r.sound(); !ok {
					t.Fatalf("erasure unsound: %s\n%s", why, r.dump())
				}
				if tc.extra != nil {
					tc.extra(t, r)
				}
				if !t.Failed() {
					lockPassed(t)
				}
			})
		}
	})
}

// allCorpus is the original corpus plus the programs added with the fixes.
func allCorpus() []e2eCase {
	return append(append([]e2eCase{}, stressCorpus...), fixCorpus...)
}

// ---------------------------------------------------------------------------
// Erase-level (text) attacks
// ---------------------------------------------------------------------------

func mustErase(t *testing.T, src string) string {
	t.Helper()
	out, err := safeErase([]byte(src))
	if err != nil {
		t.Fatalf("Erase failed: %v\ninput:\n%s", err, src)
	}
	return string(out)
}

func TestStressEraseText(t *testing.T) {
	t.Run("pragmas_keep_position", func(t *testing.T) {
		src := `//go:build linux || darwin

// Package main is a fixture.
package main

import (
	_ "embed"

	"github.com/bharathvbcr/gosree/cap"
)

//go:generate echo hi

//go:embed stress_erase_test.go
var data string

//go:noinline
func f(p cap.Mut[int]) int { return *p }

//line foo.go:10
func g() {}
`
		out := mustErase(t, src)
		for _, pair := range [][2]string{
			{"//go:build linux || darwin", "\n\n// Package main is a fixture.\npackage main"},
			{"//go:generate echo hi", "\n"},
			{"//go:embed stress_erase_test.go", "\nvar data string"},
			{"//go:noinline", "\nfunc f(p *int) int"},
			{"//line foo.go:10", "\nfunc g() {}"},
		} {
			if !strings.Contains(out, pair[0]+pair[1]) {
				t.Errorf("pragma %q lost its position:\n%s", pair[0], out)
			}
		}
		if !strings.HasPrefix(out, "//go:build") {
			t.Errorf("build constraint is no longer first:\n%s", out)
		}
		lockPassed(t)
	})

	t.Run("strings_and_comments_mentioning_markers", func(t *testing.T) {
		src := "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\n" +
			"// Use cap.Mut[T] and cap.Move(x) here.\n" +
			"var raw = `cap.Mut[T] cap.Move(x)`\n" +
			"var s = \"cap.Borrow(&x)\"\n" +
			"type Tag struct {\n\tF int `json:\"cap.Mut[T]\"`\n}\n\n" +
			"/* cap.Ref[int] */\nvar p cap.Ref[int]\n"
		out := mustErase(t, src)
		for _, keep := range []string{
			"// Use cap.Mut[T] and cap.Move(x) here.",
			"`cap.Mut[T] cap.Move(x)`",
			`"cap.Borrow(&x)"`,
			"`json:\"cap.Mut[T]\"`",
			"/* cap.Ref[int] */",
			"var p *int",
		} {
			if !strings.Contains(out, keep) {
				t.Errorf("missing %q in:\n%s", keep, out)
			}
		}
		lockPassed(t)
	})

	t.Run("cgo_preamble_survives", func(t *testing.T) {
		src := `package p

/*
#include <stdlib.h>
// cap.Move(x) gosree:iso in C comment
static int twice(int x) { return 2 * x; }
*/
import "C"

import "github.com/bharathvbcr/gosree/cap"

func F(p cap.Mut[C.int]) C.int { return C.twice(*cap.Move(p)) }
`
		out := mustErase(t, src)
		pre := "/*\n#include <stdlib.h>\n// cap.Move(x) gosree:iso in C comment\nstatic int twice(int x) { return 2 * x; }\n*/\nimport \"C\""
		if !strings.Contains(out, pre) {
			t.Errorf("cgo preamble changed:\n%s", out)
		}
		if strings.Contains(out, capImportPath) || !strings.Contains(out, "func F(p *C.int) C.int { return C.twice(*p) }") {
			t.Errorf("cgo file not erased:\n%s", out)
		}
		lockPassed(t)
	})

	t.Run("refuses_bad_input", func(t *testing.T) {
		for name, src := range map[string]string{
			"empty":        "",
			"syntax_error": "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nfunc F(p cap.Mut[int] {\n",
			"no_package":   "func F() {}\n",
		} {
			if _, err := safeErase([]byte(src)); err == nil || errors.Is(err, errErasePanicked) {
				t.Errorf("%s: Erase accepted invalid input", name)
			}
		}
		// A file with only a package clause and a type error is still
		// syntactically valid: erasure is syntactic and must not refuse.
		out := mustErase(t, "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nvar x cap.Mut[int] = 3\n")
		if !strings.Contains(out, "var x *int = 3") {
			t.Errorf("type-error file not erased:\n%s", out)
		}
		if got := mustErase(t, "package p\n"); got != "package p\n" {
			t.Errorf("package-only file changed: %q", got)
		}
		lockPassed(t)
	})

	t.Run("generated_and_test_files", func(t *testing.T) {
		src := "// Code generated by x. DO NOT EDIT.\n\npackage p_test\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nvar v cap.Iso[int]\n"
		out := mustErase(t, src)
		if !strings.HasPrefix(out, "// Code generated by x. DO NOT EDIT.\n\npackage p_test") || !strings.Contains(out, "var v *int") {
			t.Errorf("generated header or erasure wrong:\n%s", out)
		}
		lockPassed(t)
	})

	t.Run("idempotent_on_corpus", func(t *testing.T) {
		for _, tc := range allCorpus() {
			out, err := safeErase([]byte(tc.src))
			if err != nil {
				continue // refusals and panics are covered by the e2e corpus
			}
			again, err := safeErase(out)
			if err != nil || !bytes.Equal(out, again) {
				t.Errorf("%s: not idempotent (err=%v)\n--- once\n%s--- twice\n%s", tc.name, err, out, again)
			}
		}
		lockPassed(t)
	})

	t.Run("bom_and_non_utf8_comment", func(t *testing.T) {
		// Invalid UTF-8 is refused (gc refuses it too); nothing is written.
		bad := "package p\n\n// bytes: \xff\xfe\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nvar v cap.Mut[int]\n"
		if _, err := safeErase([]byte(bad)); err == nil || errors.Is(err, errErasePanicked) {
			t.Errorf("invalid UTF-8 accepted or panicked: %v", err)
		}
		bom := "\xef\xbb\xbfpackage p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nvar v cap.Mut[int]\n"
		out, err := safeErase([]byte(bom))
		t.Logf("BOM input: err=%v out=%q", err, out)
		if err != nil || !strings.Contains(string(out), "var v *int") {
			t.Errorf("BOM file not erased: err=%v out=%q", err, out)
		}
		lockPassed(t)
	})

	t.Run("safe_prefix_comment_is_a_directive", func(t *testing.T) {
		// README.md:7 defines //gosree: as a directive prefix, so a comment that
		// starts with it is removed by design; "// gosree:" (with a space) is prose
		// and must survive.
		src := "package p\n\n//gosree: callers may share this value.\n// gosree: this prose stays.\nfunc F() {}\n"
		out := mustErase(t, src)
		if strings.Contains(out, "callers may share") || !strings.Contains(out, "// gosree: this prose stays.\nfunc F() {}") {
			t.Errorf("//gosree: handling changed:\n%s", out)
		}
		lockPassed(t)
	})

	t.Run("gosree_free_file_untouched", func(t *testing.T) {
		// A file with no cap import and no directive is returned byte for
		// byte: no gofmt, no CRLF conversion.
		for _, src := range []string{
			"package p\r\n\r\nfunc  F( )  {\r\n}\r\n",
			"package p\n\nimport \"fmt\"\n\nfunc  G( )  { fmt.Println( 1 ) }\n",
			"package p\n\nimport capx \"example.com/other/cap\"\n\nvar _ = capx.Move\n",
		} {
			if out := mustErase(t, src); out != src {
				t.Errorf("gosree-free file changed:\ninput  %q\noutput %q", src, out)
			}
		}
		lockPassed(t)
	})

	t.Run("comments_around_removed_nodes", func(t *testing.T) {
		src := `package p

import (
	"fmt"

	// markers for the borrow checker
	"github.com/bharathvbcr/gosree/cap" // keep me? import trailer
)

func F(x int) int {
	fmt.Println()
	return cap.Move( /* why: ownership handoff */ x) // trailer
}
`
		out := mustErase(t, src)
		// Comments attached to the deleted import go with it (acceptable);
		// comments inside/after a removed call must survive in place.
		if !strings.Contains(out, "return /* why: ownership handoff */ x // trailer") {
			t.Errorf("comments around a removed helper call moved or were lost:\n%s", out)
		}
		multi := `package p

import (
	"fmt"
	// cap is below
	"github.com/bharathvbcr/gosree/cap"
	"os"
)

func F(x int) int {
	fmt.Fprintln(os.Stdout)
	return cap.Move(
		x, // the value
	)
}
`
		out2 := mustErase(t, multi)
		if !strings.Contains(out2, "// the value") || !strings.Contains(out2, `"os"`) || !strings.Contains(out2, `"fmt"`) {
			t.Errorf("multi-line call / import block comments mishandled:\n%s", out2)
		}
		if again := mustErase(t, out2); again != out2 {
			t.Errorf("not idempotent:\n%s\n---\n%s", out2, again)
		}
		t.Logf("multi-line output:\n%s", out2)
		lockPassed(t)
	})
}

// ---------------------------------------------------------------------------
// Scale
// ---------------------------------------------------------------------------

func TestStressEraseScale(t *testing.T) {
	t.Run("ten_thousand_markers", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\n")
		for i := 0; i < 10000; i++ {
			fmt.Fprintf(&b, "func F%d(p cap.Mut[int], q cap.Ref[[]int]) cap.Iso[int] { return cap.Move(cap.BorrowMut(p)) }\n", i)
		}
		start := time.Now()
		out := mustErase(t, b.String())
		el := time.Since(start)
		t.Logf("10k-function file (%d bytes) erased in %v", b.Len(), el)
		if strings.Contains(out, "cap.") || strings.Count(out, "return p }") != 10000 {
			t.Errorf("incomplete erasure at scale")
		}
		if el > 30*time.Second {
			t.Errorf("erasure too slow: %v", el)
		}
		lockPassed(t)
	})
	t.Run("deep_nesting", func(t *testing.T) {
		const depth = 2000
		src := "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nvar x = " +
			strings.Repeat("cap.Move(", depth) + "1" + strings.Repeat(")", depth) + "\n"
		start := time.Now()
		out := mustErase(t, src)
		t.Logf("depth %d erased in %v", depth, time.Since(start))
		if !strings.Contains(out, "var x = 1") {
			t.Errorf("deep nesting not erased: %.200s", out)
		}
		lockPassed(t)
	})
}

// ---------------------------------------------------------------------------
// CLI contract
// ---------------------------------------------------------------------------

func buildEraser(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gosree-erase-stress")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building gosree-erase: %v\n%s", err, out)
	}
	return bin
}

func runEraser(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running eraser: %v", err)
	}
	return string(out), code
}

const markedFile = "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nfunc F(p cap.Mut[int]) int { return *cap.Move(p) }\n"

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStressCLI(t *testing.T) {
	bin := buildEraser(t)

	// erasedMarked is markedFile after erasure.
	const erasedMarked = "package p\n\nfunc F(p *int) int { return *p }\n"

	t.Run("write_dot_rewrites_the_named_root", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "a.go")
		mustWrite(t, f, markedFile)
		out, code := runEraser(t, bin, dir, "-w", ".")
		if got := readFile(t, f); code != 0 || got != erasedMarked {
			t.Errorf("-w . : exit=%d output=%q file=%q", code, out, got)
		}
		// ".." and a hidden root named explicitly are walked too.
		hidden := filepath.Join(dir, ".hid")
		mustWrite(t, filepath.Join(hidden, "b.go"), markedFile)
		if out, code := runEraser(t, bin, dir, "-w", ".hid"); code != 0 || readFile(t, filepath.Join(hidden, "b.go")) != erasedMarked {
			t.Errorf("-w .hid : exit=%d output=%q", code, out)
		}
		sub := filepath.Join(dir, "sub")
		mustWrite(t, filepath.Join(sub, "c.go"), markedFile)
		if out, code := runEraser(t, bin, sub, "-w", ".."); code != 0 || readFile(t, filepath.Join(sub, "c.go")) != erasedMarked {
			t.Errorf("-w .. : exit=%d output=%q", code, out)
		}
		lockPassed(t)
	})

	t.Run("dotdotdot_pattern", func(t *testing.T) {
		dir := t.TempDir()
		top := filepath.Join(dir, "a.go")
		deep := filepath.Join(dir, "x", "y", "b.go")
		skipped := filepath.Join(dir, "vendor", "v.go")
		mustWrite(t, top, markedFile)
		mustWrite(t, deep, markedFile)
		mustWrite(t, skipped, markedFile)

		out, code := runEraser(t, bin, dir, "-l", "./...")
		if code != 0 || !strings.Contains(out, "a.go") || !strings.Contains(out, filepath.Join("x", "y", "b.go")) || strings.Contains(out, "v.go") {
			t.Errorf("-l ./... : exit=%d output=%q", code, out)
		}
		out, code = runEraser(t, bin, dir, "-d", "./...")
		if code != 0 || !strings.Contains(out, "-func F(p cap.Mut[int])") || readFile(t, top) != markedFile {
			t.Errorf("-d ./... : exit=%d output=%q", code, out)
		}
		out, code = runEraser(t, bin, dir, "-w", "./...")
		if code != 0 || readFile(t, top) != erasedMarked || readFile(t, deep) != erasedMarked || readFile(t, skipped) != markedFile {
			t.Errorf("-w ./... : exit=%d output=%q", code, out)
		}
		// dir/... walks dir, and a pattern on a file or a missing directory fails.
		more := filepath.Join(dir, "p", "q", "c.go")
		mustWrite(t, more, markedFile)
		if out, code := runEraser(t, bin, dir, "-w", "p/..."); code != 0 || readFile(t, more) != erasedMarked {
			t.Errorf("-w p/... : exit=%d output=%q", code, out)
		}
		if out, code := runEraser(t, bin, dir, "-w", "a.go/..."); code == 0 {
			t.Errorf("a pattern on a file succeeded: %q", out)
		}
		if out, code := runEraser(t, bin, dir, "-w", "nodir/..."); code == 0 {
			t.Errorf("a pattern on a missing directory succeeded: %q", out)
		}
		lockPassed(t)
	})

	t.Run("symlinks_are_never_followed_in_a_walk", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(root, "outside")
		tree := filepath.Join(root, "tree")
		target := filepath.Join(outside, "real.go")
		mustWrite(t, target, markedFile)
		mustWrite(t, filepath.Join(tree, "own.go"), markedFile)
		if err := os.Symlink(target, filepath.Join(tree, "link.go")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(tree, "linkdir")); err != nil {
			t.Fatal(err)
		}
		out, code := runEraser(t, bin, root, "-w", tree)
		if got := readFile(t, target); got != markedFile {
			t.Errorf("file outside the tree was rewritten through a symlink:\n%s", got)
		}
		if readFile(t, filepath.Join(tree, "own.go")) != erasedMarked {
			t.Errorf("the real file in the tree was not erased")
		}
		if code != 0 || !strings.Contains(out, "skipping symbolic link") || !strings.Contains(out, "link.go") || !strings.Contains(out, "linkdir") {
			t.Errorf("exit=%d, symlinks not reported: %q", code, out)
		}
		if fi, err := os.Lstat(filepath.Join(tree, "link.go")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the symlink itself was replaced")
		}

		// A symlink named on the command line is followed only inside the
		// working directory.
		out, code = runEraser(t, bin, tree, "-w", "link.go")
		if code == 0 || !strings.Contains(out, "outside the working directory") || readFile(t, target) != markedFile {
			t.Errorf("explicit outside symlink: exit=%d output=%q target=%q", code, out, readFile(t, target))
		}
		inside := filepath.Join(tree, "inside.go")
		mustWrite(t, inside, markedFile)
		alias := filepath.Join(tree, "alias.go")
		if err := os.Symlink("inside.go", alias); err != nil {
			t.Fatal(err)
		}
		out, code = runEraser(t, bin, tree, "-w", "alias.go")
		if fi, err := os.Lstat(alias); code != 0 || err != nil || fi.Mode()&os.ModeSymlink == 0 || readFile(t, inside) != erasedMarked {
			t.Errorf("explicit inside symlink: exit=%d output=%q inside=%q", code, out, readFile(t, inside))
		}
		lockPassed(t)
	})

	t.Run("write_is_all_or_nothing", func(t *testing.T) {
		dir := t.TempDir()
		good := filepath.Join(dir, "good.go")
		bad := filepath.Join(dir, "bad.go")
		badSrc := "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\nfunc F(p cap.Mut[int] {\n"
		mustWrite(t, good, markedFile)
		mustWrite(t, bad, badSrc)
		out, code := runEraser(t, bin, filepath.Dir(dir), "-w", dir)
		if code != 1 {
			t.Errorf("exit %d, want 1 for a syntax error: %s", code, out)
		}
		if readFile(t, bad) != badSrc {
			t.Errorf("file with syntax error was modified")
		}
		if readFile(t, good) != markedFile {
			t.Errorf("good file was written although a sibling failed")
		}
		if !strings.Contains(out, "bad.go") || !strings.Contains(out, "nothing was written") {
			t.Errorf("failure does not name the file or say nothing was written: %q", out)
		}
		// Without -w the good file is still processed and printed.
		out, code = runEraser(t, bin, filepath.Dir(dir), dir)
		if code != 1 || !strings.Contains(out, "func F(p *int) int") {
			t.Errorf("stdout mode with a failing sibling: exit=%d out=%q", code, out)
		}
		lockPassed(t)
	})

	t.Run("refused_construct_fails_the_run", func(t *testing.T) {
		dir := t.TempDir()
		good := filepath.Join(dir, "good.go")
		refused := filepath.Join(dir, "refused.go")
		src := "package p\n\nimport \"github.com/bharathvbcr/gosree/cap\"\n\ntype T struct{ A int }\n\ntype W struct{ cap.Mut[T] }\n"
		mustWrite(t, good, markedFile)
		mustWrite(t, refused, src)
		out, code := runEraser(t, bin, filepath.Dir(dir), "-w", dir)
		if code != 1 || !strings.Contains(out, "refused.go") || !strings.Contains(out, "cannot erase this file safely") ||
			readFile(t, good) != markedFile || readFile(t, refused) != src {
			t.Errorf("exit=%d out=%q", code, out)
		}
		lockPassed(t)
	})

	t.Run("mode_preserved", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "exe.go")
		mustWrite(t, exe, markedFile)
		if err := os.Chmod(exe, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, code := runEraser(t, bin, dir, "-w", exe); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		fi, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("mode changed to %v", fi.Mode().Perm())
		}
		if readFile(t, exe) != erasedMarked {
			t.Errorf("exe.go not rewritten")
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Errorf("temporary files left behind: %v", entries)
		}
		lockPassed(t)
	})

	t.Run("readonly_file_refused_and_nothing_written", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "exe.go")
		ro := filepath.Join(dir, "ro.go")
		mustWrite(t, exe, markedFile)
		mustWrite(t, ro, markedFile)
		if err := os.Chmod(ro, 0o444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o644) })
		out, code := runEraser(t, bin, dir, "-w", exe, ro)
		if os.Geteuid() != 0 {
			if code == 0 || readFile(t, ro) != markedFile || readFile(t, exe) != markedFile {
				t.Errorf("read-only file: exit=%d out=%q exe=%q", code, out, readFile(t, exe))
			}
		}
		lockPassed(t)
	})

	t.Run("skips_vendor_testdata_hidden_below_root", func(t *testing.T) {
		dir := t.TempDir()
		for _, sub := range []string{"vendor", "testdata", ".hidden"} {
			mustWrite(t, filepath.Join(dir, "root", sub, "x.go"), markedFile)
		}
		mustWrite(t, filepath.Join(dir, "root", "pkg", "y.go"), markedFile)
		_, code := runEraser(t, bin, dir, "-w", filepath.Join(dir, "root"))
		if code != 0 {
			t.Errorf("exit %d", code)
		}
		for _, sub := range []string{"vendor", "testdata", ".hidden"} {
			if readFile(t, filepath.Join(dir, "root", sub, "x.go")) != markedFile {
				t.Errorf("%s was rewritten", sub)
			}
		}
		if strings.Contains(readFile(t, filepath.Join(dir, "root", "pkg", "y.go")), "cap.") {
			t.Errorf("pkg/y.go not rewritten")
		}
		lockPassed(t)
	})

	t.Run("stdout_mode_does_not_write", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "a.go")
		mustWrite(t, f, markedFile)
		out, code := runEraser(t, bin, dir, f)
		if code != 0 || readFile(t, f) != markedFile || !strings.Contains(out, "func F(p *int) int { return *p }") {
			t.Errorf("stdout mode: exit=%d out=%q", code, out)
		}
		lockPassed(t)
	})
}

// ---------------------------------------------------------------------------
// Zero-cost / parity claim
// ---------------------------------------------------------------------------

var objAddr = regexp.MustCompile(`0x[0-9a-f]+`)

func TestStressBinaryParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	m := newStressModule(t)
	src := hdr + `type Item struct{ N int }

//go:noinline
func work(it cap.Mut[Item], by cap.Ref[int]) int {
	p := cap.BorrowMut(it)
	p.N += *cap.Borrow(by)
	q := cap.Move(p)
	return q.N
}

func main() {
	n := 2
	fmt.Println(work(&Item{1}, &n))
}
`
	erased := mustErase(t, src)
	// Both versions are built from the same package directory so that the
	// main package path and file name embedded in the binary are identical.
	build := func(content, binName string) string {
		mustWrite(t, filepath.Join(m.dir, "parity", "main.go"), content)
		bin := filepath.Join(m.dir, "bin", binName)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-buildid=", "-o", bin, "./parity")
		cmd.Dir = m.dir
		cmd.Env = goEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", binName, err, out)
		}
		return bin
	}
	bo, be := build(src, "orig"), build(erased, "erased")
	hash := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:8])
	}
	ho, he := hash(bo), hash(be)
	t.Logf("whole-binary sha256: original=%s erased=%s identical=%v", ho, he, ho == he)

	// The erased file is two lines shorter (the cap import line and the
	// blank line before it), which shifts every line-table entry. Re-pad the
	// import block so lines align and check full bit-for-bit identity.
	const plainImport = "import (\n\t\"fmt\"\n)"
	if !strings.Contains(erased, plainImport) {
		t.Fatalf("unexpected erased import block:\n%s", erased)
	}
	padded := strings.Replace(erased, plainImport, "import (\n\t\"fmt\"\n\t// pad\n\t// pad\n)", 1)
	bp := build(padded, "erased_padded")
	hp := hash(bp)
	t.Logf("line-aligned erased binary sha256=%s identical to original=%v", hp, hp == ho)
	detail := ""
	if hp != ho {
		a, _ := os.ReadFile(bo)
		b, _ := os.ReadFile(bp)
		ndiff, first := 0, -1
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				ndiff++
				if first < 0 {
					first = i
				}
			}
		}
		lo, hi := max(first-48, 0), min(first+48, len(a), len(b))
		detail = fmt.Sprintf("line-aligned binaries differ in %d bytes (sizes %d/%d), first at %#x (Mach-O __gopclntab section header):\norig   %q\nerased %q",
			ndiff, len(a), len(b), first, a[lo:hi], b[lo:hi])
	}
	t.Run("whole_binary_identity", func(t *testing.T) {
		knownBug(t, "ER-23", hp != ho, detail)
	})

	dis := func(bin string) string {
		cmd := exec.Command("go", "tool", "objdump", "-s", `^main\.work$`, bin)
		cmd.Env = goEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("objdump: %v\n%s", err, out)
		}
		var ins []string
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Split(line, "\t")
			if len(f) < 4 {
				continue
			}
			ins = append(ins, objAddr.ReplaceAllString(f[len(f)-1], "ADDR"))
		}
		return strings.Join(ins, "\n")
	}
	do, de := dis(bo), dis(be)
	if do == "" {
		t.Fatalf("objdump produced no instructions for main.work")
	}
	if do != de {
		t.Errorf("main.work instructions differ after erasure:\n--- original\n%s\n--- erased\n%s", do, de)
	}
	t.Logf("main.work: %d instructions, identical (addresses normalised) = %v", strings.Count(do, "\n")+1, do == de)

	// Symbol-level residue: does the annotated binary carry any cap.* symbol?
	nm := func(bin string) []string {
		cmd := exec.Command("go", "tool", "nm", bin)
		cmd.Env = goEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("nm: %v\n%s", err, out)
		}
		var syms []string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "gosree/cap") {
				syms = append(syms, strings.TrimSpace(line))
			}
		}
		sort.Strings(syms)
		return syms
	}
	t.Logf("gosree/cap symbols in original binary: %d %v; in erased: %d", len(nm(bo)), nm(bo), len(nm(be)))

	// Byte-level residue explains why whole binaries differ: the inlining
	// tree / file table of the annotated build still names cap.go and the
	// inlined helpers even though no cap function is emitted.
	residue := func(bin string) (int, int64) {
		b, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Count(b, []byte("gosree/cap")), int64(len(b))
	}
	ro, so := residue(bo)
	re, se := residue(be)
	t.Logf("bytes \"gosree/cap\" occurrences: original=%d (size %d) erased=%d (size %d)", ro, so, re, se)
	if re != 0 {
		t.Errorf("erased binary still mentions gosree/cap %d times", re)
	}
	lockPassed(t)
}

// ---------------------------------------------------------------------------
// Ledger (keep last in file)
// ---------------------------------------------------------------------------

func TestStressEraseLedger(t *testing.T) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	ids := make([]string, 0, len(stressBugRegistry))
	for id := range stressBugRegistry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var confirmed, unconfirmed []string
	for _, id := range ids {
		if ledgerConfirmed[id] > 0 {
			confirmed = append(confirmed, id)
		} else {
			unconfirmed = append(unconfirmed, id)
		}
	}
	t.Logf("STRESS LEDGER: %d locks passed, %d known bugs registered (%d confirmed in this run: %s; not exercised in this run: %s)",
		len(ledgerLocks), len(ids), len(confirmed), strings.Join(confirmed, ","), strings.Join(unconfirmed, ","))
	for _, id := range ids {
		t.Logf("  %s [%s] %s", id, stressBugRegistry[id].severity, stressBugRegistry[id].summary)
	}
}
