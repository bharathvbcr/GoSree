// Command gocap-erase transforms Go source files containing gocap directives,
// marker types, and helper expressions into 100% standard Go code.
//
// Usage:
//
//	gocap-erase [flags] [path ...]
//
// Flags:
//
//	-w        write result to (source) file instead of stdout
//	-o string write result to named file (single input only)
//	-l        list files whose formatting differs from gocap-erase
//	-d        display diffs instead of rewriting files
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

var (
	writeInPlace = flag.Bool("w", false, "write result to (source) file instead of stdout")
	outputFile   = flag.String("o", "", "write result to named file (single input only)")
	listDiffers  = flag.Bool("l", false, "list files whose formatting differs from gocap-erase")
	showDiff     = flag.Bool("d", false, "display diffs instead of rewriting files")
)

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: gocap-erase [flags] [path ...]\n\nFlags:\n")
	flag.PrintDefaults()
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if *writeInPlace && *outputFile != "" {
		fmt.Fprintf(os.Stderr, "error: cannot use both -w and -o\n")
		os.Exit(1)
	}

	args := flag.Args()
	if len(args) == 0 || (len(args) == 1 && args[0] == "-") {
		// Stdin mode
		if *writeInPlace {
			fmt.Fprintf(os.Stderr, "error: cannot use -w with standard input\n")
			os.Exit(1)
		}
		if err := processStream(os.Stdin, os.Stdout, "<stdin>", *outputFile); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *outputFile != "" && len(args) > 1 {
		fmt.Fprintf(os.Stderr, "error: cannot use -o with multiple input files\n")
		os.Exit(1)
	}

	hasError := false
	for _, arg := range args {
		fi, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			hasError = true
			continue
		}

		if fi.IsDir() {
			if *outputFile != "" {
				fmt.Fprintf(os.Stderr, "error: cannot use -o with a directory input\n")
				os.Exit(1)
			}
			err := filepath.WalkDir(arg, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					name := d.Name()
					if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(d.Name(), ".go") {
					return nil
				}
				if err := processFile(path, *writeInPlace, ""); err != nil {
					fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
					hasError = true
				}
				return nil
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "error walking directory %s: %v\n", arg, err)
				hasError = true
			}
		} else {
			if err := processFile(arg, *writeInPlace, *outputFile); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", arg, err)
				hasError = true
			}
		}
	}

	if hasError {
		os.Exit(1)
	}
}

func processStream(r io.Reader, w io.Writer, filename, outPath string) error {
	src, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	res, err := Erase(src)
	if err != nil {
		return err
	}

	differs := !bytes.Equal(src, res)

	if *listDiffers {
		if differs {
			fmt.Println(filename)
		}
		return nil
	}

	if *showDiff {
		if differs {
			fmt.Printf("--- %s\n+++ %s (erased)\n", filename, filename)
			printUnifiedDiff(string(src), string(res))
		}
		return nil
	}

	if outPath != "" {
		return os.WriteFile(outPath, res, 0644)
	}
	_, err = w.Write(res)
	return err
}

func processFile(path string, inPlace bool, outPath string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	res, err := Erase(src)
	if err != nil {
		return err
	}

	differs := !bytes.Equal(src, res)

	if *listDiffers {
		if differs {
			fmt.Println(path)
		}
		return nil
	}

	if *showDiff {
		if differs {
			fmt.Printf("--- %s\n+++ %s (erased)\n", path, path)
			printUnifiedDiff(string(src), string(res))
		}
		return nil
	}

	if inPlace {
		if differs {
			return os.WriteFile(path, res, 0644)
		}
		return nil
	}

	if outPath != "" {
		return os.WriteFile(outPath, res, 0644)
	}

	_, err = os.Stdout.Write(res)
	return err
}

type span struct {
	start int
	end   int
}

// isDirective reports whether text is a gocap or legacy safe directive comment.
func isDirective(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "//gocap:") || strings.HasPrefix(t, "//safe:") ||
		strings.HasPrefix(t, "/*gocap:") || strings.HasPrefix(t, "/*safe:")
}

// stripDirectives removes all //gocap: and //safe: directive comments cleanly
// using exact AST token byte offsets without corrupting doc comments.
func stripDirectives(src []byte) []byte {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", src, parser.ParseComments)
	if err != nil {
		return src
	}

	var spans []span
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if isDirective(c.Text) {
				start := fset.Position(c.Pos()).Offset
				end := fset.Position(c.End()).Offset

				isBlock := strings.HasPrefix(strings.TrimSpace(c.Text), "/*")
				lineStart := start
				for lineStart > 0 && src[lineStart-1] != '\n' {
					lineStart--
				}
				isLeadingWhitespaceOnly := true
				for i := lineStart; i < start; i++ {
					if src[i] != ' ' && src[i] != '\t' {
						isLeadingWhitespaceOnly = false
						break
					}
				}

				lineEnd := end
				for lineEnd < len(src) && (src[lineEnd] == ' ' || src[lineEnd] == '\t') {
					lineEnd++
				}
				isTrailingNewline := false
				if lineEnd < len(src) && src[lineEnd] == '\r' {
					lineEnd++
				}
				if lineEnd < len(src) && src[lineEnd] == '\n' {
					lineEnd++
					isTrailingNewline = true
				} else if lineEnd == len(src) {
					isTrailingNewline = true
				}

				if isLeadingWhitespaceOnly && isTrailingNewline {
					start = lineStart
					end = lineEnd
				} else if isBlock {
					if start > 0 && (src[start-1] == ' ' || src[start-1] == '\t') {
						start--
					}
				} else {
					// Inline line comment
					for start > lineStart && (src[start-1] == ' ' || src[start-1] == '\t') {
						start--
					}
				}
				spans = append(spans, span{start, end})
			}
		}
	}

	if len(spans) == 0 {
		return src
	}

	// Sort ascending and merge any overlapping or contiguous spans
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})
	var merged []span
	for _, s := range spans {
		if len(merged) == 0 {
			merged = append(merged, s)
			continue
		}
		last := &merged[len(merged)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
		} else {
			merged = append(merged, s)
		}
	}

	res := make([]byte, len(src))
	copy(res, src)
	// Delete backwards to keep preceding offsets valid
	for i := len(merged) - 1; i >= 0; i-- {
		s := merged[i]
		if s.start <= len(res) && s.end <= len(res) && s.start <= s.end {
			res = append(res[:s.start], res[s.end:]...)
		}
	}
	return res
}

// Erase transforms gocap-annotated Go source into standard Go source.
func Erase(src []byte) ([]byte, error) {
	// Stage 1: Strip directive comments cleanly using exact AST offsets
	cleanSrc := stripDirectives(src)

	// Stage 2: Parse cleaned AST
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "source.go", cleanSrc, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing source: %w", err)
	}

	// Identify imports of gocap/cap and gocap
	type gocapImport struct {
		path            string
		localName       string
		isDot           bool
		hasExplicitName bool
	}
	var imports []gocapImport
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		if path == "github.com/bharathvbcr/gocap/cap" || path == "github.com/bharathvbcr/gocap" {
			local := ""
			isDot := false
			hasExplicit := false
			if imp.Name != nil {
				hasExplicit = true
				if imp.Name.Name == "." {
					isDot = true
				} else if imp.Name.Name != "_" {
					local = imp.Name.Name
				}
			} else {
				if path == "github.com/bharathvbcr/gocap/cap" {
					local = "cap"
				} else {
					local = "gocap"
				}
			}
			imports = append(imports, gocapImport{
				path:            path,
				localName:       local,
				isDot:           isDot,
				hasExplicitName: hasExplicit,
			})
		}
	}

	// If no gocap imports exist, format and return
	if len(imports) == 0 {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, f); err != nil {
			return nil, fmt.Errorf("formatting source: %w", err)
		}
		return format.Source(buf.Bytes())
	}

	isGocapSelector := func(sel *ast.SelectorExpr, names ...string) (bool, string) {
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return false, ""
		}
		for _, imp := range imports {
			if !imp.isDot && imp.localName != "" && id.Name == imp.localName {
				for _, name := range names {
					if sel.Sel.Name == name {
						return true, name
					}
				}
			}
		}
		return false, ""
	}

	isGocapIdent := func(id *ast.Ident, names ...string) bool {
		for _, imp := range imports {
			if imp.isDot {
				for _, name := range names {
					if id.Name == name {
						return true
					}
				}
			}
		}
		return false
	}

	// Stage 3: AST rewriting with astutil.Apply (post-order bottom-up)
	f = astutil.Apply(f, nil, func(c *astutil.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.IndexExpr:
			// cap.Mut[T], cap.Ref[T], cap.Iso[T], cap.Imm[T], cap.Mub[T], cap.Rob[T]
			if sel, ok := n.X.(*ast.SelectorExpr); ok {
				if matched, _ := isGocapSelector(sel, "Mut", "Mub", "Ref", "Rob", "Iso", "Imm"); matched {
					c.Replace(&ast.StarExpr{Star: n.Pos(), X: n.Index})
				}
			} else if id, ok := n.X.(*ast.Ident); ok {
				if isGocapIdent(id, "Mut", "Mub", "Ref", "Rob", "Iso", "Imm") {
					c.Replace(&ast.StarExpr{Star: n.Pos(), X: n.Index})
				}
			}

		case *ast.CallExpr:
			// cap.BorrowMut(x), cap.Borrow(x), cap.BorrowRef(x), cap.BorrowMub(x), cap.BorrowRob(x), cap.Move(x)
			fnExpr := n.Fun
			if idx, ok := fnExpr.(*ast.IndexExpr); ok {
				fnExpr = idx.X
			} else if idxList, ok := fnExpr.(*ast.IndexListExpr); ok {
				fnExpr = idxList.X
			}

			if sel, ok := fnExpr.(*ast.SelectorExpr); ok {
				if matched, _ := isGocapSelector(sel, "BorrowMut", "Borrow", "BorrowRef", "BorrowMub", "BorrowRob", "Move"); matched {
					if len(n.Args) == 1 {
						c.Replace(n.Args[0])
					}
				}
			} else if id, ok := fnExpr.(*ast.Ident); ok {
				if isGocapIdent(id, "BorrowMut", "Borrow", "BorrowRef", "BorrowMub", "BorrowRob", "Move") {
					if len(n.Args) == 1 {
						c.Replace(n.Args[0])
					}
				}
			}
		}
		return true
	}).(*ast.File)

	// Stage 4: Import cleanup
	for _, imp := range imports {
		if imp.isDot {
			astutil.DeleteNamedImport(fset, f, ".", imp.path)
			continue
		}
		if imp.localName == "" {
			continue
		}
		stillReferenced := false
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == imp.localName {
					stillReferenced = true
					return false
				}
			}
			return true
		})
		if !stillReferenced {
			if imp.hasExplicitName {
				astutil.DeleteNamedImport(fset, f, imp.localName, imp.path)
			} else {
				astutil.DeleteImport(fset, f, imp.path)
			}
		}
	}

	// Stage 5: Format AST
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, fmt.Errorf("formatting node: %w", err)
	}

	return format.Source(buf.Bytes())
}

func printUnifiedDiff(orig, newStr string) {
	origLines := strings.Split(orig, "\n")
	newLines := strings.Split(newStr, "\n")
	m, n := len(origLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			if origLines[i] == newLines[j] {
				dp[i+1][j+1] = dp[i][j] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i+1][j+1] = dp[i+1][j]
			} else {
				dp[i+1][j+1] = dp[i][j+1]
			}
		}
	}

	type diffLine struct {
		op   byte
		text string
	}
	var diff []diffLine
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && origLines[i-1] == newLines[j-1] {
			diff = append(diff, diffLine{' ', origLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			diff = append(diff, diffLine{'+', newLines[j-1]})
			j--
		} else if i > 0 && (j == 0 || dp[i][j-1] < dp[i-1][j]) {
			diff = append(diff, diffLine{'-', origLines[i-1]})
			i--
		}
	}
	for k := len(diff) - 1; k >= 0; k-- {
		d := diff[k]
		if d.op != ' ' {
			fmt.Printf("%c%s\n", d.op, d.text)
		}
	}
}
