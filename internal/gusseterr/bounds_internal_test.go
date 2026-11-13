package gusseterr

import (
	"bytes"
	"context"
	"encoding/binary"
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
