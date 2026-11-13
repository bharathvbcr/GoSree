package errc7ddadvmiss

import "io"

type H struct{ w io.WriteCloser }

func open() io.WriteCloser { return nil }

// NEW MISS (reported on 90fa6e9, silent at 498984f): the alias a holds the written resource; the
// field is replaced afterwards, and the store resets the written flag of the path that a is keyed
// to. The plain twin below is still reported.
func aliasThenReplace(h *H) {
	a := h.w
	_, _ = a.Write(nil)
	h.w = open()
	a.Close() // want "ERR005"
}

func aliasThenReplaceTwin(cur io.WriteCloser) {
	a := cur
	_, _ = a.Write(nil)
	cur = open()
	a.Close() // want "ERR005"
}

// Same through a pointer: a := *p, then *p is replaced.
func aliasThenReplacePtr(p *io.WriteCloser) {
	a := *p
	_, _ = a.Write(nil)
	*p = open()
	a.Close() // want "ERR005"
}

// Same in a loop: each iteration writes the resource it holds, replaces the field, closes the old one.
func aliasThenReplaceLoop(h *H) {
	for i := 0; i < 2; i++ {
		a := h.w
		_, _ = a.Write(nil)
		h.w = open()
		a.Close() // want "ERR005"
	}
}
