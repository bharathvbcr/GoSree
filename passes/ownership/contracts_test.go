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

func TestStdlibCapabilityContractsDynamicAndInterface(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"capdyn/capdyn.go": `package capdyn

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

type ctxKey struct{}

var (
	liveCfg   atomic.Pointer[Config]
	liveVal   atomic.Value
	bufPool   = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	bufPool2  = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	sharedBuf = new(bytes.Buffer)
)

type ConfigAtomFull interface {
	Store(*Config)
	Load() *Config
	Swap(*Config) *Config
	CompareAndSwap(*Config, *Config) bool
}

type ConfigStorer interface {
	Store(*Config)
}

type ConfigLoader interface {
	Load() *Config
}

type SyncMapIface interface {
	Store(any, any)
	Load(any) (any, bool)
	LoadOrStore(any, any) (any, bool)
	LoadAndDelete(any) (any, bool)
	Swap(any, any) (any, bool)
	CompareAndSwap(any, any, any) bool
}

type PoolIface interface {
	Get() any
	Put(any)
}

func applyCfg(fn func(*Config), v *Config) {
	fn(v)
}

func applyGen[T any](fn func(T), v T) {
	fn(v)
}

func applyLoadCfg(fn func() *Config) *Config {
	return fn()
}

func applyCtx(fn func(context.Context, any, any) context.Context, ctx context.Context, k, v any) context.Context {
	return fn(ctx, k, v)
}

func storeViaIface(s ConfigStorer, v *Config) {
	s.Store(v)
}

func loadViaIface(l ConfigLoader) *Config {
	return l.Load()
}

// Clean controls across method values, method expressions, phis, higher-order calls, and interfaces.
func dynamicAndInterfaceCleanControls(ctx context.Context, sm *sync.Map, p *Config, cond bool) int {
	store := liveCfg.Store
	store(&Config{Limit: 1})

	swap := liveCfg.Swap
	_ = swap(&Config{Limit: 2})

	cas := liveCfg.CompareAndSwap
	_ = cas(nil, &Config{Limit: 3})

	load := liveCfg.Load
	if snap := load(); snap != nil {
		_ = snap.Limit
	}

	(*atomic.Pointer[Config]).Store(&liveCfg, &Config{Limit: 4})
	if snap := (*atomic.Pointer[Config]).Load(&liveCfg); snap != nil {
		_ = snap.Limit
	}

	get := bufPool.Get
	put := bufPool.Put
	b := get().(*bytes.Buffer)
	b.Reset()
	b.WriteString("clean")
	n := b.Len()
	put(b)

	b2 := (*sync.Pool).Get(&bufPool).(*bytes.Buffer)
	b2.Reset()
	(*sync.Pool).Put(&bufPool, b2)

	var poolGet func() any
	if cond {
		poolGet = bufPool.Get
	} else {
		poolGet = bufPool2.Get
	}
	b3 := poolGet().(*bytes.Buffer)
	bufPool.Put(b3)

	smStore := sm.Store
	smStore("route", 123)
	withVal := context.WithValue
	_ = withVal(ctx, ctxKey{}, "req-clean")

	applyCfg(liveCfg.Store, &Config{Limit: 5})
	applyCfg(func(*Config) {}, p)
	applyGen(liveCfg.Store, &Config{Limit: 6})
	if snap := applyLoadCfg(liveCfg.Load); snap != nil {
		_ = snap.Limit
	}
	fresh := applyLoadCfg(func() *Config { return &Config{Limit: 7} })
	fresh.Limit = 8

	var full ConfigAtomFull = &liveCfg
	var st ConfigStorer = full // ssa.ChangeInterface
	var ld ConfigLoader = full // ssa.ChangeInterface
	st.Store(&Config{Limit: 9})
	if snap := ld.Load(); snap != nil {
		_ = snap.Limit
	}
	storeViaIface(st, &Config{Limit: 10})
	if snap := loadViaIface(ld); snap != nil {
		_ = snap.Limit
	}

	var pi PoolIface = &bufPool
	pb := pi.Get().(*bytes.Buffer)
	pb.Reset()
	pi.Put(pb)

	return n
}

func methodValueViolations(ctx context.Context, sm *sync.Map, p *Config) {
	store := liveCfg.Store
	c1 := &Config{Limit: 1}
	store(c1)
	c1.Limit = 2 // want "SAFE003"

	store(p) // want "SAFE002"

	x := 7
	b := &x //gosree:mut
	var localPtr atomic.Pointer[int]
	storeInt := localPtr.Store
	storeInt(b) // want "CAP003"

	swap := liveCfg.Swap
	c2 := &Config{Limit: 2}
	old := swap(c2)
	c2.Limit = 3 // want "SAFE003"
	if old != nil {
		old.Limit = 4 // want "SAFE005"
	}

	cas := liveCfg.CompareAndSwap
	c3 := &Config{Limit: 5}
	_ = cas(nil, c3)
	c3.Limit = 6 // want "SAFE003"

	load := liveCfg.Load
	if loaded := load(); loaded != nil {
		loaded.Limit = 9 // want "SAFE005"
	}

	vStore := liveVal.Store
	c4 := &Config{Limit: 1}
	vStore(c4)
	c4.Limit = 2 // want "SAFE003"

	vLoad := liveVal.Load
	if v := vLoad(); v != nil {
		v.(*Config).Limit = 3 // want "SAFE005"
	}

	smStore := sm.Store
	c5 := &Config{Limit: 1}
	smStore("k", c5)
	c5.Limit = 2 // want "SAFE003"

	smLOS := sm.LoadOrStore
	c6 := &Config{Limit: 1}
	actual, _ := smLOS("k", c6)
	c6.Limit = 2 // want "SAFE003"
	if actual != nil {
		actual.(*Config).Limit = 3 // want "SAFE005"
	}

	smLoad := sm.Load
	if v, ok := smLoad("k"); ok {
		v.(*Config).Limit = 4 // want "SAFE005"
	}

	smDel := sm.LoadAndDelete
	if v, ok := smDel("k"); ok {
		v.(*Config).Limit = 5 // want "SAFE005"
	}

	withVal := context.WithValue
	c7 := &Config{Limit: 7}
	_ = withVal(ctx, ctxKey{}, c7)
	c7.Limit = 8 // want "SAFE003"

	get := bufPool.Get
	put := bufPool.Put
	pb := get().(*bytes.Buffer)
	put(pb)
	_ = pb.Bytes() // want "SAFE003"
}

func methodExprAndClosureViolations(p *Config) {
	fnStore := (*atomic.Pointer[Config]).Store
	c := &Config{Limit: 1}
	fnStore(&liveCfg, c)
	c.Limit = 2 // want "SAFE003"

	fnStore(&liveCfg, p) // want "SAFE002"

	fnLoad := (*atomic.Pointer[Config]).Load
	if x := fnLoad(&liveCfg); x != nil {
		x.Limit = 3 // want "SAFE005"
	}

	wrapLoad := func() *Config { return liveCfg.Load() }
	if x := wrapLoad(); x != nil {
		x.Limit = 4 // want "SAFE005"
	}
}

func phiAndHigherOrderViolations(ctx context.Context, p1, p2 *Config, cond bool) {
	var sink func(*Config)
	if cond {
		sink = liveCfg.Store
	} else {
		sink = func(*Config) {}
	}
	c1 := &Config{Limit: 1}
	sink(c1)
	c1.Limit = 2 // want "SAFE003"
	sink(p1)     // want "SAFE002"

	var src func() *Config
	if cond {
		src = liveCfg.Load
	} else {
		src = func() *Config { return &Config{Limit: 1} }
	}
	if x := src(); x != nil {
		x.Limit = 9 // want "SAFE005"
	}

	var maybePool func() any
	if cond {
		maybePool = bufPool.Get
	} else {
		maybePool = func() any { return sharedBuf }
	}
	notOwned := maybePool().(*bytes.Buffer)
	bufPool.Put(notOwned) // want "SAFE002"

	c2 := &Config{Limit: 1}
	applyCfg(liveCfg.Store, c2)
	c2.Limit = 2                // want "SAFE003"
	applyCfg(liveCfg.Store, p2) // want "SAFE002"

	c3 := &Config{Limit: 1}
	applyGen(liveCfg.Store, c3)
	c3.Limit = 2 // want "SAFE003"

	y := 11
	b := &y //gosree:mut
	var localPtr atomic.Pointer[int]
	applyGen(localPtr.Store, b) // want "CAP003"

	if x := applyLoadCfg(liveCfg.Load); x != nil {
		x.Limit = 5 // want "SAFE005"
	}

	c4 := &Config{Limit: 1}
	_ = applyCtx(context.WithValue, ctx, ctxKey{}, c4)
	c4.Limit = 2 // want "SAFE003"
}

func interfaceDispatchViolations(sm *sync.Map, p1, p2 *Config, unownedBuf *bytes.Buffer) {
	var full ConfigAtomFull = &liveCfg
	var st ConfigStorer = full
	var ld ConfigLoader = full

	c1 := &Config{Limit: 1}
	st.Store(c1)
	c1.Limit = 2 // want "SAFE003"
	st.Store(p1) // want "SAFE002"

	z := 13
	b := &z //gosree:mut
	var localPtr atomic.Pointer[int]
	var ist interface{ Store(*int) } = &localPtr
	ist.Store(b) // want "CAP003"

	c2 := &Config{Limit: 2}
	old := full.Swap(c2)
	c2.Limit = 3 // want "SAFE003"
	if old != nil {
		old.Limit = 4 // want "SAFE005"
	}

	c3 := &Config{Limit: 5}
	_ = full.CompareAndSwap(nil, c3)
	c3.Limit = 6 // want "SAFE003"

	if x := ld.Load(); x != nil {
		x.Limit = 9 // want "SAFE005"
	}

	c4 := &Config{Limit: 7}
	storeViaIface(st, c4)
	c4.Limit = 8          // want "SAFE003"
	storeViaIface(st, p2) // want "SAFE002"
	if x := loadViaIface(ld); x != nil {
		x.Limit = 10 // want "SAFE005"
	}

	var smi SyncMapIface = sm
	c5 := &Config{Limit: 1}
	smi.Store("k", c5)
	c5.Limit = 2 // want "SAFE003"

	c6 := &Config{Limit: 1}
	actual, _ := smi.LoadOrStore("k", c6)
	c6.Limit = 2 // want "SAFE003"
	if actual != nil {
		actual.(*Config).Limit = 3 // want "SAFE005"
	}

	if v, ok := smi.Load("k"); ok {
		v.(*Config).Limit = 4 // want "SAFE005"
	}
	if v, ok := smi.LoadAndDelete("k"); ok {
		v.(*Config).Limit = 5 // want "SAFE005"
	}

	var pi PoolIface = &bufPool
	pb := pi.Get().(*bytes.Buffer)
	pi.Put(pb)
	_ = pb.Bytes()     // want "SAFE003"
	pi.Put(unownedBuf) // want "SAFE002"

	c7 := &Config{Limit: 1}
	prev, _ := smi.Swap("k", c7)
	c7.Limit = 2 // want "SAFE003"
	if prev != nil {
		prev.(*Config).Limit = 3 // want "SAFE005"
	}

	// Bound method value taken from an interface receiver (st.Store).
	ifaceBound := st.Store
	c8 := &Config{Limit: 8}
	ifaceBound(c8)
	c8.Limit = 9 // want "SAFE003"
}

type hookHolder struct {
	store func(*Config)
	iface ConfigStorer
}

func indirectSmugglingAndOpenInterfaceViolations(openIface ConfigAtomFull, p *Config, cond bool) {
	// Open interface parameter implemented by *atomic.Pointer[Config] with no local implementer.
	openIface.Store(p) // want "SAFE002"
	if snap := openIface.Load(); snap != nil {
		snap.Limit = 1 // want "SAFE005"
	}

	// Struct field, slice, map, and Phi over interfaces.
	h := hookHolder{store: liveCfg.Store, iface: &liveCfg}
	cs := &Config{Limit: 1}
	h.store(cs)
	cs.Limit = 2 // want "SAFE003"

	ci := &Config{Limit: 1}
	h.iface.Store(ci)
	ci.Limit = 2 // want "SAFE003"

	fns := []func(*Config){liveCfg.Store}
	cSlice := &Config{Limit: 1}
	fns[0](cSlice)
	cSlice.Limit = 2 // want "SAFE003"

	ifaces := map[string]ConfigLoader{"live": &liveCfg}
	if loaded := ifaces["live"].Load(); loaded != nil {
		loaded.Limit = 9 // want "SAFE005"
	}

	var phiIface ConfigStorer
	if cond {
		phiIface = &liveCfg
	} else {
		var alt atomic.Pointer[Config]
		phiIface = &alt
	}
	cPhi := &Config{Limit: 1}
	phiIface.Store(cPhi)
	cPhi.Limit = 2 // want "SAFE003"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "capdyn")
}

func TestStdlibCapabilityContractsCrossFunctionStructMockAndUserClosures(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"capadv/capadv.go": `package capadv

import (
	"bytes"
	"sync"
	"sync/atomic"
)

type Config struct {
	Limit int
}

var (
	liveCfg   atomic.Pointer[Config]
	bufPool   = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	sharedBuf = new(bytes.Buffer)
)

type ConfigStorer interface {
	Store(*Config)
}

type ConfigLoader interface {
	Load() *Config
}

// Local mock implementer coexisting in the same package as *atomic.Pointer[Config] MakeInterface!
type benignMock struct{}

func (*benignMock) Store(*Config) {} // want Store:"ownership summary"
func (*benignMock) Load() *Config { return &Config{Limit: 0} } // want Load:"ownership summary"

type ptrHolder struct {
	store  func(*Config)
	load   func() *Config
	storer ConfigStorer
	loader ConfigLoader
}

func makeHolder() *ptrHolder {
	return &ptrHolder{
		store:  liveCfg.Store,
		load:   liveCfg.Load,
		storer: &liveCfg,
		loader: &liveCfg,
	}
}

func useHolderParam(h *ptrHolder, unowned *Config) {
	c1 := &Config{Limit: 1}
	h.store(c1)
	c1.Limit = 2      // want "SAFE003"
	h.store(unowned)  // want "SAFE002"

	if snap := h.load(); snap != nil {
		snap.Limit = 3 // want "SAFE005"
	}

	c2 := &Config{Limit: 4}
	h.storer.Store(c2)
	c2.Limit = 5 // want "SAFE003"

	if snap2 := h.loader.Load(); snap2 != nil {
		snap2.Limit = 6 // want "SAFE005"
	}
}

func driveHolderAndCoexistingMock(unownedOpen, unownedParam *Config, openStorer ConfigStorer) {
	// Ensure benignMock is also boxed in the package so local-implementer coexistence is exercised.
	var mock ConfigStorer = &benignMock{}
	_ = mock
	var realBox ConfigStorer = &liveCfg
	_ = realBox

	// Open parameter when *atomic.Pointer[Config] is boxed into ConfigStorer in this package:
	openStorer.Store(unownedOpen) // want "SAFE002"

	h := makeHolder()
	useHolderParam(h, unownedParam) // want "SAFE002"

	c3 := &Config{Limit: 7}
	makeHolder().storer.Store(c3)
	c3.Limit = 8 // want "SAFE003"
}

func runStore(fn func(*Config), v *Config) {
	fn(v)
}

func runLoad(fn func() *Config) *Config {
	return fn()
}

func runGet(fn func() any) any {
	return fn()
}

//gosree:iso v
func publishIsoStore(v *Config) {
	liveCfg.Store(v)
}

func userClosuresInHigherOrderAndPhi(unowned1, unowned2 *Config, cond bool) {
	// 1a. //gosree:iso user function value publishing parameter into global atomic.Pointer:
	c0 := &Config{Limit: 1}
	runStore(publishIsoStore, c0)
	c0.Limit = 2                       // want "SAFE003"
	runStore(publishIsoStore, unowned1) // want "SAFE002"

	// 1b. Unannotated user closure directly publishing parameter into global atomic.Pointer:
	wrapDirectStore := func(v *Config) {
		liveCfg.Store(v) // want "SAFE002"
	}
	c1 := &Config{Limit: 1}
	runStore(wrapDirectStore, c1)
	c1.Limit = 2                        // want "SAFE003"
	runStore(wrapDirectStore, unowned2) // want "SAFE002"

	// 2. User closure publishing via captured method value:
	storeMV := liveCfg.Store
	wrapMVStore := func(v *Config) {
		storeMV(v) // want "SAFE002"
	}
	var phiStore func(*Config)
	if cond {
		phiStore = wrapMVStore
	} else {
		phiStore = func(*Config) {}
	}
	c2 := &Config{Limit: 3}
	phiStore(c2)
	c2.Limit = 4 // want "SAFE003"

	// 3. User closure returning shared load via captured interface & method value:
	var ld ConfigLoader = &liveCfg
	wrapIfaceLoad := func() *Config {
		return ld.Load()
	}
	if x := runLoad(wrapIfaceLoad); x != nil {
		x.Limit = 9 // want "SAFE005"
	}

	loadMV := liveCfg.Load
	wrapMVLoad := func() *Config {
		return loadMV()
	}
	var phiLoad func() *Config
	if cond {
		phiLoad = wrapMVLoad
	} else {
		phiLoad = func() *Config { return &Config{Limit: 1} }
	}
	if y := phiLoad(); y != nil {
		y.Limit = 10 // want "SAFE005"
	}

	// 4. User closure wrapping sync.Pool.Get method value:
	getMV := bufPool.Get
	wrapPoolGet := func() any {
		return getMV()
	}
	cleanBuf := runGet(wrapPoolGet).(*bytes.Buffer)
	cleanBuf.Reset()
	bufPool.Put(cleanBuf)

	var maybePool func() any
	if cond {
		maybePool = wrapPoolGet
	} else {
		maybePool = func() any { return sharedBuf }
	}
	badBuf := maybePool().(*bytes.Buffer)
	bufPool.Put(badBuf) // want "SAFE002"
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "capadv")
}

