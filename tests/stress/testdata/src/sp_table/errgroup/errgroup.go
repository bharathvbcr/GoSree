// Package errgroup has the shape of golang.org/x/sync/errgroup but another import path.
package errgroup

import "sync"

type Group struct {
	wg  sync.WaitGroup
	mu  sync.Mutex
	err error //safe:guarded_by mu
}

func (g *Group) Go(f func() error) {
	g.wg.Add(1)
	go func() { // HIT(SAFE002)
		defer g.wg.Done()
		if err := f(); err != nil {
			g.mu.Lock()
			if g.err == nil {
				g.err = err
			}
			g.mu.Unlock()
		}
	}()
}

func (g *Group) Wait() error {
	g.wg.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}
