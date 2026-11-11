package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

func TestStdlibCapabilityContracts(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"capcontracts/capcontracts.go": `package capcontracts

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
)

type Config struct {
	Limit int
	Table map[string]int
}

//gosree:imm
type ImmSnap struct { // want ImmSnap:"imm"
	limit int
}

type GuardedSnap struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	hits int
}

var (
	liveCfg      atomic.Pointer[Config]
	liveImm      atomic.Pointer[ImmSnap]
	liveGuard    atomic.Pointer[GuardedSnap]
	liveVal      atomic.Value
	bufPool      = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	initOnlyRoutes = map[string]int{"health": 1}
	mutableRoutes  = map[string]int{"mut": 1} // want "SAFE006"
	escapedRoutes  = map[string]int{"esc": 1}
	appendBuf      = make([]int, 0, 4)
	liveSlice      atomic.Pointer[SliceSnap]
)

type SliceSnap struct {
	Items []int
}

type ctxKey struct{}

// Clean controls: publishing owned values without post-publication use,
// read-only Load, value-copy mutation, guarded mutation, and shareable payloads.
func cleanControls(ctx context.Context, sm *sync.Map) int {
	c := &Config{Limit: 10, Table: map[string]int{"a": 1}}
	liveCfg.Store(c)

	next := &Config{Limit: 20}
	_ = liveCfg.CompareAndSwap(nil, next)

	if snap := liveCfg.Load(); snap != nil {
		cp := *snap
		cp.Limit = 99 // local struct copy: does not mutate shared *Config
		_ = cp.Limit
		_ = snap.Limit
	}

	if gs := liveGuard.Load(); gs != nil {
		gs.mu.Lock()
		gs.hits++
		gs.mu.Unlock()
	}

	liveImm.Store(&ImmSnap{limit: 7})
	if im := liveImm.Load(); im != nil {
		_ = im.limit
	}

	sm.Store("route", 42)
	ctx = context.WithValue(ctx, ctxKey{}, "request-1")
	_ = ctx

	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	b.WriteString("ok")
	n := b.Len()
	bufPool.Put(b)
	return n
}

func atomicPointerStoreThenMutate() {
	c := &Config{Limit: 1}
	liveCfg.Store(c)
	c.Limit = 2 // want "SAFE003"
}

func atomicPointerStoreUnowned(p *Config) {
	liveCfg.Store(p) // want "SAFE002"
}

func atomicPointerSwapAndCASViolations() {
	a := &Config{Limit: 1}
	old := liveCfg.Swap(a)
	a.Limit = 2 // want "SAFE003"
	if old != nil {
		old.Limit = 3 // want "SAFE005"
	}

	b := &Config{Limit: 4}
	_ = liveCfg.CompareAndSwap(nil, b)
	b.Limit = 5 // want "SAFE003"
}

func atomicPointerLoadMutateFieldAndMap() {
	if c := liveCfg.Load(); c != nil {
		c.Limit = 9 // want "SAFE005"
		c.Table["x"] = 1 // want "SAFE005"
	}
}

func atomicValueViolations() {
	c := &Config{Limit: 1}
	liveVal.Store(c)
	c.Limit = 2 // want "SAFE003"

	if v := liveVal.Load(); v != nil {
		v.(*Config).Limit = 3 // want "SAFE005"
	}
}

func syncMapAndContextViolations(ctx context.Context, sm *sync.Map) context.Context {
	c1 := &Config{Limit: 1}
	sm.Store("k", c1)
	c1.Limit = 2 // want "SAFE003"

	c2 := &Config{Limit: 3}
	ctx2 := context.WithValue(ctx, ctxKey{}, c2)
	c2.Limit = 4 // want "SAFE003"
	return ctx2
}

func syncPoolPutThenUse() []byte {
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	bufPool.Put(b)
	return b.Bytes() // want "SAFE003"
}

func localBorrowPublished() {
	x := 10
	b := &x //gosree:mut
	var ptr atomic.Pointer[int]
	ptr.Store(b) // want "CAP003"
}

func initOnlyGlobalInAtomicFreezeIsClean() int {
	liveCfg.Store(&Config{Limit: 1, Table: initOnlyRoutes})
	return initOnlyRoutes["health"]
}

func initOnlyGlobalOuterStructStillKilled() {
	c := &Config{Limit: 1, Table: initOnlyRoutes}
	liveCfg.Store(c)
	c.Limit = 2 // want "SAFE003"
}

func mutatedGlobalInAtomicFreezeFailsClosed() {
	mutableRoutes["mut"] = 2
	liveCfg.Store(&Config{Limit: 1, Table: mutableRoutes}) // want "SAFE002" "SAFE004"
}

func ExportedRoutes() map[string]int { // want ExportedRoutes:"ownership summary"
	return escapedRoutes
}

func escapedGlobalInAtomicFreezeFailsClosed() {
	liveCfg.Store(&Config{Limit: 1, Table: escapedRoutes}) // want "SAFE002" "SAFE004"
}

func appendMutatedGlobalInAtomicFreezeFailsClosed() {
	_ = append(appendBuf, 99)
	liveSlice.Store(&SliceSnap{Items: appendBuf}) // want "SAFE002" "SAFE004"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "capcontracts")
}
