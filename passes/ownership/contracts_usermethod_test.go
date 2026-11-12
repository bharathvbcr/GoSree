package ownership_test

import (
	"fmt"
	"strings"
	"testing"
)

// A call of an interface method takes its contract from the method of each type its receiver may
// be: an entry of the container table, or, for a method of the package under analysis, what a
// direct call of that method gets. The tests here are about the second.

const (
	paramPublished = " // want \"SAFE002: parameter v is published via\""
	keeperHead     = "package walk\n\nimport (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\nvar _ sync.Mutex\n\ntype keeper interface{ keep(v *T) }\n\n"
	keeperK        = "type k struct{ m *sync.Map }\n\nfunc (x k) keep(v *T) {\n\tx.m.Store(\"k\", v)" + paramPublished + "\n}\n\n"
)

// TestUserMethodPublishesThroughInterface: a method that stores its argument in a sync.Map, and a
// value used after it was handed to that method. Each function reaches the method another way.
//   - direct: a plain call of the method, for comparison.
//   - local: through an interface value set in the function.
//   - either: the interface value is one of two types, and only one publishes.
//   - onlyQuiet: the interface value is the type that does not publish; nothing is reported.
//   - parameter: the interface value is a parameter, and a caller in the package passes the type.
//   - field: the interface value is a struct field, set where the struct is built.
//   - value, expr: a method value and a method expression of the interface method.
func TestUserMethodPublishesThroughInterface(t *testing.T) {
	analyseWalk(t, keeperHead+keeperK+"type quiet struct{}\n\nfunc (quiet) keep(v *T) {}\n\n"+
		"func direct(m *sync.Map) int {\n\tx := &T{}\n\tk{m}.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"func local(m *sync.Map) int {\n\tx := &T{}\n\tvar kp keeper = k{m}\n\tkp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"func either(m *sync.Map, c bool) int {\n\tx := &T{}\n\tvar kp keeper = quiet{}\n\tif c {\n\t\tkp = k{m}\n\t}\n\tkp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"func onlyQuiet() int {\n\tx := &T{}\n\tvar kp keeper = quiet{}\n\tkp.keep(x)\n\treturn x.n\n}\n\n"+
		"func parameter(kp keeper) int {\n\tx := &T{}\n\tkp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\nfunc passes(m *sync.Map) int { return parameter(k{m}) }\n\n"+
		"type svc struct{ kp keeper }\n\nfunc newSvc(m *sync.Map) *svc { return &svc{kp: k{m}} }\n\n"+
		"func (s *svc) field() int {\n\tx := &T{}\n\ts.kp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"func value(m *sync.Map) int {\n\tx := &T{}\n\tvar kp keeper = k{m}\n\tf := kp.keep\n\tf(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"func expr(m *sync.Map) int {\n\tx := &T{}\n\tvar kp keeper = k{m}\n\tkeeper.keep(kp, x)\n\treturn x.n"+usedAfter+"\n}\n")
}

// TestUserMethodOfAnotherShapePublishesThroughInterface: the publishing method is promoted from an
// embedded struct, declared on a pointer, or declared on a type with a type parameter, where the
// method's parameter has the type parameter's type and the interface's has the type it stands for.
func TestUserMethodOfAnotherShapePublishesThroughInterface(t *testing.T) {
	analyseWalk(t, keeperHead+keeperK+"type outer struct{ k }\n\n"+
		"func promoted(m *sync.Map) int {\n\tx := &T{}\n\tvar kp keeper = outer{k{m}}\n\tkp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"type pk struct{ m *sync.Map }\n\nfunc (x *pk) keep(v *T) {\n\tx.m.Store(\"k\", v)"+paramPublished+"\n}\n\n"+
		"func pointer(m *sync.Map) int {\n\tx := &T{}\n\tvar kp keeper = &pk{m}\n\tkp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n\n"+
		"type holder interface{ hold(v *T) }\n\ntype g[V any] struct{ m *sync.Map }\n\nfunc (x g[V]) hold(v V) {\n\tx.m.Store(\"k\", v)"+paramPublished+"\n}\n\n"+
		"func generic(m *sync.Map) int {\n\tx := &T{}\n\tvar h holder = g[*T]{m}\n\th.hold(x)\n\treturn x.n"+usedAfter+"\n}\n")
}

// TestUserMethodForwardsThroughInterface: the method publishes nothing itself and hands its second
// argument to the function it is given as its first, which stores it.
func TestUserMethodForwardsThroughInterface(t *testing.T) {
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\ntype runner interface{ run(f func(*T), v *T) }\n\n"+
		"type r struct{}\n\nfunc (r) run(f func(*T), v *T) { f(v) }\n\n"+
		"func forward(m *sync.Map) int {\n\tx := &T{}\n\tvar rn runner = r{}\n\trn.run(func(v *T) {\n\t\tm.Store(\"k\", v)"+paramPublished+"\n\t}, x)\n\treturn x.n"+usedAfter+"\n}\n")
}

// TestUserMethodLoadsThroughInterface: a method that returns what an atomic.Pointer holds, and a
// write through its result.
//   - direct: a plain call of the method, for comparison.
//   - local, parameter: through an interface value set in the function, and one passed by a caller.
//   - onlyFresh: the interface value is a type whose method returns a new value; nothing is reported.
func TestUserMethodLoadsThroughInterface(t *testing.T) {
	analyseWalk(t, keeperHead+"type src interface{ get() *T }\n\ntype ld struct{}\n\nfunc (ld) get() *T { return cur.Load() }\n\n"+
		"type fresh struct{}\n\nfunc (fresh) get() *T { return &T{} }\n\n"+
		"func direct() {\n\tc := ld{}.get()\n\tc.n++"+loadedWrite+"\n}\n\n"+
		"func local() {\n\tvar s src = ld{}\n\tc := s.get()\n\tc.n++"+loadedWrite+"\n}\n\n"+
		"func parameter(s src) {\n\tc := s.get()\n\tc.n++"+loadedWrite+"\n}\n\nfunc passes() { parameter(ld{}) }\n\n"+
		"func onlyFresh() {\n\tvar s src = fresh{}\n\tc := s.get()\n\tc.n++\n}\n")
}

// TestUserMethodThroughInterfaceChosenAmongMany: the receiver is one of 100 types whose method does
// nothing and one whose method publishes its argument, or returns loaded memory. With 2 candidates
// the publish and the write are reported. With 100 the last type lies past the budget, and the
// call says that what it may run was not followed to the end. A method that no type of the
// package gives a contract has nothing to say however many types its receiver may be: note, which
// no type's method does anything in, and put, which only a method of another signature publishes
// through (wide's, which takes a second argument and so cannot stand behind the interface's).
func TestUserMethodThroughInterfaceChosenAmongMany(t *testing.T) {
	keep := func(name string, n int, atCall, atUse string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func keep%s(k int, m *sync.Map) int {\n\tx := &T{}\n", name)
		pickAmong(&b, "var f keeper", n, named("q", "{}"), "pub{m}")
		fmt.Fprintf(&b, "\tf.keep(x)%s\n\treturn x.n%s\n}\n\n", atCall, atUse)
		return b.String()
	}
	var others strings.Builder
	others.WriteString("func others(k int, m *sync.Map) {\n\tx := &T{}\n")
	pickAmong(&others, "var f keeper", 100, named("q", "{}"), "pub{m}")
	others.WriteString("\tf.note(x)\n\tf.put(x)\n}\n\n")
	get := func(name string, n int, atCall, atWrite string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func get%s(k int) {\n", name)
		pickAmong(&b, "var f src", n, named("q", "{}"), "ld{}")
		fmt.Fprintf(&b, "\tc := f.get()%s\n\tc.n++%s\n}\n\n", atCall, atWrite)
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		"type keeper interface {\n\tkeep(v *T)\n\tnote(v *T)\n\tput(v *T)\n}\n\ntype src interface{ get() *T }\n\n"+
		"type pub struct{ m *sync.Map }\n\nfunc (x pub) keep(v *T) {\n\tx.m.Store(\"k\", v)"+paramPublished+"\n}\n\nfunc (pub) note(v *T) {}\n\nfunc (pub) put(v *T) {}\n\n"+
		"type wide struct{ m *sync.Map }\n\nfunc (x wide) put(v *T, tag string) {\n\tx.m.Store(tag, v)"+paramPublished+"\n}\n\n"+
		"type ld struct{}\n\nfunc (ld) get() *T { return cur.Load() }\n\n"+
		numbered(100, "type q# struct{}\n\nfunc (q#) keep(v *T) {}\n\nfunc (q#) note(v *T) {}\n\nfunc (q#) put(v *T) {}\n\nfunc (q#) get() *T { return &T{} }\n\n")+
		keep("Far", 100, " // want "+wantCutArg, "")+keep("Near", 2, "", usedAfter)+others.String()+
		get("Far", 100, " // want "+wantCutResult, "")+get("Near", 2, "", loadedWrite))
}

// TestUserMethodFreezesThroughInterface: a value that refers to a package variable written only at
// initialisation may be published through a container whose readers cannot write it
// (atomic.Pointer), and may not through one whose readers can (sync.Map). "Frozen when published"
// holds for a call of an interface method only if the method of every type the receiver may be
// freezes it, and only if those types are all known.
//   - either: one of the two types stores in a sync.Map; reported.
//   - allFrozen: the one type stores in an atomic.Pointer; nothing is reported.
//   - Exported: the receiver is a parameter of an exported function, whose one caller in the
//     package passes the freezing type; callers outside may pass another, so it is reported.
func TestUserMethodFreezesThroughInterface(t *testing.T) {
	const notFrozen = " // want \"SAFE002: composite literal is published via\" \"SAFE004: composite literal is published via\""
	analyseWalk(t, "package walk\n\nimport (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\ntype T struct{ tab map[string]int }\n\nvar table = map[string]int{\"a\": 1}\n\n"+
		"type storer interface {\n\tput(p *atomic.Pointer[T], m *sync.Map, v *T)\n}\n\n"+
		"type sp struct{}\n\nfunc (sp) put(p *atomic.Pointer[T], m *sync.Map, v *T) { p.Store(v) }"+paramPublished+"\n\n"+
		"type sm struct{}\n\nfunc (sm) put(p *atomic.Pointer[T], m *sync.Map, v *T) { m.Store(\"k\", v) }"+paramPublished+"\n\n"+
		"func either(c bool, p *atomic.Pointer[T], m *sync.Map) {\n\tx := &T{tab: table}\n\tvar f storer = sp{}\n\tif c {\n\t\tf = sm{}\n\t}\n\tf.put(p, m, x)"+notFrozen+"\n}\n\n"+
		"func allFrozen(p *atomic.Pointer[T], m *sync.Map) {\n\tx := &T{tab: table}\n\tvar f storer = sp{}\n\tf.put(p, m, x)\n}\n\n"+
		"func Exported(f storer, p *atomic.Pointer[T], m *sync.Map) { // want Exported:\"ownership summary\"\n\tx := &T{tab: table}\n\tf.put(p, m, x)"+notFrozen+"\n}\n\n"+
		"func callsExported(p *atomic.Pointer[T], m *sync.Map) { Exported(sp{}, p, m) }\n")
}

// TestUserMethodResultThroughInterfaceIsNotTakenAsNew: the result of an interface method is never
// taken as the caller's own, whatever the methods behind it return, here or through a helper that
// returns it. That was so before a method of the package answered for itself, and stays so: with
// one type that returns a new value (onlyNew, helperNew) the send is still reported, as it is
// where one of two types returns a package variable (either).
func TestUserMethodResultThroughInterfaceIsNotTakenAsNew(t *testing.T) {
	const notOwned = " // want \"is sent on a channel but is not owned here\""
	analyseWalk(t, "package walk\n\ntype T struct{ n int }\n\nvar shared = &T{}\n\ntype maker interface{ mk() *T }\n\n"+
		"type newer struct{}\n\nfunc (newer) mk() *T { return &T{} }\n\ntype old struct{}\n\nfunc (old) mk() *T { return shared }\n\n"+
		"func directNew(ch chan *T) {\n\tx := newer{}.mk()\n\tch <- x\n}\n\n"+
		"func directOld(ch chan *T) {\n\tx := old{}.mk()\n\tch <- x"+notOwned+"\n}\n\n"+
		"func either(c bool, ch chan *T) {\n\tvar m maker = newer{}\n\tif c {\n\t\tm = old{}\n\t}\n\tx := m.mk()\n\tch <- x"+notOwned+"\n}\n\n"+
		"func onlyNew(ch chan *T) {\n\tvar m maker = newer{}\n\tx := m.mk()\n\tch <- x"+notOwned+"\n}\n\n"+
		"func viaHelper(m maker) *T { return m.mk() }\n\n"+
		"func helperNew(ch chan *T) {\n\tx := viaHelper(newer{})\n\tch <- x"+notOwned+"\n}\n")
}

// TestUserMethodAnswersThroughAnotherInterface: a method that returns loaded memory, or publishes
// its argument, through an interface value its own receiver holds. The wrapping method is
// declared after the one it reaches and before it: the order the index meets them in must not
// matter.
func TestUserMethodAnswersThroughAnotherInterface(t *testing.T) {
	const leaf = "type ld struct{}\n\nfunc (ld) get() *T { return cur.Load() }\n\n" + keeperK
	const wrap = "type wrap struct{ inner src }\n\nfunc (w wrap) get() *T { return w.inner.get() }\n\ntype wrapK struct{ inner keeper }\n\nfunc (w wrapK) keep(v *T) { w.inner.keep(v) }" + paramPublished + "\n\n"
	for _, order := range [][2]string{{leaf, wrap}, {wrap, leaf}} {
		analyseWalk(t, keeperHead+"type src interface{ get() *T }\n\n"+order[0]+order[1]+
			"func loads() {\n\tvar s src = wrap{inner: ld{}}\n\tc := s.get()\n\tc.n++"+loadedWrite+"\n}\n\n"+
			"func keeps(m *sync.Map) int {\n\tx := &T{}\n\tvar kp keeper = wrapK{inner: k{m}}\n\tkp.keep(x)\n\treturn x.n"+usedAfter+"\n}\n")
	}
}

// TestSharedLoadIntoInterfaceMethodValue: loaded memory handed to a method that writes through it,
// where the method is reached through a method value and a method expression of the interface.
func TestSharedLoadIntoInterfaceMethodValue(t *testing.T) {
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\ntype bumper interface{ bump(t *T) }\n\n"+
		"type a struct{}\n\nfunc (a) bump(t *T) { t.n++ }"+loadedWrite+"\n\n"+
		"func methodValue() {\n\tc := cur.Load()\n\tvar b bumper = a{}\n\tf := b.bump\n\tf(c)\n}\n\n"+
		"type e struct{}\n\nfunc (e) bump(t *T) { t.n++ }"+loadedWrite+"\n\n"+
		"func methodExpr() {\n\tc := cur.Load()\n\tvar b bumper = e{}\n\tbumper.bump(b, c)\n}\n")
}

// TestSharedLoadCalleeInStructReachedOtherWays: loaded memory handed to the function a struct field
// holds, where the struct value reaches the call as the result of a call, as a field of another
// struct, as a slice element, and from a channel.
func TestSharedLoadCalleeInStructReachedOtherWays(t *testing.T) {
	typ := func(name string) string {
		return "type h" + name + " struct{ f func(*T) }\n\nfunc w" + name + "(t *T) { t.n++ }" + loadedWrite + "\n\n"
	}
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		typ("Call")+"func mkCall() hCall { return hCall{f: wCall} }\n\nfunc call() {\n\tc := cur.Load()\n\th := mkCall()\n\th.f(c)\n}\n\n"+
		typ("Nested")+"type outer struct{ in hNested }\n\nfunc nested() {\n\tc := cur.Load()\n\to := outer{in: hNested{f: wNested}}\n\to.in.f(c)\n}\n\n"+
		typ("Slice")+"func slice() {\n\tc := cur.Load()\n\ts := []hSlice{{f: wSlice}}\n\th := s[0]\n\th.f(c)\n}\n\n"+
		typ("Chan")+"func channel(ch chan hChan) {\n\tc := cur.Load()\n\tgo func() { ch <- hChan{f: wChan} }()\n\th := <-ch\n\th.f(c)\n}\n")
}
