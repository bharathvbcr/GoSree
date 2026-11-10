// Package sub is the callee side of sp_xpkg: its behaviour reaches the caller only as FuncFacts.
package sub

import "sync"

type Job struct{ ID int }

// Submit owns its argument and starts a goroutine with it: callers' arguments move.
//
//gocap:iso j
func Submit(j *Job) {
	go func() { j.ID++ }()
}

// Worker submits through a method with the same contract.
type Worker struct{}

//gocap:iso j
func (Worker) Submit(j *Job) {
	go func() { j.ID++ }()
}

// Submitter is the interface Worker satisfies.
type Submitter interface{ Submit(j *Job) }

// SubmitFn is Submit behind a package-level func variable.
var SubmitFn = Submit

// Pass returns its argument.
func Pass(j *Job) *Job { return j }

// Reg keeps what it is given.
type Reg struct{ items []*Job }

func (r *Reg) Add(j *Job) { r.items = append(r.items, j) }

func (r *Reg) Touch() {
	for _, j := range r.items {
		j.ID++
	}
}

// Spawn starts its callback on another goroutine; FuncFact.Spawns propagates that goroutine
// boundary to callers.
func Spawn(f func()) {
	go f()
}

// SpawnIso claims its callback.
//
//gocap:iso f
func SpawnIso(f func()) {
	go f()
}

// Go is a generic spawner that claims its value.
//
//gocap:iso v, f
func Go[T any](v T, f func(T)) {
	go f(v)
}

// Group embeds a WaitGroup; its promoted Go is the blessed spawner reached through embedding.
type Group struct {
	sync.WaitGroup
}
