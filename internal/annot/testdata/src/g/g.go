package g

import "sync"

type S struct {
	mu sync.Mutex
	n  int //safe:guarded_by mu
	//safe:guarded_by mu
	m  map[string]int
	rw sync.RWMutex
	r  []int //safe:guarded_by rw
	p  *sync.Mutex
	l  sync.Locker
	a  int //safe:guarded_by p // want `SAFE007: //safe:guarded_by names p, which is a pointer to a mutex`
	b  int //safe:guarded_by nope // want `SAFE007: //safe:guarded_by names "nope", which is not a field of this struct`
	c  int //safe:guarded_by n // want `SAFE007: //safe:guarded_by names n, which is not a sync.Mutex or sync.RWMutex`
	d  int //safe:guarded_by l // want `SAFE007: //safe:guarded_by names l, which is a sync.Locker`
	e  int //safe:guarded_by // want `SAFE007: //safe:guarded_by needs one mutex name`

	Exp int //safe:guarded_by mu // want `SAFE007: //safe:guarded_by on exported Exp`
}

// An embedded mutex is named by its type.
type E struct {
	sync.Mutex
	n int //safe:guarded_by Mutex
}

var (
	gmu   sync.Mutex
	count int //safe:guarded_by gmu
)

var total int //safe:guarded_by gmu

var notMu int

var other int //safe:guarded_by notMu // want `SAFE007: //safe:guarded_by names notMu, which is not a sync.Mutex`

//safe:holds mu
func (s *S) bumpLocked() { s.n++ }

//safe:holds gmu
func addLocked() { count++ }

//safe:holds nope // want `SAFE007: //safe:holds names "nope", which is neither a field of the receiver nor a package variable`
func badHolds() {}

//safe:holds gmu // want `SAFE007: //safe:holds must be in a function's doc comment`
type notAFunc int

// Local variables may be guarded by a mutex in scope at their declaration: a local of this function
// or an enclosing one, or a package variable.
func local() {
	var lmu sync.Mutex
	var lrw sync.RWMutex
	var x int    //safe:guarded_by lmu
	y, z := 1, 2 //safe:guarded_by lrw
	var c int    //safe:guarded_by gmu
	var p *sync.Mutex
	var bad int   //safe:guarded_by p // want `SAFE007: //safe:guarded_by names p, which is a pointer to a mutex`
	var early int //safe:guarded_by later // want `SAFE007: //safe:guarded_by names "later", which is not a variable in scope here`
	var later sync.Mutex
	x = 2        //safe:guarded_by lmu // want `SAFE007: //safe:guarded_by must be on a struct field or a variable declaration`
	x, w := 3, 4 //safe:guarded_by lmu // want `SAFE007: //safe:guarded_by on a := that redeclares x; declare guarded variables on their own line`
	func() {
		var inner int //safe:guarded_by lmu
		_ = inner
	}()
	_, _, _, _, _, _, _, _ = &lmu, &lrw, &later, x, y, z, c, w
	_, _, _ = p, bad, early
}
