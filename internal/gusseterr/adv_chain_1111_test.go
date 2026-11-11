package gusseterr_test

import (
	"context"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// TestCallgraphCapBoundary: the call-graph sweep cap is derived from the function count (the
// proven bound: one changing sweep per function plus one confirming sweep), so a chain whose
// callers precede their callees converges at every length up to MaxFuncs, as does one with
// callees first. (Originally this pinned a fixed 4096 cap that failed valid 4097-function
// chains closed; the failure path is still reachable through the internal capped entry, see
// TestOracleCallgraphCapFailsClosed.)
func TestCallgraphCapBoundary(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		n       int
		reverse bool
	}{
		{4096, false},
		{4097, false},
		{4097, true},
		{gusseterr.MaxFuncs, false},
	} {
		res, err := gusseterr.Solve(ctx, advChain(tc.n, tc.reverse))
		if err != nil {
			t.Fatalf("n=%d reverse=%v: %v", tc.n, tc.reverse, err)
		}
		for i, b := range res.MayReturnNil {
			if !b {
				t.Fatalf("n=%d reverse=%v: func %d summary not converged", tc.n, tc.reverse, i)
			}
		}
	}
}
