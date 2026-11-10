// Package rs_null: Rust has no null. A reference is always valid and absence is Option<T>, which
// the compiler forces the caller to match. Go has nil pointers, maps, funcs, interfaces, channels
// and slices, and nothing forces a check. gosree has no nil rule: its docs scope it to goroutine
// boundaries ("Inside one goroutine, aliasing is left alone"). go vet's nilness analyzer (not in
// the default vet set) catches only the deref of a value proven nil on a branch.
package rs_null

import "errors"

type Node struct {
	Val  int
	Next *Node
}

// 1. nil pointer deref: the lookup returns nil for "not found" and the caller forgets.
func find(n *Node, v int) *Node {
	for ; n != nil; n = n.Next {
		if n.Val == v {
			return n
		}
	}
	return nil
}

func Deref(head *Node) int {
	return find(head, 7).Val // MISS(NORULE): nil deref when 7 is absent; Rust's Option forces a match
}

// 2. nil map write: the zero value of a struct with a map field.
type Registry struct{ byName map[string]int }

func (r *Registry) Add(name string) {
	r.byName[name] = len(r.byName) // MISS(NORULE): panics on a zero Registry; Rust's HashMap has no nil state
}

// 3. nil func call.
type Hooks struct{ OnClose func() }

func (h Hooks) Close() {
	h.OnClose() // MISS(NORULE): panics when the hook was never set; Rust would hold Option<Box<dyn Fn()>>
}

// 4. nil interface method call.
type Logger interface{ Log(string) }

type Service struct{ log Logger }

func (s *Service) Start() {
	s.log.Log("start") // MISS(NORULE): s.log is nil in a zero Service
}

// 5. typed nil in an interface: err != nil is true for a nil *MyErr.
type MyErr struct{ msg string }

func (e *MyErr) Error() string { return e.msg }

func validate(ok bool) *MyErr {
	if ok {
		return nil
	}
	return &MyErr{"bad"}
}

func Check() error {
	return validate(true) // HIT(ERR004): returns a non-nil error holding a nil *MyErr
}

func UseCheck() string {
	if err := Check(); err != nil {
		return err.Error() // MISS(NORULE): calls (*MyErr).Error on nil and derefs it
	}
	return ""
}

// 6. zero value confused with absence: m[k] is 0 for a missing key.
func Balance(accounts map[string]int, who string) int {
	return accounts[who] // MISS(NORULE): a missing account reads as balance 0; Rust's get returns Option
}

// 7. nil channel send blocks forever.
type Pipe struct{ out chan int }

func (p *Pipe) Emit(v int) {
	p.out <- v // MISS(NORULE): a zero Pipe blocks this goroutine forever
}

var errNone = errors.New("none")

// Near-twin, accepted and correct: the comma-ok lookup.
func BalanceOK(accounts map[string]int, who string) (int, error) {
	v, ok := accounts[who]
	if !ok {
		return 0, errNone
	}
	return v, nil
}
