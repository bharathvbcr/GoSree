// Package rs_arith: Rust panics on integer overflow in debug builds, has checked_*/wrapping_*
// for release builds, makes `as` truncation explicit, and panics on an out-of-range index and on
// division by zero. Go wraps silently, truncates on conversion, and panics on the last two at
// run time. Rust's static guarantee is the same as Go's for bounds and division (both are run-time
// panics, not memory corruption); the difference is overflow. gosree has no arithmetic rule; go
// vet has none for overflow.
package rs_arith

// 1. allocation-size multiplication wraps (rs_w_overflow runs it).
func Alloc(count, size int32) []byte {
	n := count * size // MISS(NORULE): wraps to a small or negative number; Rust debug panics, release has checked_mul
	if n < 0 {
		return nil
	}
	return make([]byte, n)
}

// 2. int32(len(x)) truncation on a length prefix.
func LengthPrefix(payload []byte) int32 {
	return int32(len(payload)) // MISS(NORULE): silently truncates above 2 GiB; Rust's `as` truncates too, try_from does not (a partial draw)
}

// 3. division by zero.
func Average(sum, n int) int {
	return sum / n // MISS(NORULE): panics for n == 0; Rust panics too (a draw)
}

// 4. index out of range.
func Last(xs []int) int {
	return xs[len(xs)-1] // MISS(NORULE): panics on an empty slice; Rust panics too (a draw)
}

// 5. uint underflow in a loop bound.
func Pairs(xs []int) int {
	sum := 0
	for i := uint(0); i < uint(len(xs))-1; i++ { // MISS(NORULE): for an empty slice the bound wraps to MaxUint and xs[i] panics; Rust debug panics on the subtraction
		sum += xs[i] * xs[i+1]
	}
	return sum
}
