// Package errfe6e holds the ERR005 shapes ft-fe6e reports: the write and the Close that drops its
// error sit in a creator and in a closure that the creator runs at one call, on every path to a
// return, and whose value goes nowhere else. Each is reported at the Close, once.
package errfe6e

import "io"

func open() io.WriteCloser { return nil }

// A closure writes and the creator closes, directly or by defer.
func closureWritesCreatorCloses(w io.WriteCloser) {
	func() { _, _ = w.Write(nil) }()
	w.Close() // want "ERR005"
}

func closureWritesCreatorDefers(w io.WriteCloser) {
	defer w.Close() // want "ERR005"
	func() { _, _ = w.Write(nil) }()
}

func closureValueCalledOnce(w io.WriteCloser) {
	write := func() { _, _ = w.Write(nil) }
	write()
	w.Close() // want "ERR005"
}

// The creator writes and a closure closes, directly or by defer. A defer in the closure runs when
// the closure returns, so the report is at the call, not at the creator's exit.
func creatorWritesClosureCloses(w io.WriteCloser) {
	_, _ = w.Write(nil)
	func() { w.Close() }() // want "ERR005"
}

func creatorWritesClosureDefers(w io.WriteCloser) {
	_, _ = w.Write(nil)
	func() { defer w.Close() }() // want "ERR005"
}

func localCapturedByClosure() {
	w := open()
	cl := func() { defer w.Close() } // want "ERR005"
	_, _ = w.Write(nil)
	cl()
}

// A write after the call is not in view when the closure's deferred Close runs.
func closureDefersCreatorWritesAfter(w io.WriteCloser) {
	cl := func() { defer w.Close() }
	cl()
	_, _ = w.Write(nil)
}

// Two layers: the middle closure only passes the variable on.
func writeTwoLayersDown(w io.WriteCloser) {
	func() {
		func() { _, _ = w.Write(nil) }()
	}()
	w.Close() // want "ERR005"
}

func closeTwoLayersDown(w io.WriteCloser) {
	_, _ = w.Write(nil)
	func() {
		func() { w.Close() }() // want "ERR005"
	}()
}

// The creator is itself a closure that escapes; the closure inside it is tracked there.
func escapingCreator(w io.WriteCloser) func() {
	return func() {
		func() { _, _ = w.Write(nil) }()
		w.Close() // want "ERR005"
	}
}

// Several closures write, a third closes, each called once in order.
func threeClosures(w io.WriteCloser) {
	a := func() { _, _ = w.Write(nil) }
	b := func() { _, _ = w.Write(nil) }
	c := func() { w.Close() } // want "ERR005"
	a()
	b()
	c()
}

// Only the variable that was written is reported.
func twoVariables(a, b io.WriteCloser) {
	func() { _, _ = a.Write(nil) }()
	b.Close()
	a.Close() // want "ERR005"
}

// A closure that writes and closes is judged by its own flow: one report.
func closureWritesAndCloses(w io.WriteCloser) {
	func() {
		_, _ = w.Write(nil)
		w.Close() // want "ERR005"
	}()
}

func closureWritesAndDefers(w io.WriteCloser) {
	func() {
		_, _ = w.Write(nil)
		defer w.Close() // want "ERR005"
	}()
}
