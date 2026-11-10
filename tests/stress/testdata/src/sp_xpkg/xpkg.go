// Package sp_xpkg checks moves across package boundaries, where a callee's behaviour is a fact.
package sp_xpkg

import (
	"bytes"
	"container/list"

	"sp_xpkg/sub"
)

// control: a static call to an owned parameter moves the argument.
func staticIso() {
	j := &sub.Job{}
	sub.Submit(j)
	j.ID = 1 // HIT(SAFE003)
}

// control: a static method call with an owned parameter.
func staticMethodIso() {
	j := &sub.Job{}
	sub.Worker{}.Submit(j)
	j.ID = 1 // HIT(SAFE003)
}

// The same method through the interface it satisfies: an invoke has no summary, so the owned
// parameter is not seen and the argument does not move (graph.call, IsInvoke branch).
func interfaceIso(s sub.Submitter) {
	j := &sub.Job{}
	s.Submit(j)
	j.ID = 1 // MISS(SAFE003): an interface call has no summary (graph.call IsInvoke branch), so the owned parameter of Worker.Submit does not move j; the goroutine Submit starts writes it. Witness sp_w_isoiface
}

// The same function through a package-level func variable: a dynamic call, no move.
func funcVarIso() {
	j := &sub.Job{}
	sub.SubmitFn(j)
	j.ID = 1 // MISS(SAFE003): Submit through a package-level func variable is a dynamic call: no owned parameter, no move
}

// The same method as a method value: a bound-method wrapper has no summary (summary(): synthetic
// functions are pessimistic), which leaks but does not move.
func methodValueIso() {
	j := &sub.Job{}
	f := sub.Worker{}.Submit
	f(j)
	j.ID = 1 // MISS(SAFE003): a method value calls a bound-method wrapper, which summary() makes pessimistic: it leaks j but does not move it
}

// control: a generic spawner with owned parameters, instantiated here.
func genericIso() {
	j := &sub.Job{}
	sub.Go(j, func(p *sub.Job) { p.ID++ })
	j.ID = 1 // HIT(SAFE003)
}

// control: a callback to an owned func parameter moves its captures.
func spawnIso() {
	n := 0
	sub.SpawnIso(func() { n++ })
	n = 5 // HIT(SAFE003)
}

// A callback to Spawn, which does not claim it: the go statement in sub is reported (SAFE002
// there), the caller is silent.
func spawnPlain() {
	n := 0
	sub.Spawn(func() { n++ })
	n = 5
}

// control: Pass returns an alias of its argument (a link), so sending the result moves j.
func passAlias(ch chan *sub.Job) {
	j := &sub.Job{}
	ch <- sub.Pass(j)
	j.ID = 1 // HIT(SAFE003)
}

// control: Reg.Add keeps j inside r (a link), so moving r moves j.
func regLink() {
	r := &sub.Reg{}
	j := &sub.Job{}
	r.Add(j)
	go r.Touch()
	j.ID = 1 // HIT(SAFE003)
}

// stdlib: bytes.NewBuffer keeps its argument.
func bytesBuffer(ch chan *bytes.Buffer) {
	b := make([]byte, 8)
	ch <- bytes.NewBuffer(b)
	b[0] = 1 // HIT(SAFE003)
}

// stdlib: container/list keeps the value it is given.
func listPush(ch chan *list.List) {
	l := list.New()
	j := &sub.Job{}
	l.PushBack(j)
	ch <- l
	j.ID = 1 // HIT(SAFE003)
}

// WaitGroup.Go reached through a struct that embeds the WaitGroup, in another package.
func embeddedGo() {
	var g sub.Group
	j := &sub.Job{}
	g.Go(func() { j.ID = 2 })
	j.ID = 1 // HIT(SAFE003)
	g.Wait()
}
