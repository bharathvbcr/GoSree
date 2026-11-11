package errsameline

import (
	"compress/gzip"
	"io"
	"os"
)

func fail() error { return nil }

func okErr() error { return nil }

// The inner constructor call shares the line with the deferred Close and must not consume it.
func deferGzipChain(f *os.File) {
	defer gzip.NewWriter(f).Close() // want "ERR005"
}

func gzipChainStatement(f *os.File) {
	gzip.NewWriter(f).Close() // want "ERR005"
}

// Two discarded error calls on one line: each is reported by its own call.
func twoOnOneLine() {
	fail(); okErr() // want "ERR001"
}

// An error-free call on the same line as a discarded error call must not take its place.
func mixedLine(w io.Writer) {
	_ = len("x"); fail() // want "ERR001"
}
