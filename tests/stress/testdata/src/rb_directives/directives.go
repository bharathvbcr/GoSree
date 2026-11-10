// Package rb_directives puts a //gocap: or //safe: directive on every kind of declaration and
// statement, in legal but unusual places. The parser must report or ignore each one, never panic.
// It does not: every placement below is either applied, reported as SAFE007, or ignored.
package rb_directives

//gocap:unsafe on an import declaration
import (
	//gocap:iso x // HIT(SAFE007)
	"sync"
)

//gocap:imm // HIT(SAFE007)
//gocap:unsafe a type group
type (
	//gocap:imm
	A struct{ a int }
	//gocap:imm
	B struct{ b *int }
)

//gocap:unsafe const block
const (
	//gocap:mut // HIT(SAFE007)
	C1 = iota
	//gocap:rob c2 // HIT(SAFE007)
	C2
)

//gocap:guarded_by // HIT(SAFE007)
var (
	mu sync.Mutex
	//safe:guarded_by mu
	counter int
	//safe:guarded_by nosuchmutex // HIT(SAFE007)
	other int
	//safe:guarded_by counter // HIT(SAFE007)
	third int
)

// Bodyless has no body.
//
//gocap:iso p
//gocap:mub q
//gocap:rob p // HIT(SAFE007)
//safe:holds mu
func Bodyless(p, q *int) // HIT(SAFE001)

//gocap:iso // HIT(SAFE007)
func noArgs() {}

//gocap:iso p, p, p // HIT(SAFE007) a name listed twice is reported, and the directive has no effect
func dup(p *int) { _ = p }

//gocap:imm // HIT(SAFE007)
func immOnFunc() {}

//gocap:holds nosuch // HIT(SAFE007)
func holdsUnknown() {}

//gocap:frobnicate // HIT(SAFE007)
func unknownVerb() {}

// An empty verb (//gocap: alone) is not a directive to gofmt, which rewrites it to this line.
func emptyVerb() {}

//safe:unsafe // HIT(SAFE007)
func emptyReason() {}

/*gocap:unsafe block form */
func blockForm() {}

/*gocap:mut*/   // HIT(SAFE007)
func blockMut() {}

//gocap:unsafe trailing // with a comment
func trailing() {}

//gocap:unsafe	tab separated reason // a tab separates verb and reason as a space does
func tabbed() {}

func statements(ch chan *int) (r int) {
	//gocap:unsafe label
L:
	for i := 0; i < 2; i++ { //gocap:unsafe same-line for
		switch i { //gocap:rob // HIT(SAFE007)
		//gocap:unsafe case clause
		case 0:
			//gocap:mut // HIT(SAFE007)
			continue L
		default:
			//gocap:iso nothing // HIT(SAFE007)
			break L
		}
	}
	x := 1
	//gocap:mut x // HIT(SAFE007)
	p := &x
	//gocap:mut y // HIT(SAFE007)
	q := &x
	//gocap:rob
	var v1, v2 = &x, &x
	//gocap:mut
	_ = &x
	//gocap:rob
	_, _ = p, q
	//gocap:mut
	var _ = &x
	//gocap:unsafe defer
	defer func() { r++ }()
	//gocap:unsafe go
	go func() {}()
	//gocap:unsafe select
	select {
	//gocap:unsafe comm clause
	default:
	}
	s := []int{
		//gocap:unsafe inside a composite literal // HIT(SAFE007)
		1,
		2, //gocap:mut // HIT(SAFE007)
	}
	_ = s
	_, _ = v1, v2
	//gocap:rob
	r = *p /* gocap:mut */ + *q // HIT(SAFE007) the space after "/*" makes this a near miss, which is reported and not applied
	{
		//gocap:unsafe empty block follows // HIT(SAFE007)
	}
	//gocap:unsafe return
	return r
	//gocap:unsafe after return, nothing follows in this block // HIT(SAFE007)
}

type S struct {
	//gocap:mut // HIT(SAFE007)
	f1 int // HIT(BORROW001)
	//safe:guarded_by f1 // HIT(SAFE007)
	f2 int
	mu sync.Mutex
	//safe:guarded_by mu
	//safe:guarded_by mu // HIT(SAFE007) a repeated guard is reported
	f3 int
}

// SBlank guards a blank field: accepted silently, which is harmless.
type SBlank struct {
	mu sync.Mutex
	//safe:guarded_by mu
	_ int
}

type SEmbed struct {
	mu sync.Mutex
	//safe:guarded_by mu // HIT(SAFE007)
	sync.RWMutex
}

type I interface {
	//gocap:iso p // HIT(SAFE007)
	M(p *int)
}

func generic[T any]() {
	//gocap:mut
	var t T
	_ = t
}

//gocap:iso r
func (r *S) recv() { r.f1 = 1 }

func init() {
	//gocap:unsafe in init
	counter = 1
}

//gocap:unsafe at the end of the file with nothing after it // HIT(SAFE007)
