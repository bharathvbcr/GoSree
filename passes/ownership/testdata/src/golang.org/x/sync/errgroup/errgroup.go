// Package errgroup is a stub of golang.org/x/sync/errgroup with the same method names, so the
// callbacks test can exercise the spawner table without a dependency on x/sync.
package errgroup

// Group mirrors errgroup.Group.
type Group struct{ err error }

// Go runs f on a new goroutine in the real package.
func (g *Group) Go(f func() error) { g.err = f() }

// TryGo runs f on a new goroutine in the real package when the limit allows.
func (g *Group) TryGo(f func() error) bool { g.err = f(); return true }

// Wait blocks until every function started by Go has returned.
func (g *Group) Wait() error { return g.err }
