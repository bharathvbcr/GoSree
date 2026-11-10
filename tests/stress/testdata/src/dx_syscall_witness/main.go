// Memory corruption with no package unsafe and no flagged reflect call: reflect.Value.Pointer hands
// out a raw address as a uintptr, and a raw read(2) writes 16 bytes into an 8-byte array, over the
// pointer that follows it. The program then dereferences the forged pointer.
//
// WITNESS: panic
package main

import (
	"os"
	"reflect"
	"syscall"
)

type victim struct {
	buf [8]byte
	p   *int
}

func main() {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	junk := make([]byte, 16)
	for i := range junk {
		junk[i] = 0x41
	}
	if _, err := w.Write(junk); err != nil {
		panic(err)
	}
	x := 7
	v := &victim{p: &x}
	addr := reflect.ValueOf(&v.buf).Pointer()           // MISS(SAFE001): Value.Pointer exposes the raw address, the twin of the listed Value.UnsafeAddr
	syscall.Syscall(syscall.SYS_READ, r.Fd(), addr, 16) // MISS(NORULE): a raw syscall writes 16 bytes at a uintptr address into an 8-byte field; no rule covers syscall.Syscall
	println(*v.p)
}
