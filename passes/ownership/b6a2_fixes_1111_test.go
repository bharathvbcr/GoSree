package ownership_test

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/internal/annot/stale"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

// A closure bound to a same-named variable elsewhere in the function must not contribute its
// borrows to an unrelated goroutine capture, and the spurious CAP003 must not hide SAFE002.
func TestShadowedClosureNameIsNotABorrowCapture(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

type B struct{ n int }

type File struct{}

func (*File) Close() {} // want Close:"ownership summary"

type Job struct{ n int }

func sink(j *Job) {}

// The inner f is a different variable from the ranged f.
//
//gosree:mub b
func shadowed(b *B, files []*File) {
	{
		f := func() { b.n++ }
		f()
	}
	for _, f := range files { // want "SAFE003"
		go func() { f.Close() }() // want "SAFE002"
	}
}

// A field selector named f is not a variable named f.
//
//gosree:mub b
func selectorLHS(b *B, files []*File) {
	var h struct{ f func() }
	h.f = func() { b.n++ }
	h.f()
	for _, f := range files { // want "SAFE003"
		go func() { f.Close() }() // want "SAFE002"
	}
}

// The spurious CAP003 used to hide the real ownership finding for the same transfer.
//
//gosree:mub b
func hidesSafe002(b *B, j *Job) {
	{
		j := func() { b.n++ }
		j()
	}
	go func() { sink(j) }() // want "SAFE002"
}

//gosree:mub b
func hidesSafe003(b *B) {
	{
		j := func() { b.n++ }
		j()
	}
	j := &Job{}
	go func() { sink(j) }()
	j.n = 1 // want "SAFE003"
}

var registry []*Job // want "SAFE006"

func retain(j *Job) { registry = append(registry, j) }

//gosree:mub b
func hidesSafe004(b *B) {
	{
		j := func() { b.n++ }
		j()
	}
	j := &Job{}
	retain(j)
	go func() { sink(j) }() // want "SAFE004"
}

// The genuine shape still reports: f is the closure that captures the borrow.
//
//gosree:mub b
func genuine(b *B) {
	f := func() { b.n++ }
	go f() // want "CAP003: stack-scoped borrow 'b'"
}

// A closure that reaches the borrow through another closure variable still reports.
//
//gosree:mub b
func nested(b *B) {
	inner := func() { b.n++ }
	outer := func() { inner() }
	go outer() // want "CAP003: stack-scoped borrow 'b'"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

// immInValueType used to give up after 16 distinct types and report nothing, so an imm value
// buried after enough other field types was silently writable.
func TestImmStoreFoundBehindManyFieldTypes(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\n//gosree:imm\ntype Config struct { // want Config:\"imm\"\n\tport int\n}\n\n")
	for i := range 24 {
		fmt.Fprintf(&b, "type A%d struct{ x int }\n\n", i)
	}
	b.WriteString("type Wrapper struct {\n")
	for i := range 24 {
		fmt.Fprintf(&b, "\tf%d A%d\n", i, i)
	}
	b.WriteString("\tcfg Config\n}\n\n")
	b.WriteString("func overwrite(w *Wrapper) { *w = Wrapper{} } // want `SAFE005: store through a \\*Wrapper overwrites an immutable value`\n")
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"p/p.go": b.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

// A handler factory with an early return that gives no handler still builds one further down; the
// roots come from every return, not the first.
func TestHandlerFactoryWithEarlyNilReturn(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

import "net/http"

func makeGuarded(enabled bool) http.HandlerFunc {
	if !enabled {
		return nil
	}
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		n++ // want "SAFE008: captured variable n is written here"
	}
}

func makeUnguarded() http.HandlerFunc {
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		n++ // want "SAFE008: captured variable n is written here"
	}
}

func makeClean(enabled bool) http.HandlerFunc {
	if !enabled {
		return nil
	}
	return func(w http.ResponseWriter, r *http.Request) {
		local := 0
		local++
		_ = local
	}
}

func register(mux *http.ServeMux) {
	mux.HandleFunc("/g", makeGuarded(true))
	mux.HandleFunc("/u", makeUnguarded())
	mux.HandleFunc("/c", makeClean(true))
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

func releaseMetricsOf(t *testing.T, src string) (work, stepCaps, countCaps int) {
	t.Helper()
	var mu sync.Mutex
	seen := 0
	stop := ownership.ObserveRelease(func(pkgPath string, w, sc, cc int) {
		mu.Lock()
		defer mu.Unlock()
		if pkgPath == "p" {
			work, stepCaps, countCaps = w, sc, cc
			seen++
		}
	})
	defer stop()
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{"p/p.go": src})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
	mu.Lock()
	defer mu.Unlock()
	if seen != 1 {
		t.Fatalf("release summaries of p observed %d times, want 1 (they run only for packages with guarded fields or //gosree:holds)", seen)
	}
	return work, stepCaps, countCaps
}

// A release path longer than the step cap is widened to a prefix, and the widening is counted.
func TestLocksetReleaseStepCapIsReached(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"sync\"\n\n")
	b.WriteString("type L10 struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tv int\n}\n\n")
	for i := 9; i >= 0; i-- {
		fmt.Fprintf(&b, "type L%d struct{ x L%d }\n\n", i, i+1)
	}
	b.WriteString("func unlockDeep(l *L0) { l.x.x.x.x.x.x.x.x.x.x.mu.Unlock() }\n\n")
	b.WriteString("func unlockShallow(l *L9) { l.x.mu.Unlock() }\n")
	_, stepCaps, countCaps := releaseMetricsOf(t, b.String())
	if stepCaps == 0 {
		t.Fatalf("the 11-step release never reached the step cap (stepCaps=%d)", stepCaps)
	}
	if countCaps != 0 {
		t.Fatalf("countCaps = %d, want 0: no function has more than 32 releases", countCaps)
	}
}

// A function with more than 32 distinct releases has the rest widened to its whole parameter. A
// //gosree:holds helper that calls it is then reported as unlocking its mutex although it never
// does: the widened release covers every mutex under t. This pins today's behaviour; whether a
// widened release should say "may unlock" instead is an owner decision.
func TestLocksetReleaseCountCapIsReached(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"sync\"\n\ntype Big struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tv int\n")
	for i := range 40 {
		fmt.Fprintf(&b, "\tm%d sync.Mutex\n", i)
	}
	b.WriteString("}\n\nfunc unlockAll(t *Big) {\n")
	for i := range 40 {
		fmt.Fprintf(&b, "\tt.m%d.Unlock()\n", i)
	}
	b.WriteString("}\n\nfunc unlockTwo(t *Big) {\n\tt.m0.Unlock()\n\tt.m1.Unlock()\n}\n\n")
	b.WriteString("//gosree:holds mu\nfunc (t *Big) viaWide() {\n\tunlockAll(t) // want \"SAFE007: viaWide is //gosree:holds mu but unlocks it here\"\n}\n\n")
	b.WriteString("//gosree:holds mu\nfunc (t *Big) viaNarrow() {\n\tunlockTwo(t)\n}\n")
	_, stepCaps, countCaps := releaseMetricsOf(t, b.String())
	if countCaps == 0 {
		t.Fatalf("40 releases never reached the 32-release cap (countCaps=%d)", countCaps)
	}
	if stepCaps != 0 {
		t.Fatalf("stepCaps = %d, want 0", stepCaps)
	}
}

// The release summaries of a call DAG are computed in work proportional to its size. A diamond of
// depth 20 has 2^20 call paths; an implementation that walks paths, or that forgets which
// functions it has visited, does about a million units of work where this one does a few hundred.
func TestLocksetReleaseWorkIsLinearOnCallDiamonds(t *testing.T) {
	const depth = 20
	var b strings.Builder
	b.WriteString("package p\n\nimport \"sync\"\n\ntype T struct {\n\tmu sync.Mutex\n\t//gosree:guarded_by mu\n\tv int\n}\n\n")
	for i := range depth {
		fmt.Fprintf(&b, "func d%d(t *T) { d%d(t); d%d(t) }\n\n", i, i+1, i+1)
	}
	fmt.Fprintf(&b, "func d%d(t *T) { t.mu.Unlock() }\n", depth)
	work, _, _ := releaseMetricsOf(t, b.String())
	t.Logf("work = %d", work)
	if work > 40*depth {
		t.Fatalf("release summaries did %d units of work on a %d-deep call diamond, want at most %d", work, depth, 40*depth)
	}
}

// Whether a //gosree:unsafe on one of two Loads that reach the same write is used must not depend
// on how many calls separate the unhatched Load from the write: with a direct call and through a
// hop, the write is reported (the unhatched Load reaches it) and the hatch, which muted nothing in
// the end, is reported stale.
func TestSharedLoadHatchVerdictIndependentOfCallDepth(t *testing.T) {
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": `package p

import "sync/atomic"

type Config struct{ Limit int }

var liveDirect atomic.Pointer[Config]

func aDirect() {
	//gosree:unsafe reviewed for this caller only
	c := liveDirect.Load()
	mutateDirect(c)
}

func bDirect() {
	c := liveDirect.Load()
	mutateDirect(c)
}

func mutateDirect(c *Config) {
	c.Limit = 1
}

var liveHop atomic.Pointer[Config]

func aHop() {
	//gosree:unsafe reviewed for this caller only
	c := liveHop.Load()
	mutateHop(c)
}

func bHop() {
	c := liveHop.Load()
	hop(c)
}

func hop(c *Config) { mutateHop(c) }

func mutateHop(c *Config) {
	c.Limit = 1
}
`})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer, stale.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var writes, staleLines []int
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			line := act.Package.Fset.Position(d.Pos).Line
			switch {
			case strings.Contains(d.Message, "write through shared value"):
				writes = append(writes, line)
			case strings.Contains(d.Message, "stale escape hatch"):
				staleLines = append(staleLines, line)
			}
		}
	}
	sort.Ints(writes)
	sort.Ints(staleLines)
	// The writes are in mutateDirect (line 21) and mutateHop (line 40); the hatches on lines 10
	// and 27 mute nothing in either shape.
	if fmt.Sprint(writes) != "[21 40]" {
		t.Errorf("reported writes at lines %v, want [21 40]", writes)
	}
	if fmt.Sprint(staleLines) != "[10 27]" {
		t.Errorf("stale hatches at lines %v, want [10 27]: the verdict must not depend on call depth", staleLines)
	}
}

// A package variable is exempt from the freeze rules only while it is provably init-only: the map
// behind it must not escape anywhere (returned, retained by a callee, captured by a returned
// closure). Each shape below gives the table away, so freezing it into an atomic pointer is not a
// clean init-only freeze; the control table stays clean.
func TestInitOnlyGlobalEscapeShapesFailClosed(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"keeper/keeper.go": `package keeper

var kept []map[string]int // want "SAFE006"

func Keep(m map[string]int) { kept = append(kept, m) } // want Keep:"ownership summary"
`,
		"p/p.go": `package p

import (
	"keeper"
	"sync/atomic"
)

type Config struct {
	Limit int
	Table map[string]int
}

var liveCfg atomic.Pointer[Config]

var controlRoutes = map[string]int{"health": 1}

func controlFreeze() int {
	liveCfg.Store(&Config{Limit: 1, Table: controlRoutes})
	return controlRoutes["health"]
}

var heapRoutes = map[string]int{"a": 1}

func New() *Config { return &Config{Table: heapRoutes} } // want New:"ownership summary"

func freezeHeap() {
	liveCfg.Store(&Config{Table: heapRoutes}) // want "SAFE002" "SAFE004"
}

var closureRoutes = map[string]int{"b": 2}

func Make() func() *Config { // want Make:"ownership summary"
	cfg := Config{Table: closureRoutes}
	return func() *Config { return &cfg }
}

func freezeClosure() {
	liveCfg.Store(&Config{Table: closureRoutes}) // want "SAFE002" "SAFE004"
}

var Exposed map[string]int // want "SAFE006"

var calleeRoutes = map[string]int{"c": 3}

func Setup() { expose(calleeRoutes) } // want Setup:"ownership summary"

func expose(m map[string]int) { Exposed = m }

func freezeCallee() {
	liveCfg.Store(&Config{Table: calleeRoutes}) // want "SAFE002" "SAFE004"
}

var otherPkgRoutes = map[string]int{"d": 4}

func SetupOther() { keeper.Keep(otherPkgRoutes) } // want SetupOther:"ownership summary"

func freezeOther() {
	liveCfg.Store(&Config{Table: otherPkgRoutes}) // want "SAFE002" "SAFE004"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}
