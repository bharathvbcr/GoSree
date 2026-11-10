package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// annotation is one //safe directive added to a copy of someone else's code for a measured run: the
// comment a programmer would write who believes what the directive claims. Only comments are added;
// no code changes. It is placed relative to an anchor: the line equal to line (or, when line is empty,
// the scope line itself) inside the region that starts at the line beginning with scope (a func line,
// a type or package variable's declaration) and ends at the next top-level declaration.
type annotation struct {
	file, scope, line, directive string
	// test names the race-corpus test the annotation is for (corpus table only).
	test string
	// occurrence picks the nth matching line in the region (1-based). Zero means the line must match
	// exactly once.
	occurrence int
	// before inserts the directive on its own line above the anchor, with the anchor's indentation, so
	// it attaches to the statement, field or declaration that follows. Otherwise it is appended as a
	// trailing comment, which only works on a line that has no comment yet.
	before bool
	// category, verdict, class and note are the triage ledger for a reference-service table: the
	// finding the directive answers and why. The run does not read them.
	category, verdict, class, note string
}

// corpusAnnotations covers every test whose sharing is a lock discipline: a variable written or read
// only while one mutex is held, by the goroutines and by the test itself. Race tests of the same shape
// are annotated the same way, so the run also shows that an annotation does not hide a race.
//
// TestNoRaceRWMutex is left out: x and y are declared together (`var x, y int64 = 0, 1`), y is written
// under RLock, and a trailing directive on that line would guard y too. Guarding x alone would take a
// code change.
var corpusAnnotations = []annotation{
	// Lock discipline, no race.
	{file: "mutex_test.go", test: "TestNoRaceMutex", scope: "func TestNoRaceMutex(", line: "\tvar x int16 = 0", directive: "//gosree:guarded_by mu"},
	{file: "rwmutex_test.go", test: "TestNoRaceRWMutexMultipleReaders", scope: "func TestNoRaceRWMutexMultipleReaders(", line: "\tx := int64(0)", directive: "//gosree:guarded_by mu"},
	{file: "rwmutex_test.go", test: "TestNoRaceRWMutexTransitive", scope: "func TestNoRaceRWMutexTransitive(", line: "\tx := int64(0)", directive: "//gosree:guarded_by mu"},
	{file: "mop_test.go", test: "TestNoRaceFuncUnlock", scope: "func TestNoRaceFuncUnlock(", line: "\tx := 0", directive: "//gosree:guarded_by mu"},
	{file: "mop_test.go", test: "TestNoRaceAsFunc4", scope: "func TestNoRaceAsFunc4(", line: "\tx := 0", directive: "//gosree:guarded_by mu"},
	{file: "finalizer_test.go", test: "TestNoRaceFinGlobal", scope: "var finVar struct {", line: "\tcnt int", directive: "//gosree:guarded_by Mutex"},
	// The same shapes with a race.
	{file: "mutex_test.go", test: "TestRaceMutex", scope: "func TestRaceMutex(", line: "\tvar x int16 = 0", directive: "//gosree:guarded_by mu"},
	{file: "mutex_test.go", test: "TestRaceMutex2", scope: "func TestRaceMutex2(", line: "\tvar x int8 = 0", directive: "//gosree:guarded_by mu1"},
	{file: "rwmutex_test.go", test: "TestRaceMutexRWMutex", scope: "func TestRaceMutexRWMutex(", line: "\tvar x int16 = 0", directive: "//gosree:guarded_by mu1"},
	{file: "rwmutex_test.go", test: "TestRaceRWMutexMultipleReaders", scope: "func TestRaceRWMutexMultipleReaders(", line: "\tvar x, y int64 = 0, 1", directive: "//gosree:guarded_by mu"},
	{file: "mop_test.go", test: "TestRaceAsFunc3", scope: "func TestRaceAsFunc3(", line: "\tx := 0", directive: "//gosree:guarded_by mu"},
}

// annotatedTests names the corpus tests the annotations are for, in table order.
func annotatedTests() []string {
	out := make([]string, 0, len(corpusAnnotations))
	for _, a := range corpusAnnotations {
		if !slices.Contains(out, a.test) {
			out = append(out, a.test)
		}
	}
	return out
}

// applyAnnotations writes anns into the copy in dir. Every anchor must match exactly as described, or
// it fails: an annotation that silently did not apply would make the annotated run measure the
// unannotated code. Anchors are found in the original text, so insertions do not move later anchors.
func applyAnnotations(dir string, anns []annotation) error {
	byFile := map[string][]annotation{}
	var files []string
	for _, a := range anns {
		if byFile[a.file] == nil {
			files = append(files, a.file)
		}
		byFile[a.file] = append(byFile[a.file], a)
	}
	for _, file := range files {
		path := filepath.Join(dir, filepath.FromSlash(file))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		type edit struct {
			at    int
			order int
			a     annotation
		}
		var edits []edit
		trailing := map[int]bool{}
		for k, a := range byFile[file] {
			i, err := anchor(lines, a)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			if !a.before {
				if strings.Contains(lines[i], "//") {
					return fmt.Errorf("%s: line %q in %q already has a comment; a trailing directive would be part of it and never parse (use before)", file, lines[i], a.scope)
				}
				if trailing[i] {
					return fmt.Errorf("%s: two trailing directives on line %q in %q", file, lines[i], a.scope)
				}
				trailing[i] = true
			}
			edits = append(edits, edit{i, k, a})
		}
		for _, e := range edits {
			if !e.a.before {
				lines[e.at] += " " + e.a.directive
			}
		}
		// Insert bottom-up so earlier indices stay valid; rows on the same line keep table order.
		sort.SliceStable(edits, func(x, y int) bool {
			if edits[x].at != edits[y].at {
				return edits[x].at > edits[y].at
			}
			return edits[x].order > edits[y].order
		})
		for _, e := range edits {
			if e.a.before {
				l := lines[e.at]
				indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
				lines = slices.Insert(lines, e.at, indent+e.a.directive)
			}
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func anchor(lines []string, a annotation) (int, error) {
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, a.scope) {
			if start >= 0 {
				return 0, fmt.Errorf("scope %q appears more than once", a.scope)
			}
			start = i
		}
	}
	if start < 0 {
		return 0, fmt.Errorf("scope %q not found", a.scope)
	}
	if a.line == "" {
		if a.occurrence > 1 {
			return 0, fmt.Errorf("scope %q: an occurrence needs a line", a.scope)
		}
		return start, nil
	}
	var matches []int
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if l == a.line {
			matches = append(matches, i)
		}
		if strings.HasPrefix(l, "func ") || strings.HasPrefix(l, "var ") || strings.HasPrefix(l, "type ") {
			break
		}
	}
	switch {
	case len(matches) == 0:
		return 0, fmt.Errorf("no line %q in %q", a.line, a.scope)
	case a.occurrence == 0 && len(matches) > 1:
		return 0, fmt.Errorf("line %q appears %d times in %q; give an occurrence", a.line, len(matches), a.scope)
	case a.occurrence > len(matches):
		return 0, fmt.Errorf("line %q appears %d times in %q, not %d", a.line, len(matches), a.scope, a.occurrence)
	case a.occurrence == 0:
		return matches[0], nil
	}
	return matches[a.occurrence-1], nil
}
