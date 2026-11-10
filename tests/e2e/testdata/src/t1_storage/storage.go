package t1_storage

// TC-T1-1.1: Struct field with //gocap:mut rejected (BORROW001)
type BadStructMut struct {
	//gocap:mut
	ptr *int // want `BORROW001: struct field 'ptr' cannot have second-class borrow type`
}

// TC-T1-1.2: Struct field with //gocap:rob rejected (BORROW001)
type BadStructRob struct {
	//gocap:rob
	ptr *int // want `BORROW001: struct field 'ptr' cannot have second-class borrow type`
}

// TC-T1-1.3: Package-level global variable with //gocap:mut rejected (BORROW001)
//gocap:mut
var GlobalMut *int // want `BORROW001: package variable 'GlobalMut' cannot store second-class borrow`

// TC-T1-1.4: Package-level global variable with //gocap:rob rejected (BORROW001)
//gocap:rob
var GlobalRob *int // want `BORROW001: package variable 'GlobalRob' cannot store second-class borrow`

// TC-T1-1.5: Assignment of borrow to package global inside function rejected (BORROW001)
var TargetGlobal *int

func AssignBorrowToGlobal() {
	x := 42
	//gocap:mut
	b := &x
	TargetGlobal = b // want `BORROW001: package variable 'TargetGlobal' cannot store second-class borrow`
}

// TC-T1-1.6: Transient stack-allocated tuple holding non-escaping borrow permitted (Clean Pass)
type StackTuple struct {
	P *int
}

func ValidStackTuple() int {
	x := 100
	t := StackTuple{P: &x}
	return *t.P
}

func ValidStackTupleWithBorrow() int {
	x := 100
	//gocap:mut
	b := &x
	t := StackTuple{P: b}
	*t.P = 200
	return *t.P
}
