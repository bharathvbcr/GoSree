package advsameline

import (
	"io"
	"os"
)

func fail() error                          { return nil }
func other() error                         { return nil }
func two() (int, error)                    { return 0, nil }
func newW(w io.WriteCloser) io.WriteCloser { return w }
func use(error)                            {}

func handledThenBare() {
	err := fail(); other(); use(err) // want "ERR001.*other"
}

func bareThenHandled() {
	other(); err := fail(); use(err) // want "ERR001.*other"
}

func twoCloses(a, b io.WriteCloser) {
	_, _ = a.Write(nil); _, _ = b.Write(nil)
	a.Close(); b.Close() // want "ERR005.*resource a "
}

func mixed(w io.WriteCloser) {
	_, _ = w.Write(nil)
	fail(); w.Close() // want "ERR001.*fail" "ERR005"
}

func mixed2(w io.WriteCloser) {
	_, _ = w.Write(nil)
	w.Close(); fail() // want "ERR005" "ERR001.*fail"
}

func chain(w io.WriteCloser) {
	_, _ = w.Write(nil)
	newW(w).Close()
}

func chainDefer(w io.WriteCloser) {
	_, _ = w.Write(nil)
	defer newW(w).Close()
}

func closureClose(w io.WriteCloser) {
	_, _ = w.Write(nil)
	defer func() { w.Close() }()
}

func blankedPair() {
	a, _ := two(); b, err := two(); _, _ = a, b; use(err)
}

func blankedPair2() {
	b, err := two(); a, _ := two(); _, _ = a, b; use(err)
}

func createClose(p string) {
	f, _ := os.Create(p); f.Close() // want "ERR002" "ERR005"
}

func handledCloseThenBareClose(a, b io.WriteCloser) {
	_, _ = a.Write(nil); _, _ = b.Write(nil)
	if err := a.Close(); err != nil { use(err) }; b.Close() // want "ERR005.*resource b "
}
