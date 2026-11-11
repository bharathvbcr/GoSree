// Package svc holds mutually referencing types: a two-cycle whose post-construction write is on
// one member, and a three-cycle with a write on the last member.
package svc

// Server and Config reference each other; Config is written after construction.
type Server struct{ cfg *Config }

type Config struct {
	srv *Server
	n   int
}

func (c *Config) Bump() { c.n++ }

func New() *Server {
	s := &Server{}
	s.cfg = &Config{srv: s}
	return s
}

// A, B and C form a cycle; only C is written after construction.
type A struct{ b *B }

type B struct{ c *C }

type C struct {
	a *A
	n int
}

func (c *C) Bump() { c.n++ }

func Serve(a *Server, b *Config, x *A, y *B, z *C) {}
