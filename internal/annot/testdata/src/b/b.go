package b

// -------------------------------------------------------------
// Positive Parameter Directives
// -------------------------------------------------------------

//gosree:mub p
func paramMub(p *int) { *p = 1 }

//gosree:mut p
func paramMut(p *int) { *p = 2 }

//gosree:rob p
func paramRob(p *int) int { return *p }

//gosree:ref p
func paramRef(p *int) int { return *p }

//gosree:mut p, q
func multiMut(p, q *int) {}

//gosree:rob p, q
func multiRob(p, q *int) {}

//gosree:mut p
//gosree:rob q
func mixedParams(p, q *int) {}

// Legacy prefix parameter directives
//safe:mub p
func legacyMub(p *int) {}

//safe:rob p
func legacyRob(p *int) {}

// Method receiver parameter directive
type Node struct{ val int }

//gosree:mut n
func (n *Node) MutReceiver() { n.val = 1 }

//gosree:rob n
func (n *Node) RobReceiver() int { return n.val }

// -------------------------------------------------------------
// Positive Local Assignment Directives
// -------------------------------------------------------------

func localAssignments() {
	x := 42

	// Preceding line comment
	//gosree:mut
	b1 := &x

	// Preceding line comment with rob
	//gosree:rob
	b2 := &x

	// Trailing line comment
	b3 := &x //gosree:mut

	// Trailing line comment with rob
	b4 := &x //gosree:rob

	// Inline comment before statement on same line
	/*gosree:mut*/ b5 := &x

	// Inline comment inside statement
	b6 := /*gosree:rob*/ &x

	// Var declaration
	//gosree:mut
	var b7 = &x

	// Legacy prefix on local assignment
	//safe:mut
	b8 := &x

	// Explicit LHS name in argument
	//gosree:mut b9
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

//gosree:iso p
func canonicalIso(p *int) {}

//gosree:imm
type ImmType struct{ n int }

func safeUnsafeEscape() {
	//gosree:unsafe reason for escape
	_ = 1
}

// -------------------------------------------------------------
// Negative Cases (SAFE007 Diagnostics)
// -------------------------------------------------------------

// Unknown directive
//gosree:bogus // want `SAFE007: unknown directive //gosree:bogus`
func badDirective() {}

// Func with mut but missing parameter names
//gosree:mut // want `SAFE007: //gosree:mut needs at least one parameter name`
func missingParam(p *int) {}

// Func with rob but missing parameter names
//gosree:rob // want `SAFE007: //gosree:rob needs at least one parameter name`
func missingParamRob(p *int) {}

// Func with mut naming non-existent parameter
//gosree:mut nonExistent // want `SAFE007: //gosree:mut names "nonExistent", which is not a parameter of badParamName`
func badParamName(p *int) {}

// Func with rob naming non-existent parameter
//gosree:rob nonExistent // want `SAFE007: //gosree:rob names "nonExistent", which is not a parameter of badRobParamName`
func badRobParamName(p *int) {}

// Conflicting directives on same parameter: mut and rob
//gosree:mut p
//gosree:rob p // want `SAFE007: //gosree:rob names "p", which is already annotated as mutable borrow`
func conflictMutRob(p *int) {}

// Conflicting directives on same parameter: iso and mut
//gosree:iso p
//gosree:mut p // want `SAFE007: //gosree:mut names "p", which already has an iso capability annotation`
func conflictIsoMut(p *int) {}

// Conflicting directives on same parameter: mut and iso
//gosree:mut p
//gosree:iso p // want `SAFE007: //gosree:iso names "p", which already has a conflicting capability/borrow annotation`
func conflictMutIso(p *int) {}

// Mut directive on type declaration (invalid target)
//gosree:mut // want `SAFE007: //gosree:mut must be attached to a function declaration or an assignment statement`
type BadTypeTarget struct{}

// Rob directive on return statement (invalid target)
func badReturnTarget() {
	//gosree:rob // want `SAFE007: //gosree:rob must be attached to a function declaration or an assignment statement`
	return
}

// Assignment with mismatched variable name argument
func mismatchedAssignArg() {
	x := 10
	//gosree:mut wrongVar // want `SAFE007: //gosree:mut on assignment names "wrongVar", which does not match assigned variable`
	b := &x
	_ = b
}

// Dangling borrow directive at end of function
func danglingBorrow() {
	_ = 1
	//gosree:mut // want `SAFE007: //gosree:mut is not attached to a function or assignment`
}
