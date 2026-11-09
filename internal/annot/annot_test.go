package annot_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/internal/annot"
)

func TestDirectives(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "a")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	ix := results[0].Result.(*annot.Index)
	if got := len(ix.IsoVars); got != 1 {
		t.Errorf("IsoVars: got %d, want 1 (only p of named)", got)
	}
	if got := len(ix.ImmTypes); got != 1 {
		t.Errorf("ImmTypes: got %d, want 1 (only T)", got)
	}
	if got := ix.UnsafeScopes(); got != 1 {
		t.Errorf("UnsafeScopes: got %d, want 1 (the reasonless one still applies; the dangling one does not)", got)
	}
}

// TestGuardedDirectives checks //safe:guarded_by and //safe:holds: what they attach to, what the mutex
// may be, and the malformed cases.
func TestGuardedDirectives(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), annot.Analyzer, "g")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	ix := results[0].Result.(*annot.Index)
	byMutex := map[string]int{}
	for _, mu := range ix.Guarded {
		byMutex[mu.Name()]++
	}
	// S.n and S.m by mu, S.r by rw, E.n by the embedded Mutex, count and total by gmu.
	want := map[string]int{"mu": 2, "rw": 1, "Mutex": 1, "gmu": 2}
	if len(byMutex) != len(want) {
		t.Errorf("Guarded by mutex: got %v, want %v", byMutex, want)
	}
	for mu, n := range want {
		if byMutex[mu] != n {
			t.Errorf("Guarded by %s: got %d, want %d (all: %v)", mu, byMutex[mu], n, byMutex)
		}
	}
	if n := ix.HoldsDirectives(); n != 2 {
		t.Errorf("Holds: got %d, want 2 (bumpLocked, addLocked)", n)
	}
}
