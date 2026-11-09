package globals

import "sync"

var counter int

var ch = make(chan int)

var name = "fixed"

var mu sync.Mutex

func init() { counter = 1 }

func bump() { counter++ } // want `SAFE006: package variable counter is written outside init`

func startBump() { go bump() }

// name is only written during initialisation, so reading it from a goroutine is fine.
func readName() string { return name }

func startRead() { go readName() }

func readCounter() {
	go func() {
		_ = counter // want `SAFE006: package variable counter is written outside init`
	}()
}

// Channels and mutexes are shared by design.
func useShared() {
	go func() {
		mu.Lock()
		ch <- 1
		mu.Unlock()
	}()
}

// counter written from the main goroutine is what makes it mutable; no goroutine runs this.
func reset() { counter = 0 }
