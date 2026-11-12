package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Odd table paths must be refused and leave every file untouched.
func TestAdvApplyAnnotationsOddPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path forms")
	}
	for name, file := range map[string]string{
		"dot segment escape": "a/./../../outside.go",
		"trailing slash":     "p.go/",
		"trailing dot":       "p.go/.",
		"leading dot":        "./p.go",
		"double slash":       "sub//q.go",
		"drive":              `C:\x.go`,
		"unc":                `\\server\share\x.go`,
		"nul":                "p.go\x00../outside.go",
		"fullwidth dots":     "\uff0e\uff0e/outside.go",
		"division slash":     "sub\u2215q.go",
		"very long":          strings.Repeat("a/", 3000) + "p.go",
		"long name":          strings.Repeat("a", 5000) + ".go",
	} {
		t.Run(name, func(t *testing.T) {
			base, dir, outside := archiveFixture(t)
			if err := applyAnnotations(dir, rowFor(file)); err == nil {
				t.Errorf("row for %q applied", file)
			}
			for _, p := range []string{outside, filepath.Join(dir, "p.go"), filepath.Join(dir, "sub", "q.go"), filepath.Join(base, "outsidedir", "x.go")} {
				if got, _ := os.ReadFile(p); string(got) != outsideSrc {
					t.Errorf("%s was modified:\n%s", p, got)
				}
			}
		})
	}
}

// Archive entries that try to place or reach files outside dst, by every route a tar offers.
func TestAdvExtractTarAdversarial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	for name, tc := range map[string]struct {
		hdrs   []tar.Header
		bodies []string
	}{
		"dotdot in the middle": {[]tar.Header{{Name: "svc/a/../../../out/pwned.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"x"}},
		"absolute symlink then file": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "<out>"},
			{Name: "svc/l/pwned.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"file then symlink of the same name": {[]tar.Header{
			{Name: "svc/l", Typeflag: tar.TypeReg, Mode: 0o644},
			{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "<out>/pwned.go"},
			{Name: "svc/l", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"x", "", "y"}},
		"symlink dir created after a file beneath it": {[]tar.Header{
			{Name: "svc/d/f.go", Typeflag: tar.TypeReg, Mode: 0o644},
			{Name: "svc/d", Typeflag: tar.TypeSymlink, Linkname: "<out>"},
			{Name: "svc/d/pwned.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"x", "", "y"}},
		"chain of relative links": {[]tar.Header{
			{Name: "svc/a", Typeflag: tar.TypeSymlink, Linkname: ".."},
			{Name: "svc/a/a/pwned.go", Typeflag: tar.TypeReg, Mode: 0o644}}, []string{"", "x"}},
		"hard link": {[]tar.Header{
			{Name: "svc/h", Typeflag: tar.TypeLink, Linkname: "<out>/victim.go"}}, []string{""}},
		"fifo":   {[]tar.Header{{Name: "svc/f", Typeflag: tar.TypeFifo}}, []string{""}},
		"device": {[]tar.Header{{Name: "svc/c", Typeflag: tar.TypeChar}}, []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			dst, out := filepath.Join(base, "dst"), filepath.Join(base, "out")
			for _, d := range []string{dst, out} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			victim := filepath.Join(out, "victim.go")
			if err := os.WriteFile(victim, []byte("orig"), 0o644); err != nil {
				t.Fatal(err)
			}
			hdrs := append([]tar.Header(nil), tc.hdrs...)
			for i := range hdrs {
				hdrs[i].Linkname = strings.ReplaceAll(hdrs[i].Linkname, "<out>", out)
			}
			_, _ = extractTar(tarOf(t, hdrs, tc.bodies), "svc/", dst)
			ents, _ := os.ReadDir(out)
			if len(ents) != 1 {
				t.Errorf("files appeared outside dst: %v", ents)
			}
			if got, _ := os.ReadFile(victim); string(got) != "orig" {
				t.Errorf("a file outside dst was overwritten: %q", got)
			}
		})
	}
}
