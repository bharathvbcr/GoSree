package errsameline

import (
	"compress/gzip"
	"io"
	"os"
)

func fail() error { return nil }

func okErr() error { return nil }

func noop() {}

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
func mixedLine() {
	noop(); fail() // want "ERR001"
}

// Determinism: a call that cannot be a close shares its line with two discarded closes of
// different writable receivers. Whichever close that call could take by map order would change
// which receiver is reported, so each function is replicated and each receiver is pinned.
func shared00(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared01(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared02(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared03(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared04(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared05(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared06(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared07(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared08(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared09(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared10(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared11(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared12(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared13(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared14(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared15(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared16(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared17(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared18(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared19(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared20(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared21(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared22(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

func shared23(a, b io.WriteCloser) {
	_, _ = a.Write(nil)
	_, _ = b.Write(nil)
	noop(); a.Close(); defer b.Close() // want "ERR005.*resource a " "ERR005.*resource b "
}

