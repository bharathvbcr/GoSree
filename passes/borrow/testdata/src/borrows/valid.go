package borrows

// TC-VALID-01: Valid borrow parameter passing without lifetime annotations
//gocap:mut p
func bump(p *int) {
	*p++
}

func testValidBorrowPassing() {
	x := 10
	bump(&x)
}

// TC-VALID-02: Non-Lexical Lifetime (NLL) sequential expiry
func testNonLexicalLifetimeExpiry() {
	x := 1
	b1 := &x /*gocap:mut*/
	*b1 = 2 // b1's last use; borrow ends immediately here
	b2 := &x /*gocap:mut*/
	*b2 = 3 // b2 validly borrows x because b1 is dead
}

// TC-VALID-03: Multiple concurrent read-only borrows
func testMultiReadBorrowAllow() {
	x := 1
	r1 := &x /*gocap:rob*/
	r2 := &x /*gocap:rob*/
	println(*r1 + *r2) // both active simultaneously without conflict
}

// Valid owner mutation after borrow expired
func testOwnerMutationAfterDrop() {
	x := 1
	r := &x /*gocap:rob*/
	println(*r) // last use of r
	x = 2       // valid: r is dead at this point
}

// Loop borrow with clean per-iteration lifetime
func testLoopBorrow() {
	x := 0
	for i := 0; i < 5; i++ {
		b := &x /*gocap:mut*/
		*b += i // b dies at end of each iteration; next iteration is clean
	}
}

// Branch-local borrow
func testBranchLocalBorrow(cond bool) {
	x := 1
	if cond {
		b := &x /*gocap:mut*/
		*b = 10
	} else {
		b2 := &x /*gocap:mut*/
		*b2 = 20
	}
}

// Read-only borrow parameter passing
//gocap:rob p
func readVal(p *int) int {
	return *p
}

func testReadOnlyParam() {
	x := 42
	_ = readVal(&x)
}
