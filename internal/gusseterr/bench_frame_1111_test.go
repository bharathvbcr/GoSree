package gusseterr_test

import (
	"context"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// TestWriteBenchFrame writes the frame BenchmarkSolveFrameFile solves, so two builds can be
// compared on byte-identical input: GUSSETERR_WRITE_FRAME=<path> go test -run TestWriteBenchFrame.
func TestWriteBenchFrame(t *testing.T) {
	path := os.Getenv("GUSSETERR_WRITE_FRAME")
	if path == "" {
		t.Skip("GUSSETERR_WRITE_FRAME not set")
	}
	req, err := gusseterr.EncodePackage(buildRandomPackage(rand.New(rand.NewPCG(42, 99)), 200, 20))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, req, 0o600); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkSolveFrameFile solves the frame in GUSSETERR_BENCH_FRAME with both engines. Unlike
// BenchmarkSolvePackageAB it does not depend on the generator, so builds compare like with like.
func BenchmarkSolveFrameFile(b *testing.B) {
	path := os.Getenv("GUSSETERR_BENCH_FRAME")
	if path == "" {
		b.Skip("GUSSETERR_BENCH_FRAME not set")
	}
	req, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if _, err := gusseterr.SolveFrame(ctx, req); err != nil {
		b.Fatal(err)
	}
	b.Run("GoOracle", func(b *testing.B) {
		for b.Loop() {
			if _, err := gusseterr.SolveFrameOracle(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Engine", func(b *testing.B) {
		for b.Loop() {
			if _, err := gusseterr.SolveFrame(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})
}
