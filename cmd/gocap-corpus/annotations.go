package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// annotation is one //safe directive added to the race corpus for the annotated run, for test: the
// comment a programmer would write who believes the test's locking protects that variable. It is
// appended as a trailing comment to the first line equal to line after the line starting with scope (a
// test's func line, or a package variable's declaration), before the next top-level declaration. Only
// comments are added; no code changes.
type annotation struct {
	file, test, scope, line, directive string
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
	{"mutex_test.go", "TestNoRaceMutex", "func TestNoRaceMutex(", "\tvar x int16 = 0", "//safe:guarded_by mu"},
	{"rwmutex_test.go", "TestNoRaceRWMutexMultipleReaders", "func TestNoRaceRWMutexMultipleReaders(", "\tx := int64(0)", "//safe:guarded_by mu"},
	{"rwmutex_test.go", "TestNoRaceRWMutexTransitive", "func TestNoRaceRWMutexTransitive(", "\tx := int64(0)", "//safe:guarded_by mu"},
	{"mop_test.go", "TestNoRaceFuncUnlock", "func TestNoRaceFuncUnlock(", "\tx := 0", "//safe:guarded_by mu"},
	{"mop_test.go", "TestNoRaceAsFunc4", "func TestNoRaceAsFunc4(", "\tx := 0", "//safe:guarded_by mu"},
	{"finalizer_test.go", "TestNoRaceFinGlobal", "var finVar struct {", "\tcnt int", "//safe:guarded_by Mutex"},
	// The same shapes with a race.
	{"mutex_test.go", "TestRaceMutex", "func TestRaceMutex(", "\tvar x int16 = 0", "//safe:guarded_by mu"},
	{"mutex_test.go", "TestRaceMutex2", "func TestRaceMutex2(", "\tvar x int8 = 0", "//safe:guarded_by mu1"},
	{"rwmutex_test.go", "TestRaceMutexRWMutex", "func TestRaceMutexRWMutex(", "\tvar x int16 = 0", "//safe:guarded_by mu1"},
	{"rwmutex_test.go", "TestRaceRWMutexMultipleReaders", "func TestRaceRWMutexMultipleReaders(", "\tvar x, y int64 = 0, 1", "//safe:guarded_by mu"},
	{"mop_test.go", "TestRaceAsFunc3", "func TestRaceAsFunc3(", "\tx := 0", "//safe:guarded_by mu"},
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

// applyAnnotations writes the annotations into the corpus copy in dir. Every anchor must match exactly
// as described, or it fails: an annotation that silently did not apply would make the annotated run
// measure the unannotated corpus.
func applyAnnotations(dir string) error {
	byFile := map[string][]annotation{}
	for _, a := range corpusAnnotations {
		byFile[a.file] = append(byFile[a.file], a)
	}
	for file, anns := range byFile {
		path := filepath.Join(dir, file)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		for _, a := range anns {
			i, err := anchor(lines, a)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			lines[i] += " " + a.directive
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
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if l == a.line {
			return i, nil
		}
		if strings.HasPrefix(l, "func ") || strings.HasPrefix(l, "var ") || strings.HasPrefix(l, "type ") {
			break
		}
	}
	return 0, fmt.Errorf("no line %q in %q", a.line, a.scope)
}
