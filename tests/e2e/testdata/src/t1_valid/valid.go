package t1_valid

// TC-T1-4.1: Passing &mut x to //gosree:mut parameter (Clean Pass without 'a)
//gosree:mut p
func Bump(p *int) {
	*p++
}

func CallBump() int {
	x := 10
	Bump(&x)
	return x
}

// TC-T1-4.2: Passing &x to //gosree:rob parameter (Clean Pass without 'a)
//gosree:rob p
func Inspect(p *int) int {
	return *p * 2
}

func CallInspect() int {
	x := 10
	return Inspect(&x)
}

// TC-T1-4.3: Non-lexical lifetime expiry: sequential reuse of x (Clean Pass)
func SequentialBorrows() int {
	x := 10
	//gosree:mut
	b1 := &x
	*b1 = 20 // b1 last use here
	//gosree:mut
	b2 := &x // valid because b1 is dead!
	*b2 = 30
	return x
}

// TC-T1-4.4: Multiple simultaneous read-only borrows without mutation (Clean Pass)
func MultipleReadBorrows() int {
	x := 10
	//gosree:rob
	r1 := &x
	//gosree:rob
	r2 := &x
	return *r1 + *r2
}

// TC-T1-4.5: Callee mutating target through *p and caller continuing cleanly (Clean Pass)
//gosree:mut p
func Double(p *int) {
	*p *= 2
}

func CallerContinuing() int {
	x := 5
	Double(&x)
	x += 3
	return x
}
