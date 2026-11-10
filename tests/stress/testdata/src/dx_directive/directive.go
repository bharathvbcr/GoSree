// Package dx_directive attacks the directive lexer (internal/annot/annot.go:213 parseDirective).
// A comment that is not exactly "//gosree:" or "//gosree:" (or the /*...*/ forms) is not a directive and
// is dropped without a word. For //gosree:unsafe that fails closed (the finding stays). For every
// directive that asks for a check (iso, guarded_by, holds, imm, mub, rob) it fails open: the author
// believes a guarantee is checked and none is. An unknown verb after a correct prefix is reported,
// so the hole is exactly the prefix.
package dx_directive

import "sync"

type Job struct{ ID int }

type S struct {
	mu sync.Mutex
	n  int //gosree:guarded_by mu
	m  int // gosree:guarded_by mu // HIT(SAFE007) one space after the slashes; reported as a near miss, not applied, so m stays unguarded
	k  int //Gosree:guarded_by mu // HIT(SAFE007) capitalised prefix; reported as a near miss
	q  int /* gosree:guarded_by mu */ // HIT(SAFE007) block form with a space; reported as a near miss
	t  int //gosree:guarded_by	mu // a tab separates the verb from its argument as a space does (rb_directives:78): t is guarded by mu
	b  int //gosree:guardedby mu // HIT(SAFE007)
}

func (s *S) unlocked() {
	s.n++ // HIT(SAFE009)
	s.m++ // MISS(SAFE009): m's guard was written "// gosree:guarded_by mu" and never took effect
	s.k++ // MISS(SAFE009): k's guard was written "//Gosree:guarded_by mu" and never took effect
	s.q++ // MISS(SAFE009): q's guard was written "/* gosree:guarded_by mu */" and never took effect
}

func (s *S) locked() {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}

// iso: the control moves the caller's argument; the near-misses move nothing.
//
//gosree:iso j
func consume(j *Job) { _ = j }

// GOSREE:ISO j // HIT(SAFE007) upper-case prefix after a space; reported as a near miss
func consumeLoud(j *Job) { _ = j }

// gofmt made the line below. It was written "//gosree:Iso j", which gosree reports as an unknown
// directive. gofmt keeps "//x:y" unspaced in a doc comment only when y starts with a lower-case
// letter or digit, so it inserted the space, and the SAFE007 vanished with it: formatting the file
// turns a reported typo into a silently dropped directive.
//
// gosree:Iso j // HIT(SAFE007) the reported "//gosree:Iso j" after gofmt; reported again, as a near miss
func consumeCase(j *Job) { _ = j }

// Outside a doc comment gofmt leaves the line alone, and the typo stays reported.
func caseInBody() {
	//gosree:Unsafe reason // HIT(SAFE007)
	_ = 1
}

func callers() {
	a := &Job{}
	consume(a)
	a.ID = 1 // HIT(SAFE003)

	b := &Job{}
	consumeLoud(b)
	b.ID = 1 // MISS(SAFE003): consumeLoud's "// GOSREE:ISO j" was dropped, so passing b moved nothing
}

// A local binding annotated both mutable and read-only. On a parameter the same pair is rejected
// (annot.go:335-341); on an assignment both are recorded and nothing is said.
func mutAndRob() {
	x := 1
	//gosree:rob
	b := &x //gosree:mut // HIT(SAFE007) b would be both a \mub and a \rob borrow; the same check as for a parameter rejects it
	*b = 2  // HIT(BORROW003)
}

// Controls: misplaced directives that are reported.
//
//gosree:iso r // HIT(SAFE007)
func result() (r *Job) { return nil }

// A blank line between the directive and the type detaches it, and that is reported.
//gosree:imm // HIT(SAFE007)

type spaced struct{}
