// WITNESS: race
//
// context.Context is on the override table, so a Context crosses to a goroutine freely. A Context
// made by WithValue carries an arbitrary mutable value, which the goroutine reads back and writes
// while the parent writes it too.
package main

import "context"

type Job struct{ ID int }

type key struct{}

func main() {
	j := &Job{}
	ctx := context.WithValue(context.Background(), key{}, j)
	done := make(chan struct{})
	go func() {
		ctx.Value(key{}).(*Job).ID = 2
		close(done)
	}()
	j.ID = 1 // MISS(SAFE003): j crossed inside a Context, which the checker treats as shareable
	<-done
}
