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

func TestVerifierSoundnessFixes(t *testing.T) {
	t.Run("same_pkg_handler_overwrite_regexp_flagged_clean_passes", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"app/app.go": `package app

import (
	"net/http"
	"net/url"
	"regexp"
)

type RegexpServer struct {
	re  regexp.Regexp
	buf []byte
}

func Bad() {
	s := &RegexpServer{re: *regexp.MustCompile("a+"), buf: []byte{1}}
	mux := http.NewServeMux()
	mux.HandleFunc("/re", func(w http.ResponseWriter, r *http.Request) {
		_ = s.re.MatchString("abc")
	})
	ready := make(chan struct{})
	go func() {
		close(ready)
		mux.ServeHTTP(nil, &http.Request{Method: "GET", URL: &url.URL{Path: "/re"}})
	}()
	<-ready
	s.re = *regexp.MustCompile("b+")
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic when overwriting s.re concurrently with handler MatchString")
		}

		clean := runProjModule(t, map[string]string{
			"app/app.go": `package app

import (
	"net/http"
	"net/url"
	"regexp"
)

type RegexpServer struct {
	re  regexp.Regexp
	buf []byte
}

func Good() {
	s := &RegexpServer{re: *regexp.MustCompile("a+"), buf: []byte{1}}
	mux := http.NewServeMux()
	mux.HandleFunc("/re", func(w http.ResponseWriter, r *http.Request) {
		_ = s.re.MatchString("abc")
	})
	go func() {
		mux.ServeHTTP(nil, &http.Request{Method: "GET", URL: &url.URL{Path: "/re"}})
	}()
	_ = s.re.MatchString("xyz")
}
`,
		})
		if len(clean) != 0 {
			t.Fatalf("expected 0 diagnostics when calling MatchString concurrently without field overwrite, got %v", clean)
		}
	})

	t.Run("fork_join_footprint_overwrite_regexp_flagged_clean_passes", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"app/app.go": `package app

import (
	"regexp"
	"sync"
)

type Holder struct {
	re regexp.Regexp
	xs []int
}

func Bad() {
	h := &Holder{re: *regexp.MustCompile("a+"), xs: []int{1}}
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = h.re.MatchString("abc")
	})
	h.re = *regexp.MustCompile("b+")
	wg.Wait()
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic on fork-join concurrent h.re overwrite")
		}

		clean := runProjModule(t, map[string]string{
			"app/app.go": `package app

import (
	"regexp"
	"sync"
)

type Holder struct {
	re regexp.Regexp
	xs []int
}

func Good() {
	h := &Holder{re: *regexp.MustCompile("a+"), xs: []int{1}}
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = h.re.MatchString("abc")
	})
	_ = h.re.MatchString("xyz")
	wg.Wait()
}
`,
		})
		if len(clean) != 0 {
			t.Fatalf("expected 0 diagnostics on fork-join concurrent MatchString, got %v", clean)
		}
	})

	t.Run("cross_pkg_shared_read_overwrite_regexp_and_unmarshal_flagged", func(t *testing.T) {
		diags := runProjModule(t, map[string]string{
			"srv/srv.go": `package srv

import (
	"net/http"
	"regexp"
)

type Config struct {
	Re  regexp.Regexp
	Buf []byte
}

func RegisterRe(mux *http.ServeMux, cfg *Config) {
	mux.HandleFunc("/re", func(w http.ResponseWriter, r *http.Request) {
		_ = cfg.Re.MatchString("abc")
	})
}
`,
			"app/app.go": `package app

import (
	"net/http"
	"net/url"
	"regexp"

	"testmod/srv"
)

func BadStore() {
	cfg := &srv.Config{Re: *regexp.MustCompile("a+"), Buf: []byte{1}}
	mux := http.NewServeMux()
	srv.RegisterRe(mux, cfg)
	ready := make(chan struct{})
	go func() {
		close(ready)
		mux.ServeHTTP(nil, &http.Request{Method: "GET", URL: &url.URL{Path: "/re"}})
	}()
	<-ready
	cfg.Re = *regexp.MustCompile("b+")
}
`,
		})
		if len(diags) == 0 {
			t.Fatalf("expected diagnostic on cross-pkg SharedRead cfg.Re overwrite")
		}

		unmarshalDiags := runProjModule(t, map[string]string{
			"srv/srv.go": `package srv

import (
	"net/http"
	"regexp"
)

type Config struct {
	Re  regexp.Regexp
	Buf []byte
}

func RegisterRe(mux *http.ServeMux, cfg *Config) {
	mux.HandleFunc("/re", func(w http.ResponseWriter, r *http.Request) {
		_ = cfg.Re.MatchString("abc")
	})
}
`,
			"app/app.go": `package app

import (
	"encoding/json"
	"net/http"
	"regexp"

	"testmod/srv"
)

func clobberHelper(cfg *srv.Config, raw []byte) {
	_ = json.Unmarshal(raw, &cfg.Re)
}

func BadUnmarshal(raw []byte) {
	cfg := &srv.Config{Re: *regexp.MustCompile("a+"), Buf: []byte{1}}
	mux := http.NewServeMux()
	srv.RegisterRe(mux, cfg)
	clobberHelper(cfg, raw)
}
`,
		})
		if len(unmarshalDiags) == 0 {
			t.Fatalf("expected diagnostic when helper passes &cfg.Re to json.Unmarshal after SharedRead")
		}

		clean := runProjModule(t, map[string]string{
			"srv/srv.go": `package srv

import (
	"net/http"
	"regexp"
)

type Config struct {
	Re  regexp.Regexp
	Buf []byte
}

func RegisterRe(mux *http.ServeMux, cfg *Config) {
	mux.HandleFunc("/re", func(w http.ResponseWriter, r *http.Request) {
		_ = cfg.Re.MatchString("abc")
	})
}
`,
			"app/app.go": `package app

import (
	"net/http"
	"regexp"

	"testmod/srv"
)

func Good() {
	cfg := &srv.Config{Re: *regexp.MustCompile("a+"), Buf: []byte{1}}
	mux := http.NewServeMux()
	srv.RegisterRe(mux, cfg)
	_ = cfg.Re.MatchString("xyz")
}
`,
		})
		if len(clean) != 0 {
			t.Fatalf("expected 0 diagnostics when cfg.Re is only read via MatchString, got %v", clean)
		}
	})

	t.Run("autosync_interface_generics_and_pkg_init_enforced", func(t *testing.T) {
		baseIfacePkg := `package ifacepkg

import "sync"

type Store interface {
	Touch()
}

type SafeStore struct {
	mu sync.Mutex
	n  int
}

func (s *SafeStore) Touch() {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}

var _ Store = (*SafeStore)(nil)

func RunConcurrent(s Store) {
	var wg sync.WaitGroup
	wg.Go(func() { s.Touch() })
	wg.Go(func() { s.Touch() })
	wg.Wait()
}
`
		for _, tc := range []struct {
			name string
			code string
			want bool
		}{
			{
				name: "probe2b_boxGeneric_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
func boxGeneric[T ifacepkg.Store](x T) ifacepkg.Store { return x }

func Run() {
	s := boxGeneric(&EvilStore{})
	ifacepkg.RunConcurrent(s)
}
`,
			},
			{
				name: "probe2b2_runGen_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
func runGen[T ifacepkg.Store](x T) { ifacepkg.RunConcurrent(x) }

func Run() {
	runGen(&EvilStore{})
}
`,
			},
			{
				name: "duck_constraint_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
type Duck interface { Touch() }
func boxDuck[T Duck](x T) ifacepkg.Store { return x }

func Run() {
	s := boxDuck(&EvilStore{})
	ifacepkg.RunConcurrent(s)
}
`,
			},
			{
				name: "probe10b_generic_tuple_return_assign_call_valuespec_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
type DuckTouch interface { Touch() }
func pairDuck[T any](x T) (T, int) { return x, 0 }
func boxViaTuple[T DuckTouch](x T) (ifacepkg.Store, int) { return pairDuck(x) }

func Run() {
	s, _ := boxViaTuple(&EvilStore{})
	ifacepkg.RunConcurrent(s)
}
`,
			},
			{
				name: "generic_tuple_assign_call_and_valuespec_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
type DuckTouch interface { Touch() }
func twoDuck[T any](x T) (T, T) { return x, x }
func takeTwo(a, b ifacepkg.Store) {
	ifacepkg.RunConcurrent(a)
	ifacepkg.RunConcurrent(b)
}
func viaTupleAll[T DuckTouch](x T) {
	var a, b ifacepkg.Store = twoDuck(x)
	a, b = twoDuck(x)
	takeTwo(twoDuck(x))
	_, _ = a, b
}

func Run() {
	viaTupleAll(&EvilStore{})
}
`,
			},
			{
				name: "generic_duck_typeswitch_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
type DuckTouch interface { Touch() }
func switchDuck[T DuckTouch](x T) {
	switch s := any(x).(type) {
	case ifacepkg.Store:
		ifacepkg.RunConcurrent(s)
	}
}

func Run() {
	switchDuck(&EvilStore{})
}
`,
			},
			{
				name: "safe_generic_tuple_forwarding_clean",
				want: false,
				code: `package app

import "testmod/ifacepkg"

func pairSafe[T any](x T) (T, int) { return x, 0 }
func boxSafeTuple[T ifacepkg.Store](x T) (ifacepkg.Store, int) { return pairSafe(x) }

func Run() {
	s, _ := boxSafeTuple(&ifacepkg.SafeStore{})
	ifacepkg.RunConcurrent(s)
}
`,
			},
			{
				name: "generic_struct_holder_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }
type Holder[T ifacepkg.Store] struct { val T }
func (h *Holder[T]) Box() ifacepkg.Store { return h.val }

func Run() {
	h := &Holder[*EvilStore]{val: &EvilStore{}}
	ifacepkg.RunConcurrent(h.Box())
}
`,
			},
			{
				name: "probe2e_pkg_init_global_evil",
				want: true,
				code: `package app

import "testmod/ifacepkg"

type EvilStore struct{ n int }
func (e *EvilStore) Touch() { e.n++ }

var globalEvil ifacepkg.Store = &EvilStore{}

func Run() {
	ifacepkg.RunConcurrent(globalEvil)
}
`,
			},
			{
				name: "safe_generic_and_global_clean",
				want: false,
				code: `package app

import "testmod/ifacepkg"

func boxGeneric[T ifacepkg.Store](x T) ifacepkg.Store { return x }
func runGen[T ifacepkg.Store](x T) { ifacepkg.RunConcurrent(x) }

type Holder[T ifacepkg.Store] struct { val T }
func (h *Holder[T]) Box() ifacepkg.Store { return h.val }

var globalSafe ifacepkg.Store = &ifacepkg.SafeStore{}

func Run() {
	s := boxGeneric(&ifacepkg.SafeStore{})
	ifacepkg.RunConcurrent(s)
	runGen(&ifacepkg.SafeStore{})
	h := &Holder[*ifacepkg.SafeStore]{val: &ifacepkg.SafeStore{}}
	ifacepkg.RunConcurrent(h.Box())
	ifacepkg.RunConcurrent(globalSafe)
}
`,
			},
		} {
			diags := runProjModule(t, map[string]string{
				"ifacepkg/ifacepkg.go": baseIfacePkg,
				"app/app.go":           tc.code,
			})
			if tc.want && len(diags) == 0 {
				t.Fatalf("%s: expected diagnostic, got none", tc.name)
			}
			if !tc.want && len(diags) != 0 {
				t.Fatalf("%s: expected 0 diagnostics, got %v", tc.name, diags)
			}
		}
	})
}
