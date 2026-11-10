// Package im_globals attacks SAFE006: package variables written after initialisation where the
// write is not a store rooted at the variable in a non-init function.
package im_globals

import (
	"sync"
	"sync/atomic"
)

type stats struct{ n int }

func (s *stats) bump() { s.n++ }

func inc(p *int) { *p++ }

// Attack: &global handed to a helper that writes it.
var viaHelper int // HIT(SAFE006) inc(&viaHelper) writes it outside init; missed until 2026-11-10, when the store was rooted only at inc's parameter

func helperAttack() { inc(&viaHelper) }

// Attack: pointer-receiver method of a user type called on the global (witness: im_w_method).
var viaMethod stats // HIT(SAFE006) viaMethod.bump() writes viaMethod.n outside init; missed until 2026-11-10

func methodAttack() { viaMethod.bump() }

// Attack: the global is an interface holding a pointer.
var viaIface any = &stats{} // HIT(SAFE006): viaIface.(*stats).n = 1 writes through the global outside init

func ifaceAttack() { viaIface.(*stats).n = 1 }

// Control: the same write through a global of pointer type is caught.
var viaPtr = &stats{} // HIT(SAFE006)

func ptrAttack() { viaPtr.n = 1 }

// Attack: a goroutine started by init keeps running after init (witness: im_w_initgo).
var fromInitGoroutine int // HIT(SAFE006): written by a goroutine started in init

// Attack: init stores a closure that main calls later.
var fromInitClosure int // HIT(SAFE006): written by handler(), a closure created in init but run any time later

var handler func()

func callHandler() { handler() }

// Attack: an init-only helper starts a goroutine.
var fromInitHelperGoroutine int // HIT(SAFE006): loop is reached via `go` so it runs after init

func init() {
	table["a"] = 1
	go func() {
		fromInitGoroutine++
	}()
	handler = func() { fromInitClosure++ }
	startLoop()
}

func startLoop() { go loop() }

func loop() { fromInitHelperGoroutine++ }

// FP probe: sync.Once-guarded lazy initialisation is the idiomatic race-free pattern.
var onceCfg map[string]int // FP(SAFE006): only written inside once.Do, which happens-before every return of Do

var once sync.Once

func onceInit() map[string]int {
	once.Do(func() { onceCfg = map[string]int{"a": 1} })
	return onceCfg
}

// Control: atomic writes are method/function calls, not stores.
var atomicN int64

func atomicStore() { atomic.StoreInt64(&atomicN, 1) }

// Control: a lookup table filled only in init.
var table = map[string]int{}

func lookup(k string) int { return table[k] }

// Controls: direct writes are caught.
var direct int // HIT(SAFE006)

func directWrite() { direct = 1 }

var mapDirect = map[string]int{} // HIT(SAFE006)

func mapWrite() { mapDirect["x"] = 1 }

var garr [4]int // HIT(SAFE006)

func arrWrite() { garr[1] = 2 }

// Control: a helper called from init and from elsewhere is not init-only.
var calledTwice int // HIT(SAFE006)

func sharedHelper() { calledTwice = 1 }

func init() { sharedHelper() }

func later() { sharedHelper() }

// guarded_by naming a mutex that is never locked: SAFE006 is silenced, SAFE009 must take over.
var gmu sync.Mutex

var neverLocked int //gosree:guarded_by gmu

func writeUnlocked() { neverLocked = 1 } // HIT(SAFE009)
