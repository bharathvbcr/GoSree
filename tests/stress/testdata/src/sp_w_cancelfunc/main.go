// WITNESS: race
//
// context.CancelFunc is on the override table as a value type, and every func() converts to it. A
// closure over a local, converted to CancelFunc, crosses to a goroutine as "shareable"; it and the
// parent then both write the local.
package main

import "context"

func run(f context.CancelFunc, done chan struct{}) {
	f()
	close(done)
}

func main() {
	n := 0
	done := make(chan struct{})
	go run(context.CancelFunc(func() { n++ }), done)
	n = 5 // MISS(SAFE003): the closure over n crossed as a CancelFunc, which the checker treats as shareable
	<-done
}
