package main

import (
	"bytes"
	"strings"
	"testing"
)

// A multi-line block directive in a CRLF file: go/scanner strips the carriage returns from the
// comment text, so ast.Comment.End() (Slash + len(Text)) falls short of the comment's real end and
// stripDirectives leaves the tail ("*/") behind.
func TestAdvStripDirectivesCRLFBlockComment(t *testing.T) {
	for name, src := range map[string]string{
		"inline":     "package p\r\n\r\nfunc f(a, b int) int {\r\n\treturn a /*gosree:x\r\n\r\n*/ + b\r\n}\r\n",
		"standalone": "package p\r\n\r\n/*gosree:x\r\ny\r\n*/\r\nvar _ = 1\r\n",
	} {
		t.Run(name, func(t *testing.T) { checkStrip(t, src) })
	}
}

// The same input through Erase: a valid annotated CRLF file is refused with a parse error of text
// the eraser itself produced.
func TestAdvEraseCRLFMultilineDirective(t *testing.T) {
	src := "package p\r\n\r\nfunc f(a, b int) int {\r\n\treturn a /*gosree:x\r\n\r\n*/ + b\r\n}\r\n"
	if _, err := Erase([]byte(src)); err != nil {
		t.Errorf("Erase refused a valid CRLF file: %v", err)
	}
}

// -verify with standard input has nothing to verify: it must not exit 0 as if it had proven
// equivalence ("a check that could not run must not report the same as one that ran and passed").
func TestAdvVerifyWithStdinIsNotSilentlyIgnored(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-verify"}, strings.NewReader(directiveSrc), &stdout, &stderr)
	if code == 0 && !strings.Contains(stderr.String(), "verif") {
		t.Errorf("-verify on stdin exited 0 with no word about verification; stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

// go.mod language versions of toolchain release candidates: "go 1.27" is newer than go1.27rc1, so the
// go command would try to switch toolchains for a probe module that says it.
func TestAdvGoLanguageVersionPrerelease(t *testing.T) {
	for in, want := range map[string]string{
		"go1.27rc1":   "1.27rc1",
		"go1.28beta2": "1.28beta2",
	} {
		if got, err := goLanguageVersion(in); err != nil || got != want {
			t.Errorf("goLanguageVersion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// A development toolchain ("devel go1.28-abc Tue ...") makes every -verify fail outright.
func TestAdvGoLanguageVersionDevel(t *testing.T) {
	if got, err := goLanguageVersion("devel go1.28-0123abcd Tue Nov 10 12:00:00 2026 +0000"); err != nil {
		t.Errorf("a devel toolchain is unusable for -verify: %v (got %q)", err, got)
	}
}
