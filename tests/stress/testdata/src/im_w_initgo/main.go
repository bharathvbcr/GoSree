// WITNESS: race
//
// init starts a goroutine that keeps writing a package variable. isInit treats every closure
// nested in init as initialisation, so the write is exempt from SAFE006 although it runs
// concurrently with main.
package main

import "sync"

var hits int // HIT(SAFE006): written by a goroutine started in init, concurrently with main's reads

var wg sync.WaitGroup

func init() {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			hits++
		}
	}()
}

func main() {
	s := 0
	for i := 0; i < 1000; i++ {
		s += hits
	}
	wg.Wait()
	_ = s
}
