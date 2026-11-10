package gusseterr_test

import (
	"bytes"
	"context"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

func TestSelfTestAndPanicFirewall(t *testing.T) {
	ctx := context.Background()
	if !gusseterr.NativeActive() {
		t.Skip("native Rust Gusset engine inactive (!unix or !cgo)")
	}
	if err := gusseterr.SelfTest(ctx); err != nil {
		t.Fatalf("SelfTest failed: %v", err)
	}
	var logBuf bytes.Buffer
	_, err := gusseterr.DrainLogs(&logBuf)
	if err != nil {
		t.Fatalf("DrainLogs failed: %v", err)
	}
	if gusseterr.Opens() < 1 {
		t.Fatalf("expected shared handle Opens >= 1, got %d", gusseterr.Opens())
	}
	if alloc := gusseterr.RustAllocatedBytes(); alloc == 0 {
		t.Fatalf("expected Counting<System> global allocator to report live Rust heap bytes > 0, got 0")
	}
}

func TestMalformedFramesFailClosedWithoutPoisoning(t *testing.T) {
	ctx := context.Background()
	badFrames := [][]byte{
		nil,
		{},
		[]byte("BAD!"),
		[]byte("GSE1\xff\xff\xff\x7f"),
		[]byte("GSE1\x00\x00\x00\x00trailing"),
	}
	for i, bf := range badFrames {
		if _, err := gusseterr.SolveFrame(ctx, bf); err == nil {
			t.Fatalf("bad frame %d unexpectedly succeeded", i)
		}
		if _, err := gusseterr.SolveFrameOracle(ctx, bf); err == nil {
			t.Fatalf("oracle bad frame %d unexpectedly succeeded", i)
		}
	}
	// Shared handle must remain healthy after rejecting malformed payloads.
	if err := gusseterr.Check(ctx); err != nil {
		t.Fatalf("Check after malformed frames failed: %v", err)
	}
}

func TestCancelledContextPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pkg := buildRandomPackage(rand.New(rand.NewPCG(7, 11)), 12, 10)
	if _, err := gusseterr.Solve(ctx, pkg); err == nil {
		t.Fatal("expected cancelled context to fail Solve")
	}
	if _, err := gusseterr.SolveOracle(ctx, pkg); err == nil {
		t.Fatal("expected cancelled context to fail SolveOracle")
	}
}

func TestDifferentialParityRandomizedCFGs(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(0x60ca_9001, 0x9052_ee42))

	sawSmall := false
	sawLargeBuffer := false

	for trial := range 250 {
		nFuncs := 1 + rng.IntN(16)
		nBlocks := 2 + rng.IntN(14)
		if trial%25 == 0 {
			// Force >4 KiB payload to exercise NewBuffer + CallBuffer path!
			nFuncs = 40
			nBlocks = 18
		}
		pkg := buildRandomPackage(rng, nFuncs, nBlocks)
		req, err := gusseterr.EncodePackage(pkg)
		if err != nil {
			t.Fatalf("trial %d EncodePackage: %v", trial, err)
		}
		if len(req) <= 4096 {
			sawSmall = true
		} else {
			sawLargeBuffer = true
		}

		gotBytes, err := gusseterr.SolveFrame(ctx, req)
		if err != nil {
			t.Fatalf("trial %d SolveFrame (%d bytes): %v", trial, len(req), err)
		}
		wantBytes, err := gusseterr.SolveFrameOracle(ctx, req)
		if err != nil {
			t.Fatalf("trial %d SolveFrameOracle (%d bytes): %v", trial, len(req), err)
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			gotRes, _ := gusseterr.DecodeResult(gotBytes)
			wantRes, _ := gusseterr.DecodeResult(wantBytes)
			t.Fatalf("trial %d differential mismatch (frame=%d bytes):\nRust Gusset: %+v\nGo Oracle:   %+v",
				trial, len(req), gotRes, wantRes)
		}
	}
	if !sawSmall || !sawLargeBuffer {
		t.Fatalf("expected both <=4KiB (%v) and >4KiB (%v) frames to be exercised", sawSmall, sawLargeBuffer)
	}
}

func TestConcurrentGoroutineStress(t *testing.T) {
	ctx := context.Background()
	const workers = 32
	const iters = 15

	pkgs := make([][]gusseterr.FuncIR, workers)
	wants := make([][]byte, workers)
	for w := range workers {
		rng := rand.New(rand.NewPCG(uint64(100+w), uint64(900+w)))
		pkgs[w] = buildRandomPackage(rng, 18, 12)
		req, err := gusseterr.EncodePackage(pkgs[w])
		if err != nil {
			t.Fatal(err)
		}
		wb, err := gusseterr.SolveFrameOracle(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		wants[w] = wb
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workers*iters)
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req, err := gusseterr.EncodePackage(pkgs[id])
			if err != nil {
				errCh <- err
				return
			}
			for range iters {
				got, err := gusseterr.SolveFrame(ctx, req)
				if err != nil {
					errCh <- err
					return
				}
				if !bytes.Equal(got, wants[id]) {
					t.Errorf("worker %d concurrent output diverged from oracle", id)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent SolveFrame error: %v", err)
	}
}

func buildRandomPackage(rng *rand.Rand, nFuncs, maxBlocks int) []gusseterr.FuncIR {
	funcs := make([]gusseterr.FuncIR, nFuncs)
	for fi := range nFuncs {
		nSlots := 4 + rng.IntN(8)
		slotFlags := make([]uint8, nSlots)
		for s := range nSlots {
			if s%2 == 0 {
				slotFlags[s] |= gusseterr.SlotIsError
			}
			if rng.IntN(5) == 0 {
				slotFlags[s] |= gusseterr.SlotInitWritable
			}
		}
		var flags uint8
		if rng.IntN(2) == 0 {
			flags |= gusseterr.FuncReturnsError
		}
		if rng.IntN(3) == 0 {
			flags |= gusseterr.FuncReturnsConcreteErrPtr
		}

		nTuples := 1 + rng.IntN(4)
		tuples := make([]gusseterr.TupleMeta, nTuples)
		for ti := range nTuples {
			es := uint32(rng.IntN(nSlots))
			var tf uint8
			if rng.IntN(4) == 0 {
				tf |= gusseterr.TupleErrBlanked
				es = gusseterr.NoneU32
			}
			if rng.IntN(4) == 0 {
				tf |= gusseterr.TupleIORead
			}
			tuples[ti] = gusseterr.TupleMeta{
				ErrSlot:    es,
				CallSiteID: uint32(1000*fi + ti),
				Flags:      tf,
			}
		}

		nb := 2 + rng.IntN(maxBlocks)
		blocks := make([]gusseterr.BlockIR, nb)
		siteSeq := uint32(10_000 * (fi + 1))

		for bi := range nb {
			var phis []gusseterr.PhiIR
			if bi > 0 && rng.IntN(3) == 0 {
				p1 := uint32(rng.IntN(bi))
				p2 := uint32(rng.IntN(nb))
				phis = append(phis, gusseterr.PhiIR{
					DstSlot: uint32(rng.IntN(nSlots)),
					Incoming: []gusseterr.PhiEdge{
						{PredBB: p1, SrcSlot: uint32(rng.IntN(nSlots))},
						{PredBB: p2, SrcSlot: uint32(rng.IntN(nSlots))},
					},
				})
			}

			nOps := 1 + rng.IntN(6)
			ops := make([]gusseterr.OpIR, 0, nOps)
			for range nOps {
				siteSeq++
				sA := uint32(rng.IntN(nSlots))
				sB := uint32(rng.IntN(nSlots))
				switch rng.IntN(11) {
				case 0:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpUnhandledErr, SiteID: siteSeq})
				case 1:
					tid := uint32(rng.IntN(nTuples))
					es := tuples[tid].ErrSlot
					cSlot := uint32((int(sA) + 1) % nSlots)
					ops = append(ops, gusseterr.OpIR{
						Kind:       gusseterr.OpBindTuple,
						SiteID:     siteSeq,
						TupleID:    tid,
						SlotA:      es,
						Companions: []uint32{cSlot},
					})
				case 2:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpBindErr, SiteID: siteSeq, SlotA: sA})
				case 3:
					src := sB
					if rng.IntN(3) == 0 {
						src = gusseterr.NoneU32
					}
					ops = append(ops, gusseterr.OpIR{
						Kind:         gusseterr.OpAssignSlot,
						SiteID:       siteSeq,
						SlotA:        sA,
						SlotB:        src,
						NilHint:      uint8(rng.IntN(4)),
						WritableHint: uint8(rng.IntN(2)),
					})
				case 4:
					callee := uint32(rng.IntN(nFuncs))
					ops = append(ops, gusseterr.OpIR{
						Kind:   gusseterr.OpCallConcretePtr,
						SiteID: siteSeq,
						SlotA:  sA,
						SlotB:  callee,
					})
				case 5:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpObserveErr, SiteID: siteSeq, SlotA: sA})
				case 6:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpUseCompanion, SiteID: siteSeq, SlotA: sA})
				case 7:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpShadowErr, SiteID: siteSeq, SlotA: sA, SlotB: sB})
				case 8:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpBoxConcreteErr, SiteID: siteSeq, SlotA: sA})
				case 9:
					ops = append(ops, gusseterr.OpIR{Kind: gusseterr.OpMarkWritable, SiteID: siteSeq, SlotA: sA})
				case 10:
					ops = append(ops, gusseterr.OpIR{
						Kind:    gusseterr.OpUnhandledClose,
						SiteID:  siteSeq,
						SlotA:   sA,
						IsDefer: rng.IntN(2) == 0,
					})
				}
			}

			siteSeq++
			var term gusseterr.Terminator
			if bi == nb-1 {
				var rf uint8
				if rng.IntN(2) == 0 {
					rf |= gusseterr.RetLiteralNilErr
				}
				if rng.IntN(3) == 0 {
					rf |= gusseterr.RetConcreteLiteralNil
				}
				term = gusseterr.Terminator{
					Kind:     gusseterr.TermReturn,
					SiteID:   siteSeq,
					RetSlot:  uint32(rng.IntN(nSlots)),
					RetFlags: rf,
				}
			} else {
				switch rng.IntN(4) {
				case 0:
					// Include occasional back-edges to stress cyclic loop convergence!
					target := uint32(bi + 1)
					if rng.IntN(5) == 0 {
						target = uint32(rng.IntN(nb))
					}
					term = gusseterr.Terminator{Kind: gusseterr.TermJump, Target: target}
				case 1, 2:
					tBB := uint32(bi + 1)
					fBB := uint32(rng.IntN(nb))
					ck := uint8(rng.IntN(4))
					cs := uint32(rng.IntN(nSlots))
					term = gusseterr.Terminator{
						Kind:     gusseterr.TermCond,
						Target:   tBB,
						FalseBB:  fBB,
						CondKind: ck,
						CondSlot: cs,
					}
				case 3:
					term = gusseterr.Terminator{Kind: gusseterr.TermStop}
				}
			}
			blocks[bi] = gusseterr.BlockIR{Phis: phis, Ops: ops, Term: term}
		}

		funcs[fi] = gusseterr.FuncIR{
			Flags:     flags,
			SlotFlags: slotFlags,
			Tuples:    tuples,
			Blocks:    blocks,
		}
	}
	return funcs
}
