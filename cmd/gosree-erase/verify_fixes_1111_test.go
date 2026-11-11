package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGoLanguageVersion(t *testing.T) {
	for in, want := range map[string]string{"go1.27.3": "1.27", "go1.27": "1.27", "go1.28rc1": "1.28rc1", "go1.24.0": "1.24"} {
		got, err := goLanguageVersion(in)
		if err != nil || got != want {
			t.Errorf("goLanguageVersion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "devel +abc", "1.27", "go1"} {
		if got, err := goLanguageVersion(in); err == nil {
			t.Errorf("goLanguageVersion(%q) = %q, want an error", in, got)
		}
	}
}

// TestVerifyUsesToolchainLanguage: the probe modules used to pin `go 1.24`, so a package that uses
// newer syntax failed verification with "requires go1.26 or later" although it builds.
func TestVerifyUsesToolchainLanguage(t *testing.T) {
	lang, err := probeGoLanguage(context.Background())
	if err != nil {
		t.Skipf("no go command: %v", err)
	}
	if lang < "1.26" {
		t.Skipf("toolchain language %s predates new(expr)", lang)
	}
	src := "package p\n\nfunc F() *int { return new(1) }\n"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := VerifyCompiledTextEquivalenceContext(ctx, map[string][]byte{"p.go": []byte(src)}, Erase); err != nil {
		t.Fatalf("verification of a go%s-only source failed: %v", lang, err)
	}
}

// fakeDirs returns n directories with one plain file each, in a temp tree.
func fakeDirs(t *testing.T, n int) map[string]map[string][]byte {
	t.Helper()
	root := t.TempDir()
	byDir := map[string]map[string][]byte{}
	for i := range n {
		dir := filepath.Join(root, "d"+string(rune('a'+i)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		byDir[dir] = map[string][]byte{"a.go": []byte("package p\n")}
	}
	return byDir
}

func identity(src []byte) ([]byte, error) { return src, nil }

// TestVerifyDirectoriesDeadlinePerDirectory: each directory gets its own deadline. Five directories
// that each take 40 ms fit in 5x100 ms but not in one 100 ms.
func TestVerifyDirectoriesDeadlinePerDirectory(t *testing.T) {
	byDir := fakeDirs(t, 5)
	var mu sync.Mutex
	calls := 0
	one := func(ctx context.Context, dir string, files map[string][]byte, _ func([]byte) ([]byte, error), _ map[string][]byte) (*EquivReport, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		select {
		case <-time.After(40 * time.Millisecond):
			return &EquivReport{}, ctx.Err()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if _, err := verifyDirectories(context.Background(), byDir, identity, 100*time.Millisecond, one); err != nil {
		t.Fatalf("5 directories at 40 ms each under a 100 ms per-directory deadline: %v", err)
	}
	if calls != 5 {
		t.Errorf("verified %d directories, want 5", calls)
	}

	// A directory that overruns its own deadline still fails, naming the directory.
	slow := func(ctx context.Context, dir string, _ map[string][]byte, _ func([]byte) ([]byte, error), _ map[string][]byte) (*EquivReport, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := verifyDirectories(context.Background(), byDir, identity, 20*time.Millisecond, slow)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a directory over its deadline: got %v, want a deadline error", err)
	}
}

// TestVerifyDirectoriesCoversExcludedFiles: a file the build leaves out here (constraint or _test.go)
// is still erased, must still erase and parse, and is reported as not compile-verified. A directory
// whose every file is excluded used to be skipped without eraseFn ever seeing its files.
func TestVerifyDirectoriesCoversExcludedFiles(t *testing.T) {
	root := t.TempDir()
	only := filepath.Join(root, "only")
	mixed := filepath.Join(root, "mixed")
	for _, d := range []string{only, mixed} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	constrained := "//go:build gosree_never_set\n\npackage p\n"
	byDir := map[string]map[string][]byte{
		only:  {"x.go": []byte(constrained)},
		mixed: {"a.go": []byte("package p\n"), "b_test.go": []byte("package p\n")},
	}
	var seen []string
	erase := func(src []byte) ([]byte, error) {
		seen = append(seen, strings.TrimSpace(string(src)))
		return src, nil
	}
	verified := 0
	one := func(context.Context, string, map[string][]byte, func([]byte) ([]byte, error), map[string][]byte) (*EquivReport, error) {
		verified++
		return &EquivReport{}, nil
	}
	unverified, err := verifyDirectories(context.Background(), byDir, erase, time.Second, one)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Errorf("eraseFn saw %d files %q, want all 3", len(seen), seen)
	}
	if verified != 1 {
		t.Errorf("compile-verified %d directories, want only mixed", verified)
	}
	want := []string{filepath.Join(mixed, "b_test.go"), filepath.Join(only, "x.go")}
	if strings.Join(unverified, ",") != strings.Join(want, ",") {
		t.Errorf("unverified = %v, want %v", unverified, want)
	}

	// An excluded file that fails to erase, or erases to text that does not parse, fails verification.
	failing := func(src []byte) ([]byte, error) {
		if bytes.Contains(src, []byte("gosree_never_set")) {
			return nil, errors.New("cannot erase")
		}
		return src, nil
	}
	if _, err := verifyDirectories(context.Background(), byDir, failing, time.Second, one); err == nil || !strings.Contains(err.Error(), "x.go") {
		t.Errorf("erase failure on an excluded file: got %v", err)
	}
	broken := func(src []byte) ([]byte, error) { return []byte("package"), nil }
	if _, err := verifyDirectories(context.Background(), byDir, broken, time.Second, one); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("unparsable erased text: got %v", err)
	}
}

// TestVerifyTargetsFailsOnUnreadableSibling: a sibling file that cannot be read used to be skipped
// silently, so the package was verified without it.
func TestVerifyTargetsFailsOnUnreadableSibling(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	for _, f := range []string{a, b} {
		if err := os.WriteFile(f, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(b, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(b, 0o644) })
	var stderr bytes.Buffer
	o := &options{stderr: &stderr, stdout: &bytes.Buffer{}, erase: identity}
	err := o.verifyTargets([]target{{display: a, real: a}})
	if err == nil || !strings.Contains(err.Error(), "b.go") {
		t.Errorf("unreadable sibling: got %v, want an error naming b.go", err)
	}
}

// TestVerifyTargetsNamesUncompiledFiles: a selected _test.go file is erased and parsed, and named.
func TestVerifyTargetsNamesUncompiledFiles(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "a_test.go")
	if err := os.WriteFile(tf, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	calls := 0
	o := &options{stderr: &stderr, stdout: &bytes.Buffer{}, erase: func(src []byte) ([]byte, error) { calls++; return src, nil }}
	if err := o.verifyTargets([]target{{display: tf, real: tf}}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("eraseFn ran %d times on the _test.go file, want 1", calls)
	}
	if !strings.Contains(stderr.String(), "not compile-verified") || !strings.Contains(stderr.String(), "a_test.go") {
		t.Errorf("stderr does not name the uncompiled file: %q", stderr.String())
	}
}

// TestVerifyDirectoriesCancelled: cancelling the parent context mid-run stops the directory in
// progress and starts no later one, and the run fails.
func TestVerifyDirectoriesCancelled(t *testing.T) {
	byDir := fakeDirs(t, 5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	one := func(ctx context.Context, dir string, _ map[string][]byte, _ func([]byte) ([]byte, error), _ map[string][]byte) (*EquivReport, error) {
		calls++
		if calls == 2 {
			cancel() // Ctrl-C arrives while the second directory is being verified
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &EquivReport{}, nil
	}
	_, err := verifyDirectories(ctx, byDir, identity, time.Minute, one)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want a cancellation error", err)
	}
	if calls != 2 {
		t.Errorf("%d directories were started, want 2: none after the cancel", calls)
	}
	// Already cancelled before the first directory: nothing runs.
	calls = 0
	if _, err := verifyDirectories(ctx, byDir, identity, time.Minute, one); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Errorf("pre-cancelled: err %v, %d calls", err, calls)
	}
}
