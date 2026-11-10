package st

import "unsafe"

type Job struct{ ID int }

// Mutes an unsafeban finding: not stale.
func cast(p *int) uintptr {
	//gosree:unsafe p outlives the call
	return uintptr(unsafe.Pointer(p))
}

// Mutes an ownership finding (a use after the send): not stale.
func send(ch chan *Job) {
	j := &Job{}
	ch <- j
	//gosree:unsafe the receiver only reads ID
	j.ID = 1
}

// Mutes a borrow conflict (BORROW002): not stale.
func alias() {
	x := 1
	//gosree:unsafe deliberate alias for performance
	b1 := &x /*gosree:mut*/
	b2 := &x /*gosree:mut*/
	*b1 = 2
	*b2 = 3
}

// Mutes a borrow storage finding (BORROW001): not stale.
//
//gosree:unsafe reviewed: the field never outlives the call that sets it
type Holder struct {
	//gosree:mut
	p *int
}

// Mutes nothing.
func plain() int {
	//gosree:unsafe nothing here needs it // want `SAFE007: stale escape hatch`
	return 1
}

// A hatch in a declaration's doc comment covers the //go:linkname line above it: not stale.
//
//go:linkname nanotime runtime.nanotime
//gosree:unsafe reviewed: monotonic clock read
func nanotime() int64
