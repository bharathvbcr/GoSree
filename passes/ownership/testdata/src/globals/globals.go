package globals

import "sync"

// SAFE006 is reported where the variable is declared, once, with the writes that make it mutable.
var counter int // want `SAFE006: package variable counter is written outside init \(line 27\)`

var cache = map[string]int{} // want `SAFE006: package variable cache is written outside init`

// Channels and mutexes are used through operations and method calls, not written.
var ch = make(chan int)

var mu sync.Mutex

// Only written during initialisation: by init itself, or by a helper only init calls.
var name = "fixed"

var setup int

func init() {
	counter = 1
	initHelper()
}

func initHelper() { setup = 2 }

func bump() { counter++ }

func startBump() { go bump() }

func remember(k string) { cache[k]++ }

func useShared() {
	go func() {
		mu.Lock()
		ch <- 1
		mu.Unlock()
	}()
}

// A function value that escapes is not an init-only helper, even when only init calls it.
var hook int // want `SAFE006: package variable hook is written outside init`

func setHook() { hook = 1 }

var hooks = []func(){setHook}

// A guarded variable is checked at every access instead (SAFE009).
var guardedCount int //safe:guarded_by mu

func addGuarded() {
	mu.Lock()
	guardedCount++
	mu.Unlock()
}

// The escape hatch on the declaration accepts it.
//
//safe:unsafe a test seam, only reassigned by tests that run sequentially
var now = func() int { return 0 }

func stubNow() { now = func() int { return 1 } }
