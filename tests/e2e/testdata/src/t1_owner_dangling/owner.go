package t1_owner_dangling

import (
	"github.com/bharathvbcr/gocap/cap"
)

// TC-T1-3.1: Mutation of owner while read-only borrow is active rejected (BORROW003)
func MutateOwnerDuringRob() {
	x := 10
	//gocap:rob
	r := &x
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	println(*r)
}

// TC-T1-3.2: Mutation of owner while mutable borrow is active rejected (BORROW003)
func MutateOwnerDuringMub() {
	x := 10
	//gocap:mut
	m := &x
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	*m = 30
}

// TC-T1-3.3: Move/channel send of owner while borrowed rejected (BORROW003)
func MoveOwnerDuringBorrow(ch chan int) {
	x := 10
	//gocap:rob
	r := &x
	ch <- x // want `BORROW003: cannot move 'x' because it is borrowed`
	println(*r)
}

// TC-T1-3.4: Returning reference to local stack variable rejected (BORROW004)
func ReturnLocalStackRef() *int {
	x := 10
	//gocap:rob
	return &x // want `BORROW004: cannot return reference to local stack variable 'x'`
}

// TC-T1-3.5: Returning pointer to stack-allocated struct field rejected (BORROW004)
type LocalData struct {
	Val int
}

func ReturnStackFieldRef() *int {
	d := LocalData{Val: 42}
	//gocap:rob
	return &d.Val // want `BORROW004: cannot return reference to local stack variable`
}

// TC-T1-3.6: cap.BorrowMut owner mutation rejected (BORROW003)
func MarkerMutateOwner() {
	x := 10
	b := cap.BorrowMut(&x)
	x = 20 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	*b = 30
}

// TC-T1-3.7: Double pointer alias owner mutation rejected (BORROW003)
func DoublePointerAliasOwnerMutation() {
	x := 10
	b := cap.BorrowMut(&x)
	pp := &b
	x = 30 // want `BORROW003: cannot assign to 'x' because it is borrowed`
	**pp = 40
}

// TC-T1-3.8: Branch-merged Phi dangling stack pointer return rejected (BORROW004)
func DanglingPhiReturn(cond bool) *int {
	x, y := 10, 20
	var p *int
	if cond {
		p = &x
	} else {
		p = &y
	}
	return p // want `BORROW004: cannot return reference to local stack variable '[xy]'`
}

// TC-T1-3.9: Slice clear() while element is borrowed rejected (BORROW003)
func SliceClearWhileBorrowed() {
	s := []int{1, 2, 3}
	r := cap.Borrow(&s[0])
	clear(s) // want `BORROW003: cannot mutate 's' via clear\(\) because it is borrowed`
	_ = *r
}
