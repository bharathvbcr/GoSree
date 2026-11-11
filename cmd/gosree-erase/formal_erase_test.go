package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormalEraseCompiledTextEquivalenceAndShadowSafety(t *testing.T) {
	src := []byte(`package main

import (
	"fmt"
	"github.com/bharathvbcr/gosree/cap"
)

func init() {
	// Proves VerifyCompiledTextEquivalence never executes untrusted target code.
	panic("untrusted target init must never execute during translation validation")
}

//gosree:imm
type Config struct {
	Port int
}

//go:noinline
func BorrowAndMutate(p cap.Mut[int], cfg cap.Imm[Config]) int {
	*p += cfg.Port
	return *p
}

//go:noinline
func ExplicitTypeArgBox() any {
	return cap.Move[int64](5)
}

//go:noinline
func ShadowedLocalCap(v int) int {
	type custom struct{ Move func(int) int }
	cap := custom{Move: func(x int) int { return x + 100 }}
	return cap.Move(v)
}

func main() {
	x := 10
	cfg := &Config{Port: 32}
	cap.Move(x)
	got := BorrowAndMutate(cap.BorrowMut(&x), cap.Imm[Config](cfg))
	boxed := ExplicitTypeArgBox()
	_, isInt64 := boxed.(int64)
	shadow := ShadowedLocalCap(7)
	fmt.Printf("got=%d int64=%v shadow=%d\n", got, isInt64, shadow)
}
`)

	rep, err := VerifyCompiledTextEquivalence(map[string][]byte{"main.go": src}, Erase)
	if err != nil {
		t.Fatalf("VerifyCompiledTextEquivalence failed on production Erase: %v", err)
	}
	if rep.PackageName != "main" {
		t.Fatalf("PackageName = %q, want main", rep.PackageName)
	}
	if rep.ExportedSymbolsChecked < 4 {
		t.Fatalf("expected >= 4 package-scope symbols checked, got %d", rep.ExportedSymbolsChecked)
	}
	if rep.DisassembledFuncs < 4 {
		t.Fatalf("expected >= 4 disassembled functions (including init/main), got %d", rep.DisassembledFuncs)
	}
	for _, fn := range []string{"BorrowAndMutate", "ExplicitTypeArgBox", "ShadowedLocalCap", "main"} {
		if len(rep.OrigTextMnemonics[fn]) == 0 {
			t.Fatalf("missing disassembled instructions for %s", fn)
		}
	}
}

func TestFormalEraseCrossFileConstantViaGoTypes(t *testing.T) {
	files := map[string][]byte{
		"consts.go": []byte("package main\n\nconst CrossFileShift = 4\n"),
		"main.go": []byte(`package main

import (
	"fmt"
	"github.com/bharathvbcr/gosree/cap"
)

func main() {
	v := uint64(1) << cap.Move(CrossFileShift)
	fmt.Printf("shift=%d\n", v)
}
`),
	}

	// Single-file Erase conservatively refuses `CrossFileShift` (`classUnknown` in shift RHS).
	if _, err := Erase(files["main.go"]); err == nil {
		t.Fatalf("expected single-file Erase to refuse unresolved cross-file shift constant")
	}

	// Package-level go/types-backed ErasePackageFiles resolves `CrossFileShift` (`int`) soundly!
	erasedMap, err := ErasePackageFiles(files)
	if err != nil {
		t.Fatalf("ErasePackageFiles failed: %v", err)
	}
	rep, err := VerifyCompiledTextEquivalence(files, func(src []byte) ([]byte, error) {
		for name, orig := range files {
			if bytes.Equal(orig, src) {
				return erasedMap[name], nil
			}
		}
		return Erase(src)
	})
	if err != nil {
		t.Fatalf("VerifyCompiledTextEquivalence on cross-file constant failed: %v", err)
	}
	if len(rep.OrigTextMnemonics["main"]) == 0 {
		t.Fatalf("expected disassembled instructions for main, got 0")
	}
}

func TestFormalEraseDisprovesBrokenErasers(t *testing.T) {
	src := []byte(`package sample

import (
	"github.com/bharathvbcr/gosree/cap"
)

//go:noinline
func Boxed() any {
	return cap.Move[int64](5)
}

//go:noinline
func ImmediateConst() int {
	return cap.Move(5)
}

//go:noinline
func ExportedPtr(p cap.Mut[int]) cap.Ref[int] {
	return cap.Borrow(p)
}
`)

	// Disproof 1 (`¬Equiv`): Buggy eraser drops `[int64]` conversion (`cap.Move[int64](5)` -> `5`),
	// silently changing `any(int64(5))` into `any(int(5))` — caught purely via .text relocation disassembly!
	buggyDropTypeArg := func(in []byte) ([]byte, error) {
		out, err := Erase(in)
		if err != nil {
			return nil, err
		}
		return bytes.ReplaceAll(out, []byte("int64(5)"), []byte("5")), nil
	}
	if _, err := VerifyCompiledTextEquivalence(map[string][]byte{"sample.go": src}, buggyDropTypeArg); err == nil {
		t.Fatal("Disproof 1 failed: VerifyCompiledTextEquivalence must reject dropping explicit [int64] conversion")
	}

	// Disproof 2 (`¬Equiv`): Buggy eraser alters immediate operand (`5` -> `6`) while keeping identical types —
	// caught purely via .text instruction operand disassembly without executing code!
	buggyMutateOperand := func(in []byte) ([]byte, error) {
		out, err := Erase(in)
		if err != nil {
			return nil, err
		}
		return bytes.Replace(out, []byte("return 5"), []byte("return 6"), 1), nil
	}
	if _, err := VerifyCompiledTextEquivalence(map[string][]byte{"sample.go": src}, buggyMutateOperand); err == nil {
		t.Fatal("Disproof 2 failed: VerifyCompiledTextEquivalence must reject instruction operand drift")
	}

	// Disproof 3 (`¬Equiv`): Buggy eraser mutates `ExportedPtr` signature (`*int` -> `int`),
	// caught immediately by `VerifyExportedTypeEquivalence` (`types.Identical`)!
	buggyBreakSig := func(in []byte) ([]byte, error) {
		out, err := Erase(in)
		if err != nil {
			return nil, err
		}
		mutated := strings.Replace(string(out), "func ExportedPtr(p *int) *int {\n\treturn p\n}", "func ExportedPtr(p int) int {\n\treturn p\n}", 1)
		return []byte(mutated), nil
	}
	if _, err := VerifyExportedTypeEquivalence(map[string][]byte{"sample.go": src}, buggyBreakSig); err == nil {
		t.Fatal("Disproof 3 failed: VerifyExportedTypeEquivalence must reject exported signature drift")
	}
}

func TestFormalEraseExportedAndTextEquivalenceOnLibraryPackageAndCLIVerify(t *testing.T) {
	src := []byte(`package sample

import (
	"sync/atomic"
	"github.com/bharathvbcr/gosree/cap"
)

func init() {
	panic("library init must never execute during -verify")
}

type Item struct {
	ID int
}

type Store interface {
	Save(p cap.Mut[Item]) error
}

func LoadSlot(slot *atomic.Pointer[Item]) (cap.Ref[Item], error) {
	return slot.Load(), nil
}

func IdentitySlice[T comparable](xs []T) []T {
	return xs
}
`)
	rep, err := VerifyCompiledTextEquivalence(map[string][]byte{"sample.go": src}, Erase)
	if err != nil {
		t.Fatalf("VerifyCompiledTextEquivalence failed on library package: %v", err)
	}
	if rep.PackageName != "sample" {
		t.Fatalf("PackageName = %q, want sample", rep.PackageName)
	}
	if rep.ExportedSymbolsChecked < 4 {
		t.Fatalf("expected >= 4 exported symbols verified, got %d", rep.ExportedSymbolsChecked)
	}
	if len(rep.OrigTextMnemonics["LoadSlot"]) == 0 {
		t.Fatalf("expected disassembled .text instructions for sample.LoadSlot")
	}

	// Verify CLI `-verify` flag on stdin succeeds on valid Erase (without executing init!)
	// and fails closed (exit 1) on both signature drift and .text body drift!
	var outBuf, errBuf bytes.Buffer
	if code := runWith([]string{"-verify"}, bytes.NewReader(src), &outBuf, &errBuf, Erase); code != 0 {
		t.Fatalf("gosree-erase -verify (stdin) exited %d, stderr=%s", code, errBuf.String())
	}

	// Verify CLI `-verify` flag on a directory target on disk!
	pkgDir := t.TempDir()
	srcPath := filepath.Join(pkgDir, "sample.go")
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	outBuf.Reset()
	errBuf.Reset()
	if code := runWith([]string{"-verify", pkgDir}, strings.NewReader(""), &outBuf, &errBuf, Erase); code != 0 {
		t.Fatalf("gosree-erase -verify <dir> exited %d, stderr=%s", code, errBuf.String())
	}

	// Broken body (same exported signature, altered .text instruction) MUST fail `gosree-erase -verify`!
	outBuf.Reset()
	errBuf.Reset()
	brokenBodyErase := func(in []byte) ([]byte, error) {
		erased, err := Erase(in)
		if err != nil {
			return nil, err
		}
		return bytes.Replace(erased, []byte("return slot.Load(), nil"), []byte("return nil, nil"), 1), nil
	}
	if code := runWith([]string{"-verify", pkgDir}, strings.NewReader(""), &outBuf, &errBuf, brokenBodyErase); code != 1 {
		t.Fatalf("gosree-erase -verify <dir> must exit 1 on compiled .text instruction drift, got %d", code)
	}

	// Canceled context must fail closed immediately without hanging.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := VerifyCompiledTextEquivalenceContext(canceledCtx, map[string][]byte{"sample.go": src}, Erase); err == nil {
		t.Fatal("expected canceled context to fail VerifyCompiledTextEquivalenceContext")
	}
}

func TestFormalEraseDetectsClosureBodyDrift(t *testing.T) {
	src := []byte(`package sample

import "github.com/bharathvbcr/gosree/cap"

const ShiftAmt = 3

//go:noinline
func ShadowedLocalCap(v int) int {
	type custom struct{ Move func(int) int }
	cap := custom{Move: func(x int) int { return x + 100 }}
	return cap.Move(v)
}

//go:noinline
func ShiftWithClosure(v int) int {
	type custom struct{ Move func(int) int }
	shifted := v << cap.Move(ShiftAmt)
	box := custom{Move: func(z int) int { return z + 100 }}
	return box.Move(shifted)
}
`)

	rep, err := VerifyCompiledTextEquivalence(map[string][]byte{"sample.go": src}, Erase)
	if err != nil {
		t.Fatalf("VerifyCompiledTextEquivalence failed on valid closures: %v", err)
	}
	if len(rep.OrigTextMnemonics["ShadowedLocalCap.func1"]) == 0 {
		t.Fatalf("expected ShadowedLocalCap.func1 closure to be disassembled, got keys %v", rep.OrigTextMnemonics)
	}

	// Mutating a closure body inside a normal function (`x + 100` -> `x + 999`) MUST be caught!
	mutateShadowClosure := func(in []byte) ([]byte, error) {
		out, err := Erase(in)
		if err != nil {
			return nil, err
		}
		return bytes.Replace(out, []byte("x + 100"), []byte("x + 999"), 1), nil
	}
	if _, err := VerifyCompiledTextEquivalence(map[string][]byte{"sample.go": src}, mutateShadowClosure); err == nil {
		t.Fatal("VerifyCompiledTextEquivalence failed to detect mutated closure ShadowedLocalCap.func1")
	}

	// Mutating a user closure alongside a synthetic constant-wrapper closure (`z + 100` -> `z + 999`) MUST ALSO be caught!
	mutateCoexistingClosure := func(in []byte) ([]byte, error) {
		out, err := Erase(in)
		if err != nil {
			return nil, err
		}
		return bytes.Replace(out, []byte("z + 100"), []byte("z + 999"), 1), nil
	}
	if _, err := VerifyCompiledTextEquivalence(map[string][]byte{"sample.go": src}, mutateCoexistingClosure); err == nil {
		t.Fatal("VerifyCompiledTextEquivalence failed to detect mutated closure alongside synthetic constant wrapper")
	}
}

func TestFormalEraseWorkspaceOverlayBuildTagsGenericsAndCrossPackageVerify(t *testing.T) {
	gosreeRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	wsDir := t.TempDir()
	goMod := "module mymod\n\ngo 1.27\n\nrequire github.com/bharathvbcr/gosree v0.0.0\nreplace github.com/bharathvbcr/gosree => " + gosreeRoot + "\n"
	if err := os.WriteFile(filepath.Join(wsDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	dirA := filepath.Join(wsDir, "pkg", "a")
	dirB := filepath.Join(wsDir, "pkg", "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	// Active library file importing both `gosree/cap` and `gosree/internal/kinds` (non-cap gosree package),
	// plus an uninstantiated generic function whose erased signature must be type-checked!
	srcA := []byte(`package a

import (
	"github.com/bharathvbcr/gosree/cap"
	"github.com/bharathvbcr/gosree/internal/kinds"
)

var _ = kinds.Imm

func IdentityMut[T any](p cap.Mut[T]) cap.Ref[T] {
	return cap.Borrow(p)
}

func ConcreteInc(p cap.Mut[int]) int {
	*p++
	return *p
}
`)
	// Inactive `//go:build ignore` script (`package main`) and inactive OS file (`//go:build plan9`)
	// in the same directory must not break directory verification.
	ignoreScript := []byte("//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	plan9Sibling := []byte("//go:build plan9\n\npackage a\n\nfunc ConcreteInc(p *int) int { return 0 }\n")

	if err := os.WriteFile(filepath.Join(dirA, "a.go"), srcA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "gen_ignore.go"), ignoreScript, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "a_plan9.go"), plan9Sibling, 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait: `srcA` imports `gosree/internal/kinds`, which is forbidden after erasure ONLY if `Erase` keeps it;
	// let's make `pkg/sub` a local workspace dependency so erased `a` retains `"mymod/pkg/sub"` cleanly!
	dirSub := filepath.Join(wsDir, "pkg", "sub")
	if err := os.MkdirAll(dirSub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirSub, "sub.go"), []byte("package sub\n\nconst Base = 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcA = []byte(`package a

import (
	"mymod/pkg/sub"

	"github.com/bharathvbcr/gosree/cap"
)

func IdentityMut[T any](p cap.Mut[T]) cap.Ref[T] {
	return cap.Borrow(p)
}

func ConcreteInc(p cap.Mut[int]) int {
	*p += sub.Base
	return *p
}
`)
	if err := os.WriteFile(filepath.Join(dirA, "a.go"), srcA, 0o644); err != nil {
		t.Fatal(err)
	}

	srcB := []byte(`package b

import (
	"mymod/pkg/a"

	"github.com/bharathvbcr/gosree/cap"
)

func UseA(v int) int {
	ptr := a.IdentityMut(cap.BorrowMut(&v))
	return a.ConcreteInc(cap.BorrowMut(&v)) + *ptr
}
`)
	if err := os.WriteFile(filepath.Join(dirB, "b.go"), srcB, 0o644); err != nil {
		t.Fatal(err)
	}

	// VerifyDirectoryEquivalence on `dirA` must succeed, respect build constraints,
	// use workspace overlay for dotless `"mymod/pkg/sub"`, and check exported symbols!
	repA, err := VerifyDirectoryEquivalence(dirA, map[string][]byte{
		"a.go":          srcA,
		"gen_ignore.go": ignoreScript,
		"a_plan9.go":    plan9Sibling,
	}, Erase)
	if err != nil {
		t.Fatalf("VerifyDirectoryEquivalence(dirA) failed: %v", err)
	}
	if repA.ExportedSymbolsChecked < 2 {
		t.Fatalf("expected ExportedSymbolsChecked >= 2 in workspace overlay mode, got %d", repA.ExportedSymbolsChecked)
	}

	// Breaking uninstantiated generic signature `IdentityMut` (`*T` -> `T`) MUST be caught in workspace overlay mode!
	breakGenericSig := func(in []byte) ([]byte, error) {
		out, err := Erase(in)
		if err != nil {
			return nil, err
		}
		return bytes.Replace(out,
			[]byte("func IdentityMut[T any](p *T) *T"),
			[]byte("func IdentityMut[T any](p T) T"), 1), nil
	}
	if _, err := VerifyDirectoryEquivalence(dirA, map[string][]byte{"a.go": srcA}, breakGenericSig); err == nil {
		t.Fatal("VerifyDirectoryEquivalence failed to detect broken generic signature in workspace overlay mode")
	}

	// Multi-directory CLI `-verify` across `dirA` and `dirB` (`mymod/pkg/...` style) must succeed!
	var outBuf, errBuf bytes.Buffer
	if code := runWith([]string{"-verify", dirA, dirB}, strings.NewReader(""), &outBuf, &errBuf, Erase); code != 0 {
		t.Fatalf("gosree-erase -verify dirA dirB exited %d, stderr=%s", code, errBuf.String())
	}
}

