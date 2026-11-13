package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Adversarial tests for the link-skipping extractTar (ft-f2e5). Tests named TestAdvBreak* assert
// the behaviour the card asks for and currently FAIL; every other test passes and documents an
// edge the fix defends (or one that cannot matter for the safety property: nothing is created
// outside dst and no link is created anywhere).

// advExtract adapts extractTar for the tests.
func advExtract(r io.Reader, prefix, dst string) (files, links int, err error) {
	res, err := extractTar(r, prefix, dst)
	return res.files, res.links, err
}

func advDst(t *testing.T) (base, dst string) {
	t.Helper()
	base = t.TempDir()
	dst = filepath.Join(base, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	return base, dst
}

// advTree lists every path under root with its type and, for files, content.
func advTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			out = append(out, rel+" LINK")
		case fi.IsDir():
			out = append(out, rel+"/")
		default:
			b, _ := os.ReadFile(p)
			out = append(out, fmt.Sprintf("%s %q", rel, b))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// advNoLinksAnywhere fails when any symlink exists under root, or anything exists beside dst in base.
func advSafe(t *testing.T, base, dst string) {
	t.Helper()
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			t.Errorf("a symlink was created: %s", p)
		}
		if p != base && p != dst && !strings.HasPrefix(p, dst+string(filepath.Separator)) {
			t.Errorf("something was created outside dst: %s", p)
		}
		return nil
	})
}

// advBlock sets the checksum of the 512-byte header block.
func advChecksum(blk []byte) {
	for i := 148; i < 156; i++ {
		blk[i] = ' '
	}
	var sum int64
	for _, b := range blk[:512] {
		sum += int64(b)
	}
	copy(blk[148:], fmt.Sprintf("%06o\x00 ", sum))
}

// advRawEntry returns one tar entry (header, then body padded to 512) whose type flag is flag and
// whose size field is len(body), without a trailer. tar.Writer refuses to write a body after a link
// header, so the header is written as a regular file and its type flag patched.
func advRawEntry(t *testing.T, name string, flag byte, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)), Format: tar.FormatGNU}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	b[156] = flag
	advChecksum(b[:512])
	return b
}

func advTrailer() []byte { return make([]byte, 1024) }

// advRaw concatenates raw entries and a trailer.
func advRaw(parts ...[]byte) *bytes.Reader {
	return bytes.NewReader(append(bytes.Join(parts, nil), advTrailer()...))
}

// TestAdvSpellingsOfSkippedLinkAreRefused: names that clean to the path of a skipped link, or to a
// path beneath it, are refused whatever the raw spelling.
func TestAdvSpellingsOfSkippedLinkAreRefused(t *testing.T) {
	type tc struct{ link, child string }
	for name, c := range map[string]tc{
		"double slash":             {"svc/l", "svc/l//x.go"},
		"dot segment":              {"svc/l", "svc/l/./x.go"},
		"dir with trailing slash":  {"svc/l", "svc/l/sub/"},
		"dir named as the link":    {"svc/l", "svc/l/"},
		"dot after the link":       {"svc/l", "svc/l/."},
		"dot before the link":      {"svc/l", "svc/./l"},
		"dot-dot back to the link": {"svc/l", "svc/l/y/../x.go"},
		"dot-dot to the link":      {"svc/l", "svc/l/y/.."},
		"dot-dot via sibling":      {"svc/l", "svc/a/../l/x.go"},
		"dot-dot reenter deep":     {"svc/a/b", "svc/a/b/../../a/b/x.go"},
		"link spelled with //":     {"svc/l//", "svc/l/x.go"},
		"link spelled with ./":     {"svc/./l", "svc/l/x.go"},
		"link spelled with ..":     {"svc/a/../l", "svc/l/x.go"},
		"link with trailing slash": {"svc/l/", "svc/l"},
		"both oddly spelled":       {"svc/./a//l/", "svc/a/./l/../l/x.go"},
	} {
		for _, typ := range []byte{tar.TypeSymlink, tar.TypeLink} {
			t.Run(fmt.Sprintf("%s/%c", name, typ), func(t *testing.T) {
				base, dst := advDst(t)
				hdrs := []tar.Header{
					{Name: c.link, Typeflag: typ, Linkname: "elsewhere"},
					{Name: c.child, Typeflag: tar.TypeReg, Mode: 0o644},
				}
				body := "x"
				if strings.HasSuffix(c.child, "/") {
					hdrs[1].Typeflag, body = tar.TypeDir, ""
				}
				_, _, err := advExtract(tarOf(t, hdrs, []string{"", body}), "svc/", dst)
				if err == nil {
					t.Fatalf("link %q then entry %q was accepted", c.link, c.child)
				}
				advSafe(t, base, dst)
			})
		}
	}
}

// TestAdvNotBeneathIsNotRefused: cleaned paths that merely sit beside, above, or a string prefix
// of a skipped link must still be extracted.
func TestAdvNotBeneathIsNotRefused(t *testing.T) {
	base, dst := advDst(t)
	r := tarOf(t, []tar.Header{
		{Name: "svc/lib", Typeflag: tar.TypeSymlink, Linkname: "elsewhere"},
		{Name: "svc/library/x.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/lib.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/lib-x/y.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/li/z.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/a/b", Typeflag: tar.TypeLink, Linkname: "svc/lib.go"},
		{Name: "svc/a/b/../c.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/a/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "svc/", Typeflag: tar.TypeDir, Mode: 0o755},
	}, []string{"", "1", "2", "3", "4", "", "5", "", ""})
	files, links, err := advExtract(r, "svc/", dst)
	if err != nil {
		t.Fatalf("a path that is not at or beneath a skipped link was refused: %v", err)
	}
	if files != 5 || links != 2 {
		t.Errorf("files = %d, links = %d; want 5 and 2", files, links)
	}
	want := []string{"a/", "a/c.go \"5\"", "li/", "li/z.go \"4\"", "lib-x/", "lib-x/y.go \"3\"", "lib.go \"2\"", "library/", "library/x.go \"1\""}
	sort.Strings(want)
	if got := advTree(t, dst); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("tree = %q\nwant   %q", got, want)
	}
	advSafe(t, base, dst)
}

// TestAdvOrderingEdges: what happens to entries that arrive before the link or share its path.
func TestAdvOrderingEdges(t *testing.T) {
	t.Run("file before link of the same name stays, link skipped, later child refused", func(t *testing.T) {
		base, dst := advDst(t)
		r := tarOf(t, []tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeReg, Mode: 0o644},
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
			{Name: "svc/l/x.go", Typeflag: tar.TypeReg, Mode: 0o644},
		}, []string{"f", "", "x"})
		_, _, err := advExtract(r, "svc/", dst)
		if err == nil {
			t.Fatal("child beneath a skipped link accepted")
		}
		advSafe(t, base, dst)
	})
	t.Run("a second link at the path of a skipped link is refused, by design", func(t *testing.T) {
		_, dst := advDst(t)
		r := tarOf(t, []tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
			{Name: "svc/l", Typeflag: tar.TypeLink, Linkname: "svc/y"},
		}, []string{"", ""})
		if _, _, err := advExtract(r, "svc/", dst); err == nil {
			t.Error("a duplicate link entry was accepted")
		}
	})
	t.Run("link outside the prefix still fails closed", func(t *testing.T) {
		base, dst := advDst(t)
		_, _, err := advExtract(tarOf(t, []tar.Header{{Name: "other/l", Typeflag: tar.TypeSymlink, Linkname: "x"}}, []string{""}), "svc/", dst)
		if err == nil {
			t.Error("a link outside the prefix was accepted")
		}
		advSafe(t, base, dst)
	})
	t.Run("raw double slash after the prefix escapes", func(t *testing.T) {
		base, dst := advDst(t)
		_, _, err := advExtract(tarOf(t, []tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
			{Name: "svc//l/x.go", Typeflag: tar.TypeReg, Mode: 0o644},
		}, []string{"", "x"}), "svc/", dst)
		if err == nil {
			t.Error("svc//l/x.go was accepted")
		}
		advSafe(t, base, dst)
	})
}

// TestAdvBreakLinkAtRootUncounted: a link entry whose name is the prefix itself ("svc/") is
// skipped by the rel == "" shortcut before the type switch, so it is neither counted nor refused
// beneath. The card says every link entry is counted, and the count is compared with `tar -t`.
func TestAdvBreakLinkAtRootUncounted(t *testing.T) {
	_, dst := advDst(t)
	r := tarOf(t, []tar.Header{
		{Name: "svc/", Typeflag: tar.TypeSymlink, Linkname: "elsewhere"},
		{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"", "a"})
	files, links, err := advExtract(r, "svc/", dst)
	if err == nil {
		t.Error("svc/a.go beneath a skipped link at the subdirectory root was accepted")
	}
	if links != 1 {
		t.Errorf("files = %d, links = %d; the archive holds 1 link entry, want links = 1", files, links)
	}
}

// TestAdvBreakLinkToRootDoesNotGuardChildren: a link whose cleaned path is "." (svc/., svc/l/..) is
// counted, but the ancestor walk stops at "." so nothing beneath it is refused, unlike every other
// skipped link. Nothing escapes; the inconsistency is in the rule the card states.
func TestAdvBreakLinkToRootDoesNotGuardChildren(t *testing.T) {
	_, dst := advDst(t)
	r := tarOf(t, []tar.Header{
		{Name: "svc/.", Typeflag: tar.TypeSymlink, Linkname: "elsewhere"},
		{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"", "a"})
	if _, links, err := advExtract(r, "svc/", dst); err == nil {
		t.Errorf("entry beneath a skipped link at the root was accepted (links = %d); every other skipped link refuses its children", links)
	}
}

// TestAdvCaseOnlyDifference: link L, then l/x.go. On a case-insensitive filesystem the file lands in
// a directory that the link "would" have occupied; on a case-sensitive one it is a different path.
// Either way nothing escapes and no link exists. This documents which happened.
func TestAdvCaseOnlyDifference(t *testing.T) {
	base, dst := advDst(t)
	probe := filepath.Join(base, "Aa")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Lstat(filepath.Join(base, "aA"))
	insensitive := statErr == nil
	_ = os.Remove(probe)
	r := tarOf(t, []tar.Header{
		{Name: "svc/L", Typeflag: tar.TypeSymlink, Linkname: "../../out"},
		{Name: "svc/l/x.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/L/y.go", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"", "x", "y"})
	files, links, err := advExtract(r, "svc/", dst)
	t.Logf("case-insensitive filesystem: %v; files = %d, links = %d, err = %v; tree = %q", insensitive, files, links, err, advTree(t, dst))
	if err == nil {
		t.Error("svc/L/y.go beneath the skipped link was accepted")
	}
	if links != 1 {
		t.Errorf("links = %d, want 1", links)
	}
	advSafe(t, base, dst)
}

// TestAdvUnicodeNormalisation: NFC link name, NFD child. Same property as the case test.
func TestAdvUnicodeNormalisation(t *testing.T) {
	const nfc, nfd = "café", "café"
	for name, c := range map[string][2]string{"NFC then NFD": {nfc, nfd}, "NFD then NFC": {nfd, nfc}} {
		t.Run(name, func(t *testing.T) {
			base, dst := advDst(t)
			r := tarOf(t, []tar.Header{
				{Name: "svc/" + c[0], Typeflag: tar.TypeSymlink, Linkname: "../../out"},
				{Name: "svc/" + c[1] + "/x.go", Typeflag: tar.TypeReg, Mode: 0o644},
			}, []string{"", "x"})
			files, links, err := advExtract(r, "svc/", dst)
			t.Logf("files = %d, links = %d, err = %v, tree = %q", files, links, err, advTree(t, dst))
			if links != 1 {
				t.Errorf("links = %d, want 1", links)
			}
			advSafe(t, base, dst)
		})
	}
}

// TestAdvLinkWithBody: a link header whose size field is non-zero. archive/tar treats link types as
// header-only, so the body bytes are read as the next header. The reader must not desync into
// silent success: either an error or a well-formed next entry, and never a created link.
func TestAdvLinkWithBody(t *testing.T) {
	t.Run("garbage body is a header error", func(t *testing.T) {
		base, dst := advDst(t)
		for _, flag := range []byte{tar.TypeSymlink, tar.TypeLink} {
			r := advRaw(advRawEntry(t, "svc/l", flag, []byte("hello, body")), advRawEntry(t, "svc/a.go", tar.TypeReg, []byte("a")))
			files, links, err := advExtract(r, "svc/", dst)
			if err == nil {
				t.Errorf("flag %c: link with a 11-byte body then a file: no error (files = %d, links = %d)", flag, files, links)
			}
		}
		advSafe(t, base, dst)
	})
	t.Run("body that is itself a valid entry is read as the next entry", func(t *testing.T) {
		base, dst := advDst(t)
		smug := advRawEntry(t, "svc/smug.go", tar.TypeReg, []byte("abc"))
		r := advRaw(advRawEntry(t, "svc/l", tar.TypeSymlink, smug), advRawEntry(t, "svc/b.go", tar.TypeReg, []byte("b")))
		files, links, err := advExtract(r, "svc/", dst)
		t.Logf("files = %d, links = %d, err = %v, tree = %q", files, links, err, advTree(t, dst))
		advSafe(t, base, dst)
	})
	t.Run("smuggled child beneath the link is still refused", func(t *testing.T) {
		base, dst := advDst(t)
		smug := advRawEntry(t, "svc/l/x.go", tar.TypeReg, []byte("abc"))
		r := advRaw(advRawEntry(t, "svc/l", tar.TypeSymlink, smug))
		if _, _, err := advExtract(r, "svc/", dst); err == nil {
			t.Error("entry hidden in a link body, beneath that link, was accepted")
		}
		advSafe(t, base, dst)
	})
}

// TestAdvLongNameLinks: PAX path/linkpath records and GNU ././@LongLink entries for links.
func TestAdvLongNameLinks(t *testing.T) {
	long := strings.Repeat("segment/", 40) + "leaf"
	for name, format := range map[string]tar.Format{"PAX": tar.FormatPAX, "GNU": tar.FormatGNU} {
		for _, typ := range []byte{tar.TypeSymlink, tar.TypeLink} {
			t.Run(fmt.Sprintf("%s/%c", name, typ), func(t *testing.T) {
				base, dst := advDst(t)
				hdrs := []tar.Header{
					{Name: "svc/" + long, Typeflag: typ, Linkname: "svc/" + strings.Repeat("target/", 40), Format: format},
					{Name: "svc/" + long + "/child.go", Typeflag: tar.TypeReg, Mode: 0o644, Format: format},
				}
				files, links, err := advExtract(tarOf(t, hdrs[:1], []string{""}), "svc/", dst)
				if err != nil || files != 0 || links != 1 {
					t.Fatalf("long link alone: files = %d, links = %d, err = %v; want 0, 1, nil", files, links, err)
				}
				if _, _, err := advExtract(tarOf(t, hdrs, []string{"", "x"}), "svc/", dst); err == nil {
					t.Error("child beneath a long-named skipped link was accepted")
				}
				advSafe(t, base, dst)
			})
		}
	}
	t.Run("raw PAX path record overriding a short link name", func(t *testing.T) {
		base, dst := advDst(t)
		rec := " path=svc/overridden\n"
		rec = fmt.Sprintf("%d%s", len(rec)+2, rec)
		r := advRaw(advRawEntry(t, "PaxHeaders/x", tar.TypeXHeader, []byte(rec)),
			advRawEntry(t, "svc/short", tar.TypeSymlink, nil),
			advRawEntry(t, "svc/overridden/x.go", tar.TypeReg, []byte("x")))
		if _, _, err := advExtract(r, "svc/", dst); err == nil {
			t.Error("child beneath the PAX-renamed link was accepted")
		}
		advSafe(t, base, dst)
	})
}

// TestAdvUnsupportedTypesFailClosed: device, FIFO, contiguous and sparse entries still fail the
// whole extraction, alone and after skipped links, and nothing is created for them.
func TestAdvUnsupportedTypesFailClosed(t *testing.T) {
	for name, typ := range map[string]byte{"char": tar.TypeChar, "block": tar.TypeBlock, "fifo": tar.TypeFifo, "cont": tar.TypeCont} {
		for _, withLink := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/afterLink=%v", name, withLink), func(t *testing.T) {
				base, dst := advDst(t)
				var hdrs []tar.Header
				var bodies []string
				if withLink {
					hdrs = append(hdrs, tar.Header{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"})
					bodies = append(bodies, "")
				}
				hdrs = append(hdrs, tar.Header{Name: "svc/dev", Typeflag: typ, Mode: 0o644})
				bodies = append(bodies, "")
				_, _, err := advExtract(tarOf(t, hdrs, bodies), "svc/", dst)
				if err == nil || !strings.Contains(err.Error(), "unsupported type") {
					t.Errorf("err = %v, want an unsupported type error", err)
				}
				lstatMissing(t, filepath.Join(dst, "dev"))
				advSafe(t, base, dst)
			})
		}
	}
	t.Run("GNU sparse", func(t *testing.T) {
		base, dst := advDst(t)
		r := advRaw(advRawEntry(t, "svc/l", tar.TypeSymlink, nil), advRawEntry(t, "svc/sparse", tar.TypeGNUSparse, nil))
		files, links, err := advExtract(r, "svc/", dst)
		t.Logf("files = %d, links = %d, err = %v", files, links, err)
		if err == nil {
			t.Error("a GNU sparse entry was accepted")
		}
		lstatMissing(t, filepath.Join(dst, "sparse"))
		advSafe(t, base, dst)
	})
	t.Run("unsupported type beneath a skipped link names the link", func(t *testing.T) {
		_, dst := advDst(t)
		_, _, err := advExtract(tarOf(t, []tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
			{Name: "svc/l/dev", Typeflag: tar.TypeFifo},
		}, []string{"", ""}), "svc/", dst)
		if err == nil {
			t.Error("accepted")
		}
	})
}

// TestAdvManyLinksAllocationLinear: total bytes allocated per link entry stay flat between 25k and
// 100k entries (a quadratic structure would make the ratio approach 16, not 4).
func TestAdvManyLinksAllocationLinear(t *testing.T) {
	measure := func(n int) (alloc uint64, links int) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for i := range n {
			typ := byte(tar.TypeSymlink)
			if i%2 == 1 {
				typ = tar.TypeLink
			}
			if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("svc/d%d/l%d", i%97, i), Typeflag: typ, Linkname: "/etc/passwd"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		_, dst := advDst(t)
		r := bytes.NewReader(buf.Bytes())
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, links, err := advExtract(r, "svc/", dst)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		return after.TotalAlloc - before.TotalAlloc, links
	}
	a1, l1 := measure(25_000)
	a2, l2 := measure(100_000)
	if l1 != 25_000 || l2 != 100_000 {
		t.Fatalf("links = %d and %d, want 25000 and 100000", l1, l2)
	}
	ratio := float64(a2) / float64(a1)
	t.Logf("TotalAlloc 25k = %d B (%.0f B/entry), 100k = %d B (%.0f B/entry), ratio %.2f (linear = 4)", a1, float64(a1)/25000, a2, float64(a2)/100000, ratio)
	if ratio > 6 {
		t.Errorf("allocation ratio %.2f for 4x entries, want about 4 (linear)", ratio)
	}
}

// TestAdvTruncatedAndCorrupt: an archive cut mid-entry, or with a damaged header, after link
// entries returns an error and the partial counts are not a success.
func TestAdvTruncatedAndCorrupt(t *testing.T) {
	hdrs := []tar.Header{
		{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"},
		{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/b.go", Typeflag: tar.TypeReg, Mode: 0o644},
	}
	full, err := io.ReadAll(tarOf(t, hdrs, []string{"", strings.Repeat("a", 2000), strings.Repeat("b", 2000)}))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("cut mid body", func(t *testing.T) {
		_, dst := advDst(t)
		_, _, err := advExtract(bytes.NewReader(full[:512+512+700]), "svc/", dst)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
	t.Run("cut mid header", func(t *testing.T) {
		_, dst := advDst(t)
		_, _, err := advExtract(bytes.NewReader(full[:512+100]), "svc/", dst)
		if err == nil {
			t.Error("a header cut in half was accepted")
		}
	})
	t.Run("damaged checksum", func(t *testing.T) {
		_, dst := advDst(t)
		bad := bytes.Clone(full)
		bad[3072+148] ^= 0x01 // second regular file's header
		_, _, err := advExtract(bytes.NewReader(bad), "svc/", dst)
		if !errors.Is(err, tar.ErrHeader) {
			t.Errorf("err = %v, want tar.ErrHeader", err)
		}
	})
}

// BEGIN linkfree

// TestAdvLinkFreeArchiveTree: for an archive without link entries the extracted tree and counts are
// exactly what the unmodified code produced (this test is also run against f0dea98 in a scratch copy).
func TestAdvLinkFreeArchiveTree(t *testing.T) {
	_, dst := advDst(t)
	r := tarOf(t, []tar.Header{
		{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "0123"}},
		{Name: "backend/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "backend/svc/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "backend/svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/empty.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/sub/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "backend/svc/sub/deep/b.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/dot/./c.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/x//d.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/lib", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "backend/svc/library/e.go", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"", "", "", "first", "", "", "bb", "second", "c", "d", "lib", "e"})
	files, links, err := advExtract(r, "backend/svc/", dst)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`a.go "second"`, "dot/", `dot/c.go "c"`, `empty.go ""`, `lib "lib"`, "library/", `library/e.go "e"`,
		"sub/", "sub/deep/", `sub/deep/b.go "bb"`, "x/", `x/d.go "d"`,
	}
	if files != 8 || links != 0 {
		t.Errorf("files = %d, links = %d; want 8 and 0", files, links)
	}
	if got := advTree(t, dst); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tree = %q\nwant   %q", got, want)
	}
	for _, bad := range [][]tar.Header{
		{{Name: "backend/svc/../../x.go", Typeflag: tar.TypeReg}},
		{{Name: "elsewhere/x.go", Typeflag: tar.TypeReg}},
		{{Name: "backend/svc/dev", Typeflag: tar.TypeChar}},
	} {
		if _, _, err := advExtract(tarOf(t, bad, []string{""}), "backend/svc/", dst); err == nil {
			t.Errorf("%v accepted", bad[0].Name)
		}
	}
}

// END linkfree

// advFakeGit puts a `git` on PATH that answers rev-parse with a fixed SHA and archive with the given
// bytes and exit status.
func advFakeGit(t *testing.T, archive []byte, exit int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh script")
	}
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "fake.tar")
	if err := os.WriteFile(tarPath, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncase \"$3\" in rev-parse) echo 0123456789abcdef0123456789abcdef01234567; exit 0;; esac\ncat %q\nexit %d\n", tarPath, exit)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type advErrWriter struct{ err error }

func (w advErrWriter) Write([]byte) (int, error) { return 0, w.err }

func advLinkTar(t *testing.T) []byte {
	t.Helper()
	b, err := io.ReadAll(tarOf(t, []tar.Header{
		{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "svc/l1", Typeflag: tar.TypeSymlink, Linkname: "a.go"},
		{Name: "svc/l2", Typeflag: tar.TypeLink, Linkname: "svc/a.go"},
	}, []string{"a", "", ""}))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAdvArchiveRevDiagnostics: diag behaviour with a stubbed git, so the archive stream is ours.
func TestAdvArchiveRevDiagnostics(t *testing.T) {
	t.Run("diag writer error is returned, wrapped, with no sha", func(t *testing.T) {
		advFakeGit(t, advLinkTar(t), 0)
		boom := errors.New("stderr closed")
		sha, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), advErrWriter{boom})
		if !errors.Is(err, boom) || sha != "" {
			t.Errorf("sha = %q, err = %v; want empty sha and the writer's error", sha, err)
		}
	})
	t.Run("diag writer error is irrelevant when nothing was skipped", func(t *testing.T) {
		b, _ := io.ReadAll(tarOf(t, []tar.Header{{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"a"}))
		advFakeGit(t, b, 0)
		sha, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), advErrWriter{errors.New("x")})
		if err != nil || sha == "" {
			t.Errorf("sha = %q, err = %v; want success", sha, err)
		}
	})
	t.Run("one line, with count, sha and prefix", func(t *testing.T) {
		advFakeGit(t, advLinkTar(t), 0)
		var buf strings.Builder
		sha, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), &buf)
		if err != nil || sha == "" {
			t.Fatalf("sha = %q, err = %v", sha, err)
		}
		if strings.Count(buf.String(), "\n") != 1 || !strings.Contains(buf.String(), "skipped 2 ") || !strings.Contains(buf.String(), sha) {
			t.Errorf("diag = %q", buf.String())
		}
	})
	t.Run("truncated archive: error, nothing printed, no sha", func(t *testing.T) {
		full := advLinkTar(t)
		advFakeGit(t, full[:len(full)-700], 0)
		var buf strings.Builder
		sha, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), &buf)
		if err == nil || sha != "" || buf.Len() != 0 {
			t.Errorf("sha = %q, err = %v, diag = %q; want an error and silence", sha, err, buf.String())
		}
	})
	t.Run("git fails after a complete archive: error, nothing printed", func(t *testing.T) {
		advFakeGit(t, advLinkTar(t), 3)
		var buf strings.Builder
		sha, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), &buf)
		if err == nil || sha != "" || buf.Len() != 0 {
			t.Errorf("sha = %q, err = %v, diag = %q; want an error and silence", sha, err, buf.String())
		}
	})
	t.Run("entry beneath a skipped link: error, nothing printed", func(t *testing.T) {
		b, _ := io.ReadAll(tarOf(t, []tar.Header{
			{Name: "svc/a.go", Typeflag: tar.TypeReg, Mode: 0o644},
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "a.go"},
			{Name: "svc/l/x.go", Typeflag: tar.TypeReg, Mode: 0o644},
		}, []string{"a", "", "x"}))
		advFakeGit(t, b, 0)
		var buf strings.Builder
		sha, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), &buf)
		if err == nil || sha != "" || buf.Len() != 0 {
			t.Errorf("sha = %q, err = %v, diag = %q", sha, err, buf.String())
		}
	})
	t.Run("only links: no files error, nothing printed", func(t *testing.T) {
		b, _ := io.ReadAll(tarOf(t, []tar.Header{{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "a.go"}}, []string{""}))
		advFakeGit(t, b, 0)
		var buf strings.Builder
		_, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), &buf)
		if err == nil || buf.Len() != 0 {
			t.Errorf("err = %v, diag = %q; want the no-files error and silence", err, buf.String())
		}
	})
}

// TestAdvBreakNilDiagPanics: archiveRev dereferences diag without a check, so a caller that leaves
// it nil (a moduleOpts built without diag) panics after extraction has completed, instead of
// getting an error or the old silent behaviour.
func TestAdvBreakNilDiagPanics(t *testing.T) {
	advFakeGit(t, advLinkTar(t), 0)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("archiveRev panicked with a nil diag writer: %v", r)
		}
	}()
	if _, err := archiveRev("repo", "HEAD", "svc", filepath.Join(t.TempDir(), "o"), nil); err != nil {
		t.Logf("err = %v", err)
	}
}
