package moves

// Argument and result linking: a callee that joins two of its arguments, or returns memory of one,
// has that link in its summary, and the caller joins the same values instead of treating them as
// escaping or foreign.

type Tree struct {
	kids []*Tree
	val  []byte
}

// add stores child into root: it links them, it does not leak child.
func (t *Tree) add(child *Tree) { t.kids = append(t.kids, child) }

// first returns memory of t: its result is linked to t, not foreign.
func (t *Tree) first() *Tree { return t.kids[0] }

type keeper interface{ Keep(*Tree) }

// plant links root and child and also hands root to an interface method that may keep it: that is
// still a leak, of child too.
func plant(root, child *Tree, k keeper) {
	root.add(child)
	k.Keep(root)
}

// Two fresh trees joined by add are one owned region: moving the root moves the child with it.
func addThenMove(ch chan *Tree) {
	root, child := &Tree{}, &Tree{}
	root.add(child)
	ch <- root
}

// A child reached through first is owned when the root is, and moving it moves the root's memory.
func firstThenMove(ch chan *Tree) {
	root := &Tree{}
	root.add(&Tree{})
	first := root.first()
	ch <- first
	_ = root.val // want `SAFE003: composite literal is used after it was sent on a channel`
}

// Joining a fresh value to a parameter's memory makes it the caller's memory too.
func linkToParam(ch chan *Tree, b *Tree) {
	x := &Tree{}
	b.add(x)
	ch <- x // want `SAFE002: composite literal is sent on a channel but is not owned here: it may alias parameter b`
}

var s1, s2, s3, s4, s5, s6 = &Tree{}, &Tree{}, &Tree{}, &Tree{}, &Tree{}, &Tree{}

// pick may return a package variable or memory of t through one variable: its result is not fresh,
// it is still linked to t, and t leaks (its memory is merged with the package variables'). All of
// it must be recorded, whichever the summary sees first; with six package variables in the region, a
// summary that stopped at the first foreign event would miss the link on most runs.
func pick(t *Tree, alt int) *Tree {
	r := t.first()
	switch alt {
	case 1:
		r = s1
	case 2:
		r = s2
	case 3:
		r = s3
	case 4:
		r = s4
	case 5:
		r = s5
	case 6:
		r = s6
	}
	return r
}

func pickThenMove(ch chan *Tree) {
	root := &Tree{}
	root.add(&Tree{})
	p := pick(root, 0)
	ch <- root // want `SAFE002: composite literal is sent on a channel but is not owned here: it may alias result of moves\.pick` `SAFE004: composite literal is sent on a channel but also escapes: passed to moves\.pick`
	_ = p.val  // want `SAFE003: the result of pick is used after it was sent on a channel`
}

// A callee that links its arguments and keeps one of them in a package variable still leaks it.
func plantThenMove(ch chan *Tree, k keeper) {
	root, child := &Tree{}, &Tree{}
	plant(root, child, k)
	ch <- child // want `SAFE004: composite literal is sent on a channel but also escapes: passed to moves\.plant, which may retain or share it`
}
