// Command gosree-erase transforms Go source files containing gosree directives,
// marker types, and helper expressions into 100% standard Go code.
//
// Usage:
//
//	gosree-erase [flags] [path ...]
//
// Flags:
//
//	-w        write result to (source) file instead of stdout
//	-o string write result to named file (single input only)
//	-l        list files whose formatting differs from gosree-erase
//	-d        display diffs instead of rewriting files
//
// A path is a Go file, a directory (walked recursively), or dir/... (the same
// walk). A walk skips hidden directories, vendor, testdata and symbolic links
// below the directory it was given. With -w the invocation is all-or-nothing:
// every file is erased in memory first and nothing is written unless all of
// them succeeded.
//
// Exit status: 0 when everything requested was done (a file that needs no
// erasure counts as done); 1 when any file could not be erased or written, a
// path could not be used, or the options are inconsistent; 2 for invalid flags.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// eraseFunc is the signature of Erase; tests substitute their own.
type eraseFunc func(src []byte) ([]byte, error)

type options struct {
	write   bool
	list    bool
	diff    bool
	verify  bool
	outPath string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	erase   eraseFunc
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWith(args, stdin, stdout, stderr, Erase)
}

func runWith(args []string, stdin io.Reader, stdout, stderr io.Writer, erase eraseFunc) int {
	o := &options{stdin: stdin, stdout: stdout, stderr: stderr, erase: erase}
	flags := flag.NewFlagSet("gosree-erase", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&o.write, "w", false, "write result to (source) file instead of stdout")
	flags.StringVar(&o.outPath, "o", "", "write result to named file (single input only)")
	flags.BoolVar(&o.list, "l", false, "list files whose formatting differs from gosree-erase")
	flags.BoolVar(&o.diff, "d", false, "display diffs instead of rewriting files")
	flags.BoolVar(&o.verify, "verify", false, "verify exported API type equivalence and compiled .text instruction equivalence without executing target code")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: gosree-erase [flags] [path ...]\n\nFlags:\n")
		flags.PrintDefaults()
		fmt.Fprintf(stderr, `
Paths: a Go file, a directory (walked recursively) or dir/... (the same walk).
A walk skips hidden directories, vendor, testdata and symbolic links (it says
so on stderr). A symbolic link named on the command line is followed only if
it resolves inside the current directory. A file that needs no erasure is left
untouched and is not listed.

With -w the run is all-or-nothing: nothing is written unless every file was
erased successfully. Each file is replaced atomically (temp file and rename).

Exit status: 0 everything requested was done; 1 a file could not be erased or
written, a path could not be used, or the options conflict (with -w, nothing
was written); 2 invalid flags.
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if o.write && o.outPath != "" {
		o.errorf("error: cannot use both -w and -o")
		return 1
	}

	rest := flags.Args()
	if len(rest) == 0 || (len(rest) == 1 && rest[0] == "-") {
		if o.write {
			o.errorf("error: cannot use -w with standard input")
			return 1
		}
		if err := o.processStream(); err != nil {
			o.errorf("error: %v", err)
			return 1
		}
		return 0
	}

	if o.outPath != "" && len(rest) > 1 {
		o.errorf("error: cannot use -o with multiple input files")
		return 1
	}

	targets, ok := o.collect(rest)
	if o.verify {
		if err := o.verifyTargets(targets); err != nil {
			o.errorf("gosree-erase: equivalence verification failed: %v", err)
			return 1
		}
	}
	if o.write && !o.list && !o.diff {
		return o.writeAll(targets, ok)
	}
	for _, t := range targets {
		r := o.compute(t)
		if r.err != nil {
			o.errorf("%s: %v", t.display, r.err)
			ok = false
			continue
		}
		if err := o.emit(t.display, r.src, r.out); err != nil {
			o.errorf("%s: %v", t.display, err)
			ok = false
		}
	}
	if !ok {
		return 1
	}
	return 0
}

func (o *options) errorf(format string, args ...any) {
	fmt.Fprintf(o.stderr, format+"\n", args...)
}

// target is a file to process: display is the name shown to the user, real is
// the path that is read and written.
type target struct {
	display string
	real    string
}

// result is the outcome of erasing one file in memory.
type result struct {
	target
	src, out []byte
	mode     fs.FileMode
	err      error
}

func (r result) changed() bool { return r.err == nil && !bytes.Equal(r.src, r.out) }

// collect expands the path arguments into the Go files to process. ok is
// false if any argument could not be used.
func (o *options) collect(args []string) (targets []target, ok bool) {
	ok = true
	seen := map[string]bool{}
	add := func(t target) {
		key := t.real
		if abs, err := filepath.Abs(t.real); err == nil {
			key = abs
		}
		if !seen[key] {
			seen[key] = true
			targets = append(targets, t)
		}
	}

	for _, arg := range args {
		root, pattern := arg, false
		if s := filepath.ToSlash(arg); s == "..." || strings.HasSuffix(s, "/...") {
			pattern = true
			switch base := strings.TrimSuffix(s, "..."); base {
			case "":
				root = "."
			case "/":
				root = "/"
			default:
				root = filepath.FromSlash(strings.TrimSuffix(base, "/"))
			}
		}

		fi, err := os.Lstat(root)
		if err != nil {
			o.errorf("error: %v", err)
			ok = false
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			resolved, err := resolveInsideWorkingDir(root)
			if err != nil {
				o.errorf("error: %s: %v", arg, err)
				ok = false
				continue
			}
			if root, err = filepath.Abs(resolved); err != nil {
				o.errorf("error: %s: %v", arg, err)
				ok = false
				continue
			}
			if fi, err = os.Stat(root); err != nil {
				o.errorf("error: %v", err)
				ok = false
				continue
			}
		}

		switch {
		case fi.IsDir():
			if o.outPath != "" {
				o.errorf("error: cannot use -o with a directory input")
				ok = false
				continue
			}
			if err := o.walk(root, add); err != nil {
				o.errorf("error walking directory %s: %v", arg, err)
				ok = false
			}
		case pattern:
			o.errorf("error: %s: %s is not a directory", arg, root)
			ok = false
		case fi.Mode().IsRegular():
			add(target{display: arg, real: root})
		default:
			o.errorf("error: %s: not a regular file", arg)
			ok = false
		}
	}
	return targets, ok
}

// walk adds the Go files below root. The root the user named is always
// walked, whatever its name; below it, hidden directories, vendor and testdata
// are skipped, and symbolic links are never followed.
func (o *options) walk(root string, add func(target)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			o.errorf("gosree-erase: skipping symbolic link %s", path)
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") && d.Type().IsRegular() {
			add(target{display: path, real: path})
		}
		return nil
	})
}

// resolveInsideWorkingDir resolves a symbolic link named on the command line
// and refuses a target outside the working directory.
func resolveInsideWorkingDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if cwd, err = filepath.EvalSymlinks(cwd); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(cwd, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("symbolic link resolves to %s, outside the working directory; name the target itself to process it", target)
	}
	return target, nil
}

// compute erases one file in memory. A panic while erasing becomes an error
// for that file.
func (o *options) compute(t target) (r result) {
	r.target = t
	defer func() {
		if p := recover(); p != nil {
			r.out, r.err = nil, fmt.Errorf("internal error while erasing (nothing was written): %v", p)
		}
	}()
	fi, err := os.Stat(t.real)
	if err != nil {
		r.err = fmt.Errorf("reading file: %w", err)
		return r
	}
	r.mode = fi.Mode()
	if r.src, err = os.ReadFile(t.real); err != nil {
		r.err = fmt.Errorf("reading file: %w", err)
		return r
	}
	r.out, r.err = o.erase(r.src)
	return r
}

// emit delivers the result for the modes that do not rewrite the input file.
func (o *options) emit(name string, src, out []byte) error {
	differs := !bytes.Equal(src, out)
	switch {
	case o.list:
		if differs {
			fmt.Fprintln(o.stdout, name)
		}
		return nil
	case o.diff:
		if differs {
			fmt.Fprintf(o.stdout, "--- %s\n+++ %s (erased)\n", name, name)
			writeUnifiedDiff(o.stdout, string(src), string(out))
		}
		return nil
	case o.outPath != "":
		return os.WriteFile(o.outPath, out, 0o644)
	}
	_, err := o.stdout.Write(out)
	return err
}

func (o *options) processStream() error {
	src, err := io.ReadAll(o.stdin)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	out, err := o.erase(src)
	if err != nil {
		return err
	}
	if o.verify {
		if _, verr := VerifyCompiledTextEquivalence(map[string][]byte{"stdin.go": src}, o.erase); verr != nil {
			return fmt.Errorf("equivalence verification failed: %w", verr)
		}
	}
	return o.emit("<stdin>", src, out)
}

func (o *options) verifyTargets(targets []target) error {
	byDir := map[string]map[string][]byte{}
	var dirs []string
	for _, t := range targets {
		base := filepath.Base(t.real)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		src, err := os.ReadFile(t.real)
		if err != nil {
			return fmt.Errorf("%s: %w", t.display, err)
		}
		dir := filepath.Dir(t.real)
		if byDir[dir] == nil {
			byDir[dir] = map[string][]byte{}
			dirs = append(dirs, dir)
		}
		byDir[dir][base] = src
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		files := byDir[dir]
		// Include any unselected sibling regular non-test .go files in the same
		// package directory so `gosree-erase -verify pkg/one.go` resolves package-local
		// declarations across sibling files of `pkg`.
		if entries, err := os.ReadDir(dir); err == nil {
			for _, ent := range entries {
				name := ent.Name()
				if !ent.Type().IsRegular() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				if _, exists := files[name]; exists {
					continue
				}
				if sibSrc, rerr := os.ReadFile(filepath.Join(dir, name)); rerr == nil {
					files[name] = sibSrc
				}
			}
		}
		if _, err := VerifyDirectoryEquivalence(dir, files, o.erase); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
	}
	return nil
}

// processFile erases one file, delivering the result as the options would for
// a single path: rewritten in place, written to outPath, or printed.
func processFile(path string, inPlace bool, outPath string) error {
	o := &options{write: inPlace, outPath: outPath, stdout: os.Stdout, stderr: os.Stderr, erase: Erase}
	r := o.compute(target{display: path, real: path})
	if r.err != nil {
		return r.err
	}
	if inPlace {
		if !r.changed() {
			return nil
		}
		return o.commit([]result{r})
	}
	return o.emit(path, r.src, r.out)
}

// writeAll implements -w: erase everything in memory, then write all changed
// files, or none if anything failed.
func (o *options) writeAll(targets []target, ok bool) int {
	results := make([]result, len(targets))
	failures := 0
	for i, t := range targets {
		r := o.compute(t)
		if r.changed() {
			r.err = writable(r.real)
		}
		results[i] = r
		if r.err != nil {
			o.errorf("%s: %v", t.display, r.err)
			failures++
		}
	}
	if failures > 0 || !ok {
		o.errorf("gosree-erase: nothing was written (%d file(s) could not be erased, %d path(s) were unusable)", failures, boolInt(!ok))
		return 1
	}
	var changed []result
	for _, r := range results {
		if r.changed() {
			changed = append(changed, r)
		}
	}
	if err := o.commit(changed); err != nil {
		o.errorf("gosree-erase: %v", err)
		return 1
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// writable reports whether path can be replaced: it can be opened for writing
// and a temporary file can be created beside it.
func writable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("not writable: %w", err)
	}
	f.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gosree-erase-probe-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot create a temporary file beside it: %w", err)
	}
	tmp.Close()
	return os.Remove(tmp.Name())
}

// commit replaces each file with its erased content. Files are verified
// unchanged since they were read, written one at a time with an atomic rename,
// and the files already written are restored if a later write fails.
func (o *options) commit(changed []result) error {
	for _, r := range changed {
		cur, err := os.ReadFile(r.real)
		if err != nil {
			return fmt.Errorf("%s: %w", r.display, err)
		}
		if !bytes.Equal(cur, r.src) {
			return fmt.Errorf("%s: changed while it was being erased; nothing was written", r.display)
		}
	}
	for i, r := range changed {
		if err := writeAtomic(r.real, r.out, r.mode); err != nil {
			msg := fmt.Sprintf("%s: %v", r.display, err)
			for _, w := range changed[:i] {
				if rerr := writeAtomic(w.real, w.src, w.mode); rerr != nil {
					msg += fmt.Sprintf("; could not restore %s: %v", w.display, rerr)
				}
			}
			return errors.New(msg)
		}
	}
	return nil
}

// writeAtomic replaces path with data through a temporary file in the same
// directory, keeping the permission bits, so a crash cannot leave a truncated
// source file.
func writeAtomic(path string, data []byte, mode fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gosree-erase-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(mode.Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type span struct {
	start int
	end   int
}

// isDirective reports whether text is a //gosree: or /*gosree: directive comment.
func isDirective(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasPrefix(t, "//gosree:") || strings.HasPrefix(t, "/*gosree:")
}

// stripDirectives removes all //gosree: and /*gosree: directive comments cleanly
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

func printUnifiedDiff(orig, newStr string) { writeUnifiedDiff(os.Stdout, orig, newStr) }

func writeUnifiedDiff(w io.Writer, orig, newStr string) {
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
			fmt.Fprintf(w, "%c%s\n", d.op, d.text)
		}
	}
}
