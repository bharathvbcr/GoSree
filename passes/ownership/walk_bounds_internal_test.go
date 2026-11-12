package ownership

import "testing"

// A callable handed on through more callables than evalValueAsCallable follows is not worked out.
// It used to answer "no contract" and nothing else, which reads as a call that publishes nothing.
// It now also marks the question as cut, as a trace out of budget does, so the checks that ask
// (resolveCallContractCut) report the call as one whose callees could not all be found.
func TestCallableNestedPastTheLimitIsACut(t *testing.T) {
	pkg := buildWalk(t, "package p\n\nfunc f() {}\n")
	fn := pkg.Func("f")
	st := &state{}
	if _, ok := st.evalValueAsCallable(fn, fn, nil, 7); ok {
		t.Fatal("a callable past the limit answered with a contract")
	}
	if !st.originCut {
		t.Error("a callable past the limit did not mark the question as cut")
	}

	// Within the limit, a plain function has no contract and nothing is cut.
	st = &state{}
	if _, ok := st.evalValueAsCallable(fn, fn, nil, 0); ok {
		t.Fatal("a function with no contract answered with one")
	}
	if st.originCut {
		t.Error("a function within the limit was marked as cut")
	}
}
