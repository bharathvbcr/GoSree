package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const outsideSrc = "package p\n\nfunc F() {\n\tx := 0\n}\n"

// archiveFixture makes <base>/archive with a plain p.go, and <base>/outside.go beside it, the file
// an escaping table row would reach.
func archiveFixture(t *testing.T) (base, dir, outside string) {
	t.Helper()
	base = t.TempDir()
	dir = filepath.Join(base, "archive")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(base, "outside.go")
	for _, p := range []string{outside, filepath.Join(dir, "p.go"), filepath.Join(dir, "sub", "q.go")} {
		if err := os.WriteFile(p, []byte(outsideSrc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(base, "outsidedir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "outsidedir", "x.go"), []byte(outsideSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return base, dir, outside
}

func rowFor(file string) []annotation {
	return []annotation{{file: file, scope: "func F(", line: "\tx := 0", directive: "//gosree:guarded_by mu"}}
}

// TestApplyAnnotationsStaysInsideArchive checks that a table row can only reach regular files inside
// the archive: no escaping path, no symlink anywhere on the way.
func TestApplyAnnotationsStaysInsideArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	setup := func(t *testing.T) (base, dir, outside string) {
		base, dir, outside = archiveFixture(t)
		link := func(target, name string) {
			t.Helper()
			if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
		link("../outside.go", "esc.go")
		link("esc.go", "chain1.go") // a chain: chain1 -> esc -> ../outside.go
		link("p.go", "inside.go")   // inside the archive, still refused: a row names a file, not a link
		link("../outsidedir", "dlink")
		link("sub", "insidedir")
		return base, dir, outside
	}

	for name, file := range map[string]string{
		"dotdot":               "../outside.go",
		"dotdot in the middle": "sub/../../outside.go",
		"dotdot staying in":    "sub/../p.go",
		"absolute":             "<abs>",
		"backslash dotdot":     `..\outside.go`,
		"symlink out":          "esc.go",
		"symlink chain":        "chain1.go",
		"symlink in":           "inside.go",
		"symlinked dir out":    "dlink/x.go",
		"symlinked dir in":     "insidedir/q.go",
		"empty":                "",
		"directory":            "sub",
	} {
		t.Run(name, func(t *testing.T) {
			base, dir, outside := setup(t)
			if file == "<abs>" {
				file = outside
			}
			err := applyAnnotations(dir, rowFor(file))
			if err == nil {
				t.Fatalf("row for %q applied; want a refusal", file)
			}
			for _, p := range []string{outside, filepath.Join(base, "outsidedir", "x.go"), filepath.Join(dir, "p.go"), filepath.Join(dir, "sub", "q.go")} {
				got, rerr := os.ReadFile(p)
				if rerr != nil {
					t.Fatal(rerr)
				}
				if string(got) != outsideSrc {
					t.Errorf("%s was written by a refused row:\n%s", p, got)
				}
			}
		})
	}

	// A plain in-archive path still works.
	_, dir, _ := setup(t)
	if err := applyAnnotations(dir, rowFor("sub/q.go")); err != nil {
		t.Fatalf("sub/q.go: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "sub", "q.go")); !strings.Contains(string(got), "//gosree:guarded_by mu") {
		t.Errorf("sub/q.go was not annotated:\n%s", got)
	}
}

func tarOf(t *testing.T, hdrs []tar.Header, bodies []string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := range hdrs {
		h := hdrs[i]
		h.Size = int64(len(bodies[i]))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

// TestExtractTarDoesNotWriteThroughSymlinks: an archive that plants a symlink and then writes a file
// beneath it must not write outside the destination.
func TestExtractTarDoesNotWriteThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	base := t.TempDir()
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := tarOf(t,
		[]tar.Header{
			{Name: "svc/link", Typeflag: tar.TypeSymlink, Linkname: "../out"},
			{Name: "svc/link/pwned.go", Typeflag: tar.TypeReg, Mode: 0o644},
		},
		[]string{"", "package p\n"})
	if _, err := extractTar(r, "svc/", dst); err == nil {
		t.Error("extractTar wrote a file beneath a symlink that leaves the destination without an error")
	}
	if _, err := os.Stat(filepath.Join(base, "out", "pwned.go")); err == nil {
		t.Error("a file was written outside the destination through a symlink")
	}
}
