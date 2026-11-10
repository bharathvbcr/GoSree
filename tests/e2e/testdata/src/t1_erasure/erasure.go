package t1_erasure

// TC-T1-9.1: Directives in function declarations erase cleanly with 0 bytes/instructions overhead
//gosree:iso p
func DirectivesIsoParam(p *int) int {
	return *p
}

// TC-T1-9.2: Legacy //safe:* directives backward compatibility
//safe:iso q
func LegacySafeParam(q *int) int {
	return *q
}

//safe:imm
type LegacyImmType struct {
	Val int
}

// TC-T1-9.3: //gosree:unsafe <reason> suppresses diagnostics for attached statement
func UnsafeEscapeHatch() {
	x := 10
	//gosree:mut
	b1 := &x
	//gosree:unsafe deliberate alias test for benchmarking
	//gosree:mut
	b2 := &x
	*b1 = 20
	*b2 = 30
}

// TC-T1-9.4: Standard Go code structure verifies clean compilation and zero-cost behavior
type StandardTuple struct {
	A int
	B string
}

func CleanStandardGo(t StandardTuple) (int, string) {
	return t.A, t.B
}

// TC-T1-9.5: Clean type check verification
func StandardCaller() int {
	x := 42
	return DirectivesIsoParam(&x)
}
