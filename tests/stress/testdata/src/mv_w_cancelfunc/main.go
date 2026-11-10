// WITNESS: race
//
// context.CancelFunc is in the shareable override table ("a CancelFunc may be called by multiple
// goroutines simultaneously"). The override is keyed on the named type, so any closure converted
// to context.CancelFunc is shareable, whatever it captured: sending it moves nothing. The same
// closure sent as a plain func() is rejected (mv_shareable plainFunc).
package main

import (
	"context"
	"sync"
)

type Job struct{ Done int }

func main() {
	stops := make(chan context.CancelFunc, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		(<-stops)()
	}()
	j := &Job{}
	stops <- func() { j.Done++ }
	j.Done++ // MISS(SAFE003): the closure sent on stops writes j on the receiving goroutine
	wg.Wait()
}
