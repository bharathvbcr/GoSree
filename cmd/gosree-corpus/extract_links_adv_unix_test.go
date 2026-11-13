//go:build !windows

package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

func advCPU() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// TestAdvBreakDeepNameIsQuadratic: with one skipped link recorded, every later entry pays
// skippedAncestor, which calls path.Dir (a Clean over the whole string) once per path component and
// hashes the whole string for each map lookup: O(components x length) per entry. A header name can
// be up to 1 MiB through a PAX record. The same entries without a skipped link cost O(length), so
// the ratio between a 4x longer name is about 4; with the link it is about 16.
// Process CPU time (not wall clock) with min-of-3, so load on the machine matters little.
func TestAdvBreakDeepNameIsQuadratic(t *testing.T) {
	build := func(k int, withLink bool) *bytes.Reader {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if withLink {
			if err := tw.WriteHeader(&tar.Header{Name: "svc/l", Typeflag: tar.TypeSymlink, Linkname: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 1 {
			if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("svc/%sz%d", strings.Repeat("a/", k), i), Typeflag: tar.TypeLink, Linkname: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(buf.Bytes())
	}
	cost := func(k int, withLink bool) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			r := build(k, withLink)
			_, dst := advDst(t)
			start := advCPU()
			if _, _, err := advExtract(r, "svc/", dst); err != nil {
				t.Fatal(err)
			}
			if d := advCPU() - start; d < best {
				best = d
			}
		}
		return best
	}
	const small, large = 2500, 10000
	noLink := float64(cost(large, false)) / float64(max(cost(small, false), time.Microsecond))
	link := float64(cost(large, true)) / float64(max(cost(small, true), time.Microsecond))
	t.Logf("CPU ratio for a 4x longer name (linear = 4, quadratic = 16): without a skipped link %.1f, with one %.1f", noLink, link)
	if link > 10 {
		t.Errorf("work per entry grows %.1fx for a 4x longer path when a link was skipped (%.1fx without): quadratic in the path's component count", link, noLink)
	}
}
