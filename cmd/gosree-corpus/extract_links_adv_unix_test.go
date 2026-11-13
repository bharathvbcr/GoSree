//go:build !windows

package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdvBreakDeepNameIsQuadratic: a header name can be 1 MiB through a PAX record, and with a link
// skipped every later entry pays a lookup per path component over the whole name. extractTar bounds
// the cleaned name at maxEntryName, which fixes the work per entry. The test is deterministic: a
// name one byte over the bound is refused with a clear error, and a name exactly at the bound,
// beneath a plain directory with a skipped link elsewhere, extracts.
func TestAdvBreakDeepNameIsQuadratic(t *testing.T) {
	deep := func(n int) string { // a cleaned name of exactly n bytes, 'a/' repeated, ending in a file name
		return strings.Repeat("a/", (n-2)/2) + strings.Repeat("z", n-2*((n-2)/2))
	}
	at, over := deep(maxEntryName), deep(maxEntryName+1)
	if len(at) != maxEntryName || len(over) != maxEntryName+1 {
		t.Fatalf("test names are %d and %d bytes", len(at), len(over))
	}
	hdrs := func(name string) ([]tar.Header, []string) {
		return []tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
			{Name: "svc/" + name, Typeflag: tar.TypeReg, Mode: 0o644},
		}, []string{"", "x"}
	}

	_, dst := advDst(t)
	h, b := hdrs(over)
	if _, links, err := advExtract(tarOf(t, h, b), "svc/", dst); err == nil || !strings.Contains(err.Error(), "over the 4096-byte bound") {
		t.Errorf("a %d-byte name: err = %v, want the over-the-bound error", len(over), err)
	} else if links != 1 {
		t.Errorf("links = %d, want the link before the refused entry counted", links)
	}

	_, dst = advDst(t)
	h, b = hdrs(at)
	files, links, err := advExtract(tarOf(t, h, b), "svc/", dst)
	if err != nil {
		t.Fatalf("a %d-byte name, at the bound: %v", len(at), err)
	}
	if files != 1 || links != 1 {
		t.Errorf("files = %d, links = %d; want 1 and 1", files, links)
	}
	if _, err := os.Lstat(filepath.Join(dst, "a", "a")); err != nil {
		t.Errorf("the deep file was not written: %v", err)
	}
}
