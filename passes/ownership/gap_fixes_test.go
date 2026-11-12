package ownership_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

func loadTempModule(t *testing.T, goVer string, files map[string]string) []*packages.Package {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo "+goVer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  dir,
		Env: append(os.Environ(),
			"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Fatalf("%s: %v", p.PkgPath, e)
		}
	}
	return pkgs
}

func TestClosureUnlockGuardExempt(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

import "sync"

type S struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	n    int
	data []int
}

func (s *S) closureUnlocks() {
	s.mu.Lock()
	func() { s.mu.Unlock() }()
	s.n = 1 // want "SAFE009"
}

func newS(items []int) *S {
	s := &S{}
	f := func() { _ = s }
	s.data = items
	s.n = 1
	_ = f
	return s
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

func TestValidateImmDeterministicOrder(t *testing.T) {
	var b strings.Builder
	b.WriteString("package p\n\n")
	for i := range 25 {
		fmt.Fprintf(&b, "//gosree:imm\ntype Bad%02d struct { s []int }\n\n", i)
	}
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": b.String()})

	render := func() string {
		g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		var lines []string
		for _, act := range g.Roots {
			for _, d := range act.Diagnostics {
				p := act.Package.Fset.Position(d.Pos)
				lines = append(lines, fmt.Sprintf("%d:%s", p.Line, d.Message))
			}
		}
		return strings.Join(lines, "\n")
	}

	first := render()
	for run := 1; run < 10; run++ {
		if got := render(); got != first {
			t.Fatalf("run %d emitted SAFE005 diagnostics in different order from run 0:\ngot:\n%s\nwant:\n%s", run, got, first)
		}
	}
}

func TestGo122ThreeClauseLoopVarFinal(t *testing.T) {
	src := `package p

import "sync"

func CleanThreeClause(n int) {
	var wg sync.WaitGroup
	ch := make(chan int, 8)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- i
		}()
	}
	wg.Wait()
}

func MutatedInBodyAfterSpawn(n int) {
	for i := 0; i < n; i++ {
		go func() {
			_ = i
		}()
		i++
	}
}

func MutatedInClosure(n int) {
	for i := 0; i < n; i++ {
		go func() {
			i++
		}()
	}
}

func ManualPhiSharedPreLoopAlloc() {
	a := new(int)
	for k := 0; k < 2; k++ {
		var cur *int
		if k == 0 {
			cur = a
		} else {
			cur = a
		}
		*cur = k
		go func() {
			_ = *cur
		}()
	}
}
`
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var diags []string
	byLine := map[int][]string{}
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			ln := act.Package.Fset.Position(d.Pos).Line
			diags = append(diags, fmt.Sprintf("L%d: %s", ln, d.Message))
			byLine[ln] = append(byLine[ln], d.Message)
		}
	}
	for ln, msgs := range byLine {
		if ln >= 7 && ln <= 17 {
			t.Errorf("CleanThreeClause reported false positive at line %d: %v", ln, msgs)
		}
	}
	hasInRange := func(lo, hi int) bool {
		for ln := lo; ln <= hi; ln++ {
			if len(byLine[ln]) > 0 {
				return true
			}
		}
		return false
	}
	if !hasInRange(19, 26) {
		t.Errorf("MutatedInBodyAfterSpawn was not flagged; all diags: %v", diags)
	}
	if !hasInRange(28, 34) {
		t.Errorf("MutatedInClosure was not flagged; all diags: %v", diags)
	}
	if !hasInRange(36, 50) {
		t.Errorf("ManualPhiSharedPreLoopAlloc was not flagged; all diags: %v", diags)
	}
}

func TestPackageLevelFuncLitChecked(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"p/p.go": `package p

var global *int // want "SAFE006"

var fn = func(ch chan *int) {
	x := new(int)
	ch <- x // want "SAFE002" "SAFE004"
	*x = 1  // want "SAFE003"
	global = x
}
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, ownership.Analyzer, "p")
}

func TestReleaseSummariesRecursiveAndClosureDiamondFast(t *testing.T) {
	var diamond strings.Builder
	diamond.WriteString(`
func closureDiamond(c []bool) {
	x := new(int)
	f := func() { _ = *x }
`)
	for i := range 20 {
		fmt.Fprintf(&diamond, "\tif c[%d] { g := f; f = g } else { h := f; f = h }\n", i)
	}
	diamond.WriteString(`	go f()
	*x = 9
}
`)
	src := `package p

import "sync"

type Node struct {
	mu sync.Mutex
	//gosree:guarded_by mu
	val int
	left, right, mid *Node
}

func walkUnlock(n *Node, c []bool) {
	if n == nil {
		return
	}
	n.mu.Unlock()
	walkUnlock(n.left, c)
	walkUnlock(n.right, c)
	walkUnlock(n.mid, c)
}

func callerDirect(n *Node, c []bool) {
	n.mu.Lock()
	walkUnlock(n, c)
	n.val = 1
}

func callerChild(n *Node, c []bool) {
	n.left.mu.Lock()
	walkUnlock(n, c)
	n.left.val = 2
}
` + diamond.String()
	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src})
	var work int
	stop := ownership.ObserveRelease(func(pkgPath string, w, _, _ int) {
		if strings.HasSuffix(pkgPath, "/p") {
			work = w
		}
	})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
	stop()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// A work bound instead of a wall clock: the release summaries of this package do a few hundred
	// units of work (call-graph visits, propagations, additions); an exponential walk does millions.
	t.Logf("release summary work = %d", work)
	if work == 0 || work > 500 {
		t.Fatalf("recursive releaseSummaries did %d units of work (want between 1 and 500)", work)
	}
	safe009 := map[int]bool{}
	hasSafe003 := false
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			if strings.HasPrefix(d.Message, "SAFE009:") {
				safe009[act.Package.Fset.Position(d.Pos).Line] = true
			}
			if strings.HasPrefix(d.Message, "SAFE003:") {
				hasSafe003 = true
			}
		}
	}
	if !safe009[25] || !safe009[31] {
		t.Fatalf("expected SAFE009 on lines 25 and 31, got lines %v", safe009)
	}
	if !hasSafe003 {
		t.Fatalf("expected SAFE003 on *x = 9 after go f() through 20-level closure phi diamond")
	}
}

func TestLocksetStepPrefixBoundaryAndDeepCallChain(t *testing.T) {
	var src strings.Builder
	src.WriteString(`package p

import "sync"

type WideStruct struct {
	f0  int
	f1  int
	f2  int
	f3  int
	f4  int
	f5  int
	f6  int
	f7  int
	f8  int
	f9  int
	mu10 sync.Mutex
	//gosree:guarded_by mu10
	val11 int
}

var extTake func(*int)

func cleanAddrField1WhileMu10Held(w *WideStruct, dynTake func(*int)) {
	w.mu10.Lock()
	defer w.mu10.Unlock()
	// Taking &w.f1 (*.1) and passing to a dynamic call must NOT kill w.mu10 (*.10)!
	dynTake(&w.f1)
	w.val11 = 42
}

type ChainLocks struct {
`)
	for i := range 20 {
		fmt.Fprintf(&src, "\tmu%d sync.Mutex\n", i)
	}
	src.WriteString("\t//gosree:guarded_by mu19\n\tval19 int\n}\n\n")
	for i := range 19 {
		fmt.Fprintf(&src, "func rel%d(c *ChainLocks) { c.mu%d.Unlock(); rel%d(c) }\n", i, i, i+1)
	}
	src.WriteString("func rel19(c *ChainLocks) { c.mu19.Unlock() }\n\n")
	src.WriteString(`func deepChainCaller(c *ChainLocks) {
	c.mu19.Lock()
	rel0(c)
	c.val19 = 99
}
`)

	pkgs := loadTempModule(t, "1.27", map[string]string{"p/p.go": src.String()})
	g, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	safe009Lines := map[int]string{}
	for _, act := range g.Roots {
		for _, d := range act.Diagnostics {
			if strings.HasPrefix(d.Message, "SAFE009:") {
				ln := act.Package.Fset.Position(d.Pos).Line
				safe009Lines[ln] = d.Message
			}
		}
	}
	// Line 28 is `w.val11 = 42` inside cleanAddrField1WhileMu10Held: must have ZERO SAFE009!
	if msg, bad := safe009Lines[28]; bad {
		t.Errorf("step-prefix collision (.1 vs .10): unexpected false positive SAFE009 on line 28: %s", msg)
	}
	// `c.val19 = 99` inside deepChainCaller is 4 lines from EOF: must report SAFE009!
	totalLines := strings.Count(src.String(), "\n")
	wantDeepLine := totalLines - 1
	if _, ok := safe009Lines[wantDeepLine]; !ok {
		t.Errorf("deep 20-callee unlock chain dropped release of mu19 (expected SAFE009 on line %d, got %v)", wantDeepLine, safe009Lines)
	}
}

func TestFuncFactGobRoundTrip(t *testing.T) {
	orig := &ownership.FuncFact{
		Leaks:       []bool{true, false, false, true, false, false, false, false, true},
		Iso:         []bool{false, true},
		Spawns:      []bool{false, false, true},
		Fresh:       true,
		FreshResult: []bool{true, false},
		Writes:      []bool{true, false, true},
		Links:       []ownership.Link{{A: 0, DepthA: 1, B: 2, DepthB: 0}},
		Unknown:     false,
	}
	raw, err := orig.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode: %v", err)
	}
	var dec ownership.FuncFact
	if err := dec.GobDecode(raw); err != nil {
		t.Fatalf("GobDecode: %v", err)
	}
	if dec.Fresh != orig.Fresh || dec.Unknown != orig.Unknown ||
		len(dec.Leaks) != len(orig.Leaks) || dec.Leaks[8] != true ||
		len(dec.Links) != 1 || dec.Links[0] != orig.Links[0] {
		t.Fatalf("GobDecode mismatch: got %+v, want %+v", dec, orig)
	}
}

func TestConcurrentContextWithCancelImportedFactRaceFree(t *testing.T) {
	files := map[string]string{}
	for i := range 8 {
		pkg := fmt.Sprintf("p%d", i)
		files[pkg+"/p.go"] = fmt.Sprintf(`package %s

import "context"

func Run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
}
`, pkg)
	}
	pkgs := loadTempModule(t, "1.27", files)
	if _, err := checker.Analyze([]*analysis.Analyzer{ownership.Analyzer}, pkgs, nil); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
}
