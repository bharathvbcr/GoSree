package main

import (
	"bytes"
	"go/parser"
	"go/scanner"
	"go/token"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const directiveSrc = "package p\n\n//gosree:imm\ntype T struct{}\n"

// TestIncompatibleFlagCombinations: -w and -o name where the result goes, -l and -d only report. A
// combination used to do the report and write nothing, exiting 0; it is now refused as invalid flags
// before any file is touched.
func TestIncompatibleFlagCombinations(t *testing.T) {
	for name, args := range map[string][]string{
		"-w -l":    {"-w", "-l"},
		"-l -w":    {"-l", "-w"},
		"-w -d":    {"-w", "-d"},
		"-o -l":    {"-o", "OUT", "-l"},
		"-o -d":    {"-o", "OUT", "-d"},
		"-w -l -d": {"-w", "-l", "-d"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "a.go")
			out := filepath.Join(dir, "out.go")
			if err := os.WriteFile(file, []byte(directiveSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			args = append([]string(nil), args...)
			for i, a := range args {
				if a == "OUT" {
					args[i] = out
				}
			}
			var stdout, stderr bytes.Buffer
			code := run(append(args, file), strings.NewReader(""), &stdout, &stderr)
			if code != 2 {
				t.Errorf("exit %d, want 2 (invalid flags); stdout %q stderr %q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "cannot use") {
				t.Errorf("stderr does not say which flags conflict: %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("a refused combination printed %q", stdout.String())
			}
			if got, _ := os.ReadFile(file); string(got) != directiveSrc {
				t.Errorf("the input was modified: %q", got)
			}
			if _, err := os.Stat(out); err == nil {
				t.Errorf("-o target was written")
			}
		})
	}

	// Combinations that mean something still work.
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte(directiveSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-l"}, {"-d"}, {"-l", "-d"}, {"-w"}, {"-o", filepath.Join(dir, "o.go")}} {
		var stdout, stderr bytes.Buffer
		if code := run(append(args, file), strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
		if err := os.WriteFile(file, []byte(directiveSrc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStdinPanicExitsOne: a panic while erasing standard input used to unwind past run and exit with
// the runtime's status 2, which is reserved for invalid flags.
func TestStdinPanicExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	boom := func([]byte) ([]byte, error) { panic("boom") }
	code := runWith(nil, strings.NewReader(directiveSrc), &stdout, &stderr, boom)
	if code != 1 {
		t.Errorf("exit %d, want 1; stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr does not carry the panic: %q", stderr.String())
	}
}

// tokens scans src the way the compiler does (comments are whitespace, a newline after a statement
// end is a semicolon) and returns every token with its literal.
func tokens(src []byte) []string {
	fset := token.NewFileSet()
	file := fset.AddFile("t.go", -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		out = append(out, tok.String()+" "+lit)
	}
}

func checkStrip(t *testing.T, src string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "t.go", src, 0); err != nil {
		return // stripDirectives hands back what it cannot parse
	}
	got := stripDirectives([]byte(src))
	if _, err := parser.ParseFile(token.NewFileSet(), "t.go", got, 0); err != nil {
		t.Fatalf("erased output does not parse: %v\nsrc:    %q\nerased: %q", err, src, got)
	}
	want, have := tokens([]byte(src)), tokens(got)
	if strings.Join(want, "|") != strings.Join(have, "|") {
		t.Fatalf("token sequence changed\nsrc:    %q\nerased: %q\nwant %v\ngot  %v", src, got, want, have)
	}
}

func TestStripDirectivesNeverJoinsTokens(t *testing.T) {
	for _, src := range []string{
		"package p\n\nfunc f(x int) int {\n\treturn /*gosree:mut*/x\n}\n",
		"package p\n\nfunc f(x int) int {\n\treturn/*gosree:mut*/x\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\treturn a/*gosree:x*/+b\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\treturn a /*gosree:x*/+ b\n}\n",
		"package p\n\nfunc f(a int) int {\n\ta -/*gosree:x*/-a\n\treturn a\n}\n",
		"package p\n\nfunc f(a int) int {\n\ta +/*gosree:x*/+\n\treturn a\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\t/*gosree:x*/ return a + b\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\treturn a + b /*gosree:x*/\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\treturn a + b //gosree:x\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\treturn a /*gosree:x*//*gosree:y*/b\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\treturn a /*gosree:x\n*/ + b\n}\n",
		"package p\n\nfunc f(a, b int) int {\n\tx := a /*gosree:x\n*/\n\treturn x + b\n}\n",
		"package p\n\nvar _ = /*gosree:x*/1\n",
		"package p/*gosree:x*/\n",
	} {
		checkStrip(t, src)
	}
	// The case the review named, checked on the output text as well.
	if got := string(stripDirectives([]byte("package p\n\nfunc f(x int) int {\n\treturn /*gosree:mut*/x\n}\n"))); !strings.Contains(got, "return x") {
		t.Errorf("return /*gosree:mut*/x became %q", got)
	}
}

var (
	genTemplates = [][]string{
		{"return", "x"},
		{"x", ":=", "a", "+", "b"},
		{"x", "=", "-", "a", "-", "-", "b"},
		{"x", "++"},
		{"x", "=", "a", "*", "&", "b"},
		{"x", "=", "a", "<", "-", "b"},
		{"x", "=", "f", "(", "a", ",", "b", ")"},
		{"for", "i", ":=", "0", ";", "i", "<", "a", ";", "i", "++", "{", "}"},
		{"if", "a", ">", "b", "{", "x", "=", "1", "}"},
	}
	genSeps = []string{
		"", " ", "\t", "/*gosree:x*/", " /*gosree:x*/", "/*gosree:x*/ ", " /*gosree:x*/ ",
		"/*gosree:a*//*gosree:b*/", " /*gosree:a*/ /*gosree:b*/ ", "/*gosree:m\n*/", " /*gosree:m\n*/ ", "/* plain */", " /* plain */",
	}
	genEnds = []string{"", " ", "//gosree:e", " //gosree:e", "/*gosree:e*/", " /*gosree:e*/", "// plain", " /*gosree:e*/ // plain"}
)

func genSource(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("package p\n\nfunc f(a, b int) int {\n\tvar x, i int\n\t_ = i\n")
	for range 1 + rng.IntN(4) {
		b.WriteString("\t")
		if rng.IntN(3) == 0 {
			b.WriteString(genSeps[rng.IntN(len(genSeps))])
		}
		tpl := genTemplates[rng.IntN(len(genTemplates))]
		for i, tok := range tpl {
			if i > 0 {
				b.WriteString(genSeps[rng.IntN(len(genSeps))])
				if tok != tpl[i-1] && rng.IntN(2) == 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), " ") && !strings.HasSuffix(b.String(), "*/") {
					b.WriteString(" ")
				}
			}
			b.WriteString(tok)
		}
		b.WriteString(genEnds[rng.IntN(len(genEnds))])
		b.WriteString("\n")
	}
	b.WriteString("\treturn x\n}\n")
	return b.String()
}

// TestStripDirectivesProperty: for generated sources, directive removal parses and leaves the token
// sequence unchanged.
func TestStripDirectivesProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	checked := 0
	for range 4000 {
		src := genSource(rng)
		if _, err := parser.ParseFile(token.NewFileSet(), "t.go", src, 0); err == nil {
			checked++
		}
		checkStrip(t, src)
	}
	if checked < 500 {
		t.Fatalf("only %d of the generated sources parsed; the generator is too loose to test anything", checked)
	}
}

func FuzzStripDirectives(f *testing.F) {
	rng := rand.New(rand.NewPCG(3, 5))
	for range 40 {
		f.Add(genSource(rng))
	}
	f.Add("package p\n\nfunc f(x int) int {\n\treturn /*gosree:mut*/x\n}\n")
	f.Fuzz(func(t *testing.T, src string) {
		checkStrip(t, src)
	})
}
