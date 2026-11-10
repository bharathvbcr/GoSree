// Package lk_holdsvalue locks a fixed crash: a //gosree:holds method called through a method value.
//
// The static callee of f() is the bound-method wrapper, whose object is the annotated method, so
// the holds check runs; the wrapper call has no arguments, so there is no receiver to ask the type
// of. Until 2026-11-10 holdsPaths (passes/ownership/lockset.go) dereferenced that nil receiver and
// the panic took the whole driver down: a stack trace instead of findings for every package. It now
// reports the call, which is right: f() runs without s.mu held.
//
// If the panic comes back, this package kills the test process; move it to a root with a
// `// CRASH: why` line and the suite will probe it in a child process instead.
package lk_holdsvalue

import "sync"

type S struct {
	mu sync.Mutex
	n  int //gosree:guarded_by mu
}

//gosree:holds mu
func (s *S) bumpLocked() { s.n++ }

func (s *S) methodValue() {
	f := s.bumpLocked
	f() // HIT(SAFE009)
}
