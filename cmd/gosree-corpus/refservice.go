package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// A reference service is measured on an archived commit, never on a checkout: a checkout moves, and
// with -tests a single changed test file changes the denominator. The archive reads the repository's
// object store only, so the repository itself is left untouched.

// archiveRev extracts subdir of repo at rev into dst (replacing it) and returns the commit's full SHA.
func archiveRev(repo, rev, subdir, dst string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", rev+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s in %s: %w", rev, repo, err)
	}
	sha := strings.TrimSpace(string(out))
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	prefix := strings.Trim(path.Clean(filepath.ToSlash(subdir)), "/") + "/"
	cmd := exec.Command("git", "-C", repo, "archive", "--format=tar", sha, "--", prefix)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	n, xerr := extractTar(pipe, prefix, dst)
	if _, err := io.Copy(io.Discard, pipe); err != nil && xerr == nil {
		xerr = err
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("git archive %s %s: %w: %s", sha, prefix, err, stderr.String())
	}
	if xerr != nil {
		return "", xerr
	}
	if n == 0 {
		return "", fmt.Errorf("git archive %s %s: no files", sha, prefix)
	}
	return sha, nil
}

// extractTar writes the entries under prefix to dst, with prefix stripped, and counts the files.
func extractTar(r io.Reader, prefix, dst string) (int, error) {
	tr := tar.NewReader(r)
	files := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return files, err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue // git's commit-id comment
		}
		rel, ok := strings.CutPrefix(h.Name, prefix)
		if !ok {
			// git lists the directories leading to the prefix ("backend/") before it.
			if h.Typeflag == tar.TypeDir && strings.HasPrefix(prefix, strings.TrimSuffix(h.Name, "/")+"/") {
				continue
			}
			return files, fmt.Errorf("archive entry %q is outside %q", h.Name, prefix)
		}
		if rel == "" {
			continue
		}
		clean := path.Clean(rel)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return files, fmt.Errorf("archive entry %q escapes the destination", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return files, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return files, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				return files, errors.Join(err, f.Close())
			}
			if err := f.Close(); err != nil {
				return files, err
			}
			files++
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return files, err
			}
		default:
			return files, fmt.Errorf("archive entry %q has unsupported type %q", h.Name, h.Typeflag)
		}
	}
}

// tableColumns is the header row of an annotation table.
var tableColumns = []string{"file", "scope", "line", "occurrence", "mode", "directive", "category", "verdict", "class", "note"}

// annotationTable is a reference service's triage ledger: one row per directive, with the finding it
// answers and the verdict behind it. Lines starting with # are comments, except "#rev <sha>", which
// names the commit the anchors were written against.
type annotationTable struct {
	rev  string
	rows []annotation
}

// loadAnnotationTable parses a tab-separated annotation table. In the scope, line and directive
// columns, \t stands for a tab (Go source is indented with tabs) and \\ for a backslash.
func loadAnnotationTable(file string) (annotationTable, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return annotationTable{}, err
	}
	var tab annotationTable
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	header := false
	for n := 1; sc.Scan(); n++ {
		l := sc.Text()
		if strings.TrimSpace(l) == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(l, "#rev "); ok {
			tab.rev = strings.TrimSpace(rest)
			continue
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		cols := strings.Split(l, "\t")
		if !header {
			if strings.Join(cols, "\t") != strings.Join(tableColumns, "\t") {
				return tab, fmt.Errorf("%s:%d: header must be %q", file, n, strings.Join(tableColumns, " "))
			}
			header = true
			continue
		}
		if len(cols) != len(tableColumns) {
			return tab, fmt.Errorf("%s:%d: %d columns, want %d", file, n, len(cols), len(tableColumns))
		}
		a := annotation{
			file: cols[0], scope: unescape(cols[1]), line: unescape(cols[2]), directive: unescape(cols[5]),
			category: cols[6], verdict: cols[7], class: cols[8], note: cols[9],
		}
		if cols[3] != "" {
			if a.occurrence, err = strconv.Atoi(cols[3]); err != nil || a.occurrence < 1 {
				return tab, fmt.Errorf("%s:%d: occurrence %q is not a positive number", file, n, cols[3])
			}
		}
		switch cols[4] {
		case "before":
			a.before = true
		case "trail":
		default:
			return tab, fmt.Errorf("%s:%d: mode %q, want before or trail", file, n, cols[4])
		}
		if a.file == "" || a.scope == "" || !strings.HasPrefix(a.directive, "//safe:") || a.verdict == "" {
			return tab, fmt.Errorf("%s:%d: file, scope, a //safe: directive and a verdict are required", file, n)
		}
		tab.rows = append(tab.rows, a)
	}
	if err := sc.Err(); err != nil {
		return tab, err
	}
	if !header {
		return tab, fmt.Errorf("%s: no header row", file)
	}
	return tab, nil
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// directiveCounts counts the rows of each directive verb (unsafe, guarded_by, holds, iso, imm), with
// one per name a guarded_by, holds or iso row lists.
func directiveCounts(rows []annotation) map[string]int {
	m := map[string]int{}
	for _, a := range rows {
		verb, args, _ := strings.Cut(strings.TrimPrefix(a.directive, "//safe:"), " ")
		switch verb {
		case "holds", "iso":
			m[verb] += len(strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' }))
		default:
			m[verb]++
		}
	}
	return m
}

// Finding is one diagnostic in the -list output: enough to diff two runs by package, category and
// enclosing function, since inserted directive lines shift line numbers.
type Finding struct {
	Package  string `json:"package"`
	Pos      string `json:"pos"`
	Func     string `json:"func"`
	Category string `json:"category"`
	Message  string `json:"message"`
	// Transfer is where the transfer that caused the finding is, and TransferFunc the function
	// holding it: the finding's own position for SAFE002 and SAFE004, the related position gosree
	// gives for a finding away from its transfer, and empty for findings no transfer causes.
	Transfer     string `json:"transfer,omitempty"`
	TransferFunc string `json:"transfer_func,omitempty"`
	// Related lists the finding's other related positions (the writes behind a SAFE006).
	Related []string `json:"related,omitempty"`
}

// listFindings returns every diagnostic of the root actions, positioned relative to dir.
func listFindings(acts []*checker.Action, dir string) []Finding {
	var out []Finding
	for _, act := range acts {
		where := func(pos token.Pos) string {
			p := act.Package.Fset.Position(pos)
			file := p.Filename
			if rel, err := filepath.Rel(dir, file); err == nil {
				file = filepath.ToSlash(rel)
			}
			return fmt.Sprintf("%s:%d:%d", file, p.Line, p.Column)
		}
		for _, d := range act.Diagnostics {
			f := Finding{
				Package:  act.Package.ID,
				Pos:      where(d.Pos),
				Func:     enclosingFunc(act.Package.Syntax, d.Pos),
				Category: d.Category,
				Message:  d.Message,
			}
			t := token.NoPos
			if d.Category == ownership.NotOwned || d.Category == ownership.Escapes {
				t = d.Pos
			}
			for _, r := range d.Related {
				if r.Message == ownership.TransferRelated {
					t = r.Pos
				} else {
					f.Related = append(f.Related, where(r.Pos))
				}
			}
			if t.IsValid() {
				f.Transfer, f.TransferFunc = where(t), enclosingFunc(act.Package.Syntax, t)
			}
			out = append(out, f)
		}
	}
	return out
}

// enclosingFunc names the top-level function or method containing pos ("F", "T.M", "(*T).M"), or ""
// outside any function.
func enclosingFunc(files []*ast.File, pos token.Pos) string {
	for _, f := range files {
		if pos < f.Pos() || pos >= f.End() {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || pos < fd.Pos() || pos >= fd.End() {
				continue
			}
			if fd.Recv == nil || len(fd.Recv.List) == 0 {
				return fd.Name.Name
			}
			return recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
		}
	}
	return ""
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "(*" + recvName(t.X) + ")"
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
