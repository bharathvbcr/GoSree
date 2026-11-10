// WITNESS: race
//
// *slog.Logger is on the override table ("documented safe for concurrent use"), but it calls its
// Handler's Handle from every goroutine that logs. A Handler that counts in a plain field races
// with itself; nothing registers Handle as a callback that runs concurrently.
package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

type countHandler struct {
	slog.Handler
	n int
}

func (h *countHandler) Handle(ctx context.Context, r slog.Record) error {
	h.n++ // MISS(SAFE008): Handle runs on both logging goroutines at once
	return h.Handler.Handle(ctx, r)
}

func main() {
	logger := slog.New(&countHandler{Handler: slog.NewTextHandler(io.Discard, nil)})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { logger.Info("x") })
	}
	wg.Wait()
}
