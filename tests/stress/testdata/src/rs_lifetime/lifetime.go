// Package rs_lifetime: Rust ties every borrow to the lifetime of what it borrows, and close/drop
// consumes the value, so a closed file, a dropped sender or a buffer handed back cannot be used
// again. Go has a garbage collector, so these are not use-after-free of memory, but each is a use
// after the value's logical end of life, and two of them (Scanner.Bytes, Buffer.Bytes) are real
// dangling borrows: the bytes change under the holder. gocap has no rule for any of them outside
// goroutine transfers.
package rs_lifetime

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"sync"
)

// 1. use after Close.
func ReadAfterClose(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	f.Close()
	return io.ReadAll(f) // MISS(NORULE): f is closed; Rust's drop(f) moves f, so this would not compile
}

// 2. send on a closed channel panics.
func SendAfterClose(ch chan int) {
	close(ch)
	ch <- 1 // MISS(NORULE): panics; Rust's drop(tx) consumes the sender
}

// 3. double close panics.
func DoubleClose(ch chan int) {
	close(ch)
	close(ch) // MISS(NORULE): panics; Rust's drop takes ownership once
}

// 4. use after sync.Pool.Put: another goroutine may already hold the buffer.
var bufs = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func Encode(s string) []byte {
	b := bufs.Get().(*bytes.Buffer)
	b.Reset()
	b.WriteString(s)
	bufs.Put(b)
	return b.Bytes() // MISS(SAFE003): b was given to the pool, which hands it to other goroutines (seed_moves 14 is the same hole)
}

// 5. Scanner.Bytes kept across the next Scan: the slice is overwritten (rs_w_scanner runs it).
func Lines(r io.Reader) [][]byte {
	var out [][]byte
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		out = append(out, sc.Bytes()) // MISS(NORULE): the bytes are valid only until the next Scan; Rust's lending iterator ties them to it
	}
	return out
}

// 6. bytes.Buffer.Bytes kept across a later write.
func Header() []byte {
	var b bytes.Buffer
	b.WriteString("v1")
	h := b.Bytes()
	b.Reset()
	b.WriteString("v2")
	return h // MISS(NORULE): h aliases b's array, which now holds "v2"; Rust: b cannot be written while h borrows it
}

// 7. a sub-slice of a pooled buffer returned to the caller.
var scratch = sync.Pool{New: func() any { b := make([]byte, 0, 256); return &b }}

func Format(name string) []byte {
	bp := scratch.Get().(*[]byte)
	b := append((*bp)[:0], "hello, "...)
	b = append(b, name...)
	*bp = b
	scratch.Put(bp)
	return b[:len(b)] // MISS(SAFE003): the caller keeps memory the pool will hand to the next Get
}

// 8. closure capturing a variable reassigned later.
func Callbacks() []func() int {
	var fs []func() int
	x := 1
	fs = append(fs, func() int { return x })
	x = 2 // MISS(NORULE): the closure sees 2, not 1; Rust's closure borrows x and forbids this write (or moves a copy)
	return fs
}
