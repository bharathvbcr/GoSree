// WITNESS: race
//
// A package variable written from another package. SAFE006 looks, in each package, for writes to
// that package's own variables: the importer's write is to someone else's variable, and the owner
// has no write to find. The variable is mutated outside init, unguarded, and nobody reports it.
package main

import (
	"sync"

	"im_w_xpkg/sub"
)

func main() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s := 0
		for i := 0; i < 1000; i++ {
			s += sub.Read()
		}
		_ = s
	}()
	for i := 0; i < 1000; i++ {
		sub.Limit = i // MISS(SAFE006): a package variable written outside init and unguarded, from an importing package
	}
	wg.Wait()
}
