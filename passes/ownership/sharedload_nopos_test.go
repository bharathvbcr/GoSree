package ownership_test

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

// TestSharedLoadWriteInSyntheticWrapperHasPosition: a value loaded from an atomic pointer is read-only
// once published (SAFE005). Here it is written by a method of a generic type of another package, so
// the call goes through a wrapper go/ssa synthesises, whose instructions have no source position,
// and the check follows the value into it. The finding used to be reported at no position, and
// findings of one line are one finding, so the five calls marked below were reported as one, at no
// line. Each is now reported at the call in the source that leads into the wrapper, once per call:
// two calls of one method from two functions, two methods, a method value, and a hatch on such a
// call mutes it.
func TestSharedLoadWriteInSyntheticWrapperHasPosition(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"loadnopos/dep/dep.go": `package dep

type Cache[V any] struct{ m map[string]V }

func New[V any]() *Cache[V] { return &Cache[V]{m: map[string]V{}} }

func (c *Cache[V]) Put(k string, v V) { c.m[k] = v }

func (c *Cache[V]) Drop(k string) { delete(c.m, k) }

func (c *Cache[V]) Len() int { return len(c.m) }

func Fill[V any](c *Cache[V], k string, v V) { c.m[k] = v }
`,
		"loadnopos/loadnopos.go": `package loadnopos

import (
	"sync/atomic"

	"loadnopos/dep"
)

var (
	cur   atomic.Pointer[dep.Cache[int]]
	other atomic.Pointer[dep.Cache[int]]
)

func first() {
	c := cur.Load()
	c.Put("k", 1) // want "is passed to .*Put, which writes it"
}

func second() {
	c := other.Load()
	c.Put("j", 2)  // want "is passed to .*Put, which writes it"
	c.Drop("j")    // want "is passed to .*Drop, which writes it"
	_ = c.Len()
}

func methodValue() {
	c := cur.Load()
	put := c.Put
	put("k", 3) // want "is passed to .*Put, which writes it"
}

func genericFunc() {
	c := cur.Load()
	dep.Fill(c, "k", 5) // want "is passed to .*Fill, which writes it"
}

func genericFuncAgain() {
	c := other.Load()
	dep.Fill(c, "j", 6) // want "is passed to .*Fill, which writes it"
	dep.Fill(c, "i", 7) // want "is passed to .*Fill, which writes it"
}

func hatched() {
	c := cur.Load()
	//gosree:unsafe this cache is private to the caller until it is stored back
	c.Put("k", 4)
	//gosree:unsafe and so is this one
	dep.Fill(c, "k", 8)
	drop := c.Drop
	//gosree:unsafe and this one, called through a method value
	drop("k")
}

// A method value handed on as an argument is called later; the finding is at the call that hands it on.
func handedOn() {
	c := other.Load()
	each(c.Drop) // want "is passed to .*Drop, which writes it"
}

func each(f func(string)) { f("k") }

// promoted reaches Put and Drop through a wrapper for the embedded field's methods.
type promoted struct{ *dep.Cache[int] }

var wrapped atomic.Pointer[promoted]

// A promoted method, a method value of one (a wrapper that calls a wrapper), and a method
// expression.
func throughPromoted() {
	p := wrapped.Load()
	p.Put("k", 9) // want "is passed to .*Put, which writes it"
	drop := p.Drop
	drop("k") // want "is passed to .*Drop, which writes it"
	fill := (*dep.Cache[int]).Put
	fill(cur.Load(), "k", 10) // want "is passed to .*Put, which writes it"
}

func publish() {
	cur.Store(dep.New[int]())
	other.Store(dep.New[int]())
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "loadnopos")
}

// TestSharedLoadWriteThroughLongChain: the write is 70 pointers away from the value loaded from the
// atomic pointer. The walk back from the write used to give up after 64 steps, and the write was not
// reported.
func TestSharedLoadWriteThroughLongChain(t *testing.T) {
	src := func(start, want string) string {
		return "package longchain\n\nimport \"sync/atomic\"\n\ntype node struct {\n\tnext *node\n\truns int\n}\n\nvar cur atomic.Pointer[node]\n\n" +
			"func f() {\n\tn := " + start + "\n\tn" + strings.Repeat(".next", 70) + ".runs++" + want + "\n\t_ = cur.Load()\n}\n"
	}
	for name, c := range map[string]struct{ start, want string }{
		"from the loaded node": {"cur.Load()", ` // want "write through shared value loaded from"`},
		"from a new node":      {"&node{}", ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir, cleanup, err := analysistest.WriteFiles(map[string]string{"longchain/longchain.go": src(c.start, c.want)})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			analysistest.Run(t, dir, ownership.Analyzer, "longchain")
		})
	}
}
