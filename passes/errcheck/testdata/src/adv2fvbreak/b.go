// Package adv2fvbreak: BREAK of 9244bb2. Reassigning a captured variable resets the shared written
// flag even when the stored value is itself a written resource, so a true ERR005 that aabf10f
// reported (by accident: the flag left over from the earlier w.Write) is lost. Correct: ERR005 on the
// marked Close. The test fails until the reset takes the stored value into account.
package adv2fvbreak

import "io"

// P1b: w written earlier AND other written, then w = other, close. Correct: ERR005.
func p1b(w io.WriteCloser, other io.WriteCloser) func() {
	return func() {
		_, _ = w.Write(nil)
		_, _ = other.Write(nil)
		w = other
		w.Close() // want "ERR005"
	}
}
