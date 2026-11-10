// Package sp_table attacks the spawner table (passes/ownership/spawn.go): entries are matched by
// the FullName of a static callee, so a blessed spawner reached any other way is an ordinary call.
package sp_table

import (
	"sync"
	"time"

	"sp_table/errgroup"
)

type Job struct{ ID int }

// control: the blessed spawner called directly.
func direct() {
	var wg sync.WaitGroup
	j := &Job{}
	wg.Go(func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
	wg.Wait()
}

// control: a method expression is still a static call to the same function.
func methodExpr() {
	var wg sync.WaitGroup
	j := &Job{}
	(*sync.WaitGroup).Go(&wg, func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
	wg.Wait()
}

// A method value: the call goes through a bound-method wrapper, which is not in the table.
func methodValue() {
	var wg sync.WaitGroup
	j := &Job{}
	start := wg.Go
	start(func() { j.ID = 2 })
	j.ID = 1 // MISS(SAFE003): a method value (wg.Go) is a bound-method wrapper, not the static callee the spawner table is keyed on, so the callback is no transfer. Witness sp_w_methodvalue
	wg.Wait()
}

// The spawner behind an interface.
type goer interface{ Go(func()) }

func viaInterface() {
	var wg sync.WaitGroup
	var g goer = &wg
	j := &Job{}
	g.Go(func() { j.ID = 2 })
	j.ID = 1 // MISS(SAFE003): WaitGroup.Go behind an interface is an invoke; spawnOf returns false for invokes
	wg.Wait()
}

// time.AfterFunc in a package-level func variable: a dynamic call.
var after = time.AfterFunc

func viaFuncVar() {
	j := &Job{}
	after(time.Millisecond, func() { j.ID = 2 })
	j.ID = 1 // MISS(SAFE003): time.AfterFunc in a package func variable is a dynamic call, not in the table
}

// control: a function value of the package function itself is a static callee.
func viaFuncValue() {
	j := &Job{}
	af := time.AfterFunc
	af(time.Millisecond, func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
}

// A type alias is the same type: the join is still recognised.
type WG = sync.WaitGroup

func aliasJoin() {
	var wg WG
	j := &Job{}
	wg.Go(func() { j.ID = 2 })
	wg.Wait()
	j.ID = 1
}

// Impersonation: a local package called errgroup with an errgroup-shaped Group. Its FullName is
// "(*sp_table/errgroup.Group).Go", so it gets no Wait join; summary propagation sees that Go
// starts its callback on a goroutine and flags the post-spawn use of j.
func lookalike() {
	var g errgroup.Group
	j := &Job{}
	g.Go(func() error { j.ID = 2; return nil })
	_ = g.Wait()
	j.ID = 1 // HIT(SAFE003)
}
