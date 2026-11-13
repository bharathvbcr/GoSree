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
// When the archive held symlink or hard-link entries, which are skipped, one line on diag says how many.
func archiveRev(repo, rev, subdir, dst string, diag io.Writer) (string, error) {
	if diag == nil {
		return "", errors.New("archiveRev: no diagnostic writer: the count of skipped links would go nowhere")
	}
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
	res, xerr := extractTar(pipe, prefix, dst)
	if _, err := io.Copy(io.Discard, pipe); err != nil && xerr == nil {
		xerr = err
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("git archive %s %s: %w: %s", sha, prefix, err, stderr.String())
	}
	if xerr != nil {
		return "", xerr
	}
	if res.files == 0 {
		if res.rootLink {
			return "", fmt.Errorf("git archive %s %s: no files: the subdirectory is itself a symlink or hard link, which is skipped", sha, prefix)
		}
		return "", fmt.Errorf("git archive %s %s: no files", sha, prefix)
	}
	if res.links > 0 {
		if _, err := fmt.Fprintf(diag, "gosree-corpus: skipped %d symlink or hard-link entries in the archive of %s %s; none was created\n", res.links, sha, prefix); err != nil {
			return "", err
		}
	}
	return sha, nil
}

// extraction is what extractTar did: the regular files it wrote and the link entries it skipped.
type extraction struct {
	files, links int
	// rootLink is set when the subdirectory itself was a link entry, so nothing beneath it was kept.
	rootLink bool
}

// maxEntryName bounds an entry's cleaned name, relative to the prefix, in bytes: PATH_MAX on Linux
// (macOS is 1024). A PAX record can name 1 MiB, and the skipped-link lookup costs one hash per path
// component over the name, so the bound keeps that work per entry fixed. No git tree comes near it.
const maxEntryName = 4096

// maxEntryDepth bounds an entry's path components. os.Root resolves a path one component at a time
// from the root on every MkdirAll and OpenFile, so an entry costs work quadratic in its depth: a
// single 2048-component name (within maxEntryName) took 34 s to extract here, and that cost predates
// the link handling. 128 is several times the depth of any real source tree and caps that cost at
// about 1/250 of the 2048-level case.
const maxEntryDepth = 128

// extractTar writes the entries under prefix to dst, with prefix stripped, and counts the files.
// Every write goes through an os.Root on dst. Symlink and hard-link entries are never created: they
// are skipped and counted, because a link in an archived tree is either irrelevant to the analysis or
// a way to reach outside dst. Nothing may then be written at or beneath the path of a skipped link, so
// an entry there fails the extraction; a real git tree cannot hold a path that is both a link and a
// directory, and extracting such an entry would silently build a tree the commit does not have. The
// set of skipped paths grows with the archive's own link entries, which the archive already bounds.
func extractTar(r io.Reader, prefix, dst string) (extraction, error) {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return extraction{}, err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	var res extraction
	skipped := map[string]struct{}{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return res, nil
		}
		if err != nil {
			return res, err
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
			return res, fmt.Errorf("archive entry %q is outside %q", h.Name, prefix)
		}
		clean := path.Clean(rel) // "." for the prefix directory itself, spelled "" or "." or "l/.."
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return res, fmt.Errorf("archive entry %q escapes the destination", h.Name)
		}
		if len(clean) > maxEntryName {
			return res, fmt.Errorf("archive entry %.64q... names a path of %d bytes, over the %d-byte bound", h.Name, len(clean), maxEntryName)
		}
		if depth := strings.Count(clean, "/") + 1; depth > maxEntryDepth {
			return res, fmt.Errorf("archive entry %.64q... is %d components deep, over the %d-component bound", h.Name, depth, maxEntryDepth)
		}
		if link, ok := skippedAncestor(skipped, clean); ok {
			return res, fmt.Errorf("archive entry %q is at or beneath the skipped link %q", h.Name, link)
		}
		if clean == "." {
			if h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink {
				skipped[clean] = struct{}{} // the whole subdirectory is a link: every later entry is refused
				res.links++
				res.rootLink = true
			}
			continue
		}
		target := filepath.FromSlash(clean)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, 0o755); err != nil {
				return res, err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return res, err
			}
			f, err := root.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return res, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				return res, errors.Join(err, f.Close())
			}
			if err := f.Close(); err != nil {
				return res, err
			}
			res.files++
		case tar.TypeSymlink, tar.TypeLink:
			skipped[clean] = struct{}{}
			res.links++
		default:
			return res, fmt.Errorf("archive entry %q has unsupported type %q", h.Name, h.Typeflag)
		}
	}
}

// skippedAncestor returns the skipped link that clean is, or lies beneath, if any. A skipped link
// at the root (".") covers every entry. Otherwise it looks up each '/'-delimited prefix of clean,
// then clean itself; clean is already cleaned and no longer than maxEntryName.
func skippedAncestor(skipped map[string]struct{}, clean string) (string, bool) {
	if len(skipped) == 0 {
		return "", false
	}
	if _, ok := skipped["."]; ok {
		return ".", true
	}
	for i := 0; i < len(clean); i++ {
		if clean[i] != '/' {
			continue
		}
		if _, ok := skipped[clean[:i]]; ok {
			return clean[:i], true
		}
	}
	if _, ok := skipped[clean]; ok {
		return clean, true
	}
	return "", false
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
		if a.file == "" || a.scope == "" || !strings.HasPrefix(a.directive, "//gosree:") || a.verdict == "" {
			return tab, fmt.Errorf("%s:%d: file, scope, a //gosree: directive and a verdict are required", file, n)
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
		verb, args, _ := strings.Cut(strings.TrimPrefix(a.directive, "//gosree:"), " ")
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

// listFindings returns every finding of the root actions (findingDiagnostics, the same set the report
// counts), positioned relative to dir.
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
		for _, d := range findingDiagnostics(act) {
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
