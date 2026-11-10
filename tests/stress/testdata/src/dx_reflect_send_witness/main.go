// A channel send through reflect.Value.Send is a transfer the ownership pass never sees, and SAFE001
// does not flag Send (it is neither Set* nor Unsafe*). The sender keeps writing the value it moved.
//
// WITNESS: race
package main

import (
	"reflect"
	"sync"
)

type Job struct{ ID int }

func main() {
	ch := make(chan *Job, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		j := <-ch
		j.ID = 2
	}()
	j := &Job{}
	reflect.ValueOf(ch).Send(reflect.ValueOf(j)) // MISS(SAFE001): reflect.Value.Send moves j to another goroutine behind the analysis
	j.ID = 1                                     // MISS(SAFE003): j is used after it was sent on ch; the move went through reflection
	wg.Wait()
}
