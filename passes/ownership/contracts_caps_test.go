package ownership_test

import (
	"fmt"
	"strings"
	"testing"
)

// The contract resolution (contracts.go) stopped several walks at fixed sizes, and a walk that
// stopped answered as if it had finished. Each test below is a program just past one of those
// sizes, next to the same program well inside it, with the findings both must give.

// conv wraps expr in n conversions that alternate between the interface types a0 and a1.
func conv(expr string, n int) string {
	for i := range n {
		expr = fmt.Sprintf("a%d(%s)", i%2, expr)
	}
	return expr
}

const convTypes = "type a0 interface{}\n\ntype a1 interface{}\n\ntype T struct{ n int }\n\n"

// What is reported at a call whose callees could not all be found because a trace was cut: that an
// argument may be published by one not reached, and that the result may be loaded memory.
const (
	cutNote       = "what this call may run could not be followed to the end .a function or interface value comes through more than 14 steps or from more than 96 values., "
	wantCutArg    = "\"SAFE002: " + cutNote + "so whether something not reached publishes or keeps an argument passed here is not known\""
	wantCutResult = "\"SAFE005: " + cutNote + "so whether its result is memory loaded from a shared container is not known, and writes through the result are not checked\""
)

// TestPublishedValueThroughManyConversions: a string-like value is boxed, converted through 18
// interface types and stored in a sync.Map, and the box is used afterwards. A string is safe to
// share, so nothing is given away. Stripping the conversions to see that (unwrapPublishedArg) used
// to stop after 16, and the use after the store was reported.
func TestPublishedValueThroughManyConversions(t *testing.T) {
	var meths, ifaces strings.Builder
	for i := range 18 {
		fmt.Fprintf(&meths, "func (id) m%d() {}\n\n", i)
	}
	for i := 0; i <= 18; i++ {
		fmt.Fprintf(&ifaces, "type i%d interface {\n", i)
		for j := i; j < 18; j++ {
			fmt.Fprintf(&ifaces, "\tm%d()\n", j)
		}
		ifaces.WriteString("}\n\n")
	}
	box := "i0(v)"
	for i := 1; i <= 18; i++ {
		box = fmt.Sprintf("i%d(%s)", i, box)
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype id string\n\n"+meths.String()+ifaces.String()+
		"func far(m *sync.Map, s string) any {\n\tv := id(s)\n\tx := "+box+"\n\tm.Store(\"k\", x)\n\treturn x\n}\n\n"+
		"func near(m *sync.Map, s string) any {\n\tv := id(s)\n\tx := i1(i0(v))\n\tm.Store(\"k\", x)\n\treturn x\n}\n")
}

// TestContractIndexSettlesOnRotatedParameters: a helper calls its first function parameter with a
// value, or calls itself with its function parameters rotated by one. Which parameter ends up
// called is learnt one parameter per round, so 100 parameters need 100 rounds of the contract
// index. The rounds used to stop at four per function plus 16, and a sync.Map.Store passed as the
// last parameter was then not seen to publish the value.
//
// The index learns that from the calls; the trace of which function p0 may be goes through the
// same 100 calls and is cut by its depth, so both calls of the long helper also say that what they
// may run could not be followed.
func TestContractIndexSettlesOnRotatedParameters(t *testing.T) {
	rot := func(name string, n int) string {
		var ps, args []string
		for i := range n {
			ps = append(ps, fmt.Sprintf("p%d", i))
			args = append(args, fmt.Sprintf("p%d", (i+1)%n))
		}
		cutHere, cutToo := "", ""
		if n > 14 {
			cutHere, cutToo = " // want "+wantCutArg, " "+wantCutArg
		}
		return fmt.Sprintf("func %s(n int, v any, %s func(k, v any)) {\n\tif n == 0 {\n\t\tp0(\"k\", v)%s\n\t\treturn\n\t}\n"+
			"\t%s(n-1, v, %s) // want \"SAFE002: parameter v is published via\" \"SAFE004: parameter v is published via\"%s\n}\n\n",
			name, strings.Join(ps, ", "), cutHere, name, strings.Join(args, ", "), cutToo)
	}
	use := func(name, helper string, n int) string {
		return fmt.Sprintf("func %s(m *sync.Map) int {\n\tx := &T{}\n\t%s(%d, x, %sm.Store)\n"+
			"\treturn x.n // want \"SAFE003: composite literal is used after it was published via\"\n}\n\n",
			name, helper, n-1, strings.Repeat("noop, ", n-1))
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\nfunc noop(k, v any) {}\n\n"+
		rot("rotFar", 100)+rot("rotNear", 3)+use("far", "rotFar", 100)+use("near", "rotNear", 3))
}

// TestPoolGetterThroughManyConversions: two functions return what a sync.Pool hands out, through
// 14 conversions, and one of them is called through a variable. What a pool hands out belongs to
// the caller, so sending it on is fine. Seeing that the functions return it
// (returnedValueFreshness) used to stop 13 conversions in, and the send was reported.
func TestPoolGetterThroughManyConversions(t *testing.T) {
	getter := func(name string, n int) string {
		return "func " + name + "(p *sync.Pool) a1 {\n\treturn " + conv("p.Get()", n) + "\n}\n\n"
	}
	call := func(name, a, b string) string {
		return "func " + name + "(k bool, p *sync.Pool, ch chan *T) {\n\tf := " + a + "\n\tif k {\n\t\tf = " + b + "\n\t}\n\tx := f(p).(*T)\n\tch <- x\n}\n\n"
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\n"+convTypes+
		getter("getFar", 14)+getter("getFarB", 14)+getter("getNear", 2)+getter("getNearB", 2)+
		call("far", "getFar", "getFarB")+call("near", "getNear", "getNearB"))
}

// TestLoaderThroughManyConversions: two functions return what a sync.Map holds, through 14
// conversions, one of them is called through a variable, and the value is written. Finding the
// Load behind the returned value (collectReturnedCalls) used to stop 13 conversions in, and the
// write to shared memory was not reported.
func TestLoaderThroughManyConversions(t *testing.T) {
	loader := func(name string, n int) string {
		return "func " + name + "(m *sync.Map) a1 {\n\tv, _ := m.Load(\"k\")\n\treturn " + conv("v", n) + "\n}\n\n"
	}
	write := func(name, a, b string) string {
		return "func " + name + "(k bool, m *sync.Map) {\n\tf := " + a + "\n\tif k {\n\t\tf = " + b + "\n\t}\n\tc := f(m).(*T)\n" +
			"\tc.n++ // want \"SAFE005: write through shared value loaded from\"\n}\n\n"
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\n"+convTypes+
		loader("loadFar", 14)+loader("loadFarB", 14)+loader("loadNear", 2)+loader("loadNearB", 2)+
		write("far", "loadFar", "loadFarB")+write("near", "loadNear", "loadNearB"))
}

// TestCallbackArgumentThroughManyConversions: a helper hands its parameter to a callback (or to
// sync.Map.Store itself) through more conversions than the walk back to the parameter
// (paramFlowIndexSeen, 16 steps, after the 16 that unwrapPublishedArg strips) used to follow. The
// helper was then reported for publishing a value it does not own, instead of its caller for using
// the value afterwards; with 40 conversions the caller's use was not reported at all.
func TestCallbackArgumentThroughManyConversions(t *testing.T) {
	use := func(name, call string) string {
		return "func " + name + "(m *sync.Map) int {\n\tx := &T{}\n\t" + call + "\n" +
			"\treturn x.n // want \"SAFE003: composite literal is used after it was published via\"\n}\n\n"
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\n"+convTypes+
		"func applyFar(f func(k, v any), v any) {\n\tf(\"k\", "+conv("v", 18)+")\n}\n\n"+
		"func applyNear(f func(k, v any), v any) {\n\tf(\"k\", "+conv("v", 2)+")\n}\n\n"+
		"func putFar(m *sync.Map, v any) {\n\tm.Store(\"k\", "+conv("v", 40)+") // want \"SAFE002: parameter v is published via\"\n}\n\n"+
		"func putNear(m *sync.Map, v any) {\n\tm.Store(\"k\", "+conv("v", 2)+") // want \"SAFE002: parameter v is published via\"\n}\n\n"+
		use("far", "applyFar(m.Store, x)")+use("near", "applyNear(m.Store, x)")+use("farPut", "putFar(m, x)")+use("nearPut", "putNear(m, x)"))
}

// TestCallbackArgumentThroughManyVariables: a helper copies its parameter through 14 variables
// whose address is taken before handing it to a callback. The walk back to the parameter used to
// stop 13 variables deep.
func TestCallbackArgumentThroughManyVariables(t *testing.T) {
	cells := func(n int) string {
		var b strings.Builder
		b.WriteString("\tc0 := v\n\tkeep(&c0)\n")
		for i := 1; i < n; i++ {
			fmt.Fprintf(&b, "\tc%d := c%d\n\tkeep(&c%d)\n", i, i-1, i)
		}
		fmt.Fprintf(&b, "\tf(\"k\", c%d)\n", n-1)
		return b.String()
	}
	use := func(name, helper string) string {
		return "func " + name + "(m *sync.Map) int {\n\tx := &T{}\n\t" + helper + "(m.Store, x)\n" +
			"\treturn x.n // want \"SAFE003: composite literal is used after it was published via\"\n}\n\n"
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\nfunc keep(*any) {}\n\n"+
		"func applyFar(f func(k, v any), v any) {\n"+cells(14)+"}\n\n"+
		"func applyNear(f func(k, v any), v any) {\n"+cells(2)+"}\n\n"+
		use("far", "applyFar")+use("near", "applyNear"))
}

// poolGetters declares mk0 .. mk<n-1>, each returning what a sync.Pool hands out, and shared, which
// returns a package variable.
func poolGetters(n int) string {
	var b strings.Builder
	b.WriteString("var g = &T{}\n\nfunc shared(p *sync.Pool) any { return g }\n\n")
	for i := range n {
		fmt.Fprintf(&b, "func mk%d(p *sync.Pool) any { return p.Get() }\n\n", i)
	}
	return b.String()
}

// TestCalleeChosenAmongManyIsNotFullyKnown: a function is chosen among 100 pool getters and one
// function that returns a package variable, called, and its result sent on a channel. Tracing what
// the called value may be (traceSSAOrigins) has a budget of 96 values. Running out of it used to
// answer "these are all", so the result counted as the caller's own and the send passed. It now
// answers "incomplete", and the send is reported. That also holds where every candidate is a pool
// getter (farFresh): past the budget the check does not know that. The call itself says that its
// result may be loaded memory for all the check knows.
func TestCalleeChosenAmongManyIsNotFullyKnown(t *testing.T) {
	pick := func(name string, n int, last, want string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int, p *sync.Pool, ch chan *T) {\n\tvar f func(*sync.Pool) any\n\tswitch k {\n", name)
		for i := range n {
			fmt.Fprintf(&b, "\tcase %d:\n\t\tf = mk%d\n", i, i)
		}
		cut := ""
		if n > 96 {
			cut = " // want " + wantCutResult
		}
		fmt.Fprintf(&b, "\tdefault:\n\t\tf = %s\n\t}\n\tx := f(p).(*T)%s\n\tch <- x%s\n}\n\n", last, cut, want)
		return b.String()
	}
	const notOwned = " // want \"SAFE002: this value is sent on a channel but is not owned here\""
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\n"+poolGetters(100)+
		pick("far", 100, "shared", notOwned)+pick("near", 2, "shared", notOwned)+
		pick("farFresh", 100, "mk0", notOwned)+pick("nearFresh", 2, "mk0", ""))
}

// TestStructChosenAmongManyIsNotFullyKnown: the same, with the function read from a field of a
// struct chosen among 101 (tracePtrBaseOrigins, the same budget).
func TestStructChosenAmongManyIsNotFullyKnown(t *testing.T) {
	pick := func(name string, n int) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int, p *sync.Pool, ch chan *T) {\n\tvar s *holder\n\tswitch k {\n", name)
		for i := range n {
			fmt.Fprintf(&b, "\tcase %d:\n\t\ts = &holder{f: mk%d}\n", i, i)
		}
		cut := ""
		if n > 96 {
			cut = " // want " + wantCutResult
		}
		b.WriteString("\tdefault:\n\t\ts = &holder{f: shared}\n\t}\n\tx := s.f(p).(*T)" + cut + "\n" +
			"\tch <- x // want \"SAFE002: this value is sent on a channel but is not owned here\"\n}\n\n")
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\ntype holder struct{ f func(*sync.Pool) any }\n\n"+poolGetters(100)+
		pick("far", 100)+pick("near", 2))
}

// TestSharedLoadCalleeChosenAmongMany: memory loaded from an atomic.Pointer is passed to a function
// chosen among 101, the last of which writes it. The shared-load check looks into the functions the
// trace finds; the ones past the trace's budget it used to skip without a word. It now reports at
// the call that it could not find them all. With three candidates the write itself is found.
func TestSharedLoadCalleeChosenAmongMany(t *testing.T) {
	var readers strings.Builder
	for i := range 100 {
		fmt.Fprintf(&readers, "func r%d(t *T) {}\n\n", i)
	}
	pick := func(name string, n int, want string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int) {\n\tc := cur.Load()\n\tvar f func(*T)\n\tswitch k {\n", name)
		for i := range n {
			fmt.Fprintf(&b, "\tcase %d:\n\t\tf = r%d\n", i, i)
		}
		fmt.Fprintf(&b, "\tdefault:\n\t\tf = %s\n\t}\n\tf(c)%s\n}\n\n", "writer"+strings.TrimPrefix(name, "pick"), want)
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		"func writerFar(t *T) { t.n++ }\n\n"+
		"func writerNear(t *T) { t.n++ } // want \"SAFE005: write through shared value loaded from\"\n\n"+readers.String()+
		pick("pickFar", 100, " // want \"SAFE005: which functions the value called here may be could not be followed to the end .it comes through more than 14 steps or from more than 96 values.\" "+wantCutArg)+
		pick("pickNear", 2, ""))
}

// TestSharedLoadCalleeThroughManyConversions: memory loaded from an atomic.Pointer is passed to a
// writer that was converted between two named function types 16 times. The trace of what the called
// value may be goes 14 steps deep; past that it found no callee, and the write was passed without a
// word. It then reported at the call that it could not follow the value. Since the call asks
// callTargets first, which follows conversions of a function to the end as it does for a closure,
// the writer is found and its write is reported, however many conversions there are (far; what the
// call may publish is still worked out by the trace, which is cut and says so). Where
// the function comes in as a parameter the trace is still what follows it, and it still says when
// it is cut (farParam). Through 2 conversions the write itself is found, as before.
func TestSharedLoadCalleeThroughManyConversions(t *testing.T) {
	fconv := func(expr string, n int) string {
		for i := range n {
			expr = fmt.Sprintf("f%d(%s)", i%2, expr)
		}
		return expr
	}
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		"type f0 func(*T)\n\ntype f1 func(*T)\n\n"+
		"func writerFar(t *T) { t.n++ } // want \"SAFE005: write through shared value loaded from\"\n\n"+
		"func writerParam(t *T) { t.n++ }\n\n"+
		"func writerNear(t *T) { t.n++ } // want \"SAFE005: write through shared value loaded from\"\n\n"+
		"func far() {\n\tc := cur.Load()\n\tf := "+fconv("writerFar", 16)+"\n\tf(c) // want "+wantCutArg+"\n}\n\n"+
		"func farParam(g func(*T)) {\n\tc := cur.Load()\n\tf := "+fconv("g", 16)+"\n\tf(c) // want \"SAFE005: which functions the value called here may be could not be followed to the end .it comes through more than 14 steps or from more than 96 values.\" "+wantCutArg+"\n}\n\n"+
		"func callFarParam() { farParam(writerParam) }\n\n"+
		"func near() {\n\tc := cur.Load()\n\tf := "+fconv("writerNear", 2)+"\n\tf(c)\n}\n")
}

// cutCall is what a call that passes loaded memory to a callee the trace could not reach gets: that
// the callee's writes are not checked, and, as for any argument, that it may be published.
const cutCall = " // want \"SAFE005: which functions the value called here may be could not be followed to the end .it comes through more than 14 steps or from more than 96 values.\" " + wantCutArg

// TestSharedLoadCalleeInStructThroughManyConversions: the same, with the writer in a field of a
// struct whose pointer was converted between two named types 16 times (tracePtrBaseOrigins, the
// same depth). The long trace is cut and says so. It also finds the writer, because a trace that
// loses the pointer falls back on every store into that field of the struct type and of the types
// converted to it. Where no such store exists (unset: the field is filled in somewhere the trace
// cannot see), the cut is all there is to report.
func TestSharedLoadCalleeInStructThroughManyConversions(t *testing.T) {
	pconv := func(typ, expr string, n int) string {
		for i := range n {
			expr = fmt.Sprintf("(*%s%d)(%s)", typ, i%2, expr)
		}
		return expr
	}
	const write = " // want \"SAFE005: write through shared value loaded from\""
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		"type base struct{ f func(*T) }\n\ntype h0 base\n\ntype h1 base\n\n"+
		"type empty struct{ f func(*T) }\n\ntype e0 empty\n\ntype e1 empty\n\n"+
		"func writerFar(t *T) { t.n++ }"+write+"\n\n"+
		"func writerNear(t *T) { t.n++ }"+write+"\n\n"+
		"func far() {\n\tc := cur.Load()\n\tb := &base{f: writerFar}\n\tp := "+pconv("h", "b", 16)+"\n\tp.f(c)"+cutCall+"\n}\n\n"+
		"func near() {\n\tc := cur.Load()\n\tb := &base{f: writerNear}\n\tp := "+pconv("h", "b", 2)+"\n\tp.f(c)\n}\n\n"+
		"func unset() {\n\tc := cur.Load()\n\tb := &empty{}\n\tp := "+pconv("e", "b", 16)+"\n\tp.f(c)"+cutCall+"\n}\n")
}

// TestSharedLoadCalleeInStructThroughManyCalls: the writer is in a field of a struct value handed
// down through 16 functions (traceStructFieldValueOrigins, the same depth). Past the depth the
// trace falls back on every store into that field of that type, which here finds the writer, so
// the write is reported at both lengths; the long one now also says the trace was cut.
func TestSharedLoadCalleeInStructThroughManyCalls(t *testing.T) {
	chain := func(name string, n int, want string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "type holder%s struct{ f func(*T) }\n\n", name)
		fmt.Fprintf(&b, "func start%s() { step%s0(holder%s{f: writer%s}) }\n\n", name, name, name, name)
		for i := range n {
			fmt.Fprintf(&b, "func step%s%d(h holder%s) { step%s%d(h) }\n\n", name, i, name, name, i+1)
		}
		fmt.Fprintf(&b, "func step%s%d(h holder%s) {\n\tc := cur.Load()\n\th.f(c)%s\n}\n\n", name, n, name, want)
		return b.String()
	}
	const write = " // want \"SAFE005: write through shared value loaded from\""
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		"func writerFar(t *T) { t.n++ }"+write+"\n\n"+
		"func writerNear(t *T) { t.n++ }"+write+"\n\n"+
		chain("Far", 16, cutCall)+chain("Near", 2, ""))
}

// TestSharedLoadCalleeInUnsetStructThroughManyCalls: as above, but nothing in the package stores
// into the field: the struct stands for one filled in where the trace cannot see. There is then no
// store for the trace to fall back on, and the depth of traceStructFieldValueOrigins alone decides:
// the long chain says the trace was cut. The short chain reaches the empty struct, finds no callee,
// and like any call of a function the check cannot find for a reason other than its bounds it says
// nothing.
func TestSharedLoadCalleeInUnsetStructThroughManyCalls(t *testing.T) {
	chain := func(name string, n int, want string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "type holder%s struct{ f func(*T) }\n\n", name)
		fmt.Fprintf(&b, "func start%s() { step%s0(holder%s{}) }\n\n", name, name, name)
		for i := range n {
			fmt.Fprintf(&b, "func step%s%d(h holder%s) { step%s%d(h) }\n\n", name, i, name, name, i+1)
		}
		fmt.Fprintf(&b, "func step%s%d(h holder%s) {\n\tc := cur.Load()\n\th.f(c)%s\n}\n\n", name, n, name, want)
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		chain("Far", 16, cutCall)+chain("Near", 2, ""))
}

// TestSharedLoadCalleeInConvertedStruct: the writer is in a field of a struct, and the struct is
// converted to another struct type before the call. A conversion keeps the fields, so the writer
// is the callee whichever type the call sees; it used to be lost at the conversion, because the
// stores into a field are looked up by the struct type and the converted type has none.
//   - direct: the conversion is in sight of the call.
//   - chain: the converted struct comes down through two calls.
//   - stored: the converted struct is put in a map and read back, so the trace sees a map read of
//     the converted type and falls back on the stores of the types converted to it.
//   - pointer: a pointer is converted, the field is set through the converted pointer and called
//     through the original one; the link runs both ways.
//   - only: the call's struct holds a reader. Another struct of the first type holds a writer and
//     is never called with loaded memory. The conversion in sight is followed to its own struct,
//     so the writer is not blamed.
func TestSharedLoadCalleeInConvertedStruct(t *testing.T) {
	const write = " // want \"SAFE005: write through shared value loaded from\""
	types := func(name string) string {
		return fmt.Sprintf("type base%s struct{ f func(*T) }\n\ntype holder%s base%s\n\nfunc writer%s(t *T) { t.n++ }", name, name, name, name)
	}
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		types("Direct")+write+"\n\n"+
		"func direct() {\n\tc := cur.Load()\n\th := holderDirect(baseDirect{f: writerDirect})\n\th.f(c)\n}\n\n"+
		types("Chain")+write+"\n\n"+
		"func chain() { chain1(holderChain(baseChain{f: writerChain})) }\n\nfunc chain1(h holderChain) { chain2(h) }\n\n"+
		"func chain2(h holderChain) {\n\tc := cur.Load()\n\th.f(c)\n}\n\n"+
		types("Stored")+write+"\n\n"+
		"func stored() {\n\tc := cur.Load()\n\tm := map[string]holderStored{\"k\": holderStored(baseStored{f: writerStored})}\n\th := m[\"k\"]\n\th.f(c)\n}\n\n"+
		types("Pointer")+write+"\n\n"+
		"func pointer() {\n\tc := cur.Load()\n\tb := &basePointer{}\n\tp := (*holderPointer)(b)\n\tp.f = writerPointer\n\tb.f(c)\n}\n\n"+
		types("Only")+"\n\n"+
		"func readerOnly(t *T) {}\n\nfunc spare() baseOnly { return baseOnly{f: writerOnly} }\n\n"+
		"func only() {\n\tc := cur.Load()\n\th := holderOnly(baseOnly{f: readerOnly})\n\th.f(c)\n}\n")
}
