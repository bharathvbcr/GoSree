package errc7ddadvgap

import "io"

type H struct{ w io.WriteCloser }

type outer struct{ in H }

type H2 struct {
	H
	n int
}

func open() io.WriteCloser { return nil }

// GAP (not a regression: 90fa6e9 reports nothing here either). A field of a field is a chain of
// field addresses, and the chain is not flattened: `&o.in` is a different SSA value at each use, so
// the two uses of o.in.w get different roots and different written-flag slots. The flat twin
// (h.w) is reported.
func nested(o *outer) {
	_, _ = o.in.w.Write(nil)
	o.in.w.Close() // want "ERR005"
}

func nestedValue() {
	var o outer
	o.in.w = open()
	_, _ = o.in.w.Write(nil)
	o.in.w.Close() // want "ERR005"
}

func embedded(h *H2) {
	_, _ = h.w.Write(nil)
	h.H.w.Close() // want "ERR005"
}

func flat(h *H) {
	_, _ = h.w.Write(nil)
	h.w.Close() // want "ERR005"
}
