package ownership_test

import (
	"strconv"
	"strings"
	"testing"
)

// A function that publishes its argument, by itself or through a callable or a receiver handed to
// it, has a publishing contract at its call. The contract used to stand for everything the
// function does with that argument: whatever else it did with it (stored it, handed it to a second
// callable that is not known or does not publish) was dropped at the call, and the same statements
// written out in the caller were reported.
const publishAndMoreHead = `package p

import (
	"sync"
	"sync/atomic"
)

type T struct{ n int }

var table = map[string]int{"a": 1}

type H struct{ tab map[string]int }

func pubH(f, g func(*H), h *H) {
	f(h)
	g(h)
}

func pubLoad(m *sync.Map, x *T) bool {
	_, dup := m.LoadOrStore("k", x)
	return dup
}

func pubLoadAndKeep(m *sync.Map, x *T) bool {
	_, dup := m.LoadOrStore("k", x)
	sink = x
	return dup
}

var sink *T

type storer interface{ Store(*T) }

func wrapKeepEarly(f func(*T), x *T) { pubAndKeep(f, x) }

func wrapOnlyEarly(f func(*T), x *T) { pubOnly(f, x) }

func pubAndKeep(f func(*T), x *T) {
	f(x)
	sink = x
}

func pubAndCall(f, g func(*T), x *T) {
	f(x)
	g(x)
}

func pubOnly(f func(*T), x *T) { f(x) }

func storeAndKeep(s storer, x *T) {
	s.Store(x)
	sink = x
}

func storeAndCall(s storer, g func(*T), x *T) {
	s.Store(x)
	g(x)
}

func storeOnly(s storer, x *T) { s.Store(x) }

func wrapKeep(f func(*T), x *T) { pubAndKeep(f, x) }

func wrapOnly(f func(*T), x *T) { pubOnly(f, x) }

func wrapCall(f func(*T), x *T) { pubAndCall(f, keepIt, x) }

type keepStore struct{ p *atomic.Pointer[T] }

func (k keepStore) Store(x *T) {
	k.p.Store(x)
	sink = x
}

type plainStore struct{ p *atomic.Pointer[T] }

func (k plainStore) Store(x *T) { k.p.Store(x) }

func keepIt(t *T) { sink = t }

var pool sync.Pool

func pubAndGet(f func(*T), g func(*T) any, x *T) {
	f(x)
	_ = g(x)
}

func getOnly(t *T) any { return pool.Get() }

func getAndKeep(t *T) any {
	sink = t
	return pool.Get()
}

var ptr atomic.Pointer[T]

func storeP(x *T) { ptr.Store(x) }

func storeAndKeepP(x *T) {
	ptr.Store(x)
	sink = x
}

func readOnly(t *T) { _ = t.n }
`

// publishAndMoreDiags returns what is reported inside Pub, whose body is body. What the helpers
// above are reported for in their own bodies is the same in every case and is left out.
func publishAndMoreDiags(t *testing.T, params, body string) []string {
	t.Helper()
	pubLine := strings.Count(publishAndMoreHead, "\n") + 2
	var out []string
	for _, l := range advDiagLines(t, publishAndMoreHead+"\nfunc Pub(p *atomic.Pointer[T], c bool, m *sync.Map"+params+") {\n\tx := &T{}\n\t"+body+"\n}\n", "") {
		num, _, ok := strings.Cut(l, ":")
		if !ok {
			t.Fatalf("a finding without a line number: %q", l)
		}
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil {
			t.Fatalf("a finding without a line number: %q", l)
		}
		if n >= pubLine {
			out = append(out, l)
		}
	}
	return out
}

func TestPublishingHelperThatAlsoLeaksIsReported(t *testing.T) {
	const g = ", g func(*T)"
	for _, tc := range []struct{ name, params, body, writtenOut string }{
		{"published, then stored", "", "pubAndKeep(p.Store, x)", "p.Store(x)\n\tsink = x"},
		{"published, then handed to a callable that is not known", g, "pubAndCall(p.Store, g, x)", "p.Store(x)\n\tg(x)"},
		{"handed to a callable that is not known, then published", g, "pubAndCall(g, p.Store, x)", "g(x)\n\tp.Store(x)"},
		{"published, then handed to a function that stores it", "", "pubAndCall(p.Store, keepIt, x)", "p.Store(x)\n\tkeepIt(x)"},
		{"published through a receiver, then stored", "", "storeAndKeep(p, x)", "p.Store(x)\n\tsink = x"},
		{"published through a receiver, then handed to a callable that is not known", g, "storeAndCall(p, g, x)", "p.Store(x)\n\tg(x)"},
		{"through a wrapper of a helper that also stores", "", "wrapKeep(p.Store, x)", ""},
		{"through a wrapper declared before the helper that also stores", "", "wrapKeepEarly(p.Store, x)", ""},
		{"through a wrapper that fixes the second callable", "", "wrapCall(p.Store, x)", ""},
		{"the helper is one of two a variable may hold", "", "h := pubOnly\n\tif c {\n\t\th = pubAndKeep\n\t}\n\th(p.Store, x)", ""},
		{"a method that publishes and stores", "", "keepStore{p}.Store(x)", ""},
		{"a method that publishes and stores, through an interface", "", "var s storer = keepStore{p}\n\ts.Store(x)", ""},
		{"published into a map that hands back what it holds, then stored", "", "pubLoadAndKeep(m, x)", ""},
		{"handed to a function that publishes and stores", "", "pubOnly(storeAndKeepP, x)", "storeAndKeepP(x)"},
		{"published, then handed to a function with a contract of its own that stores it", "", "pubAndGet(p.Store, getAndKeep, x)", ""},
		{"a table written only at initialisation, published frozen and handed to a callable that is not known", ", ph *atomic.Pointer[H], gh func(*H)", "_ = x\n\tpubH(ph.Store, gh, &H{tab: table})", "_ = x\n\th := &H{tab: table}\n\tph.Store(h)\n\tgh(h)"},
	} {
		if got := publishAndMoreDiags(t, tc.params, tc.body); len(got) == 0 {
			t.Errorf("%s: %q leaves x where other code can reach it besides the publish, and nothing was reported", tc.name, tc.body)
		}
		if tc.writtenOut == "" {
			continue
		}
		if got := publishAndMoreDiags(t, tc.params, tc.writtenOut); len(got) == 0 {
			t.Errorf("%s: the same statements written out, %q, are not reported, so the helper form need not be", tc.name, tc.writtenOut)
		}
	}
}

// What the fix must not take away: a function that does nothing with its argument but publish it
// is still a publish and nothing more, by every route the test above takes.
func TestPublishingHelperThatOnlyPublishesStaysQuiet(t *testing.T) {
	for _, body := range []string{
		"p.Store(x)",
		"pubOnly(p.Store, x)",
		"pubAndCall(p.Store, p.Store, x)",
		"storeOnly(p, x)",
		"wrapOnly(p.Store, x)",
		"wrapOnlyEarly(p.Store, x)",
		"h := pubOnly\n\tif c {\n\t\th = wrapOnly\n\t}\n\th(p.Store, x)",
		"plainStore{p}.Store(x)",
		"pubLoad(m, x)",
		"pubOnly(storeP, x)",
		"pubAndGet(p.Store, getOnly, x)",
		"var s storer = plainStore{p}\n\ts.Store(x)",
	} {
		if got := publishAndMoreDiags(t, "", body); len(got) != 0 {
			t.Errorf("%q only publishes x and was reported: %v", body, got)
		}
	}
}

// A callable that is known and keeps nothing is not something else done with x: the helper form
// is as quiet as the same statements written out.
func TestPublishingHelperWithACallableThatKeepsNothingStaysQuiet(t *testing.T) {
	for _, body := range []string{
		"readOnly(x)\n\tp.Store(x)",
		"pubAndCall(readOnly, p.Store, x)",
		"pubAndCall(func(*T) {}, p.Store, x)",
		"h := readOnly\n\tif c {\n\t\th = func(*T) {}\n\t}\n\tpubAndCall(h, p.Store, x)",
	} {
		if got := publishAndMoreDiags(t, "", body); len(got) != 0 {
			t.Errorf("%q hands x to a function that keeps nothing and publishes it, and was reported: %v", body, got)
		}
	}
	// One of the two functions the variable may hold stores x: reported.
	if got := publishAndMoreDiags(t, "", "h := readOnly\n\tif c {\n\t\th = keepIt\n\t}\n\tpubAndCall(h, p.Store, x)"); len(got) == 0 {
		t.Error("one of the two functions handed to the helper stores x, and nothing was reported")
	}
}

// A known miss, pinned so that closing it is a decision and not an accident. Written out, a use
// of x after it is published is reported, also a read. Inside a helper the read comes after the
// publish too, but the helper's summary says only that it keeps nothing, not in which order it
// does things, so the call is a publish and nothing more.
func TestReadAfterPublishInsideAHelperIsAKnownMiss(t *testing.T) {
	if got := publishAndMoreDiags(t, "", "p.Store(x)\n\treadOnly(x)"); len(got) == 0 {
		t.Fatal("written out, a read of x after it is published is no longer reported: that one was a hit")
	}
	if got := publishAndMoreDiags(t, "", "pubAndCall(p.Store, readOnly, x)"); len(got) != 0 {
		t.Fatalf("the miss is closed (good): update this test and tests/stress/FINDINGS.md. Got %v", got)
	}
}
