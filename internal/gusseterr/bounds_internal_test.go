package gusseterr

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// decodedBytesOf is the decoded-size cost model computed from the decoded structures, not from
// the frame: what decodePackage must have charged for a frame that decodes to funcs.
func decodedBytesOf(funcs []FuncIR) uint64 {
	var total uint64
	for i := range funcs {
		f := &funcs[i]
		total += DecodedFuncBytes + uint64(len(f.SlotFlags)) + uint64(len(f.Tuples))*DecodedTupleBytes
		for _, b := range f.Blocks {
			total += DecodedBlockBytes + uint64(len(b.Phis))*DecodedPhiBytes + uint64(len(b.Ops))*DecodedOpBytes
			for _, phi := range b.Phis {
				total += uint64(len(phi.Incoming)) * DecodedPhiEdgeBytes
			}
			for _, op := range b.Ops {
				total += uint64(len(op.Companions)) * DecodedCompanionBytes
			}
		}
	}
	return total
}

// boundPackages are packages that between them have every structure the decoder charges for:
// slots, tuples, blocks, phis with edges, ops and bound companions.
func boundPackages() map[string][]FuncIR {
	rng := rand.New(rand.NewPCG(0xb0, 0x11))
	pkgs := map[string][]FuncIR{
		"phis, tuples and companions": {phiMergedTuples()},
		"empty":                       nil,
		"bodyless":                    {{Flags: FuncReturnsConcreteErrPtr | FuncExternalMayNil}},
		"chain":                       callChain(50),
	}
	for i := range 20 {
		pkgs[fmt.Sprintf("random %d", i)] = advCallGraph(rng)
	}
	return pkgs
}

// TestDecodedSizeModelCoversRecords: each cost in the model is at least the in-memory size of the
// Go record it pays for (the Rust records are smaller; the Rust unit tests assert theirs), so the
// bound limits what the decoders allocate and not a figure that has drifted from it.
func TestDecodedSizeModelCoversRecords(t *testing.T) {
	for _, c := range []struct {
		name  string
		model uintptr
		real  uintptr
	}{
		{"FuncIR", DecodedFuncBytes, unsafe.Sizeof(FuncIR{})},
		{"TupleMeta", DecodedTupleBytes, unsafe.Sizeof(TupleMeta{})},
		{"BlockIR", DecodedBlockBytes, unsafe.Sizeof(BlockIR{})},
		{"PhiIR", DecodedPhiBytes, unsafe.Sizeof(PhiIR{})},
		{"PhiEdge", DecodedPhiEdgeBytes, unsafe.Sizeof(PhiEdge{})},
		{"OpIR", DecodedOpBytes, unsafe.Sizeof(OpIR{})},
		{"companion slot", DecodedCompanionBytes, unsafe.Sizeof(uint32(0))},
	} {
		if c.model < c.real {
			t.Errorf("%s: the model charges %d bytes but the record is %d", c.name, c.model, c.real)
		}
	}
}

// TestDecodedBoundExactAndOneOver: a frame decodes with a decoded bound equal to what it costs
// and is refused, with the decoded_bytes limit error, with one byte less; the decoder charges
// exactly the cost model.
func TestDecodedBoundExactAndOneOver(t *testing.T) {
	for name, funcs := range boundPackages() {
		frame, err := EncodePackage(funcs)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cost := decodedBytesOf(funcs)
		got, err := decodePackage(frame, cost)
		if err != nil {
			t.Fatalf("%s: frame costing %d refused at a bound of %d: %v", name, cost, cost, err)
		}
		if len(got) != len(funcs) {
			t.Fatalf("%s: decoded %d functions, want %d", name, len(got), len(funcs))
		}
		if cost == 0 {
			continue
		}
		if _, err := decodePackage(frame, cost-1); err == nil || !strings.Contains(err.Error(), "decoded_bytes") {
			t.Fatalf("%s: frame costing %d at a bound of %d: err = %v, want a decoded_bytes limit error", name, cost, cost-1, err)
		}
		if _, err := SolveFrameOracle(context.Background(), frame); err != nil {
			t.Fatalf("%s: SolveFrameOracle under the real bound: %v", name, err)
		}
	}
}

// TestHostileCountsAreRefusedBeforeTheyAllocate: a frame that claims large counts with few bytes
// is refused at the first charge that passes the bound (so nothing near the claimed size is
// allocated), and under the real bound it is a truncation or limit error that allocates little.
func TestHostileCountsAreRefusedBeforeTheyAllocate(t *testing.T) {
	// One function with MaxOpsPerBlock ops claimed in its only block and none of them present.
	f := []byte("GSE1")
	f = binary.LittleEndian.AppendUint32(f, 1)
	f = append(f, 0)
	f = binary.LittleEndian.AppendUint32(f, 0) // slots
	f = binary.LittleEndian.AppendUint32(f, 0) // tuples
	f = binary.LittleEndian.AppendUint32(f, 1) // blocks
	f = append(f, TermStop)
	f = binary.LittleEndian.AppendUint32(f, 0) // phis
	f = binary.LittleEndian.AppendUint32(f, MaxOpsPerBlock)

	claimed := uint64(DecodedFuncBytes + DecodedBlockBytes + MaxOpsPerBlock*DecodedOpBytes)
	if _, err := decodePackage(f, claimed); err == nil || strings.Contains(err.Error(), "decoded_bytes") {
		t.Fatalf("claim of %d bytes at a bound of %d: err = %v, want a truncation error (the claim fits)", claimed, claimed, err)
	}
	if _, err := decodePackage(f, claimed-1); err == nil || !strings.Contains(err.Error(), "decoded_bytes") {
		t.Fatalf("claim of %d bytes at a bound of %d: err = %v, want the decoded_bytes limit error", claimed, claimed-1, err)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := DecodePackage(f)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("a frame claiming ops it does not contain decoded")
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("decoding a %d-byte hostile frame allocated %d bytes", len(f), grew)
	}
}

// TestDecodeBudgetChargeNeverWraps: the charge refuses on overflow instead of wrapping to a small
// total.
func TestDecodeBudgetChargeNeverWraps(t *testing.T) {
	b := decodeBudget{max: ^uint64(0)}
	if err := b.charge(^uint64(0), 2); err == nil {
		t.Fatal("count x unit overflowing uint64 must be refused")
	}
	b = decodeBudget{used: ^uint64(0) - 1, max: ^uint64(0)}
	if err := b.charge(2, 1); err == nil {
		t.Fatal("used + charge overflowing uint64 must be refused")
	}
}

// TestFuncWireSizeMatchesEncodedLength: the size computed before a function is appended is exactly
// the number of bytes appended, for every op, terminator and phi shape the generators make.
func TestFuncWireSizeMatchesEncodedLength(t *testing.T) {
	for name, funcs := range boundPackages() {
		frame, err := encodePackage(funcs, MaxFrameBytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := 8
		for i := range funcs {
			want += int(funcWireSize(&funcs[i]))
		}
		if len(frame) != want {
			t.Fatalf("%s: frame is %d bytes, the sizes add up to %d", name, len(frame), want)
		}
	}
	all := OpIR{}
	for _, kind := range []uint8{OpUnhandledErr, OpBindTuple, OpBindErr, OpAssignSlot, OpCallConcretePtr, OpObserveErr, OpUseCompanion, OpShadowErr, OpBoxConcreteErr, OpMarkWritable, OpUnhandledClose} {
		all.Kind = kind
		all.Companions = []uint32{0, 0, 0}
		fn := FuncIR{SlotFlags: []uint8{0}, Blocks: []BlockIR{{Ops: []OpIR{all}, Term: Terminator{Kind: TermStop}}}}
		frame, err := encodePackage([]FuncIR{fn}, MaxFrameBytes)
		if err != nil {
			t.Fatalf("op kind %d: %v", kind, err)
		}
		if want := 8 + int(funcWireSize(&fn)); len(frame) != want {
			t.Fatalf("op kind %d: frame is %d bytes, the size is %d", kind, len(frame), want)
		}
	}
}

// TestEncoderRefusesBeforeOvershooting: with a last function that crosses the frame bound, the
// encoder refuses before appending any of it: the memory it allocates is a small fraction of that
// function's encoding, and the package one byte under the bound still fits, with a buffer no
// larger than the bound.
func TestEncoderRefusesBeforeOvershooting(t *testing.T) {
	const bound = 4096
	big := FuncIR{SlotFlags: []uint8{0}, Blocks: []BlockIR{{
		Ops:  make([]OpIR, MaxOpsPerBlock),
		Term: Terminator{Kind: TermStop},
	}}}
	for i := range big.Blocks[0].Ops {
		big.Blocks[0].Ops[i] = OpIR{Kind: OpUnhandledErr, SiteID: uint32(i)}
	}
	if size := funcWireSize(&big); size < 1<<20 {
		t.Fatalf("test function encodes to only %d bytes", size)
	}
	pkg := append(callChain(20), big)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := encodePackage(pkg, bound)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "frame bytes") {
		t.Fatalf("package whose last function crosses the bound: err = %v, want a frame-bytes limit error", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 2*bound+4096 {
		t.Fatalf("the encoder allocated %d bytes for a bound of %d: it appended the oversized function before refusing it", grew, bound)
	}

	small := callChain(20)
	full, err := encodePackage(small, MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []int{len(full), len(full) + 1, 2 * len(full)} {
		got, err := encodePackage(small, b)
		if err != nil || !bytes.Equal(got, full) || cap(got) > b {
			t.Fatalf("bound %d: len %d cap %d err %v, want the frame (%d bytes) in a buffer within the bound", b, len(got), cap(got), err, len(full))
		}
	}
	if _, err := encodePackage(small, len(full)-1); err == nil || !strings.Contains(err.Error(), "frame bytes") {
		t.Fatalf("bound one under the frame: err = %v", err)
	}
	if _, err := encodePackage(nil, 7); err == nil {
		t.Fatal("a bound under the 8-byte header must refuse even an empty package")
	}
	if got, err := encodePackage(nil, 8); err != nil || len(got) != 8 {
		t.Fatalf("empty package at a bound of 8: len %d err %v", len(got), err)
	}
}

// TestOracleResponseCap: SolveFrameOracle refuses a response over MaxResponseBytes with the text
// of the native path's refusal ("output N bytes exceeds maximum M bytes") before encoding it, and
// accepts one exactly at the bound.
func TestOracleResponseCap(t *testing.T) {
	ctx := context.Background()
	frame, err := EncodePackage(callChain(10))
	if err != nil {
		t.Fatal(err)
	}
	full, err := SolveFrameOracle(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	got, err := solveFrameOracle(ctx, frame, len(full))
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("response exactly at the bound: err = %v", err)
	}
	_, err = solveFrameOracle(ctx, frame, len(full)-1)
	if want := fmt.Sprintf("output %d bytes exceeds maximum %d bytes", len(full), len(full)-1); err == nil || err.Error() != want {
		t.Fatalf("response one byte over the bound: err = %v, want %q", err, want)
	}
	if MaxResponseBytes != 1<<30 {
		t.Fatalf("MaxResponseBytes = %d, want gusset's MaxBufferBytes (1 GiB)", MaxResponseBytes)
	}
}

// modelGolden is the decoded-size cost, the frame length and the phase-1 package size of
// hubChain(100, true, true, false). The Rust unit test model_matches_the_go_golden pins the same
// numbers for the same package, so both engines charge and refuse at exactly the same modelled
// byte count (the bound check itself is exact in each: TestDecodedBoundExactAndOneOver and
// decoded_bound_exact_and_one_over).
const (
	modelGoldenCost  = 30389
	modelGoldenFrame = 5785
	modelGoldenSize  = 299
)

func TestDecodedModelGoldenMatchesRust(t *testing.T) {
	p := hubChain(100, true, true, false)
	frame, err := EncodePackage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodedBytesOf(p); got != modelGoldenCost || len(frame) != modelGoldenFrame || packageSize(p) != modelGoldenSize {
		t.Fatalf("cost %d, frame %d, size %d; the Rust golden is %d, %d, %d", got, len(frame), packageSize(p), modelGoldenCost, modelGoldenFrame, modelGoldenSize)
	}
	if _, err := decodePackage(frame, modelGoldenCost); err != nil {
		t.Fatalf("at the golden cost: %v", err)
	}
	if _, err := decodePackage(frame, modelGoldenCost-1); !errors.Is(err, ErrDecodedBytesExceeded) {
		t.Fatalf("one byte under the golden cost: %v", err)
	}
}

// TestRefusalsNameTheirBoundAndSentinel: every limit a decoder or the solver refuses on says what
// the bound was, and wraps the sentinel a caller classifies it with.
func TestRefusalsNameTheirBoundAndSentinel(t *testing.T) {
	for name, c := range map[string]struct {
		frame []byte
		bound uint64
		kind  error
	}{
		"num_funcs":   {rawFrame(MaxFuncs + 1), MaxFuncs, ErrLimitExceeded},
		"num_slots":   {rawFrame(1, []byte{0}, le32(MaxSlots+1, 0, 0)), MaxSlots, ErrLimitExceeded},
		"num_tuples":  {rawFrame(1, []byte{0}, le32(0, MaxTuples+1, 0)), MaxTuples, ErrLimitExceeded},
		"num_blocks":  {rawFrame(1, []byte{0}, le32(0, 0, MaxBlocks+1)), MaxBlocks, ErrLimitExceeded},
		"num_phis":    {rawFrame(1, []byte{0}, le32(0, 0, 1), []byte{TermStop}, le32(MaxPhisPerBlock+1), make([]byte, 16)), MaxPhisPerBlock, ErrLimitExceeded},
		"num_ops":     {rawFrame(1, []byte{0}, le32(0, 0, 1), []byte{TermStop}, le32(0, MaxOpsPerBlock+1), make([]byte, 16)), MaxOpsPerBlock, ErrLimitExceeded},
		"state cells": {rawFrame(1, []byte{0}, le32(4096, 0, 2049)), MaxStateCells, ErrLimitExceeded},
	} {
		_, err := decodePackage(c.frame, MaxDecodedBytes)
		want := fmt.Sprintf("over the bound of %d", c.bound)
		if err == nil || !strings.Contains(err.Error(), want) || !errors.Is(err, c.kind) {
			t.Errorf("%s: err = %v, want it to name %q and wrap %v", name, err, want, c.kind)
		}
	}
	_, err := decodePackage(make([]byte, MaxFrameBytes+1), MaxDecodedBytes)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("over the bound of %d", MaxFrameBytes)) || !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("frame_bytes: %v", err)
	}
	fn := FuncIR{SlotFlags: []uint8{0}, Blocks: []BlockIR{{Ops: make([]OpIR, 4), Term: Terminator{Kind: TermStop}}}}
	for i := range fn.Blocks[0].Ops {
		fn.Blocks[0].Ops[i] = OpIR{Kind: OpUnhandledErr}
	}
	frame, _ := EncodePackage([]FuncIR{fn})
	_, err = decodePackage(frame, 100)
	if err == nil || !errors.Is(err, ErrDecodedBytesExceeded) || !strings.Contains(err.Error(), "decoded_bytes over the bound of") {
		t.Errorf("decoded_bytes: %v", err)
	}
	_, err = solveOracle(context.Background(), callCycle(9), 2)
	if !errors.Is(err, ErrCallgraphBudgetExceeded) {
		t.Errorf("call-graph cap: %v", err)
	}
	if _, err := solveFrameOracle(context.Background(), mustEncode(t, callChain(10)), 5); !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("response cap: %v", err)
	}
}

func mustEncode(t *testing.T, funcs []FuncIR) []byte {
	t.Helper()
	f, err := EncodePackage(funcs)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestEngineRefusalMapsCodeToSentinelWithOracleText: the message the Rust engine hands gusset
// ("<code>: <text>") becomes an error whose text is the text alone (what the oracle prints for
// the same refusal) and which matches the code's sentinel and the transport error.
func TestEngineRefusalMapsCodeToSentinelWithOracleText(t *testing.T) {
	transport := errors.New("gusset error [1]: carrier")
	for code, kind := range map[string]error{
		"work_budget_exceeded":      ErrWorkBudgetExceeded,
		"decoded_bytes_exceeded":    ErrDecodedBytesExceeded,
		"callgraph_budget_exceeded": ErrCallgraphBudgetExceeded,
		"iteration_budget_exceeded": ErrIterationBudgetExceeded,
		"response_too_large":        ErrResponseTooLarge,
		"limit_exceeded":            ErrLimitExceeded,
	} {
		err := engineRefusal(code+": the text: with a colon", transport)
		if err == nil || err.Error() != "the text: with a colon" || !errors.Is(err, kind) || !errors.Is(err, transport) {
			t.Errorf("%s: %v", code, err)
		}
	}
	for _, code := range []string{"truncated", "bad_magic", "invalid_enum"} {
		err := engineRefusal(code+": malformed", transport)
		if err == nil || err.Error() != "malformed" || !errors.Is(err, transport) {
			t.Errorf("%s: %v", code, err)
		}
	}
	for _, msg := range []string{"cancelled: solver cancelled", "no code here", "unknown_code: x", ""} {
		if err := engineRefusal(msg, transport); err != nil {
			t.Errorf("%q must not map: %v", msg, err)
		}
	}
}

// TestPhase1WorkUnitCountsStateCells: the unit of a solve is a function's ops plus blocks x
// max(slots, 1), so a wide function counts for what its solver state costs and a slotless one
// still counts its blocks.
func TestPhase1WorkUnitCountsStateCells(t *testing.T) {
	for _, c := range []struct{ blocks, slots int }{{2048, 1}, {2048, 4096}, {7, 0}, {1, 3}} {
		fn := shapeFunc(c.blocks, c.slots)
		var st phase1Stats
		if _, err := solveOracleLimited(context.Background(), []FuncIR{fn}, 2, Phase1WorkFactor, Phase1WorkFloor, &st); err != nil {
			t.Fatal(err)
		}
		if want := uint64(c.blocks) * uint64(max(c.slots, 1)); st.work != want || packageSize([]FuncIR{fn}) != want {
			t.Errorf("%d blocks x %d slots: %d work units, size %d, want %d", c.blocks, c.slots, st.work, packageSize([]FuncIR{fn}), want)
		}
	}
}
