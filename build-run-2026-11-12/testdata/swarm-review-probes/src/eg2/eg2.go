// Package eg2: AutoSync probes. Each type looks self-locked but leaks or reads without the lock.
package eg2

import "sync"

// H1: every write under mu, but Err reads without the lock.
type H1 struct {
	mu  sync.Mutex
	err error
}

func (g *H1) Err() error { return g.err }

func (g *H1) Go(f func() error) {
	go func() {
		if err := f(); err != nil {
			g.mu.Lock()
			g.err = err
			g.mu.Unlock()
		}
	}()
}

// H2: a locked getter hands out the inner pointer, which is then written outside the lock.
type inner struct{ n int }

type H2 struct {
	mu sync.Mutex
	p  *inner
}

func (g *H2) P() *inner { g.mu.Lock(); defer g.mu.Unlock(); return g.p }

func (g *H2) Go() {
	go func() {
		g.mu.Lock()
		g.p.n++
		g.mu.Unlock()
	}()
	g.P().n = 0 // race: written outside the lock
}

// H3: locked everywhere except an exported field anyone can write.
type H3 struct {
	mu sync.Mutex
	N  int
	m  int
}

func (g *H3) Go() {
	go func() {
		g.mu.Lock()
		g.m++
		g.mu.Unlock()
		g.N++ // unlocked write of an exported field
	}()
}

// H4: RWMutex, but the goroutine writes under RLock.
type H4 struct {
	mu sync.RWMutex
	n  int
}

func (g *H4) Go() {
	go func() {
		g.mu.RLock()
		g.n++ // write under a read lock: concurrent Go calls race
		g.mu.RUnlock()
	}()
}

// H5: two mutexes, field written under different ones.
type H5 struct {
	a, b sync.Mutex
	n    int
}

func (g *H5) Go() {
	go func() {
		g.a.Lock()
		g.n++
		g.a.Unlock()
	}()
	g.b.Lock()
	g.n++ // race: a different lock
	g.b.Unlock()
}

// H6: the struct is copied by value (copies the mutex) and the copy's field written.
type H6 struct {
	mu sync.Mutex
	n  int
}

func (g *H6) Go() {
	go func() {
		g.mu.Lock()
		g.n++
		g.mu.Unlock()
	}()
}

func UseH6() {
	g := &H6{}
	g.Go()
	g.n = 3 // race: outside the lock, from outside the methods
}

// H7: errgroup shape, with err guarded, but the closure also writes a captured slice element.
type H7 struct {
	wg  sync.WaitGroup
	mu  sync.Mutex
	err error //gosree:guarded_by mu
	out []int
}

func (g *H7) Go(i int, f func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.out = append(g.out, i) // unlocked append to a field: concurrent Go calls race
		if err := f(); err != nil {
			g.mu.Lock()
			g.err = err
			g.mu.Unlock()
		}
	}()
}
