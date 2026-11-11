// WITNESS: race
//
// context.Context is in the shareable override table ("documented safe for simultaneous use by
// multiple goroutines", internal/kinds/kinds.go), so a context has no alias class. That is true of
// the context's own machinery, not of the values it carries: context.WithValue stores a pointer
// the goroutine reads back and writes, and capturing ctx moves nothing the checker tracks.
package main

import (
	"context"
	"sync"
)

type Session struct{ Hits int }

type sessionKey struct{}

func main() {
	s := &Session{}
	ctx := context.WithValue(context.Background(), sessionKey{}, s)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx.Value(sessionKey{}).(*Session).Hits++
	}()
	s.Hits++ // HIT(SAFE003): the goroutine reaches s through ctx and writes it
	wg.Wait()
}
