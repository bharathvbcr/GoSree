// WITNESS: panic
//
// An allocation size that wraps: 65537 records of 65536 bytes is 4 GiB + 64 KiB, which in int32
// arithmetic is 65536. The buffer is a thousandth of what the caller believes it allocated; the
// write of the second record fails Go's bounds check. Go stays memory-safe here (the bounds check
// is the backstop), but the overflow itself is silent. Rust panics on the multiplication in a
// debug build and offers checked_mul in release. gosree has no rule; go vet has none.
package main

import "fmt"

func alloc(count, size int32) []byte {
	n := count * size // MISS(NORULE): wraps silently
	return make([]byte, n)
}

func main() {
	const count, size = 65537, 65536
	buf := alloc(count, size)
	fmt.Println("allocated", len(buf), "bytes for", count, "records")
	buf[2*size-1] = 1 // panic: index out of range [131071] with length 65536
}
