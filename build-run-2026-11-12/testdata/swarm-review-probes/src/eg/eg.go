// Package eg: errgroup-shaped variants for SAFE002 on a captured receiver.
package eg

import "sync"

// A1: the fixture's shape (err guarded by mu).
type G1 struct {
	wg  sync.WaitGroup
	mu  sync.Mutex
	err error //gosree:guarded_by mu
}

func (g *G1) Go(f func() error) {
	g.wg.Add(1)
	go func() {
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

// A2: same, but the goroutine writes err WITHOUT the lock. Concurrent Go calls race on g.err.
type G2 struct {
	wg  sync.WaitGroup
	mu  sync.Mutex
	err error
}

func (g *G2) Go(f func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := f(); err != nil {
			g.err = err
		}
	}()
}

// A3: guarded err, plus an unguarded counter the goroutine bumps with no lock.
type G3 struct {
	wg  sync.WaitGroup
	mu  sync.Mutex
	n   int
	err error //gosree:guarded_by mu
}

func (g *G3) Go(f func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.n++
		if err := f(); err != nil {
			g.mu.Lock()
			g.err = err
			g.mu.Unlock()
		}
	}()
}

// A4: guarded counter, but the caller-side method writes it without the lock.
type G4 struct {
	mu sync.Mutex
	n  int
}

func (g *G4) Go() {
	go func() {
		g.mu.Lock()
		g.n++
		g.mu.Unlock()
	}()
	g.n = 0
}

// A5: a plain struct with no lock at all, captured by a goroutine from a receiver.
type G5 struct{ n int }

func (g *G5) Go() {
	go func() { g.n++ }()
}
