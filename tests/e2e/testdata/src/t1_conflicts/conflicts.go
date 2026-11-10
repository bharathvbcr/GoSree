package t1_conflicts

// TC-T1-2.1: Simultaneous multiple mutable borrows of same allocation rejected (BORROW002)
func SimultaneousMut() {
	x := 10
	//gocap:mut
	b1 := &x
	//gocap:mut
	b2 := &x // want `BORROW002: cannot borrow 'x' as mutable more than once at a time`
	*b1 = 20
	*b2 = 30
}

// TC-T1-2.2: Mutable borrow aliasing active read-only borrow rejected (BORROW002)
func MutAliasingReadOnly() {
	x := 10
	//gocap:rob
	r := &x
	//gocap:mut
	m := &x // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	println(*r)
	*m = 20
}

// TC-T1-2.3: Read-only borrow aliasing active mutable borrow rejected (BORROW002)
func ReadOnlyAliasingMut() {
	x := 10
	//gocap:mut
	m := &x
	//gocap:rob
	r := &x // want `BORROW002: cannot borrow 'x' as read-only because it is also borrowed as mutable`
	*m = 20
	println(*r)
}

// TC-T1-2.4: Three simultaneous borrows (2 read, 1 mut) rejected at mut (BORROW002)
func ThreeBorrows() {
	x := 10
	//gocap:rob
	r1 := &x
	//gocap:rob
	r2 := &x
	//gocap:mut
	m := &x // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	println(*r1 + *r2)
	*m = 30
}

// TC-T1-2.5: Re-borrowing mutably from an active read-only borrow rejected (BORROW002)
func ReborrowMutFromRob() {
	x := 10
	//gocap:rob
	r := &x
	//gocap:mut
	m := r // want `BORROW002: cannot borrow 'x' as mutable because it is also borrowed as read-only`
	println(*r)
	*m = 20
}
