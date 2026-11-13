package errc7ddadvdup

import "io"

// One Close statement, one diagnostic. d1, d2 and d5 (a deferred closure that writes and then
// closes) report the Close twice at 498984f: once from the closure's own flow, once from the replay of
// its Close as a deferred close in the creator. d3, d4 and d6 report once.
func d1(w io.WriteCloser) {
	defer func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}()
}

func d1Twin(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close() // want "ERR005"
}

func d2(ws []io.WriteCloser) {
	for _, w := range ws {
		defer func() {
			_, _ = w.Write(nil)
			w.Close() // want "ERR005"
		}()
	}
}

func d3(w io.WriteCloser) {
	_, _ = w.Write(nil)
	defer func() {
		w.Close() // want "ERR005"
	}()
}

func d4(w io.WriteCloser) {
	func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}()
}

func d5(w io.WriteCloser) {
	cl := func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}
	defer cl()
}

func d6(w io.WriteCloser) {
	defer func() {
		_, _ = w.Write(nil)
		defer w.Close() // want "ERR005"
	}()
}
