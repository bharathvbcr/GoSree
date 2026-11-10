// WITNESS: race
//
// runtime.AddCleanup runs the cleanup "in a separate goroutine" once the object is unreachable
// (runtime/mcleanup.go). It is not in the spawner table, so a cleanup that captures a local the
// parent keeps writing is not seen as crossing a goroutine boundary. (Under -race the cleanup
// goroutine is ordered after everything before it runs, so the conflicting write is the parent's
// write after the cleanup has run.)
package main

import (
	"runtime"
	"time"
)

type Job struct{ ID int }

func main() {
	j := &Job{}
	attach(j)
	for range 5 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	j.ID = 1 // MISS(SAFE003): the cleanup goroutine wrote j, with nothing ordering it before this write
}

func attach(j *Job) {
	k := new([64]byte)
	runtime.AddCleanup(k, func(int) { j.ID = 2 }, 0)
}
