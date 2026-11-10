//go:build !unix || !cgo

package gusseterr

import (
	"context"
	"io"
	"sync/atomic"
	"time"
)

var crossings atomic.Int64

// NativeActive reports whether the in-process Rust Gusset engine is linked.
func NativeActive() bool { return false }

// Crossings reports the number of SolveFrame calls made.
func Crossings() int64 { return crossings.Load() }

// Opens reports 0 when running on the pure-Go fallback.
func Opens() int64 { return 0 }

// SolveFrame runs the bit-exact pure-Go reference oracle when cgo or unix is unavailable.
func SolveFrame(ctx context.Context, frame []byte) ([]byte, error) {
	crossings.Add(1)
	return SolveFrameOracle(ctx, frame)
}

// Solve solves funcs via the bit-exact pure-Go reference oracle.
func Solve(ctx context.Context, funcs []FuncIR) (PackageResult, error) {
	crossings.Add(1)
	return SolveOracle(ctx, funcs)
}

// Check verifies the pure-Go fallback solver.
func Check(ctx context.Context) error {
	_, err := SolveOracle(ctx, nil)
	return err
}

// SelfTest runs Check on non-unix / non-cgo builds.
func SelfTest(ctx context.Context) error {
	return Check(ctx)
}

// DrainLogs is a no-op when the Rust runtime is not linked.
func DrainLogs(_ io.Writer) (int, error) { return 0, nil }

// RustAllocatedBytes reports 0 when the Rust runtime is not linked.
func RustAllocatedBytes() uint64 { return 0 }

// Close is a no-op on fallback builds.
func Close() error { return nil }

// Shutdown is a no-op on fallback builds.
func Shutdown(_ time.Duration) error { return nil }
