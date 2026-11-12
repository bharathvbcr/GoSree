package ownership_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// The trace of which functions a called value may be stops at a depth and at a budget of values
// (originDepth, originBudget in contracts.go). A contract resolved from a trace that was cut speaks
// for the callees it reached. What any one callee may do, publish an argument or return loaded
// memory, was then simply missing for the others: the finding that depends on it was not made and
// nothing said so. Each test below has a program just past a bound next to the same program inside
// it. The long one must say at the call that it could not be followed (wantCutArg, wantCutResult);
// the short one must give the finding.

// pickAmong writes "decl; switch k { case i: f = cand(i) ...; default: f = last }".
func pickAmong(b *strings.Builder, decl string, n int, cand func(i int) string, last string) {
	fmt.Fprintf(b, "\t%s\n\tswitch k {\n", decl)
	for i := range n {
		fmt.Fprintf(b, "\tcase %d:\n\t\tf = %s\n", i, cand(i))
	}
	fmt.Fprintf(b, "\tdefault:\n\t\tf = %s\n\t}\n", last)
}

// numbered writes n declarations from a template in which # stands for the index.
func numbered(n int, template string) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(strings.ReplaceAll(template, "#", strconv.Itoa(i)))
	}
	return b.String()
}

// named returns a function that names the i-th candidate.
func named(prefix, suffix string) func(int) string {
	return func(i int) string { return prefix + strconv.Itoa(i) + suffix }
}

const (
	usedAfter  = " // want \"SAFE003: composite literal is used after it was published via\""
	paramPub   = " // want \"SAFE002: parameter v is published via\""
	pubProgram = "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\nfunc pub(m *sync.Map, v *T) {\n\tm.Store(\"k\", v)" + paramPub + "\n}\n\n"
	nops       = "func nop#(m *sync.Map, v *T) {}\n\n"
)

// TestPublisherChosenAmongMany: a value is handed to a function chosen among 100 that do nothing
// and one that stores it in a sync.Map, and is used afterwards. With 2 candidates the use after the
// store is reported. With 100 the publisher lies past the budget, and the use passed in silence.
// A call that passes nothing a callee could publish (farPlain) has nothing to say.
func TestPublisherChosenAmongMany(t *testing.T) {
	pick := func(name string, n int, atCall, atUse string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int, m *sync.Map) int {\n\tx := &T{}\n", name)
		pickAmong(&b, "var f func(*sync.Map, *T)", n, named("nop", ""), "pub")
		fmt.Fprintf(&b, "\tf(m, x)%s\n\treturn x.n%s\n}\n\n", atCall, atUse)
		return b.String()
	}
	var plain strings.Builder
	plain.WriteString("func farPlain(k int) {\n")
	pickAmong(&plain, "var f func(int, string)", 100, named("plain", ""), "plain0")
	plain.WriteString("\tf(k, \"s\")\n}\n\n")
	analyseWalk(t, pubProgram+numbered(100, nops)+numbered(100, "func plain#(n int, s string) {}\n\n")+
		pick("far", 100, " // want "+wantCutArg, "")+pick("near", 2, "", usedAfter)+plain.String())
}

// TestPublisherThroughManyConversions: the same, with the one publisher behind 16 conversions
// between two function types, past the depth of the trace.
func TestPublisherThroughManyConversions(t *testing.T) {
	conv := func(n int) string {
		expr := "pub"
		for i := range n {
			expr = fmt.Sprintf("g%d(%s)", i%2, expr)
		}
		return expr
	}
	use := func(name string, n int, atCall, atUse string) string {
		return fmt.Sprintf("func %s(m *sync.Map) int {\n\tx := &T{}\n\tf := %s\n\tf(m, x)%s\n\treturn x.n%s\n}\n\n", name, conv(n), atCall, atUse)
	}
	analyseWalk(t, pubProgram+"type g0 func(*sync.Map, *T)\n\ntype g1 func(*sync.Map, *T)\n\n"+
		use("far", 16, " // want "+wantCutArg, "")+use("near", 2, "", usedAfter))
}

// TestPublisherChosenInsideAHelper: the choice is made inside a helper, and it is the helper's
// caller that uses the value afterwards. The helper's summary is built from the same cut trace, so
// the caller's finding goes missing too; the report is at the call inside the helper, where the
// trace was cut.
func TestPublisherChosenInsideAHelper(t *testing.T) {
	helper := func(name string, n int, atCall, atUse string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func put%s(k int, m *sync.Map, v *T) {\n", name)
		pickAmong(&b, "var f func(*sync.Map, *T)", n, named("nop", ""), "pub")
		fmt.Fprintf(&b, "\tf(m, v)%s\n}\n\n", atCall)
		fmt.Fprintf(&b, "func use%s(k int, m *sync.Map) int {\n\tx := &T{}\n\tput%s(k, m, x)\n\treturn x.n%s\n}\n\n", name, name, atUse)
		return b.String()
	}
	analyseWalk(t, pubProgram+numbered(100, nops)+
		helper("Far", 100, " // want "+wantCutArg, "")+helper("Near", 2, paramPub, usedAfter))
}

// TestPublisherHandedToACallee: the function is chosen by the caller and handed to a callee that
// calls it. Both calls are cut: the caller's, which resolves what the callee does with the function
// it is handed, and the callee's own call of it.
func TestPublisherHandedToACallee(t *testing.T) {
	use := func(name string, n int, atCall, atInner, atUse string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func use%s(k int, m *sync.Map) int {\n\tx := &T{}\n", name)
		pickAmong(&b, "var f func(*sync.Map, *T)", n, named("nop", ""), "pub")
		fmt.Fprintf(&b, "\tapply%s(f, m, x)%s\n\treturn x.n%s\n}\n\n", name, atCall, atUse)
		fmt.Fprintf(&b, "func apply%s(f func(*sync.Map, *T), m *sync.Map, v *T) {\n\tf(m, v)%s\n}\n\n", name, atInner)
		return b.String()
	}
	analyseWalk(t, pubProgram+numbered(100, nops)+
		use("Far", 100, " // want "+wantCutArg, " // want "+wantCutArg, "")+use("Near", 2, "", "", usedAfter))
}

// TestInterfacePublisherChosenAmongMany: the value is stored through an interface whose receiver is
// one of 100 types that do nothing or a *sync.Map.
func TestInterfacePublisherChosenAmongMany(t *testing.T) {
	use := func(name string, n int, atCall, atUse string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func use%s(k int, m *sync.Map) int {\n\tx := &T{}\n", name)
		pickAmong(&b, "var f storer", n, named("&s", "{}"), "m")
		fmt.Fprintf(&b, "\tf.Store(\"k\", x)%s\n\treturn x.n%s\n}\n\n", atCall, atUse)
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\ntype T struct{ n int }\n\ntype storer interface{ Store(k, v any) }\n\n"+
		numbered(100, "type s# struct{}\n\nfunc (*s#) Store(k, v any) {} // want Store:\"ownership summary\"\n\n")+
		use("Far", 100, " // want "+wantCutArg, "")+use("Near", 2, "", usedAfter))
}

// TestInterfaceCallThatHasNoContract: an interface method call gets a contract from the methods of
// the standard containers, by name and signature, or from a method of the package that publishes
// or loads (contracts_usermethod_test.go), so a call no such method can stand behind has nothing
// to report however many types its receiver may be, and its receiver is not traced. Each function
// below calls a method on a receiver that is one of 101 types whose methods do nothing. The program
// imports sync: a signature is compared only against a container the package imports itself.
//   - write: no container has a method Write.
//   - put: sync.Pool has Put(any), which is not Put(string, *T).
//   - load: sync.Map has Load(any) (any, bool), which publishes nothing, so the argument has
//     nothing to say; the result may be loaded memory, and that is said.
//   - get: sync.Pool has Get() any, which hands out a value of the caller's own, not loaded memory.
//   - relay: the receiver is handed to a function that calls Write on it. Resolving that call looks
//     at what the function does with the receiver it is handed, which is nothing with a contract.
func TestInterfaceCallThatHasNoContract(t *testing.T) {
	impl := func(typ, method, sig, body string) string {
		return numbered(101, "type "+typ+"# struct{}\n\nfunc (*"+typ+"#) "+method+sig+" { "+body+" } // want "+method+":\"ownership summary\"\n\n")
	}
	use := func(name, iface, typ, call string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int) {\n\tx := &T{}\n", name)
		pickAmong(&b, "var f "+iface, 100, named("&"+typ, "{}"), "&"+typ+"100{}")
		fmt.Fprintf(&b, "\t%s\n\t_ = x\n}\n\n", call)
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport \"sync\"\n\nvar _ sync.Mutex\n\ntype T struct{ n int }\n\n"+
		"type writer interface{ Write(v *T) }\n\ntype putter interface{ Put(k string, v *T) }\n\n"+
		"type loader interface{ Load(k any) (any, bool) }\n\ntype getter interface{ Get() any }\n\n"+
		impl("w", "Write", "(v *T)", "")+impl("p", "Put", "(k string, v *T)", "")+
		impl("l", "Load", "(k any) (any, bool)", "return nil, false")+impl("g", "Get", "() any", "return nil")+
		use("write", "writer", "w", "f.Write(x)")+use("put", "putter", "p", "f.Put(\"k\", x)")+
		use("load", "loader", "l", "_, _ = f.Load(x) // want "+wantCutResult)+use("get", "getter", "g", "_ = f.Get()")+
		use("relay", "writer", "w", "writeTo(f, x)")+"func writeTo(w writer, v *T) { w.Write(v) }\n")
}

// TestLoaderChosenAmongMany: a function chosen among 100 that return a new value and one that
// returns what an atomic.Pointer holds; the result is written. With 2 candidates the write through
// loaded memory is reported. With 100 the loader lies past the budget and the write passed. A call
// whose result cannot be loaded memory worth checking (farCount, an int) has nothing to say.
func TestLoaderChosenAmongMany(t *testing.T) {
	use := func(name string, n int, atCall, atWrite string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int) {\n", name)
		pickAmong(&b, "var f func() *T", n, named("mk", ""), "load")
		fmt.Fprintf(&b, "\tc := f()%s\n\tc.n++%s\n}\n\n", atCall, atWrite)
		return b.String()
	}
	var count strings.Builder
	count.WriteString("func farCount(k int) int {\n")
	pickAmong(&count, "var f func() int", 100, named("count", ""), "count0")
	count.WriteString("\treturn f()\n}\n\n")
	analyseWalk(t, "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"+
		"func load() *T { return cur.Load() }\n\n"+
		numbered(100, "func mk#() *T { return &T{} }\n\n")+numbered(100, "func count#() int { return # }\n\n")+
		use("far", 100, " // want "+wantCutResult, "")+
		use("near", 2, "", " // want \"SAFE005: write through shared value loaded from\"")+count.String())
}

// TestFreezeIsNotAssumedPastTheCut: a value that refers to a package variable written only at
// initialisation may be published through a container whose readers cannot write it
// (atomic.Pointer), and may not through one whose readers can (sync.Map). With one publisher of
// each kind the second is seen and the value is reported. With 100 of the first kind before it,
// the second lies past the budget; "every callee freezes it" was then taken from the 96 found.
func TestFreezeIsNotAssumedPastTheCut(t *testing.T) {
	const notFrozen = " // want \"SAFE002: composite literal is published via\" \"SAFE004: composite literal is published via\""
	use := func(name string, n int, last, want string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func use%s(k int, p *atomic.Pointer[T], m *sync.Map) {\n\tx := &T{tab: table}\n", name)
		pickAmong(&b, "var f func(*atomic.Pointer[T], *sync.Map, *T)", n, named("storeP", ""), last)
		fmt.Fprintf(&b, "\tf(p, m, x)%s\n}\n\n", want)
		return b.String()
	}
	analyseWalk(t, "package walk\n\nimport (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\ntype T struct{ tab map[string]int }\n\nvar table = map[string]int{\"a\": 1}\n\n"+
		"func storeM(p *atomic.Pointer[T], m *sync.Map, v *T) { m.Store(\"k\", v) }"+paramPub+"\n\n"+
		numbered(100, "func storeP#(p *atomic.Pointer[T], m *sync.Map, v *T) { p.Store(v) }"+paramPub+"\n\n")+
		use("Far", 100, "storeM", notFrozen)+use("Near", 2, "storeM", notFrozen)+use("AllFrozen", 2, "storeP0", ""))
}
