package ownership_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bharathvbcr/gosree/internal/annot/stale"
	"github.com/bharathvbcr/gosree/passes/ownership"
)

func runProjModule(t *testing.T, files map[string]string) []string {
	t.Helper()
	pkgs := loadTempModule(t, "1.26", files)
	gr, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer, stale.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var out []string
	for _, act := range gr.Roots {
		for _, d := range act.Diagnostics {
			pos := act.Package.Fset.Position(d.Pos)
			out = append(out, fmt.Sprintf("%s:%d: %s", act.Package.PkgPath, pos.Line, d.Message))
		}
	}
	sort.Strings(out)
	return out
}

func TestDisjointFieldProjectionGodStruct(t *testing.T) {
	baseSvc := `package svc

import "sync"

type BadState struct{ Count int }

type Manager struct {
	mu    sync.Mutex
	state *BadState
}

func NewManager() *Manager {
	return &Manager{state: &BadState{Count: 1}}
}

// Unlocked interior return: Manager is NOT AutoSync!
func (m *Manager) GetState() *BadState {
	return m.state
}

func (m *Manager) UpdateLocked(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Count = n
}

type Cache struct {
	mu    sync.RWMutex
	items map[string]string
}

func NewCache() *Cache {
	return &Cache{items: map[string]string{}}
}

func (c *Cache) Put(k, v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[k] = v
}

func (c *Cache) Get(k string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.items[k]
}

type Gateway struct {
	mu      sync.Mutex
	metrics map[string]int
	cache   *Cache
	badMgr  *Manager
	version string
}

func NewGateway() *Gateway {
	return &Gateway{
		metrics: map[string]int{},
		cache:   NewCache(),
		badMgr:  NewManager(),
		version: "v1",
	}
}

func (g *Gateway) SafeRecord(key, val string) string {
	g.mu.Lock()
	g.metrics[key]++
	g.mu.Unlock()
	g.cache.Put(key, val)
	return g.version + ":" + g.cache.Get(key)
}

func (g *Gateway) UnsafeTouchBad() {
	s := g.badMgr.GetState()
	s.Count++
}
`

	t.Run("safe_projection_clean", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"svc/svc.go": baseSvc,
			"api/api.go": `package api

import (
	"net/http"
	"testmod/svc"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	gw := svc.NewGateway()
	mux.HandleFunc("/safe", func(w http.ResponseWriter, r *http.Request) {
		_ = gw.SafeRecord(r.URL.Path, "ok")
	})
	return mux
}
`,
		})
		if len(diags) != 0 {
			t.Fatalf("expected 0 diagnostics on safe disjoint field projection, got:\n%s", strings.Join(diags, "\n"))
		}
	})

	t.Run("unsafe_projection_flagged", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"svc/svc.go": baseSvc,
			"api/api.go": `package api

import (
	"net/http"
	"testmod/svc"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	gw := svc.NewGateway()
	mux.HandleFunc("/unsafe", func(w http.ResponseWriter, r *http.Request) {
		gw.UnsafeTouchBad()
	})
	return mux
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic when handler touches unsynchronized GetState() field")
		}
	})
}

func TestCrossPackageSharedReadRouteRegistration(t *testing.T) {
	routesPkg := `package routes

import "net/http"

type AppConfig struct {
	Version string
	Tags    []string
}

func RegisterRoutes(mux *http.ServeMux, cfg *AppConfig) {
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(cfg.Version))
		for _, tag := range cfg.Tags {
			_, _ = w.Write([]byte(tag))
		}
	})
}
`

	t.Run("frozen_caller_clean", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"routes/routes.go": routesPkg,
			"server/server.go": `package server

import (
	"net/http"
	"testmod/routes"
)

func Build() http.Handler {
	mux := http.NewServeMux()
	cfg := &routes.AppConfig{Version: "1.0", Tags: []string{"prod"}}
	routes.RegisterRoutes(mux, cfg)
	return mux
}
`,
		})
		if len(diags) != 0 {
			t.Fatalf("expected 0 diagnostics when caller freezes cfg before RegisterRoutes, got:\n%s", strings.Join(diags, "\n"))
		}
	})

	t.Run("post_call_mutation_fires_SAFE003", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"routes/routes.go": routesPkg,
			"server/server.go": `package server

import (
	"net/http"
	"testmod/routes"
)

func BuildBad() http.Handler {
	mux := http.NewServeMux()
	cfg := &routes.AppConfig{Version: "1.0", Tags: []string{"prod"}}
	routes.RegisterRoutes(mux, cfg)
	cfg.Version = "2.0"
	return mux
}
`,
		})
		found := false
		for _, d := range diags {
			if strings.Contains(d, "SAFE003") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected SAFE003 on post-RegisterRoutes mutation, got: %v", diags)
		}
	})
}

func TestRouterShutdownSiblingClosureIsolation(t *testing.T) {
	t.Run("clean_handler_not_poisoned_by_sibling_shutdown", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"api/api.go": `package api

import (
	"context"
	"net/http"
	"sync"
)

type SafeService struct {
	mu    sync.Mutex
	items map[string]int
}

func NewSafeService() *SafeService {
	return &SafeService{items: map[string]int{}}
}

func (s *SafeService) Get(k string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[k]
}

func (s *SafeService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.items)
	return nil
}

type ReadOnlyBanner struct {
	text string
}

func NewBanner(t string) *ReadOnlyBanner { return &ReadOnlyBanner{text: t} }
func (b *ReadOnlyBanner) Text() string    { return b.text }

func NewRouterWithShutdown() (http.Handler, func(context.Context) error) {
	mux := http.NewServeMux()
	svc := NewSafeService()
	banner := NewBanner("hello")

	mux.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		_ = svc.Get(r.URL.Path)
		_, _ = w.Write([]byte(banner.Text()))
	})

	shutdown := func(ctx context.Context) error {
		_ = banner.Text()
		return svc.Close()
	}
	return mux, shutdown
}
`,
		})
		if len(diags) != 0 {
			t.Fatalf("expected 0 diagnostics on NewRouterWithShutdown with safe shutdown closure, got:\n%s", strings.Join(diags, "\n"))
		}
	})

	t.Run("mutating_shutdown_on_unguarded_handler_flagged", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"api/api.go": `package api

import (
	"context"
	"net/http"
)

type UnsafeBanner struct {
	text   string
	closed bool
}

func (b *UnsafeBanner) Text() string {
	if b.closed {
		return ""
	}
	return b.text
}

func (b *UnsafeBanner) Close() error {
	b.closed = true
	return nil
}

func NewRouterWithShutdown() (http.Handler, func(context.Context) error) {
	mux := http.NewServeMux()
	banner := &UnsafeBanner{text: "hello"}

	mux.HandleFunc("/banner", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(banner.Text()))
	})

	shutdown := func(ctx context.Context) error {
		return banner.Close()
	}
	return mux, shutdown
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic when returned shutdown closure mutates unguarded field read by HTTP handler")
		}
	})
}

func TestHTTPClientConcurrentDoVsTimeoutMutation(t *testing.T) {
	t.Run("concurrent_do_clean", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"p/p.go": `package p

import (
	"net/http"
	"time"
)

type Fetcher struct {
	hc       *http.Client
	endpoint string
}

func NewFetcher(ep string) *Fetcher {
	return &Fetcher{
		hc:       &http.Client{Timeout: 5 * time.Second},
		endpoint: ep,
	}
}

func (f *Fetcher) Fetch(req *http.Request) (*http.Response, error) {
	return f.hc.Do(req)
}

func Serve() http.Handler {
	mux := http.NewServeMux()
	f := NewFetcher("https://example.com")
	mux.HandleFunc("/proxy", func(w http.ResponseWriter, r *http.Request) {
		resp, err := f.Fetch(r)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	})
	return mux
}
`,
		})
		if len(diags) != 0 {
			t.Fatalf("expected 0 diagnostics on concurrent (*http.Client).Do, got:\n%s", strings.Join(diags, "\n"))
		}
	})

	t.Run("post_registration_timeout_mutation_flagged", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"p/p.go": `package p

import (
	"net/http"
	"time"
)

func ServeBad() http.Handler {
	mux := http.NewServeMux()
	hc := &http.Client{Timeout: 5 * time.Second}
	mux.HandleFunc("/proxy", func(w http.ResponseWriter, r *http.Request) {
		resp, err := hc.Do(r)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	})
	hc.Timeout = 10 * time.Second
	return mux
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic on post-registration hc.Timeout mutation")
		}
	})
}

func TestDefensiveInterfaceCloneAutoGuard(t *testing.T) {
	t.Run("cloned_concrete_slice_and_map_clean", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"cfg/cfg.go": `package cfg

import (
	"maps"
	"slices"
	"sync"
)

type RuntimeConfig struct {
	mu   sync.RWMutex
	vals map[string]any
}

func NewRuntimeConfig() *RuntimeConfig {
	return &RuntimeConfig{vals: map[string]any{}}
}

func cloneVal(v any) any {
	switch x := v.(type) {
	case []string:
		return slices.Clone(x)
	case map[string]string:
		return maps.Clone(x)
	default:
		return v
	}
}

func (c *RuntimeConfig) SetTags(k string, tags []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vals[k] = slices.Clone(tags)
}

func (c *RuntimeConfig) SetLabels(k string, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vals[k] = maps.Clone(labels)
}

func (c *RuntimeConfig) Get(k string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneVal(c.vals[k])
}

func Share(rc *RuntimeConfig) {
	var wg sync.WaitGroup
	wg.Go(func() {
		rc.SetTags("a", []string{"x"})
	})
	wg.Go(func() {
		_ = rc.Get("a")
	})
	wg.Wait()
}
`,
		})
		if len(diags) != 0 {
			t.Fatalf("expected 0 diagnostics on defensive interface clone RuntimeConfig, got:\n%s", strings.Join(diags, "\n"))
		}
	})

	t.Run("uncloned_mutable_pointer_in_any_map_flagged", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"cfg/cfg.go": `package cfg

import (
	"slices"
	"sync"
)

type MutableBox struct{ N int }

type RuntimeConfig struct {
	mu   sync.RWMutex
	vals map[string]any
}

func NewRuntimeConfig() *RuntimeConfig {
	return &RuntimeConfig{vals: map[string]any{}}
}

func cloneVal(v any) any {
	switch x := v.(type) {
	case []string:
		return slices.Clone(x)
	default:
		return v
	}
}

func (c *RuntimeConfig) SetBox(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vals[k] = &MutableBox{N: 1}
}

func (c *RuntimeConfig) Get(k string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneVal(c.vals[k])
}

func RaceIt() {
	rc := NewRuntimeConfig()
	rc.SetBox("b")
	var wg sync.WaitGroup
	wg.Go(func() {
		if b, ok := rc.Get("b").(*MutableBox); ok {
			b.N++
		}
	})
	wg.Go(func() {
		if b, ok := rc.Get("b").(*MutableBox); ok {
			b.N++
		}
	})
	wg.Wait()
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic when uncloned *MutableBox falls through default branch of cloneVal")
		}
	})

	t.Run("shallow_clone_pointee_mutation_flagged", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"cfg/cfg.go": `package cfg

import (
	"slices"
	"sync"
)

type Box struct{ N int }

func mutateViaShallowClone(xs []*Box) {
	cp := slices.Clone(xs)
	if len(cp) > 0 {
		cp[0].N++
	}
}

func RunRace(xs []*Box) {
	var wg sync.WaitGroup
	wg.Go(func() { mutateViaShallowClone(xs) })
	wg.Go(func() { mutateViaShallowClone(xs) })
	wg.Wait()
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic when mutating pointer element through shallow slices.Clone")
		}
	})
}
