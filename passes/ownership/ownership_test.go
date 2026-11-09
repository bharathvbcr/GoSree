package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gocap/passes/ownership"
)

func TestMoves(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "moves")
}

func TestGlobals(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "globals")
}

func TestImm(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "imm")
}

// TestCallbacks checks that functions which run a callback on another goroutine (time.AfterFunc,
// WaitGroup.Go, errgroup, net/http handlers) are goroutine boundaries, and that sync.Once.Do is not.
func TestCallbacks(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "callbacks")
}

// TestHandlerWrites checks SAFE008: writes through memory a handler shares with its other
// invocations, directly or in functions of this package it calls, and what is not one.
func TestHandlerWrites(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "handlers")
}

// TestGuarded checks SAFE009: guarded memory is accessed only under its mutex, //safe:holds helpers
// are called with it held, and nothing read from guarded memory leaves the lock.
func TestGuarded(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "guarded")
}

// TestGuardedLocals checks SAFE009 on guarded local variables shared with closures: the race
// corpus's lock-discipline shapes are clean, its lock-misuse shapes are reported, and a guarded local
// is not moved by the goroutines that capture it.
func TestGuardedLocals(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "guardedlocal")
}

// TestSuppression checks that //safe:unsafe on a transfer mutes everything that transfer reports, and
// that Stats counts transfers, suppressed transfers and suppressed findings once each.
func TestSuppression(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "supp")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	got := *results[0].Result.(*ownership.Stats)
	want := ownership.Stats{
		// One per go statement, send or handler registration: suppressedHandler 1,
		// writeLineHatch 1, suppressedGo 1, suppressedSend 1, otherTransfer 2, useSite 1,
		// cleanTransfer 1.
		Transfers: 8,
		// suppressedHandler, suppressedGo, suppressedSend and the first transfer of otherTransfer.
		// cleanTransfer is in a scope but had nothing to suppress; writeLineHatch's directive is on
		// a write, not on the transfer.
		SuppressedTransfers: 4,
		// suppressedHandler: SAFE008; writeLineHatch: SAFE008 for a; suppressedGo: SAFE002 +
		// SAFE003; suppressedSend: deferred SAFE003 + SAFE003; otherTransfer: SAFE002; useSite:
		// SAFE003.
		Suppressed:  8,
		UnsafeScope: 7,
	}
	if got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
}

// TestCrossPackage checks that summaries travel as facts, including from the standard library.
func TestCrossPackage(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "user")
}
