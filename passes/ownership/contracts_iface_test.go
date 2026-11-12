package ownership_test

import (
	"fmt"
	"strings"
	"testing"
)

// Memory loaded from a shared container is read-only (SAFE005), and the check follows it into the
// functions it is passed to. It did not follow it into a call of an interface method: the call was
// skipped whatever its receiver was, and a method that wrote the memory was not reported. The
// tests below pass loaded memory through an interface in the ways a program can.

const loadedWrite = " // want \"SAFE005: write through shared value loaded from\""

const ifaceProgram = "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ n int }\n\nvar cur atomic.Pointer[T]\n\n"

// TestSharedLoadIntoInterfaceMethod: the receiver is of one type in sight.
//   - arg: the method writes its argument.
//   - recv: the loaded memory is itself the receiver, and the method writes through it.
//   - value: the method has a value receiver, so the call goes through a wrapper.
//   - promoted: the method belongs to an embedded type.
//   - reader: the method only reads, and nothing is reported.
func TestSharedLoadIntoInterfaceMethod(t *testing.T) {
	analyseWalk(t, ifaceProgram+
		"type bumper interface{ bump(t *T) }\n\ntype self interface{ touch() }\n\n"+
		"type ptr struct{}\n\nfunc (*ptr) bump(t *T) { t.n++ }"+loadedWrite+"\n\n"+
		"func arg() {\n\tc := cur.Load()\n\tvar b bumper = &ptr{}\n\tb.bump(c)\n}\n\n"+
		"func (t *T) touch() { t.n++ }"+loadedWrite+"\n\n"+
		"func recv() {\n\tvar s self = cur.Load()\n\ts.touch()\n}\n\n"+
		"type val struct{}\n\nfunc (val) bump(t *T) { t.n++ }"+loadedWrite+"\n\n"+
		"func value() {\n\tc := cur.Load()\n\tvar b bumper = val{}\n\tb.bump(c)\n}\n\n"+
		"type inner struct{}\n\nfunc (inner) bump(t *T) { t.n++ }"+loadedWrite+"\n\ntype outer struct{ inner }\n\n"+
		"func promoted() {\n\tc := cur.Load()\n\tvar b bumper = outer{}\n\tb.bump(c)\n}\n\n"+
		"type quiet struct{}\n\nfunc (quiet) bump(t *T) { _ = t.n }\n\n"+
		"func reader() {\n\tc := cur.Load()\n\tvar b bumper = quiet{}\n\tb.bump(c)\n}\n")
}

// TestSharedLoadIntoInterfaceMethodOfAnotherPackage: the method is (*bytes.Buffer).Read, which
// writes the slice it is handed. Its body is not in this package, so what it does is taken from its
// summary and reported at the call.
func TestSharedLoadIntoInterfaceMethodOfAnotherPackage(t *testing.T) {
	analyseWalk(t, "package walk\n\nimport (\n\t\"bytes\"\n\t\"io\"\n\t\"sync/atomic\"\n)\n\ntype T struct{ buf []byte }\n\nvar cur atomic.Pointer[T]\n\n"+
		"func read() {\n\tc := cur.Load()\n\tvar r io.Reader = &bytes.Buffer{}\n\t_, _ = r.Read(c.buf) // want \"SAFE005: shared value loaded from .* is passed to .*Read, which writes it\"\n}\n")
}

// TestSharedLoadIntoInterfaceMethodChosenAmongMany: the receiver is one of 100 types whose method
// reads, or one whose method writes. With 2 the write is found. With 100 the trace of the receiver
// is cut before the writer, and the call says so. A call that hands over nothing loaded (plain)
// does not trace its receiver and says nothing.
func TestSharedLoadIntoInterfaceMethodChosenAmongMany(t *testing.T) {
	var types strings.Builder
	for i := range 100 {
		fmt.Fprintf(&types, "type r%d struct{}\n\nfunc (r%d) bump(t *T) {}\n\n", i, i)
	}
	use := func(name, last string, n int, arg, want string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(k int) {\n\tc := cur.Load()\n\t_ = c\n\tvar f bumper\n\tswitch k {\n", name)
		for i := range n {
			fmt.Fprintf(&b, "\tcase %d:\n\t\tf = r%d{}\n", i, i)
		}
		fmt.Fprintf(&b, "\tdefault:\n\t\tf = %s{}\n\t}\n\tf.bump(%s)%s\n}\n\n", last, arg, want)
		return b.String()
	}
	const cut = " // want \"SAFE005: which types the receiver of this call may be could not be followed to the end .it comes through more than 14 steps or from more than 96 values., so what the methods not reached write through the shared value passed to them is not checked\""
	analyseWalk(t, ifaceProgram+"type bumper interface{ bump(t *T) }\n\n"+
		"type wFar struct{}\n\nfunc (wFar) bump(t *T) { t.n++ }\n\n"+
		"type wNear struct{}\n\nfunc (wNear) bump(t *T) { t.n++ }"+loadedWrite+"\n\n"+types.String()+
		use("far", "wFar", 100, "c", cut)+use("near", "wNear", 2, "c", "")+use("plain", "wFar", 100, "&T{}", ""))
}

// TestSharedLoadIntoInterfaceMethodOfUnknownReceiver: the receiver is a parameter of an exported
// function, so the types it may be are not all in this package. The check follows the ones it can
// see and says nothing about the rest, as it does for a function value of unknown origin: here it
// sees none, and nothing is reported. This is a limit of the check, written down so that a change
// to it is a decision.
func TestSharedLoadIntoInterfaceMethodOfUnknownReceiver(t *testing.T) {
	analyseWalk(t, ifaceProgram+"type Bumper interface{ Bump(t *T) }\n\n"+
		"func Use(b Bumper) { // want Use:\"ownership summary\"\n\tc := cur.Load()\n\tb.Bump(c)\n}\n")
}
