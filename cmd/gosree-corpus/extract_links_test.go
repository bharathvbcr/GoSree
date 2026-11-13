package main

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runExtract extracts r into dst and returns the file and skipped-link counts.
func runExtract(r io.Reader, prefix, dst string) (files, skipped int, err error) {
	res, err := extractTar(r, prefix, dst)
	return res.files, res.links, err
}

// runArchive archives and returns what archiveRev wrote to its diagnostic writer.
func runArchive(repo, rev, subdir, dst string) (sha, diag string, err error) {
	var buf strings.Builder
	sha, err = archiveRev(repo, rev, subdir, dst, &buf)
	return sha, buf.String(), err
}

func newDst(t *testing.T) (base, dst string) {
	t.Helper()
	base = t.TempDir()
	dst = filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	return base, dst
}

func lstatMissing(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); err == nil {
		t.Errorf("%s exists, want nothing created", p)
	}
}

// TestExtractTarSkipsLinks: a symlink and a hard link are never created, the regular files around
// them are, and the skipped count is 2.
func TestExtractTarSkipsLinks(t *testing.T) {
	_, dst := newDst(t)
	r := tarOf(t, []tar.Header{
		{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/sym", Typeflag: tar.TypeSymlink, Linkname: "a.go"},
		{Name: "svc/hard", Typeflag: tar.TypeLink, Linkname: "svc/a.go"},
		{Name: "svc/sub/b.go", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"package a\n", "", "", "package b\n"})
	files, skipped, err := runExtract(r, "svc/", dst)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 || skipped != 2 {
		t.Errorf("files = %d, skipped = %d; want 2 and 2", files, skipped)
	}
	lstatMissing(t, filepath.Join(dst, "sym"))
	lstatMissing(t, filepath.Join(dst, "hard"))
	for _, f := range []string{"a.go", "sub/b.go"} {
		if fi, err := os.Lstat(filepath.Join(dst, f)); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("%s: %v, want a regular file", f, err)
		}
	}
}

// TestExtractTarHardLinkOnly: a hard link used to fail the whole extraction as an unsupported type.
func TestExtractTarHardLinkOnly(t *testing.T) {
	_, dst := newDst(t)
	r := tarOf(t, []tar.Header{{Name: "svc/h", Typeflag: tar.TypeLink, Linkname: "svc/x"}}, []string{""})
	files, skipped, err := runExtract(r, "svc/", dst)
	if err != nil {
		t.Fatalf("a hard-link-only archive failed: %v", err)
	}
	if files != 0 || skipped != 1 {
		t.Errorf("files = %d, skipped = %d; want 0 and 1", files, skipped)
	}
	lstatMissing(t, filepath.Join(dst, "h"))
}

// TestExtractTarRefusesBeneathSkippedLink: nothing may be written at or beneath the path of a link
// that was skipped, whatever the order of the entries.
func TestExtractTarRefusesBeneathSkippedLink(t *testing.T) {
	for name, tc := range map[string]struct {
		hdrs   []tar.Header
		bodies []string
	}{
		"file beneath symlink": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "d"},
			{Name: "svc/l/x.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"file beneath hard link": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeLink, Linkname: "svc/d"},
			{Name: "svc/l/x.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"deep file beneath symlink": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "d"},
			{Name: "svc/l/a/b/c.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"directory beneath symlink": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "d"},
			{Name: "svc/l/sub/", Typeflag: tar.TypeDir, Mode: 0o755}}, []string{"", ""}},
		"same name as symlink": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "d"},
			{Name: "svc/l", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"same name as hard link, directory": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeLink, Linkname: "svc/d"},
			{Name: "svc/l/", Typeflag: tar.TypeDir, Mode: 0o755}}, []string{"", ""}},
		"directory listed before the link": {[]tar.Header{
			{Name: "svc/d/", Typeflag: tar.TypeDir, Mode: 0o755},
			{Name: "svc/d", Typeflag: tar.TypeSymlink, Linkname: "e"},
			{Name: "svc/d/x.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "", "x"}},
		"file listed before the link": {[]tar.Header{
			{Name: "svc/d/f.go", Typeflag: tar.TypeReg, Mode: 0o644},
			{Name: "svc/d", Typeflag: tar.TypeSymlink, Linkname: "e"},
			{Name: "svc/d/x.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"f", "", "x"}},
		"dot-prefixed spelling of the child": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "d"},
			{Name: "svc/./l/../l/x.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"link beneath a skipped link": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "d"},
			{Name: "svc/l/m", Typeflag: tar.TypeSymlink, Linkname: "e"}}, []string{"", ""}},
	} {
		t.Run(name, func(t *testing.T) {
			_, dst := newDst(t)
			_, _, err := runExtract(tarOf(t, tc.hdrs, tc.bodies), "svc/", dst)
			if err == nil {
				t.Fatal("an entry at or beneath a skipped link was accepted")
			}
			if !strings.Contains(err.Error(), "skipped link") {
				t.Errorf("error %q does not say the entry is at or beneath a skipped link", err)
			}
			lstatMissing(t, filepath.Join(dst, "l", "x.go"))
			lstatMissing(t, filepath.Join(dst, "d", "x.go"))
		})
	}
}

// TestExtractTarSkipsOddlyNamedLinks: non-ASCII names, spaces and ./-prefixed names are skipped
// and counted like any other.
func TestExtractTarSkipsOddlyNamedLinks(t *testing.T) {
	_, dst := newDst(t)
	names := []string{"svc/liên kết", "svc/日本語", "svc/with space", "svc/./dot", "svc/a/../b", "svc/emoji-\U0001F600", "svc/d/nested link"}
	hdrs := []tar.Header{{Name: "svc/keep.go", Typeflag: tar.TypeReg, Mode: 0o644}}
	bodies := []string{"package p\n"}
	for i, n := range names {
		typ := byte(tar.TypeSymlink)
		if i%2 == 1 {
			typ = tar.TypeLink
		}
		hdrs = append(hdrs, tar.Header{Name: n, Typeflag: typ, Linkname: "keep.go"})
		bodies = append(bodies, "")
	}
	files, skipped, err := runExtract(tarOf(t, hdrs, bodies), "svc/", dst)
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 || skipped != len(names) {
		t.Errorf("files = %d, skipped = %d; want 1 and %d", files, skipped, len(names))
	}
	ents, _ := os.ReadDir(dst)
	if len(ents) != 1 || ents[0].Name() != "keep.go" {
		t.Errorf("destination holds %v, want only keep.go", ents)
	}
}

// TestExtractTarManyLinks: thousands of link entries, with absolute and escaping targets, create
// nothing and are all counted. The skipped-path set grows with the archive's own link count and
// nothing else.
func TestExtractTarManyLinks(t *testing.T) {
	base, dst := newDst(t)
	const n = 5000
	var hdrs []tar.Header
	var bodies []string
	for i := range n {
		target := "/etc/passwd"
		switch i % 3 {
		case 1:
			target = "../../../../out"
		case 2:
			target = filepath.Join(base, "outside")
		}
		typ := byte(tar.TypeSymlink)
		if i%2 == 1 {
			typ = tar.TypeLink
		}
		hdrs = append(hdrs, tar.Header{Name: fmt.Sprintf("svc/l/%d", i), Typeflag: typ, Linkname: target})
		bodies = append(bodies, "")
	}
	files, skipped, err := runExtract(tarOf(t, hdrs, bodies), "svc/", dst)
	if err != nil {
		t.Fatal(err)
	}
	if files != 0 || skipped != n {
		t.Errorf("files = %d, skipped = %d; want 0 and %d", files, skipped, n)
	}
	var found []string
	_ = filepath.WalkDir(dst, func(p string, _ os.DirEntry, _ error) error {
		if p != dst {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("%d paths created in dst, want none: first %v", len(found), found[:min(3, len(found))])
	}
	lstatMissing(t, filepath.Join(base, "out"))
}

// TestArchiveRevReportsSkippedLinks: a real git archive of a tree holding a symlink extracts, and
// one line on the injected writer says how many entries were skipped. A tree without links prints
// nothing.
func TestArchiveRevReportsSkippedLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "svc", "a.go"), []byte("package svc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "plain")
	dst := filepath.Join(t.TempDir(), "out")
	if _, diag, err := runArchive(repo, "HEAD", "svc", dst); err != nil || diag != "" {
		t.Fatalf("plain tree: err = %v, diag = %q; want nil and nothing", err, diag)
	}
	for _, l := range []string{"link1", "link2"} {
		if err := os.Symlink("a.go", filepath.Join(repo, "svc", l)); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".")
	git("commit", "-q", "-m", "links")
	_, diag, err := runArchive(repo, "HEAD", "svc", dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diag, "skipped 2 symlink or hard-link entries") || strings.Count(diag, "\n") != 1 {
		t.Errorf("diagnostic = %q, want one line saying 2 entries were skipped", diag)
	}
	lstatMissing(t, filepath.Join(dst, "link1"))
	lstatMissing(t, filepath.Join(dst, "link2"))
}

// TestExtractTarRefusesNonDirAtRoot: only a directory (the prefix's own entry) or a link may name
// the extraction root; a regular file spelled svc/. or svc/l/.. is refused, not dropped.
func TestExtractTarRefusesNonDirAtRoot(t *testing.T) {
	for _, name := range []string{"svc/.", "svc/l/.."} {
		_, dst := newDst(t)
		files, _, err := runExtract(tarOf(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"x"}), "svc/", dst)
		if err == nil || !strings.Contains(err.Error(), "names the extraction root itself") {
			t.Errorf("regular file %q: files = %d, err = %v; want the extraction-root error", name, files, err)
		}
	}
	_, dst := newDst(t)
	if _, _, err := runExtract(tarOf(t, []tar.Header{{Name: "svc/./", Typeflag: tar.TypeDir, Mode: 0o755}}, []string{""}), "svc/", dst); err != nil {
		t.Errorf("a directory entry for the root: %v", err)
	}
}
