package gusseterr_test

import (
	"bytes"
	"context"
	"math/rand/v2"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// FuzzDecoders feeds arbitrary bytes to every decoder and engine. The property is that nothing
// panics or aborts: an invalid frame is an error, and when both engines accept a frame they
// return identical bytes. A Rust panic would retire the shared handle, which Opens() shows.
func FuzzDecoders(f *testing.F) {
	ctx := context.Background()
	f.Add([]byte(nil))
	f.Add([]byte("GSE1"))
	f.Add([]byte("GSR1"))
	f.Add([]byte("GSE1\x00\x00\x00\x00"))
	f.Add(headerOnlyFrame(4096, 0, 2049))
	f.Add(headerOnlyFrame(1, 1, 1))
	for _, pkg := range [][]gusseterr.FuncIR{
		errPhiLoopPackage(),
		twoBranchTuplePackage(true),
		buildRandomPackage(rand.New(rand.NewPCG(1, 2)), 4, 6),
		buildRandomPackage(rand.New(rand.NewPCG(3, 4)), 8, 10),
	} {
		req, err := gusseterr.EncodePackage(pkg)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(req)
		if resp, err := gusseterr.SolveFrameOracle(ctx, req); err == nil {
			f.Add(resp)
		}
	}

	if err := gusseterr.Check(ctx); err != nil { // open the shared handle before counting opens
		f.Fatal(err)
	}
	opens := gusseterr.Opens()

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = gusseterr.DecodeResult(data)
		_, decErr := gusseterr.DecodePackage(data)
		want, oracleErr := gusseterr.SolveFrameOracle(ctx, data)
		if decErr != nil && oracleErr == nil {
			t.Fatalf("SolveFrameOracle accepted a frame DecodePackage rejected: %v", decErr)
		}
		got, err := gusseterr.SolveFrame(ctx, data)
		if gusseterr.Opens() != opens {
			t.Fatalf("shared handle was retired (engine panic?) on frame %x", data)
		}
		if (err == nil) != (oracleErr == nil) {
			t.Fatalf("engines disagree on validity of %x: engine err = %v, oracle err = %v", data, err, oracleErr)
		}
		if err == nil && !bytes.Equal(got, want) {
			t.Fatalf("engines disagree on %x", data)
		}
	})
}

// TestDecodersRejectRandomFramesWithoutPanicking is the bounded, always-on form of the fuzz
// target: random noise, noise behind valid magics, and byte mutations of valid frames.
func TestDecodersRejectRandomFramesWithoutPanicking(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(0xf00d, 0xbeef))
	valid, err := gusseterr.EncodePackage(buildRandomPackage(rng, 6, 8))
	if err != nil {
		t.Fatal(err)
	}
	if err := gusseterr.Check(ctx); err != nil {
		t.Fatal(err)
	}
	opens := gusseterr.Opens()
	accepted := 0
	const trials = 3000
	for i := range trials {
		var frame []byte
		switch i % 3 {
		case 0:
			frame = make([]byte, rng.IntN(200))
			for j := range frame {
				frame[j] = byte(rng.IntN(256))
			}
		case 1:
			frame = []byte("GSE1")
			for range rng.IntN(200) {
				frame = append(frame, byte(rng.IntN(256)))
			}
		default:
			frame = bytes.Clone(valid)
			for range 1 + rng.IntN(4) {
				frame[rng.IntN(len(frame))] = byte(rng.IntN(256))
			}
		}
		_, _ = gusseterr.DecodeResult(frame)
		want, oErr := gusseterr.SolveFrameOracle(ctx, frame)
		got, err := gusseterr.SolveFrame(ctx, frame)
		if (err == nil) != (oErr == nil) {
			t.Fatalf("frame %d (%x): engine err = %v, oracle err = %v", i, frame, err, oErr)
		}
		if err == nil {
			accepted++
			if !bytes.Equal(got, want) {
				t.Fatalf("frame %d (%x): engines disagree", i, frame)
			}
		}
		if gusseterr.Opens() != opens {
			t.Fatalf("frame %d (%x): shared handle was retired", i, frame)
		}
	}
	if accepted == trials {
		t.Fatal("every random frame was accepted; the decoders are not validating")
	}
}
