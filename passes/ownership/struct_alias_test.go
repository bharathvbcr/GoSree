package ownership_test

import (
	"strconv"
	"strings"
	"testing"
)

// A struct made in one place is written wherever its pointer goes. The trace asked what a field
// of it holds by reading the stores made through the allocation itself (s := &S{}; s.fn = ...)
// and answered that those were all. A pointer that had been returned, stored, or joined with
// another on a branch is another name for the same struct, and a store through that name was
// not read: a struct made by a constructor and then given a publisher by its caller was called
// as what the constructor had put there.
//
// Where the pointer goes somewhere the trace does not read, the stores this package makes to that
// field of that struct type stand in for what may be written there, as they do where a pointer
// leads nowhere in sight, and the answer may not be all. A store made to another allocation is
// not one of them: a struct made somewhere else is other memory, whatever its type.
const structAliasHead = `package p

import "sync/atomic"

type T struct{ n int }

var p atomic.Pointer[T]

func noop(*T) {}

type S struct{ fn func(*T) }

type S2 S

func newS() *S { return &S{fn: noop} }

type W struct{ s *S }

var keepS *S
`

// Functions that write a struct through the package variable that may hold its pointer.
const (
	structAliasArm         = "\nfunc armKept() { keepS.fn = p.Store }\n"
	structAliasOverwrite   = "\nfunc overwriteKept() {\n\tu := S{fn: noop}\n\tu.fn = p.Store\n\t*keepS = u\n}\n"
	structAliasArmField    = "\nfunc setFn(pf *func(*T)) { *pf = p.Store }\n\nfunc armKeptField() { setFn(&keepS.fn) }\n"
	structAliasOverwriteAs = "\nfunc overwriteKeptAs() {\n\tu := S2{fn: noop}\n\tu.fn = p.Store\n\t*(*S2)(keepS) = u\n}\n"
	structAliasOther       = "\nvar keepG S\n\nfunc armOther() {\n\tkeepG.fn = p.Store\n\tu := S{fn: noop}\n\tu.fn = p.Store\n\tkeepG = u\n}\n"
)

func structAliasDiags(t *testing.T, body string) []string {
	t.Helper()
	return advDiagLines(t, structAliasHead+"\nfunc use(c bool) int {\n\tx := &T{}\n\t"+body+"\n\treturn x.n\n}\n", "is used after it was published")
}

func TestStructWrittenThroughAnotherNameForItsPointer(t *testing.T) {
	for name, body := range map[string]string{
		"control: set through the allocation itself":                "s := &S{fn: noop}\n\ts.fn = p.Store\n\ts.fn(x)",
		"made by a constructor, set by the caller":                  "s := newS()\n\ts.fn = p.Store\n\ts.fn(x)",
		"set through a pointer chosen on a branch":                  "s := &S{fn: noop}\n\tu := &S{fn: noop}\n\tr := s\n\tif c {\n\t\tr = u\n\t}\n\tr.fn = p.Store\n\ts.fn(x)",
		"set through a pointer kept in another struct":              "s := &S{fn: noop}\n\tw := W{s: s}\n\tw.s.fn = p.Store\n\ts.fn(x)",
		"set through a pointer kept in a package variable":          "s := &S{fn: noop}\n\tkeepS = s\n\tkeepS.fn = p.Store\n\ts.fn(x)",
		"set through a pointer variable a closure captures":         "q := &S{fn: noop}\n\tq.fn = p.Store\n\tpeek := func() bool { return q != nil }\n\t_ = peek()\n\tq.fn(x)",
		"a closure sets it through the pointer variable":            "q := &S{fn: noop}\n\tset := func() { q.fn = p.Store }\n\tset()\n\tq.fn(x)",
		"set through a pointer that may be this struct or another":  "s := newS()\n\tu := &S{fn: noop}\n\tr := u\n\tif c {\n\t\tr = s\n\t}\n\tr.fn = p.Store\n\ts.fn(x)",
		"set under another type through a pointer that may be this": "s := &S{fn: noop}\n\tu := &S{fn: noop}\n\tr := u\n\tif c {\n\t\tr = s\n\t}\n\t(*S2)(r).fn = p.Store\n\ts.fn(x)",
		"another struct is copied over one that may be this":        "s := &S{fn: noop}\n\tu := &S{fn: noop}\n\tr := u\n\tif c {\n\t\tr = s\n\t}\n\tv := S{fn: p.Store}\n\t*r = v\n\ts.fn(x)",
	} {
		if got := structAliasDiags(t, body); len(got) == 0 {
			t.Errorf("%s: the field may hold the publisher when it is called, and the use of x after that was not reported", name)
		}
	}
	// Written where the struct's pointer went: by a function that reads the package variable.
	const useHead = "\nfunc use(c bool) int {\n\tx := &T{}\n\t"
	const useTail = "\n\treturn x.n\n}\n"
	for _, c := range []struct{ name, writers, body string }{
		{"its field is set", structAliasArm, "s := &S{fn: noop}\n\tkeepS = s\n\tarmKept()\n\ts.fn(x)"},
		{"another struct is copied over it", structAliasOverwrite, "s := &S{fn: noop}\n\tkeepS = s\n\toverwriteKept()\n\ts.fn(x)"},
		{"another struct is copied over it under another type", structAliasOverwriteAs, "s := &S{fn: noop}\n\tkeepS = s\n\toverwriteKeptAs()\n\ts.fn(x)"},
		{"its field's address is handed to a setter", structAliasArmField, "s := &S{fn: noop}\n\tkeepS = s\n\tarmKeptField()\n\ts.fn(x)"},
		{"made by a constructor, a function sets some struct", structAliasArm, "s := newS()\n\tarmKept()\n\ts.fn(x)"},
	} {
		if got := advDiagLinesAfter(t, structAliasHead+c.writers, useHead+c.body+useTail, "is used after it was published"); len(got) == 0 {
			t.Errorf("%s: the field may hold the publisher when it is called, and the use of x after that was not reported", c.name)
		}
	}
	// With those functions in the package, a struct whose pointer stays in sight is not written
	// by them, and neither is one when all they write is a package variable of the type.
	for _, c := range []struct{ name, writers, body string }{
		{"its pointer stays here", structAliasArm + structAliasOverwrite + structAliasArmField, "s := &S{fn: noop}\n\tarmKept()\n\toverwriteKept()\n\tarmKeptField()\n\ts.fn(x)"},
		{"a package variable of the type is written", structAliasOther, "s := newS()\n\tarmOther()\n\ts.fn(x)"},
	} {
		if got := advDiagLinesAfter(t, structAliasHead+c.writers, useHead+c.body+useTail, "is used after it was published"); len(got) != 0 {
			t.Errorf("%s: the field never holds the publisher, and a publish of x was reported: %s", c.name, strings.Join(got, "; "))
		}
	}
	for name, body := range map[string]string{
		"made by a constructor, nothing sets it":                     "s := newS()\n\ts.fn(x)",
		"made by a constructor, set to something else":               "s := newS()\n\ts.fn = noop\n\ts.fn(x)",
		"a pointer variable a closure captures, set to something":    "q := &S{fn: noop}\n\tq.fn = noop\n\tpeek := func() bool { return q != nil }\n\t_ = peek()\n\tq.fn(x)",
		"its pointer goes nowhere, another struct has the publisher": "s := &S{fn: noop}\n\tu := &S{fn: p.Store}\n\t_ = u\n\ts.fn(x)",
		// The struct's pointer is out of sight (a constructor returned it, a package variable
		// holds it), and the only publisher is stored to another allocation.
		"made by a constructor, another struct holds the publisher":        "s := newS()\n\tu := &S{fn: p.Store}\n\t_ = u\n\ts.fn(x)",
		"made by a constructor, another struct is set after":               "s := newS()\n\tu := &S{fn: noop}\n\tu.fn = p.Store\n\ts.fn(x)",
		"made by a constructor, one of two other structs is set":           "s := newS()\n\tu := &S{fn: noop}\n\tv := &S{fn: noop}\n\tr := u\n\tif c {\n\t\tr = v\n\t}\n\tr.fn = p.Store\n\ts.fn(x)",
		"kept in a package variable, another struct holds the publisher":   "s := &S{fn: noop}\n\tkeepS = s\n\tu := &S{fn: p.Store}\n\t_ = u\n\ts.fn(x)",
		"made by a constructor, another struct is set under another type":  "s := newS()\n\tu := &S{fn: noop}\n\t(*S2)(u).fn = p.Store\n\ts.fn(x)",
		"made by a constructor, one of two others chosen in a loop is set": "s := newS()\n\tu := &S{fn: noop}\n\tv := &S{fn: noop}\n\tr := u\n\tfor i := 0; i < 3; i++ {\n\t\tif c {\n\t\t\tr = v\n\t\t}\n\t}\n\tr.fn = p.Store\n\ts.fn(x)",
		"made by a constructor, another struct is copied over another":     "s := newS()\n\tu := &S{fn: noop}\n\tv := S{fn: p.Store}\n\t*u = v\n\ts.fn(x)",
	} {
		if got := structAliasDiags(t, body); len(got) != 0 {
			t.Errorf("%s: the field never holds the publisher, and a publish of x was reported: %s", name, strings.Join(got, "; "))
		}
	}
}

// advDiagLinesAfter gives the diagnostics of head+body that match and are on a line of body.
func advDiagLinesAfter(t *testing.T, head, body, match string) []string {
	t.Helper()
	headLines := strings.Count(head, "\n")
	var out []string
	for _, d := range advDiagLines(t, head+body, match) {
		line, _, _ := strings.Cut(d, ":")
		if n, err := strconv.Atoi(line); err != nil || n > headLines {
			out = append(out, d)
		}
	}
	return out
}

// A struct made in a function with a type parameter and read from a function without one: the
// struct and the stores that may be to it are found in the one body the function has, whichever
// instance the call names, so a store through a pointer that may be the struct is not taken for
// a store to other memory. The store is through the last of more names than are read.
func TestStructMadeInAGenericFunctionIsWrittenThroughAnotherName(t *testing.T) {
	gen := func(last string) string {
		var b strings.Builder
		b.WriteString("\nfunc mk[U any](c bool, v U) *S {\n\ts := &S{fn: noop}\n\tu := &S{fn: noop}\n")
		const names = 80
		for i := range names {
			val := "noop"
			if i == names-1 {
				val = last
			}
			n := strconv.Itoa(i)
			b.WriteString("\tr" + n + " := u\n\tif c {\n\t\tr" + n + " = s\n\t}\n\tr" + n + ".fn = " + val + "\n")
		}
		b.WriteString("\treturn s\n}\n\nfunc use(c bool) int {\n\tx := &T{}\n\ts := mk[int](c, 1)\n\tif c {\n\t\ts = mk[string](c, \"\")\n\t}\n\ts.fn(x)\n\treturn x.n\n}\n")
		return b.String()
	}
	if got := advDiagLinesAfter(t, structAliasHead, gen("p.Store"), "is used after it was published"); len(got) == 0 {
		t.Errorf("the field may hold the publisher when it is called, and the use of x after that was not reported")
	}
	if got := advDiagLinesAfter(t, structAliasHead, gen("noop"), "is used after it was published"); len(got) != 0 {
		t.Errorf("the field never holds the publisher, and a publish of x was reported: %s", strings.Join(got, "; "))
	}
}

// Three structs whose pointers are out of sight and that are copied over one another in a ring
// where the pointers went: each is read for what is copied over it once on a walk. A field asked of a
// struct value is not marked as visited, so without that the copies would be read again at each
// struct the walk comes to, until the depth limit cuts the question.
func TestStructsCopiedOverEachOtherOutOfSightAreReadOnce(t *testing.T) {
	const cross = `
var keepA, keepB, keepC *S

func cross() {
	*keepA = *keepB
	*keepB = *keepC
	*keepC = *keepA
}
`
	body := func(set string) string {
		return "\nfunc use(c bool) int {\n\tx := &T{}\n\ts := &S{fn: noop}\n\tkeepA = s\n\tu := &S{fn: noop}\n\tkeepB = u\n\tv := &S{fn: noop}\n\tkeepC = v\n\tv.fn = " + set + "\n\tcross()\n\ts.fn(x)\n\treturn x.n\n}\n"
	}
	if got := advDiagLinesAfter(t, structAliasHead+cross, body("p.Store"), "is used after it was published"); len(got) == 0 {
		t.Errorf("the publisher in one struct is copied to the next and from there to the one called, and the use of x after the call was not reported")
	}
	for _, set := range []string{"p.Store", "noop"} {
		if got := advDiagLinesAfter(t, structAliasHead+cross, body(set), "could not be followed"); len(got) != 0 {
			t.Errorf("with %s: the copies are all in sight, and the walk was cut: %s", set, strings.Join(got, "; "))
		}
	}
	if got := advDiagLinesAfter(t, structAliasHead+cross, body("noop"), "is used after it was published"); len(got) != 0 {
		t.Errorf("no struct holds the publisher, and a publish of x was reported: %s", strings.Join(got, "; "))
	}
}

// The names of a struct are read up to a limit (cellViewLimit), and past it the struct is one
// whose address is out of sight. A store through a name that was not read is then found among
// the stores that stand in: the pointer it is made through may be this struct, so it is not a
// store to other memory.
func TestStoreThroughANameOfAStructPastTheViewLimit(t *testing.T) {
	var body strings.Builder
	body.WriteString("s := &S{fn: noop}\n\tu := &S{fn: noop}\n")
	const names = 80
	for i := range names {
		val := "noop"
		if i == names-1 {
			val = "p.Store"
		}
		n := strconv.Itoa(i)
		body.WriteString("\tr" + n + " := u\n\tif c {\n\t\tr" + n + " = s\n\t}\n\tr" + n + ".fn = " + val + "\n")
	}
	body.WriteString("\ts.fn(x)")
	if got := structAliasDiags(t, body.String()); len(got) == 0 {
		t.Errorf("the last of %d names of the struct sets the publisher, and the use of x after the call was not reported", names)
	}
}

// What is copied over a struct is read once for each field: a field whose value is moved from
// another field of the same struct, where the pointer went, is read for that other field too.
func TestStructCopiedOverOutOfSightIsReadForEachField(t *testing.T) {
	const head = `
type S3 struct{ a, b func(*T) }

func other(*T) {}

var keep3 *S3

func cp() {
	u := S3{a: noop, b: noop}
	u.b = %s
	*keep3 = u
}

func mv() {
	w := S3{a: noop, b: noop}
	w.a = keep3.b
	*keep3 = w
}
`
	const body = "\nfunc use(c bool) int {\n\tx := &T{}\n\ts := &S3{a: noop, b: other}\n\tkeep3 = s\n\tcp()\n\tmv()\n\ts.a(x)\n\treturn x.n\n}\n"
	if got := advDiagLinesAfter(t, structAliasHead+strings.Replace(head, "%s", "p.Store", 1), body, "is used after it was published"); len(got) == 0 {
		t.Errorf("the publisher is copied into one field and moved from there to the one called, and the use of x after the call was not reported")
	}
	if got := advDiagLinesAfter(t, structAliasHead+strings.Replace(head, "%s", "noop", 1), body, "is used after it was published"); len(got) != 0 {
		t.Errorf("neither field holds the publisher, and a publish of x was reported: %s", strings.Join(got, "; "))
	}
}

// What is copied over a struct is read once for each struct: one that is copied over the struct
// asked about is read for what is copied over it in turn, here through the last of more names
// than are read, a pointer that may be it and cannot be the struct asked about.
func TestStructCopiedOverOutOfSightIsReadForEachStruct(t *testing.T) {
	const head = `
var keepA, keepB *S

func other(*T) {}

func copyAB() { *keepA = *keepB }
`
	body := func(last string) string {
		var b strings.Builder
		b.WriteString("\nfunc use(c bool) int {\n\tx := &T{}\n\ts := &S{fn: noop}\n\tkeepA = s\n\tu := &S{fn: other}\n\tkeepB = u\n\tw := &S{fn: noop}\n\tpub := S{fn: " + last + "}\n")
		const names = 80
		for i := range names {
			n := strconv.Itoa(i)
			b.WriteString("\tr" + n + " := w\n\tif c {\n\t\tr" + n + " = u\n\t}\n")
			if i == names-1 {
				b.WriteString("\t*r" + n + " = pub\n")
			} else {
				b.WriteString("\tr" + n + ".fn = noop\n")
			}
		}
		b.WriteString("\tcopyAB()\n\ts.fn(x)\n\treturn x.n\n}\n")
		return b.String()
	}
	if got := advDiagLinesAfter(t, structAliasHead+head, body("p.Store"), "is used after it was published"); len(got) == 0 {
		t.Errorf("the publisher is copied over one struct and from there over the one called, and the use of x after the call was not reported")
	}
	if got := advDiagLinesAfter(t, structAliasHead+head, body("noop"), "is used after it was published"); len(got) != 0 {
		t.Errorf("no struct holds the publisher, and a publish of x was reported: %s", strings.Join(got, "; "))
	}
}
