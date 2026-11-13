//go:build !windows

package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// advSkipThen extracts a symlink entry followed by one regular file, so the lookup against a skipped
// link runs for the file.
func advSkipThen(t *testing.T, name string) (files, links int, err error, dst string) {
	t.Helper()
	_, dst = advDst(t)
	files, links, err = advExtract(tarOf(t, []tar.Header{
		{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
		{Name: "svc/" + name, Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"", "x"}), "svc/", dst)
	return files, links, err, dst
}

// longComponents returns a cleaned name of exactly n bytes made of components of 200 bytes.
func longComponents(n int) string {
	const c = 200
	var parts []string
	for n > c+1 {
		parts = append(parts, strings.Repeat("d", c))
		n -= c + 1
	}
	return strings.Join(append(parts, strings.Repeat("f", n)), "/")
}

// deepComponents returns a name of exactly d one-byte components.
func deepComponents(d int) string { return strings.TrimSuffix(strings.Repeat("a/", d), "/") }

// TestAdvBreakDeepNameIsQuadratic: a header name can be 1 MiB through a PAX record, and with a link
// skipped every later entry pays a lookup per path component. extractTar bounds the cleaned name at
// maxEntryName bytes and the depth at maxEntryDepth components, which fixes the work per entry.
// The cases are deterministic and assert no time: a name at the byte bound extracts and one byte
// over is refused; a path exactly at the depth bound extracts and one component over is refused.
func TestAdvBreakDeepNameIsQuadratic(t *testing.T) {
	at, over := longComponents(maxEntryName), longComponents(maxEntryName+1)
	if len(at) != maxEntryName || len(over) != maxEntryName+1 {
		t.Fatalf("test names are %d and %d bytes", len(at), len(over))
	}
	files, links, err, dst := advSkipThen(t, at)
	if err != nil {
		t.Fatalf("a %d-byte name at the bound: %v", len(at), err)
	}
	if files != 1 || links != 1 {
		t.Errorf("files = %d, links = %d; want 1 and 1", files, links)
	}
	if _, err := os.Lstat(filepath.Join(dst, strings.Repeat("d", 200))); err != nil {
		t.Errorf("the long-component file was not written: %v", err)
	}
	if _, links, err, _ := advSkipThen(t, over); err == nil || !strings.Contains(err.Error(), "over the 4096-byte bound") || links != 1 {
		t.Errorf("a %d-byte name: err = %v, links = %d; want the over-the-bound error and links = 1", len(over), err, links)
	}
}

// TestAdvDepthBound: depth is bounded apart from length, because os.Root walks the path from the
// root for every component it creates.
func TestAdvDepthBound(t *testing.T) {
	files, links, err, dst := advSkipThen(t, deepComponents(maxEntryDepth))
	if err != nil || files != 1 || links != 1 {
		t.Fatalf("depth %d: files = %d, links = %d, err = %v; want 1, 1, nil", maxEntryDepth, files, links, err)
	}
	if _, err := os.Lstat(filepath.Join(dst, strings.Repeat("a/", maxEntryDepth-1)+"a")); err != nil {
		t.Errorf("the file at the depth bound was not written: %v", err)
	}
	_, links, err, dst = advSkipThen(t, deepComponents(maxEntryDepth+1))
	if err == nil || !strings.Contains(err.Error(), "components deep") || links != 1 {
		t.Errorf("depth %d: err = %v, links = %d; want the depth error and links = 1", maxEntryDepth+1, err, links)
	}
	if _, err := os.Lstat(filepath.Join(dst, "a")); err == nil {
		t.Error("the over-deep entry created a directory before being refused")
	}
}
