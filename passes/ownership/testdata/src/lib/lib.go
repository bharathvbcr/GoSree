package lib

type Thing struct {
	n    int
	data []byte
}

var kept []*Thing

// NewThing returns a fresh allocation.
func NewThing() *Thing { return &Thing{data: make([]byte, 8)} }

// Shared returns memory the package keeps.
func Shared() *Thing { return kept[0] }

// Keep retains its argument.
func Keep(t *Thing) { kept = append(kept, t) }

// Size only reads its argument.
func Size(t *Thing) int { return t.n + len(t.data) }

// Consume takes ownership of its argument.
//
//safe:iso t
func Consume(ch chan *Thing, t *Thing) { ch <- t }
