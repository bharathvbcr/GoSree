// Package dep is the other package of lk_w_getter: a long-lived dependency and the gateway that
// hands it out.
package dep

import "sync"

// Loop is shared by every request. Run writes it with no lock.
type Loop struct{ runs int }

func (l *Loop) Run() { l.runs++ }

// NewLoop returns a loop nothing else holds.
func NewLoop() *Loop { return &Loop{} }

// Gateway holds the loop. Loop is exported so a caller can read the field; the methods return the
// same kind of pointer.
type Gateway struct {
	mu     sync.RWMutex
	Loop   *Loop
	plain  *Loop
	locked *Loop
}

func New() *Gateway {
	return &Gateway{Loop: &Loop{}, plain: &Loop{}, locked: &Loop{}}
}

// Plain returns the pointer with no lock.
func (g *Gateway) Plain() *Loop { return g.plain }

// Locked reads the pointer under the lock, as a getter for a swappable dependency does. The lock
// protects the pointer, not what it points to.
func (g *Gateway) Locked() *Loop {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.locked
}

// Lookup returns the loop and whether it is set.
func (g *Gateway) Lookup() (*Loop, bool) { return g.plain, g.plain != nil }

// Pair returns a loop nothing else holds and the shared one, in that order.
func (g *Gateway) Pair() (*Loop, *Loop) { return &Loop{}, g.plain }

// Self returns the gateway.
func (g *Gateway) Self() *Gateway { return g }

// Box is a wrapper made for one caller around the shared loop.
type Box struct {
	L *Loop
	n int
}

// Boxed returns a new Box that holds the shared loop.
func (g *Gateway) Boxed() *Box { return &Box{L: g.plain} }

// Touch writes the box, not the loop.
func (b *Box) Touch() { b.n++ }
