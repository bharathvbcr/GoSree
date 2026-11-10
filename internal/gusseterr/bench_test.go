package gusseterr_test

import (
	"context"
	"math/rand/v2"
	"testing"

	"github.com/bharathvbcr/gocap/internal/gusseterr"
)

func BenchmarkSolvePackageAB(b *testing.B) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		nFuncs int
		blocks int
	}{
		{"MediumPkg_50Funcs", 50, 16},
		{"LargePkg_200Funcs", 200, 20},
	} {
		rng := rand.New(rand.NewPCG(42, 99))
		pkg := buildRandomPackage(rng, tc.nFuncs, tc.blocks)
		req, err := gusseterr.EncodePackage(pkg)
		if err != nil {
			b.Fatal(err)
		}
		// Warm up shared Gusset handle before timed loops.
		if _, err := gusseterr.SolveFrame(ctx, req); err != nil {
			b.Fatal(err)
		}

		b.Run(tc.name+"/GoOracle", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(req)))
			for b.Loop() {
				out, err := gusseterr.SolveFrameOracle(ctx, req)
				if err != nil || len(out) == 0 {
					b.Fatalf("oracle failed: %v", err)
				}
			}
		})

		b.Run(tc.name+"/RustGusset", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(req)))
			for b.Loop() {
				out, err := gusseterr.SolveFrame(ctx, req)
				if err != nil || len(out) == 0 {
					b.Fatalf("gusset failed: %v", err)
				}
			}
		})
	}
}
