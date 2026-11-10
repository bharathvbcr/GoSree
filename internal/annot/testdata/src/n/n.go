package n

import "sync"

type S struct {
	mu sync.Mutex
	a  int // safe:guarded_by mu // want `SAFE007: "// safe:guarded_by mu" is not a gosree directive, so it has no effect: write it as //safe:guarded_by mu`
	b  int //Safe:guardedby mu // want `SAFE007: "//Safe:guardedby mu" is not a gosree directive`
	c  int //safe:guarded_by	mu
	d  int //safe:gaurded_by mu // want `SAFE007: unknown directive //safe:gaurded_by \(did you mean //safe:guarded_by\?`
}

// GOSREE:ISO p // want `SAFE007: "// GOSREE:ISO p" is not a gosree directive, so it has no effect: write it as //gosree:iso p`
func loud(p *int) { _ = p }

// gosree: // want `SAFE007: "// gosree:" is not a gosree directive`
func empty() {}

// Prose is not a directive.
//
// safe: this lock is held by the caller
// gosree: see the docs
// Safe: unsafe.Pointer is never used here.
// gosree:isolation is a design note
func prose() {}

func hatches() {
	//gosree:unsafe // want `SAFE007: //gosree:unsafe needs a reason after the directive`
	_ = 1
	//gosree:unsafe ​ // want `SAFE007: //gosree:unsafe needs a reason after the directive: "\\u200b" has no visible letter or digit`
	_ = 2
	//gosree:unsafe	tab-separated reason
	_ = 3
	//gosree:unsafe reviewed
	//gosree:unsafe reviewed twice // want `SAFE007: //gosree:unsafe repeats a //gosree:unsafe that already covers exactly this code`
	_ = 4
	if true {
		_ = 5
	} else { //gosree:unsafe else // want `SAFE007: //gosree:unsafe on the line of an opening brace covers nothing`
		_ = 6
	}
}

// Inline parameter directives.
func inline(a, b *int /*gosree:mub*/, c *int /*gosree:rob c*/) {}

func inlineBad(a /*gosree:mub*/ *int) {} // want `SAFE007: //gosree:mub is inside the declaration of a; write it directly after the parameter's type`

func inlineResult() (r *int /*gosree:rob*/) { return nil } // want `SAFE007: //gosree:rob on a result`

//gosree:iso p, p // want `SAFE007: //gosree:iso names "p" twice`
func dupIso(p *int) {}

//gosree:iso p, nosuch // want `SAFE007: //gosree:iso names "nosuch", which is not a parameter of partial`
func partial(p *int) {}

//gosree:imm extra // want `SAFE007: //gosree:imm takes no arguments`
type I struct{}

func binding() {
	x := 1
	//gosree:rob
	b := &x //gosree:mut // want `SAFE007: //gosree:mut conflicts with the //gosree:rob directive already on this statement`
	_ = b
}
