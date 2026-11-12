package ownership_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// The checks other than the handler write check also stopped walks at fixed sizes, and a walk that
// stopped answered "nothing here". Each test below is a program just past one of those sizes, with
// the finding it must still give, or, where the size made the checker report code that is right,
// without one.

func analyseWalk(t *testing.T, src string) {
	t.Helper()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"walk/walk.go": src})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "walk")
}

// nested declares struct types name0 .. name<depth-1>, each holding the next in a field named in,
// and the last holding leaf's fields.
func nested(name string, depth int, leaf string) string {
	var b strings.Builder
	for i := range depth - 1 {
		fmt.Fprintf(&b, "type %s%d struct{ in %s%d }\n\n", name, i, name, i+1)
	}
	fmt.Fprintf(&b, "type %s%d struct {\n%s}\n\n", name, depth-1, leaf)
	return b.String()
}

// TestGuardedWriteThroughDeepValue: a guarded field holds a value 70 structs deep, and a field of
// the innermost is written without the mutex. The walk from the written address back to the guarded
// field (slotOf) used to stop after 64 steps and call the write unguarded memory.
func TestGuardedWriteThroughDeepValue(t *testing.T) {
	path := "h.data" + strings.Repeat(".in", 69) + ".x"
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\n"+nested("l", 70, "\tx int\n")+
		"type holder struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tdata l0\n}\n\n"+
		"func unlocked(h *holder) {\n\t"+path+" = 1 // want `is guarded by mu but is written here without holding it`\n}\n\n"+
		"func locked(h *holder) {\n\th.mu.Lock()\n\t"+path+" = 2\n\th.mu.Unlock()\n}\n")
}

// TestLockThroughDeepPath: the mutex and the field it guards are 40 structs deep. The walk that
// names a lock by its path (pathOf) used to stop after 32 steps, so the Lock was not seen and the
// guarded write under it was reported.
func TestLockThroughDeepPath(t *testing.T) {
	path := "h" + strings.Repeat(".in", 39)
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\n"+nested("w", 40, "\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tn int\n")+
		"func locked(h *w0) {\n\t"+path+".mu.Lock()\n\t"+path+".n++\n\t"+path+".mu.Unlock()\n}\n\n"+
		"func unlocked(h *w0) {\n\t"+path+".n++ // want `is guarded by mu but is written here without holding it`\n}\n")
}

// TestLockFlowThroughLongGotoChain: a guarded write is reached through 80 blocks laid out so that
// each pass of the held-locks analysis gets one block further. The analysis used to stop after 64
// passes, and what it had not reached was not checked.
func TestLockFlowThroughLongGotoChain(t *testing.T) {
	const n = 80
	var b strings.Builder
	b.WriteString("package walk\n\nimport \"sync\"\n\ntype holder struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tn    int\n\tstop bool\n}\n\n")
	fmt.Fprintf(&b, "func chain(h *holder) {\n\tgoto L%d\nL1:\n\th.n++ // want `is guarded by mu but is written here without holding it`\n\treturn\n", n)
	for i := 2; i <= n; i++ {
		fmt.Fprintf(&b, "L%d:\n\tif h.stop {\n\t\treturn\n\t}\n\tgoto L%d\n", i, i-1)
	}
	b.WriteString("}\n")
	analyseWalk(t, b.String())
}

// TestGuardedLocalInDeepClosure: a guarded local variable is written without its mutex in the
// innermost of 40 nested closures.
func TestGuardedLocalInDeepClosure(t *testing.T) {
	const depth = 40
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\nfunc f() {\n\tvar mu sync.Mutex\n\tvar x int //gosree:guarded_by mu\n\t_ = &mu\n\t"+
		strings.Repeat("func() {\n", depth)+"x = 1 // want `is guarded by mu but is written here without holding it`\n"+strings.Repeat("}()\n", depth)+"\tmu.Lock()\n\tx++\n\tmu.Unlock()\n}\n")
}

// TestLockPassedThroughManyInterfaces: the struct whose lock is held is handed to a function value
// through 13 interface conversions, each to an interface with one method fewer. Whoever gets it may
// unlock it, so the guarded write after the call is not known to be under the lock. Seeing through
// the conversions (argValues) used to stop after 8, and the lock then read as still held.
func TestLockPassedThroughManyInterfaces(t *testing.T) {
	const depth = 12
	var b strings.Builder
	b.WriteString("package walk\n\nimport \"sync\"\n\ntype holder struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tn int\n}\n\n")
	for i := range depth {
		fmt.Fprintf(&b, "func (*holder) m%d() {}\n\n", i)
	}
	for i := 0; i <= depth; i++ {
		fmt.Fprintf(&b, "type i%d interface {\n", i)
		for j := i; j < depth; j++ {
			fmt.Fprintf(&b, "\tm%d()\n", j)
		}
		b.WriteString("}\n\n")
	}
	arg := "i0(h)"
	for i := 1; i <= depth; i++ {
		arg = fmt.Sprintf("i%d(%s)", i, arg)
	}
	fmt.Fprintf(&b, "func handOver(h *holder, release func(i%d)) {\n\th.mu.Lock()\n\trelease(%s)\n\th.n++ // want `is guarded by mu but is written here without holding it`\n}\n\n", depth, arg)
	fmt.Fprintf(&b, "func direct(h *holder, release func(i%d)) {\n\th.mu.Lock()\n\trelease(h)\n\th.n++ // want `is guarded by mu but is written here without holding it`\n}\n\n", depth)
	b.WriteString("func kept(h *holder) {\n\th.mu.Lock()\n\th.n++\n\th.mu.Unlock()\n}\n")
	analyseWalk(t, b.String())
}

// TestImmStoreIntoWideStruct: a struct holds an imm value after 20 fields of 20 other types, and is
// overwritten through a pointer. The search of its fields for an imm value (immInValueType) used to
// stop after 16 types.
func TestImmStoreIntoWideStruct(t *testing.T) {
	var types, fields strings.Builder
	for i := range 20 {
		fmt.Fprintf(&types, "type t%d struct{ a%d int%d }\n\n", i, i, []int{8, 16, 32, 64}[i%4])
		fmt.Fprintf(&fields, "\tf%d t%d\n", i, i)
	}
	analyseWalk(t, "package walk\n\n//gosree:imm\ntype cfg struct{ name string }\n\n"+types.String()+
		"type outer struct {\n"+fields.String()+"\tc cfg\n}\n\n"+
		"func overwrite(p *outer) {\n\t*p = outer{} // want `store through a \\*outer overwrites an immutable value`\n}\n")
}

// TestImmWriteThroughDeepValue: a field 70 structs inside an imm value is written. The walk from
// the written address back to the imm value (traceImmAddr) used to stop after 64 steps.
func TestImmWriteThroughDeepValue(t *testing.T) {
	analyseWalk(t, "package walk\n\n"+nested("n", 70, "\tx int\n")+
		"//gosree:imm\ntype cfg struct{ data n0 }\n\n"+
		"func write(c *cfg) {\n\tc.data"+strings.Repeat(".in", 69)+".x = 1 // want `write to a field of cfg`\n}\n")
}

// TestGlobalWriteThroughLongChain: a package variable is written 40 pointers away. The walk from
// the written address back to the variable (globalRoot) used to stop after 64 steps, two per
// pointer.
func TestGlobalWriteThroughLongChain(t *testing.T) {
	analyseWalk(t, "package walk\n\ntype node struct {\n\tnext *node\n\tn    int\n}\n\n"+
		"var root = &node{} // want `SAFE006: package variable root is written outside init`\n\n"+
		"func bump() {\n\troot"+strings.Repeat(".next", 40)+".n = 1\n}\n")
}

// TestGoThroughManyConversions: a goroutine is started on a closure converted between two function
// types 14 times. Finding the closure behind the conversions (resolveClosureVal) used to stop after
// 10, and what the closure captures was then not seen to move to the goroutine.
func TestGoThroughManyConversions(t *testing.T) {
	conv := "func() { x.n++ }"
	for i := range 14 {
		conv = fmt.Sprintf("f%d(%s)", i%2, conv)
	}
	analyseWalk(t, `package walk

type f0 func()

type f1 func()

type box struct{ n int }

func start() int {
	x := &box{}
	go `+conv+`()
	return x.n // want `+"`SAFE003: variable x is used after it was captured by a goroutine`"+`
}

func plain() int {
	x := &box{}
	go func() { x.n++ }()
	return x.n // want `+"`SAFE003: variable x is used after it was captured by a goroutine`"+`
}
`)
}

// TestBorrowThroughManyConversions: a closure that captures a stack-scoped borrow is started as a
// goroutine through 14 conversions. With the closure not found behind them, the borrow it captures
// was not seen to cross to the goroutine.
func TestBorrowThroughManyConversions(t *testing.T) {
	conv := "func() { *b = 2 }"
	for i := range 14 {
		conv = fmt.Sprintf("f%d(%s)", i%2, conv)
	}
	analyseWalk(t, `package walk

type f0 func()

type f1 func()

func converted() {
	val := 1
	//gosree:mut
	b := &val
	go `+conv+`() // want `+"`CAP003: stack-scoped borrow .b. .* cannot cross goroutine boundary`"+`
}

func plain() {
	val := 1
	//gosree:mut
	b := &val
	go func() { *b = 2 }() // want `+"`CAP003: stack-scoped borrow .b. .* cannot cross goroutine boundary`"+`
}
`)
}

// TestImmCallThroughManySlices: an array inside an imm value is sliced, the slice is sliced again 70
// times, and the last is handed to a function that writes it. The walk forward from the field to the
// calls that receive it (checkImmDerivedCalls) used to stop after 64 values.
func TestImmCallThroughManySlices(t *testing.T) {
	analyseWalk(t, "package walk\n\n//gosree:imm\ntype cfg struct{ arr [4]int }\n\nfunc fill(s []int) { s[0] = 1 }\n\n"+
		"func far(c *cfg) {\n\ts := c.arr[:]\n"+strings.Repeat("\ts = s[:]\n", 70)+"\tfill(s) // want `SAFE005`\n}\n\n"+
		"func near(c *cfg) {\n\ts := c.arr[:]\n\tfill(s) // want `SAFE005`\n}\n")
}

// TestImmReturnsThatDoNotSettle: pick calls itself with its two parameters swapped, so what its
// returns point at alternates between two answers and never settles. The rounds stop at
// immMaxRounds; that used to pass without a word and now is reported at the function. The write
// through its result is found either way.
func TestImmReturnsThatDoNotSettle(t *testing.T) {
	analyseWalk(t, `package walk

//gosree:imm
type cfg struct{ x int }

func pick(a, b *cfg, swap bool) *int { // want `+"`SAFE005: what pick returns did not settle after 8 passes over its recursive calls`"+`
	if swap {
		return pick(b, a, false)
	}
	return &a.x
}

func use(a, b *cfg) {
	*pick(a, b, true) = 1 // want `+"`write to a field of cfg`"+`
}
`)
}

// TestBorrowThroughManyAssignments: a stack-scoped borrow is copied from variable to variable six
// times before it is sent on a channel or handed to a go statement. The lookup of where a variable
// got its value used to stop five steps from the send.
func TestBorrowThroughManyAssignments(t *testing.T) {
	copies := "\tb1 := b\n"
	for i := 2; i <= 6; i++ {
		copies += fmt.Sprintf("\tb%d := b%d\n", i, i-1)
	}
	analyseWalk(t, "package walk\n\nfunc far(ch chan *int) {\n\tval := 1\n\t//gosree:mut\n\tb := &val\n"+copies+
		"\tch <- b6 // want `CAP003: stack-scoped borrow .b. .* cannot be sent on a channel`\n}\n\n"+
		"func farGo() {\n\tval := 1\n\t//gosree:mut\n\tb := &val\n"+copies+
		"\tgo func(p *int) { *p = 2 }(b6) // want `CAP003: stack-scoped borrow .b. .* cannot be passed as argument to go statement`\n}\n\n"+
		"func near(ch chan *int) {\n\tval := 1\n\t//gosree:mut\n\tb := &val\n\tb1 := b\n"+
		"\tch <- b1 // want `CAP003: stack-scoped borrow .b. .* cannot be sent on a channel`\n}\n")
}

// TestBorrowThroughManyClosures: a closure that writes through a stack-scoped borrow is called by a
// second closure, that by a third, and so on seven deep, and the last is called in a goroutine.
// Listing what a closure captures through the closures it calls used to stop five deep.
func TestBorrowThroughManyClosures(t *testing.T) {
	lits := "\tf1 := func() { *b = 2 }\n"
	for i := 2; i <= 7; i++ {
		lits += fmt.Sprintf("\tf%d := func() { f%d() }\n", i, i-1)
	}
	analyseWalk(t, "package walk\n\nfunc far() {\n\tval := 1\n\t//gosree:mut\n\tb := &val\n"+lits+
		"\tgo func() { f7() }() // want `CAP003: stack-scoped borrow .b. .* cannot be captured by a goroutine`\n}\n\n"+
		"func near() {\n\tval := 1\n\t//gosree:mut\n\tb := &val\n\tf1 := func() { *b = 2 }\n"+
		"\tgo func() { f1() }() // want `CAP003: stack-scoped borrow .b. .* cannot be captured by a goroutine`\n}\n")
}

// TestBorrowParamThroughDeepValue: the address of a field 70 structs inside a borrowed parameter is
// sent on a channel. The walk from the sent value back to the parameter (borrowFromVal) used to
// stop after 64 values, and the send was then reported as a plain hand-over of the parameter.
func TestBorrowParamThroughDeepValue(t *testing.T) {
	analyseWalk(t, "package walk\n\n"+nested("n", 70, "\tx int\n")+
		"//gosree:mut b\nfunc far(b *n0, ch chan *int) {\n\tch <- &b"+strings.Repeat(".in", 69)+".x // want `CAP003: stack-scoped borrow .b. .* cannot be sent on a channel`\n}\n\n"+
		"//gosree:mut b\nfunc near(b *n0, ch chan *n1) {\n\tch <- &b.in // want `CAP003: stack-scoped borrow .b. .* cannot be sent on a channel`\n}\n")
}

// TestGoOnVariableAssignedManyTimes: a function variable is assigned 40 functions and then a
// closure, and a goroutine is started on it. Finding the closure among what the variable was
// assigned (resolveClosureVal) used to stop after 32 values, and the goroutine then read as one
// started on an unknown function.
func TestGoOnVariableAssignedManyTimes(t *testing.T) {
	var funcs, stores strings.Builder
	for i := range 40 {
		fmt.Fprintf(&funcs, "func g%d() {}\n\n", i)
		fmt.Fprintf(&stores, "\tf = g%d\n", i)
	}
	analyseWalk(t, "package walk\n\ntype box struct{ n int }\n\n"+funcs.String()+
		"func far() int {\n\tx := &box{}\n\tvar f func()\n\tp := &f\n"+stores.String()+"\tf = func() { x.n++ }\n\t_ = p\n\tgo f()\n"+
		"\treturn x.n // want `SAFE003: variable x is used after it was captured by a goroutine`\n}\n\n"+
		"func near() int {\n\tx := &box{}\n\tvar f func()\n\tp := &f\n\tf = func() { x.n++ }\n\t_ = p\n\tgo f()\n"+
		"\treturn x.n // want `SAFE003: variable x is used after it was captured by a goroutine`\n}\n")
}

// TestTryLockThroughManyNegations: the result of TryLock is negated 20 times before it is tested,
// so the branch taken is the one that holds the lock. Following the condition back to the TryLock
// (unwrapBoolCond) used to stop after 16 steps, and the guarded write under the lock was reported.
// With an odd number of negations the branch does not hold the lock, and the write is reported.
func TestTryLockThroughManyNegations(t *testing.T) {
	nots := "\ta0 := !ok\n"
	for i := 1; i < 20; i++ {
		nots += fmt.Sprintf("\ta%d := !a%d\n", i, i-1)
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype holder struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tn int\n}\n\n"+
		"func held(h *holder) {\n\tok := h.mu.TryLock()\n"+nots+"\tif a19 {\n\t\th.n++\n\t\th.mu.Unlock()\n\t}\n}\n\n"+
		"func notHeld(h *holder) {\n\tok := h.mu.TryLock()\n"+strings.TrimSuffix(nots, "\ta19 := !a18\n")+"\tif a18 {\n\t\th.n++ // want `is guarded by mu but is written here without holding it`\n\t\treturn\n\t}\n\th.mu.Unlock()\n}\n")
}

// TestGoOnClosureConvertedManyTimes: a closure that keeps what it captures is converted six times
// and started with go. The goroutine owns the captures, so nothing escapes at the closure. Following
// the conversions to the go statement (closureUsedOnlyAtCallSites) used to stop after five, and the
// closure was then reported as both handed to a goroutine and escaping.
func TestGoOnClosureConvertedManyTimes(t *testing.T) {
	analyseWalk(t, `package walk

type f0 func()

type f1 func()

type box struct{ n int }

var sink *box // want `+"`SAFE006: package variable sink is written outside init`"+`

func far() {
	x := &box{}
	go f1(f0(f1(f0(f1(f0(func() { sink = x }))))))()
}

func near() {
	x := &box{}
	go f0(func() { sink = x })()
}
`)
}

// TestLockWalksEndOnLoopCarriedCells: the walks along a lock's path and from a branch condition have
// no step limit, and end because each step goes to a value defined strictly earlier (an operand, or
// what a store that comes before the load put in the variable). A variable assigned inside a loop
// from a value read from itself is the shape that would go round for ever if that were wrong. The
// findings are today's answers for these loops, the same with and without the old limits (the lock
// taken through a variable the loop reassigns is not followed, so walk and single are reported
// although they lock); what the test pins is that the analysis ends, with these answers.
func TestLockWalksEndOnLoopCarriedCells(t *testing.T) {
	analyseWalk(t, `package walk

import "sync"

type holder struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n    int
	next *holder
}

func walk(start *holder) {
	var p *holder
	q := &p
	p = start
	for p != nil {
		cur := p
		cur.mu.Lock()
		cur.n++ // want `+"`is guarded by mu but is written here without holding it`"+`
		cur.mu.Unlock()
		p = cur.next
	}
	_ = q
}

func single(k int) {
	var p *holder
	q := &p
	for i := 0; i < k; i++ {
		cur := p
		if cur != nil {
			cur.mu.Lock()
			cur.n++ // want `+"`is guarded by mu but is written here without holding it`"+`
			cur.mu.Unlock()
		}
		p = cur.next
	}
	_ = q
}

func condTry(h *holder, k int) {
	var ok bool
	q := &ok
	for i := 0; i < k; i++ {
		if ok {
			h.n++ // want `+"`is guarded by mu but is written here without holding it`"+`
			h.mu.Unlock()
		}
		ok = !ok && h.mu.TryLock()
	}
	_ = q
}
`)
}
