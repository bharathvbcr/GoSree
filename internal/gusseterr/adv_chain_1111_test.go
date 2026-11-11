package gusseterr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// TestCallgraphCapBoundary pins the exact depth at which the summary fixpoint fails closed: a
// chain whose callers precede their callees needs one changing sweep per function plus one
// confirming sweep, so MaxCallgraphIters functions converge and one more fails. A chain with
// callees first converges in two sweeps at any length.
func TestCallgraphCapBoundary(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		n       int
		reverse bool
		fail    bool
	}{
		{gusseterr.MaxCallgraphIters, false, false},
		{gusseterr.MaxCallgraphIters + 1, false, true},
		{gusseterr.MaxCallgraphIters + 1, true, false},
	} {
		res, err := gusseterr.Solve(ctx, advChain(tc.n, tc.reverse))
		if tc.fail {
			if err == nil || !strings.Contains(err.Error(), "did not converge") {
				t.Fatalf("n=%d reverse=%v: err = %v, want a convergence error", tc.n, tc.reverse, err)
			}
			continue
		}
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
