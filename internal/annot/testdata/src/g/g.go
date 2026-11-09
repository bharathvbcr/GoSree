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

func local() {
	var mu sync.Mutex
	_ = &mu
	var x int //safe:guarded_by mu // want `SAFE007: //safe:guarded_by on a local variable is not supported yet`
	_ = x
}
