// WITNESS: race
//
// testing.B.RunParallel runs its body on GOMAXPROCS goroutines at once (testing/benchmark.go). It is
// an ordinary call to the checker, so a body that writes a captured local is not seen as a callback
// that races with itself. testing.Benchmark runs it outside `go test`.
package main

import (
	"runtime"
	"testing"
)

func main() {
	runtime.GOMAXPROCS(4)
	testing.Benchmark(func(b *testing.B) {
		n := 0
		b.SetParallelism(2)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				n++ // MISS(SAFE008): every RunParallel goroutine writes n
			}
		})
	})
}
