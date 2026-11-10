// Package mv_iso probes //gocap:iso parameters. The two halves of the rule are checked in different
// places: the iso callee treats the parameter as owned (buildGraph gives it no foreign event, so the
// callee may move it on without SAFE002), while the caller treats the argument as moved only when
// it can see the callee statically (graph.call: sum.isIso). Every call that reaches an iso callee
// some other way -- an interface method, a method value, a function value -- drops the caller's
// half, and the move is checked nowhere.
package mv_iso

import "github.com/bharathvbcr/gocap/cap"

type Job struct{ ID int }

var keep *Job // HIT(SAFE006)

//gocap:iso j
func take(j *Job) { go func() { j.ID++ }() }

// control: a direct call to an iso parameter is a move.
func direct() {
	j := &Job{}
	take(j)
	j.ID = 1 // HIT(SAFE003)
}

// control: the caller kept an alias made before the call.
func aliasBefore() {
	j := &Job{}
	k := j
	take(j)
	k.ID = 1 // HIT(SAFE003)
}

type Sink interface{ Take(j *Job) }

type S struct{}

//gocap:iso j
func (S) Take(j *Job) { go func() { j.ID++ }() }

// iso through an interface method: the call is an escape, not a move (WITNESS mv_w_iso_iface).
func viaInterface() {
	var s Sink = S{}
	j := &Job{}
	s.Take(j)
	j.ID = 1 // MISS(SAFE003): j was passed to iso parameter j of S.Take, which started a goroutine writing it
}

// iso through a method value: the bound-method wrapper is synthetic and gets the pessimistic
// summary, which has no iso flags.
func viaMethodValue() {
	f := S{}.Take
	j := &Job{}
	f(j)
	j.ID = 1 // MISS(SAFE003): f is S.Take; j moved into its iso parameter
}

func apply(f func(*Job), j *Job) { f(j) }

// iso through a function value handed to a helper.
func viaFuncValue() {
	j := &Job{}
	apply(take, j)
	j.ID = 1 // MISS(SAFE003): apply calls take(j), which moves j to a goroutine
}

func applyG[T any](f func(T), v T) { f(v) }

// iso through a generic higher-order helper.
func viaGeneric() {
	j := &Job{}
	applyG(take, j)
	j.ID = 1 // MISS(SAFE003): applyG calls take(j), which moves j to a goroutine
}

//gocap:iso j
func stash(j *Job) { keep = j }

// an iso parameter the callee stores in a global: the caller's use is a use after the move, and
// the store is reported at the global (SAFE006 above).
func isoStash() {
	j := &Job{}
	stash(j)
	j.ID = 1 // HIT(SAFE003)
}

//gocap:iso j
func passThrough(j *Job) *Job { return j }

// control: an iso callee that hands the value straight back. The caller owns the result again.
func isoReturn() {
	j := &Job{}
	k := passThrough(j)
	k.ID = 1
}

// marker form: a cap.Iso parameter behaves like the comment form for a direct call.
func takeM(j cap.Iso[Job]) { go func() { j.ID++ }() }

func markerDirect() {
	j := &Job{}
	takeM(j)
	j.ID = 1 // HIT(SAFE003)
}

// marker form on a function literal parameter (the comment form cannot name one).
func markerLit() {
	f := func(j cap.Iso[Job]) { go func() { j.ID++ }() }
	j := &Job{}
	f(j)
	j.ID = 1 // HIT(SAFE003)
}
