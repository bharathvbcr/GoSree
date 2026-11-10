package b

// -------------------------------------------------------------
// Positive Parameter Directives
// -------------------------------------------------------------

//gocap:mub p
func paramMub(p *int) { *p = 1 }

//gocap:mut p
func paramMut(p *int) { *p = 2 }

//gocap:rob p
func paramRob(p *int) int { return *p }

//gocap:ref p
func paramRef(p *int) int { return *p }

//gocap:mut p, q
func multiMut(p, q *int) {}

//gocap:rob p, q
func multiRob(p, q *int) {}

//gocap:mut p
//gocap:rob q
func mixedParams(p, q *int) {}

// Legacy prefix parameter directives
//safe:mub p
func legacyMub(p *int) {}

//safe:rob p
func legacyRob(p *int) {}

// Method receiver parameter directive
type Node struct{ val int }

//gocap:mut n
func (n *Node) MutReceiver() { n.val = 1 }

//gocap:rob n
func (n *Node) RobReceiver() int { return n.val }

// -------------------------------------------------------------
// Positive Local Assignment Directives
// -------------------------------------------------------------

func localAssignments() {
	x := 42

	// Preceding line comment
	//gocap:mut
	b1 := &x

	// Preceding line comment with rob
	//gocap:rob
	b2 := &x

	// Trailing line comment
	b3 := &x //gocap:mut

	// Trailing line comment with rob
	b4 := &x //gocap:rob

	// Inline comment before statement on same line
	/*gocap:mut*/ b5 := &x

	// Inline comment inside statement
	b6 := /*gocap:rob*/ &x

	// Var declaration
	//gocap:mut
	var b7 = &x

	// Legacy prefix on local assignment
	//safe:mut
	b8 := &x

	// Explicit LHS name in argument
	//gocap:mut b9
	b9 := &x

	_ = b1
	_ = b2
	_ = b3
	_ = b4
	_ = b5
	_ = b6
	_ = b7
	_ = b8
	_ = b9
}

// -------------------------------------------------------------
// Positive Escape Hatch & Canonical Iso/Imm
// -------------------------------------------------------------

//gocap:iso p
func canonicalIso(p *int) {}

//gocap:imm
type ImmType struct{ n int }

func safeUnsafeEscape() {
	//gocap:unsafe reason for escape
	_ = 1
}

// -------------------------------------------------------------
// Negative Cases (SAFE007 Diagnostics)
// -------------------------------------------------------------

// Unknown directive
//gocap:bogus // want `SAFE007: unknown directive //gocap:bogus`
func badDirective() {}

// Func with mut but missing parameter names
//gocap:mut // want `SAFE007: //gocap:mut needs at least one parameter name`
func missingParam(p *int) {}

// Func with rob but missing parameter names
//gocap:rob // want `SAFE007: //gocap:rob needs at least one parameter name`
func missingParamRob(p *int) {}

// Func with mut naming non-existent parameter
//gocap:mut nonExistent // want `SAFE007: //gocap:mut names "nonExistent", which is not a parameter of badParamName`
func badParamName(p *int) {}

// Func with rob naming non-existent parameter
//gocap:rob nonExistent // want `SAFE007: //gocap:rob names "nonExistent", which is not a parameter of badRobParamName`
func badRobParamName(p *int) {}

// Conflicting directives on same parameter: mut and rob
//gocap:mut p
//gocap:rob p // want `SAFE007: //gocap:rob names "p", which is already annotated as mutable borrow`
func conflictMutRob(p *int) {}

// Conflicting directives on same parameter: iso and mut
//gocap:iso p
//gocap:mut p // want `SAFE007: //gocap:mut names "p", which already has an iso capability annotation`
func conflictIsoMut(p *int) {}

// Conflicting directives on same parameter: mut and iso
//gocap:mut p
//gocap:iso p // want `SAFE007: //gocap:iso names "p", which already has a conflicting capability/borrow annotation`
func conflictMutIso(p *int) {}

// Mut directive on type declaration (invalid target)
//gocap:mut // want `SAFE007: //gocap:mut must be attached to a function declaration or an assignment statement`
type BadTypeTarget struct{}

// Rob directive on return statement (invalid target)
func badReturnTarget() {
	//gocap:rob // want `SAFE007: //gocap:rob must be attached to a function declaration or an assignment statement`
	return
}

// Assignment with mismatched variable name argument
func mismatchedAssignArg() {
	x := 10
	//gocap:mut wrongVar // want `SAFE007: //gocap:mut on assignment names "wrongVar", which does not match assigned variable`
	b := &x
	_ = b
}

// Dangling borrow directive at end of function
func danglingBorrow() {
	_ = 1
	//gocap:mut // want `SAFE007: //gocap:mut is not attached to a function or assignment`
}
