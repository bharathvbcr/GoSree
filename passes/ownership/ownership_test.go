package ownership_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bharathvbcr/gosree/passes/ownership"
)

func TestMoves(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "moves")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	// One //gosree:iso parameter: j of the owned-parameter test.
	if got := results[0].Result.(*ownership.Stats).Iso; got != 1 {
		t.Errorf("Stats.Iso = %d, want 1", got)
	}
}

func TestGlobals(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "globals")
}

func TestImm(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "imm")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	// Config holds; Bad and Open are annotated but fail the imm rules.
	if got := results[0].Result.(*ownership.Stats).Imm; got != 1 {
		t.Errorf("Stats.Imm = %d, want 1", got)
	}
}

// TestCallbacks checks that functions which run a callback on another goroutine (time.AfterFunc,
// WaitGroup.Go, errgroup, net/http handlers) are goroutine boundaries, and that sync.Once.Do is not.
func TestCallbacks(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "callbacks")
}

// TestHandlerWrites checks SAFE008: writes through memory a handler shares with its other
// invocations, directly or in functions of this package it calls, and what is not one.
func TestHandlerWrites(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "handlers", "store", "store/inner")
}

// TestGuarded checks SAFE009: guarded memory is accessed only under its mutex, //gosree:holds helpers
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

// TestSuppression checks that //gosree:unsafe on a transfer mutes everything that transfer reports, and
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
		// Every transfer with a finding, muted or not: all but cleanTransfer. useSite's only
		// finding is muted at the use, so it is flagged but not a suppressed transfer.
		FlaggedTransfers: 7,
		// suppressedHandler, suppressedGo, suppressedSend and the first transfer of otherTransfer.
		// cleanTransfer is in a scope but had nothing to suppress; writeLineHatch's directive is on
		// a write, not on the transfer.
		SuppressedTransfers: 4,
		// suppressedHandler: SAFE008; writeLineHatch: SAFE008 for a; suppressedGo: SAFE002 +
		// SAFE003; suppressedSend: deferred SAFE003 + SAFE003; otherTransfer: SAFE002; useSite:
		// SAFE003.
		Suppressed: 8,
		// Which scopes muted nothing is the stale analyzer's result (internal/annot/stale), not a Stats
		// field: it is only known after every pass that mutes has run.
		UnsafeScope: 8,
	}
	if got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
}

// TestTransferRelated checks that a finding away from its transfer (a use after the move) points back
// at the transfer, and that one at the transfer does not point at itself.
func TestTransferRelated(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "supp")
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	fset := r.Action.Package.Fset
	var checked int
	for _, d := range r.Action.Diagnostics {
		var rel []int
		for _, ri := range d.Related {
			if ri.Message == ownership.TransferRelated {
				rel = append(rel, fset.Position(ri.Pos).Line)
			}
		}
		line := fset.Position(d.Pos).Line
		switch d.Category {
		case ownership.UseAfter:
			// q.ID = 2 at line 52 follows go use(q) at line 51.
			if line == 52 {
				checked++
				if len(rel) != 1 || rel[0] != 51 {
					t.Errorf("SAFE003 at line 52 points at transfer lines %v, want [51]", rel)
				}
			}
		default:
			if len(rel) > 0 && rel[0] == line {
				t.Errorf("%s at line %d points at its own line", d.Category, line)
			}
		}
	}
	if checked != 1 {
		t.Errorf("found %d SAFE003 at line 52, want 1", checked)
	}
}

// TestCrossPackage checks that summaries travel as facts, including from the standard library.
func TestCrossPackage(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ownership.Analyzer, "user")
}
