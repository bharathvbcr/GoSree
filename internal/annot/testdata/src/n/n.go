package n

import "sync"

type S struct {
	mu sync.Mutex
	a  int // safe:guarded_by mu // want `SAFE007: "// safe:guarded_by mu" is not a gocap directive, so it has no effect: write it as //safe:guarded_by mu`
	b  int //Safe:guardedby mu // want `SAFE007: "//Safe:guardedby mu" is not a gocap directive`
	c  int //safe:guarded_by	mu
	d  int //safe:gaurded_by mu // want `SAFE007: unknown directive //safe:gaurded_by \(did you mean //safe:guarded_by\?`
}

// GOCAP:ISO p // want `SAFE007: "// GOCAP:ISO p" is not a gocap directive, so it has no effect: write it as //gocap:iso p`
func loud(p *int) { _ = p }

// gocap: // want `SAFE007: "// gocap:" is not a gocap directive`
func empty() {}

// Prose is not a directive.
//
// safe: this lock is held by the caller
// gocap: see the docs
// Safe: unsafe.Pointer is never used here.
// gocap:isolation is a design note
func prose() {}

func hatches() {
	//gocap:unsafe // want `SAFE007: //gocap:unsafe needs a reason after the directive`
	_ = 1
	//gocap:unsafe ​ // want `SAFE007: //gocap:unsafe needs a reason after the directive: "\\u200b" has no visible letter or digit`
	_ = 2
	//gocap:unsafe	tab-separated reason
	_ = 3
	//gocap:unsafe reviewed
	//gocap:unsafe reviewed twice // want `SAFE007: //gocap:unsafe repeats a //gocap:unsafe that already covers exactly this code`
	_ = 4
	if true {
		_ = 5
	} else { //gocap:unsafe else // want `SAFE007: //gocap:unsafe on the line of an opening brace covers nothing`
		_ = 6
	}
}

// Inline parameter directives.
func inline(a, b *int /*gocap:mub*/, c *int /*gocap:rob c*/) {}

func inlineBad(a /*gocap:mub*/ *int) {} // want `SAFE007: //gocap:mub is inside the declaration of a; write it directly after the parameter's type`

func inlineResult() (r *int /*gocap:rob*/) { return nil } // want `SAFE007: //gocap:rob on a result`

//gocap:iso p, p // want `SAFE007: //gocap:iso names "p" twice`
func dupIso(p *int) {}

//gocap:iso p, nosuch // want `SAFE007: //gocap:iso names "nosuch", which is not a parameter of partial`
func partial(p *int) {}

//gocap:imm extra // want `SAFE007: //gocap:imm takes no arguments`
type I struct{}

func binding() {
	x := 1
	//gocap:rob
	b := &x //gocap:mut // want `SAFE007: //gocap:mut conflicts with the //gocap:rob directive already on this statement`
	_ = b
}
