package main

import (
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"unicode/utf8"
)

// Adversarial cases for gosreeCommit. Each asserts what an honest provenance field would say; a
// failure is a place where the field misleads or fails open.

func commitFor(t *testing.T, settings ...debug.BuildSetting) string {
	t.Helper()
	old := readBuildInfo
	readBuildInfo = stamped(settings...)
	t.Cleanup(func() { readBuildInfo = old })
	return gosreeCommit()
}

func bs(k, v string) debug.BuildSetting { return debug.BuildSetting{Key: k, Value: v} }

func moduleDocOf(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("module.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

var hexRev = regexp.MustCompile(`^[0-9a-f]{7}(-dirty)?$`)

// A revision from hg, svn, fossil or bzr is not a git hash. Presented as bare 7 characters it reads
// as a git commit that `git show` would resolve.
func TestAdvNonGitVCSIsNamed(t *testing.T) {
	for _, vcs := range []string{"hg", "svn", "fossil", "bzr"} {
		t.Run(vcs, func(t *testing.T) {
			got := commitFor(t, bs("vcs", vcs), bs("vcs.revision", "0123456789abcdef0123456789abcdef01234567"), bs("vcs.modified", "false"))
			if !strings.Contains(got, vcs) {
				t.Fatalf("gosreeCommit() = %q for vcs=%s: indistinguishable from a git hash", got, vcs)
			}
		})
	}
}

// The git case must stay a bare hash so existing records stay comparable.
func TestAdvGitVCSStaysBare(t *testing.T) {
	got := commitFor(t, bs("vcs", "git"), bs("vcs.revision", "0123456789abcdef0123456789abcdef01234567"))
	if got != "0123456" {
		t.Fatalf("gosreeCommit() = %q", got)
	}
}

// A revision that is not hexadecimal must not be passed through as if it were a commit.
func TestAdvMalformedRevision(t *testing.T) {
	cases := map[string]string{
		"non-hex":            "not-a-hash-at-all",
		"leading whitespace": "  \t0123456789abcdef",
		"embedded newline":   "012\n456789abcdef",
		"embedded space":     "abc defghijk",
		"control byte":       "ab\x00cdef0123",
		"multibyte split":    "ééééé",
		"quote and brace":    `"},{"x":"`,
	}
	for name, rev := range cases {
		t.Run(name, func(t *testing.T) {
			got := commitFor(t, bs("vcs", "git"), bs("vcs.revision", rev))
			if !utf8.ValidString(got) {
				t.Fatalf("gosreeCommit() = %q is not valid UTF-8", got)
			}
			if !hexRev.MatchString(got) && got != noVCSStamp {
				t.Fatalf("gosreeCommit() = %q from revision %q: neither a hex hash nor the no-stamp marker", got, rev)
			}
		})
	}
}

// Very long revisions are bounded (defended).
func TestAdvVeryLongRevisionIsBounded(t *testing.T) {
	got := commitFor(t, bs("vcs", "git"), bs("vcs.revision", strings.Repeat("a", 1<<20)), bs("vcs.modified", "true"))
	if got != "aaaaaaa-dirty" {
		t.Fatalf("len=%d", len(got))
	}
}

// Go emits only "true" or "false". Any other value is a stamp this code does not understand, and
// reading it as clean is failing open.
func TestAdvUnrecognisedModifiedIsNotClean(t *testing.T) {
	for _, v := range []string{"TRUE", "True", "1", "yes", " true", "true\n", ""} {
		t.Run("modified="+v, func(t *testing.T) {
			got := commitFor(t, bs("vcs", "git"), bs("vcs.revision", "0123456789abcdef"), bs("vcs.modified", v))
			if v == "" {
				t.Skip("absent or empty modified is how a go without the setting looks; documented as clean")
			}
			if got == "0123456" {
				t.Fatalf("vcs.modified=%q reported clean: %q", v, got)
			}
		})
	}
}

// Duplicate keys never come from go build, but the answer should not depend on their order.
func TestAdvDuplicateRevisionKeys(t *testing.T) {
	a := commitFor(t, bs("vcs", "git"), bs("vcs.revision", "1111111aaaa"), bs("vcs.revision", "2222222bbbb"))
	b := commitFor(t, bs("vcs", "git"), bs("vcs.revision", "2222222bbbb"), bs("vcs.revision", "1111111aaaa"))
	if a != b {
		t.Fatalf("order-dependent: %q vs %q (last one wins silently)", a, b)
	}
}

// vcs.revision without vcs: the VCS is unknown, so the revision cannot be called a git hash.
func TestAdvRevisionWithoutVCS(t *testing.T) {
	got := commitFor(t, bs("vcs.revision", "0123456789abcdef"))
	if got == "0123456" {
		t.Fatalf("gosreeCommit() = %q with no vcs setting: presented as a git hash", got)
	}
}

// vcs.modified alone, with no revision, must not look like a stamp.
func TestAdvModifiedWithoutRevision(t *testing.T) {
	if got := commitFor(t, bs("vcs", "git"), bs("vcs.modified", "true")); got != noVCSStamp {
		t.Fatalf("gosreeCommit() = %q", got)
	}
}

// The doc comment claims the output has "the same shape as `git describe --always --dirty`".
// These pin where it differs, so the claim cannot be relied on: describe prints the nearest tag as
// v1.2-3-gabc1234, and abbreviates to more than 7 characters when 7 is ambiguous. The function
// always emits a bare 7-character prefix. Untracked files make vcs.modified true but not describe's
// -dirty (see the scratchpad build record).
func TestAdvDocClaimsDescribeShape(t *testing.T) {
	got := commitFor(t, bs("vcs", "git"), bs("vcs.revision", "0123456789abcdef0123456789abcdef01234567"))
	if regexp.MustCompile(`^v?\d+(\.\d+)*-\d+-g[0-9a-f]+$`).MatchString(got) {
		t.Skip("emits tag-relative describe output")
	}
	doc := moduleDocOf(t)
	if strings.Contains(doc, "same shape as `git describe --always --dirty`") {
		t.Fatalf("doc overclaims: output %q never has describe's tag form, abbreviation growth, or its untracked-file rule", got)
	}
}
