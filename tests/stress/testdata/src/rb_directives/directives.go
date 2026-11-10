// Package rb_directives puts a //gosree: or //gosree: directive on every kind of declaration and
// statement, in legal but unusual places. The parser must report or ignore each one, never panic.
// It does not: every placement below is either applied, reported as SAFE007, or ignored.
package rb_directives

//gosree:unsafe on an import declaration
import (
	//gosree:iso x // HIT(SAFE007)
	"sync"
)

//gosree:imm // HIT(SAFE007)
//gosree:unsafe a type group
type (
	//gosree:imm
	A struct{ a int }
	//gosree:imm
	B struct{ b *int }
)

//gosree:unsafe const block
const (
	//gosree:mut // HIT(SAFE007)
	C1 = iota
	//gosree:rob c2 // HIT(SAFE007)
	C2
)

//gosree:guarded_by // HIT(SAFE007)
var (
	mu sync.Mutex
	//gosree:guarded_by mu
	counter int
	//gosree:guarded_by nosuchmutex // HIT(SAFE007)
	other int
	//gosree:guarded_by counter // HIT(SAFE007)
	third int
)

// Bodyless has no body.
//
//gosree:iso p
//gosree:mub q
//gosree:rob p // HIT(SAFE007)
//gosree:holds mu
func Bodyless(p, q *int) // HIT(SAFE001)

//gosree:iso // HIT(SAFE007)
func noArgs() {}

//gosree:iso p, p, p // HIT(SAFE007) a name listed twice is reported, and the directive has no effect
func dup(p *int) { _ = p }

//gosree:imm // HIT(SAFE007)
func immOnFunc() {}

//gosree:holds nosuch // HIT(SAFE007)
func holdsUnknown() {}

//gosree:frobnicate // HIT(SAFE007)
func unknownVerb() {}

// An empty verb (//gosree: alone) is not a directive to gofmt, which rewrites it to this line.
func emptyVerb() {}

//gosree:unsafe // HIT(SAFE007)
func emptyReason() {}

/*gosree:unsafe block form */
func blockForm() {}

/*gosree:mut*/   // HIT(SAFE007)
func blockMut() {}

//gosree:unsafe trailing // with a comment
func trailing() {}

//gosree:unsafe	tab separated reason // a tab separates verb and reason as a space does
func tabbed() {}

func statements(ch chan *int) (r int) {
	//gosree:unsafe label
L:
	for i := 0; i < 2; i++ { //gosree:unsafe same-line for
		switch i { //gosree:rob // HIT(SAFE007)
		//gosree:unsafe case clause
		case 0:
			//gosree:mut // HIT(SAFE007)
			continue L
		default:
			//gosree:iso nothing // HIT(SAFE007)
			break L
		}
	}
	x := 1
	//gosree:mut x // HIT(SAFE007)
	p := &x
	//gosree:mut y // HIT(SAFE007)
	q := &x
	//gosree:rob
	var v1, v2 = &x, &x
	//gosree:mut
	_ = &x
	//gosree:rob
	_, _ = p, q
	//gosree:mut
	var _ = &x
	//gosree:unsafe defer
	defer func() { r++ }()
	//gosree:unsafe go
	go func() {}()
	//gosree:unsafe select
	select {
	//gosree:unsafe comm clause
	default:
	}
	s := []int{
		//gosree:unsafe inside a composite literal // HIT(SAFE007)
		1,
		2, //gosree:mut // HIT(SAFE007)
	}
	_ = s
	_, _ = v1, v2
	//gosree:rob
	r = *p /* gosree:mut */ + *q // HIT(SAFE007) the space after "/*" makes this a near miss, which is reported and not applied
	{
		//gosree:unsafe empty block follows // HIT(SAFE007)
	}
	//gosree:unsafe return
	return r
	//gosree:unsafe after return, nothing follows in this block // HIT(SAFE007)
}

type S struct {
	//gosree:mut // HIT(SAFE007)
	f1 int // HIT(BORROW001)
	//gosree:guarded_by f1 // HIT(SAFE007)
	f2 int
	mu sync.Mutex
	//gosree:guarded_by mu
	//gosree:guarded_by mu // HIT(SAFE007) a repeated guard is reported
	f3 int
}

// SBlank guards a blank field: accepted silently, which is harmless.
type SBlank struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	_ int
}

type SEmbed struct {
	mu sync.Mutex
	//gosree:guarded_by mu // HIT(SAFE007)
	sync.RWMutex
}

type I interface {
	//gosree:iso p // HIT(SAFE007)
	M(p *int)
}

func generic[T any]() {
	//gosree:mut
	var t T
	_ = t
}

//gosree:iso r
func (r *S) recv() { r.f1 = 1 }

func init() {
	//gosree:unsafe in init
	counter = 1
}

//gosree:unsafe at the end of the file with nothing after it // HIT(SAFE007)
