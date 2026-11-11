package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUnmatchedWildcards(t *testing.T) {
	matches := map[string][]string{"./a": {"m/a"}, "./a/...": {"m/a"}, "./empty/...": nil, "./also/...": nil}
	list := func(p string) ([]string, error) {
		if p == "./broken/..." {
			return nil, errors.New("go list failed")
		}
		return matches[p], nil
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"./a", "./empty/..."}, []string{"./empty/..."}},
		{[]string{"-json", "./a/...", "./empty/...", "./also/..."}, []string{"./empty/...", "./also/..."}},
		{[]string{"./a", "./a/..."}, nil},
		{[]string{"./empty/..."}, nil},         // alone: the driver reports it
		{[]string{"./a", "./broken/..."}, nil}, // could not ask: no claim
		{[]string{"./a", "./plain"}, nil},      // not a wildcard
		{[]string{"-V=full", "./a", "./empty/..."}, nil},
		{[]string{"-tags", "x", "./a", "./empty/..."}, []string{"./empty/..."}},
	} {
		if got := unmatchedWildcards(tc.args, list); !slices.Equal(got, tc.want) {
			t.Errorf("unmatchedWildcards(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestGoListOnThisModule(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"go.mod": "module example.com/m\n\ngo 1.24\n", "a/a.go": "package a\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if got, err := goList("./a/..."); err != nil || len(got) != 1 {
		t.Errorf("a wildcard with a package: %v, %v", got, err)
	}
	if got, err := goList("./empty/..."); err != nil || len(got) != 0 {
		t.Errorf("a wildcard over a directory with no package: %v, %v", got, err)
	}
}
