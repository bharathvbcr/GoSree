package ownership_test

import "testing"

// A package variable written only at initialisation may be published frozen (into an
// atomic.Pointer) without a finding, but only while its memory leaves by no other route. Whether it
// does is worked out for the whole package, and it has to be worked out whenever such a publish is
// checked, however the publishing call is reached. It used to be worked out only in a package with
// a direct call to a freezing publisher, so the same publish made through an interface was passed
// as clean.

func escapeRoutes(leak bool, body string) string {
	src := "package walk\n\nimport \"sync/atomic\"\n\ntype T struct{ tab map[string]int }\n\nvar table = map[string]int{\"a\": 1}\n\n"
	if leak {
		src += "func leak() map[string]int { return table }\n\n"
	}
	return src + body
}

const escapedTableWant = " // want \"SAFE002: composite literal is published via .* but is not owned here: it may alias package variable table\" \"SAFE004: composite literal is published via .* but also escapes: stored in package variable table\""

// The four ways the test reaches (*atomic.Pointer[T]).Store. The expectation on the call goes
// between the two parts.
var freezingPublishRoutes = map[string][2]string{
	"direct call":       {"func pub(p *atomic.Pointer[T]) {\n\tx := &T{tab: table}\n\tp.Store(x)", "\n}\n"},
	"method value":      {"func pub(p *atomic.Pointer[T]) {\n\tx := &T{tab: table}\n\tf := p.Store\n\tf(x)", "\n}\n"},
	"interface":         {"type storer interface{ Store(v *T) }\n\nfunc pub(p *atomic.Pointer[T]) {\n\tx := &T{tab: table}\n\tvar s storer = p\n\ts.Store(x)", "\n}\n"},
	"method expression": {"func pub(p *atomic.Pointer[T]) {\n\tx := &T{tab: table}\n\tf := (*atomic.Pointer[T]).Store\n\tf(p, x)", "\n}\n"},
}

// The table is returned by another function, so it is not init-only: publishing a value that holds
// it is reported, by every route.
func TestEscapedTablePublishedByEveryRoute(t *testing.T) {
	for name, parts := range freezingPublishRoutes {
		t.Run(name, func(t *testing.T) {
			analyseWalk(t, escapeRoutes(true, parts[0]+escapedTableWant+parts[1]))
		})
	}
}

// The table goes nowhere else, so the exemption holds: nothing is reported, by any route.
func TestInitOnlyTablePublishedByEveryRoute(t *testing.T) {
	for name, parts := range freezingPublishRoutes {
		t.Run(name, func(t *testing.T) {
			analyseWalk(t, escapeRoutes(false, parts[0]+parts[1]))
		})
	}
}
