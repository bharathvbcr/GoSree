// Package gusseterr bridges GoSree's error-discipline analyzer (passes/errcheck)
// to the in-process Rust CFG/SSA solver (rust/gosree-errsolve) over Gusset,
// accompanied by a bit-exact pure-Go reference oracle for differential parity
// verification and non-cgo portability.
package gusseterr

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const NoneU32 uint32 = ^uint32(0)

// Fewest wire bytes of each record a count in the frame can claim, mirroring the *_WIRE_BYTES
// constants in rust/gosree-errsolve/src/lib.rs: a function header, a tuple, a block (terminator
// tag, phi count, op count), a phi (slot and arity), a phi edge, an op (kind and site id) and a
// companion slot.
const (
	minFuncWireBytes   = 13
	tupleWireBytes     = 9
	minBlockWireBytes  = 9
	minPhiWireBytes    = 6
	phiEdgeWireBytes   = 8
	minOpWireBytes     = 5
	companionWireBytes = 4
)

// Limits matching rust/gosree-errsolve/src/lib.rs.
const (
	// MaxFuncs bounds the functions in one package frame, set by real packages: ScholarLM's
	// internal/wisdev with its tests has 18,973. Every per-package structure is linear in it
	// (summary bits, caller lists, the call-graph SCC arrays, the phase-1 worklists), and phase 1
	// costs O((functions + call edges) x log functions) plus one solve per function and per
	// callee flip inside a call-graph cycle.
	//
	// History: phase 1 used to visit dirty functions in index order, so a function calling every
	// member of a caller-first chain was solved about once per member (186 s at this limit,
	// docs/ERROR_DISCIPLINE.md). Since 2026-11-12 it runs the call graph's strongly connected
	// components callees first, so that function is solved once, and Phase1WorkFactor bounds what
	// a cycle can still cost. Mirrors MAX_FUNCS in rust/gosree-errsolve/src/lib.rs.
	MaxFuncs             = 262_144
	MaxSlots             = 262_144
	MaxTuples            = 16_384
	MaxBlocks            = 32_768
	MaxPhisPerBlock      = 16_384
	MaxPhiArity          = 4_096
	MaxOpsPerBlock       = 262_144
	MaxCompanions        = 256
	MaxIterationsPerFunc = 250_000

	// MaxStateCells bounds blocks x slots for one function. The solver holds four full
	// abstract states per block (entry, exit and up to two outgoing edges): 4 x 28 = 112 bytes
	// per (block, slot) cell in Rust, so 2^23 = 8,388,608 cells is at most 896 MiB of Rust state
	// (measured peak RSS about 0.9 GiB). The Go oracle clones a state per step and measured 1.3 to
	// 1.6 GiB at the budget (shapes 2048x4096, 32768x256, 8192x1024), so the roughly 1 GiB target
	// holds for the Rust engine only. States are freed between functions, so the bound does not
	// multiply with MaxFuncs. Mirrors MAX_STATE_CELLS_PER_FUNC in rust/gosree-errsolve/src/lib.rs.
	MaxStateCells = 8_388_608

	// MaxFrameBytes bounds a GSE1 request frame: 1 GiB, gusset.MaxBufferBytes, the largest
	// payload the linked engine can be handed at all. The encoder and both decoders enforce it,
	// so the build without cgo refuses the same frames as the native one instead of solving
	// what the native build cannot be sent. Mirrors MAX_FRAME_BYTES in
	// rust/gosree-errsolve/src/lib.rs.
	MaxFrameBytes = 1 << 30

	// MaxDecodedBytes bounds what a decoder builds from one frame, in the bytes the cost model
	// below charges before each structure is allocated: 2 GiB, twice MaxFrameBytes. A frame byte
	// can decode to many more bytes (a 5-byte op to a DecodedOpBytes record, a 9-byte empty
	// block to DecodedBlockBytes), so the frame bound alone does not bound memory: a 100 MiB
	// frame of ops decoded to 1,139 MiB. The worst case is 11.2 decoded bytes per frame byte,
	// so a densely packed frame over about 190 MiB is refused, and a 1 GiB frame that is mostly
	// slot flags or phi edges is accepted. Mirrors MAX_DECODED_BYTES in
	// rust/gosree-errsolve/src/lib.rs.
	MaxDecodedBytes = 1 << 31

	// The decoded-size cost model, mirrored by the DECODED_*_BYTES constants in
	// rust/gosree-errsolve/src/lib.rs. Each is at least the in-memory size of the record in both
	// engines (asserted by tests); Go's records are the larger, so the model charges Go's sizes.
	DecodedFuncBytes      = 96
	DecodedTupleBytes     = 12
	DecodedBlockBytes     = 96
	DecodedPhiBytes       = 32
	DecodedPhiEdgeBytes   = 8
	DecodedOpBytes        = 56
	DecodedCompanionBytes = 4

	// MaxResponseBytes bounds a GSR1 response: 1 GiB, gusset.MaxBufferBytes, which the native
	// path refuses an engine output over with "output N bytes exceeds maximum M bytes". The
	// oracle refuses before encoding, with the same text. Mirrors MAX_RESPONSE_BYTES in
	// rust/gosree-errsolve/src/lib.rs.
	MaxResponseBytes = 1 << 30

	// Phase1WorkFactor and Phase1WorkFloor bound the solves of the interprocedural summary
	// phase: at most Phase1WorkFactor x the package's size in work units, and at least
	// Phase1WorkFloor. A function's work unit count is its ops plus its state cells, blocks x
	// max(slots, 1) (the product MaxStateCells bounds), and a solve of it costs one count. The unit is a property of the package, not of the machine, so both
	// engines refuse exactly the same packages at the same solve. docs/ERROR_DISCIPLINE.md has
	// the measured ratios the factor was chosen from. Mirrors PHASE1_WORK_FACTOR and
	// PHASE1_WORK_FLOOR in rust/gosree-errsolve/src/lib.rs.
	Phase1WorkFactor = 64
	Phase1WorkFloor  = 1 << 20
)

// checkStateCells enforces MaxStateCells for one function; every encoder and decoder uses it so
// both engines apply the identical rule.
func checkStateCells(nBlocks, nSlots uint64) error {
	if nBlocks*nSlots > MaxStateCells {
		return limitErr("state cells (blocks x slots)")
	}
	return nil
}

// Sentinels for the refusals a caller can classify with errors.Is. Both engines return errors
// that wrap them with identical text: the oracle builds them directly, and the native path maps
// the code the Rust engine puts in front of its message (SolveError::code) back to them.
var (
	// ErrWorkBudgetExceeded: phase 1 did more work than the package's budget allows.
	ErrWorkBudgetExceeded = errors.New("gusseterr: phase-1 work budget exceeded")
	// ErrDecodedBytesExceeded: the decoded program would be over MaxDecodedBytes.
	ErrDecodedBytesExceeded = errors.New("gusseterr: decoded size over MaxDecodedBytes")
	// ErrCallgraphBudgetExceeded: a component's summary sweeps did not converge in the cap.
	ErrCallgraphBudgetExceeded = errors.New("gusseterr: call-graph sweep cap exceeded")
	// ErrIterationBudgetExceeded: a function's CFG worklist ran past MaxIterationsPerFunc.
	ErrIterationBudgetExceeded = errors.New("gusseterr: CFG worklist budget exceeded")
	// ErrResponseTooLarge: the GSR1 response would be over MaxResponseBytes.
	ErrResponseTooLarge = errors.New("gusseterr: response over MaxResponseBytes")
	// ErrLimitExceeded: a count, size or frame bound of Section 3.3 of the design doc was passed.
	ErrLimitExceeded = errors.New("gusseterr: GSE1 limit exceeded")
)

// refusal is an error whose text is exactly msg and which matches kind (and cause, if any).
type refusal struct {
	kind  error
	msg   string
	cause error
}

func (e *refusal) Error() string { return e.msg }

func (e *refusal) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func refuse(kind error, format string, args ...any) error {
	return &refusal{kind: kind, msg: fmt.Sprintf(format, args...)}
}

// limitBound is the bound a limit error names, mirroring limit_bound in
// rust/gosree-errsolve/src/lib.rs.
func limitBound(what string) (uint64, bool) {
	switch what {
	case "frame_bytes":
		return MaxFrameBytes, true
	case "decoded_bytes":
		return MaxDecodedBytes, true
	case "num_funcs":
		return MaxFuncs, true
	case "num_slots":
		return MaxSlots, true
	case "num_tuples":
		return MaxTuples, true
	case "num_blocks":
		return MaxBlocks, true
	case "state cells (blocks x slots)":
		return MaxStateCells, true
	case "num_phis":
		return MaxPhisPerBlock, true
	case "phi_arity":
		return MaxPhiArity, true
	case "num_ops":
		return MaxOpsPerBlock, true
	case "companions":
		return MaxCompanions, true
	}
	return 0, false
}

// limitErr is the error for passing the limit named what, with the text of
// SolveError::LimitExceeded in the Rust engine: "GSE1 limit exceeded: <what> over the bound of <n>".
// The decoded-size limit has its own sentinel.
func limitErr(what string) error {
	kind := ErrLimitExceeded
	if what == "decoded_bytes" {
		kind = ErrDecodedBytesExceeded
	}
	if bound, ok := limitBound(what); ok {
		return refuse(kind, "GSE1 limit exceeded: %s over the bound of %d", what, bound)
	}
	return refuse(kind, "GSE1 limit exceeded: %s", what)
}

// Function flags.
const (
	FuncReturnsError          uint8 = 1 << 0
	FuncReturnsConcreteErrPtr uint8 = 1 << 1
	FuncExternalMayNil        uint8 = 1 << 2
)

// Slot flags.
const (
	SlotIsError         uint8 = 1 << 0
	SlotInitMayNilParam uint8 = 1 << 1
	SlotInitWritable    uint8 = 1 << 2
)

// Tuple flags.
const (
	TupleErrBlanked uint8 = 1 << 0
	TupleIORead     uint8 = 1 << 1
)

// Terminator kinds.
const (
	TermJump   uint8 = 0
	TermCond   uint8 = 1
	TermReturn uint8 = 2
	TermStop   uint8 = 3
)

// Branch condition kinds.
const (
	CondOpaque uint8 = 0
	CondIsNil  uint8 = 1
	CondNotNil uint8 = 2
	CondGtZero uint8 = 3
)

// Return flags.
const (
	RetLiteralNilErr      uint8 = 1 << 0
	RetConcreteLiteralNil uint8 = 1 << 1
)

// Operation kinds.
const (
	OpUnhandledErr    uint8 = 1
	OpBindTuple       uint8 = 2
	OpBindErr         uint8 = 3
	OpAssignSlot      uint8 = 4
	OpCallConcretePtr uint8 = 5
	OpObserveErr      uint8 = 6
	OpUseCompanion    uint8 = 7
	OpShadowErr       uint8 = 8
	OpBoxConcreteErr  uint8 = 9
	OpMarkWritable    uint8 = 10
	OpUnhandledClose  uint8 = 11
)

// Nil assignment hints.
const (
	AssignNilCopy    uint8 = 0
	AssignNilNonNil  uint8 = 1
	AssignNilMayNil  uint8 = 2
	AssignNilMustNil uint8 = 3
)

// Rule codes.
const (
	RuleERR001 uint8 = 1
	RuleERR002 uint8 = 2
	RuleERR003 uint8 = 3
	RuleERR004 uint8 = 4
	RuleERR005 uint8 = 5
)

// Diagnostic sub-kinds.
const (
	SubDefault           uint8 = 0
	SubERR002Unchecked   uint8 = 0
	SubERR002Blanked     uint8 = 1
	SubERR003Shadowed    uint8 = 1
	SubERR003Overwritten uint8 = 2
	SubERR003Nilerr      uint8 = 3
	SubERR004MayNil      uint8 = 0
	SubERR004MustNil     uint8 = 1
	SubERR005SyncClose   uint8 = 0
	SubERR005DeferClose  uint8 = 1
)

var (
	reqMagic  = [4]byte{'G', 'S', 'E', '1'}
	respMagic = [4]byte{'G', 'S', 'R', '1'}

	// ErrClosed reports a call made after Close or Shutdown.
	ErrClosed = errors.New("gusseterr: engine closed")
)

// TupleMeta describes one (T..., error) call tuple in a function.
type TupleMeta struct {
	ErrSlot    uint32
	CallSiteID uint32
	Flags      uint8
}

// PhiEdge is one predecessor incoming edge of a Phi node.
type PhiEdge struct {
	PredBB  uint32
	SrcSlot uint32
}

// PhiIR represents a slot-level SSA Phi join at block entry.
type PhiIR struct {
	DstSlot  uint32
	Incoming []PhiEdge
}

// Terminator ends a CFG basic block.
type Terminator struct {
	Kind     uint8
	Target   uint32 // Jump target, or Cond true_bb
	FalseBB  uint32 // Cond false_bb
	CondKind uint8  // Cond kind
	CondSlot uint32 // Cond subject slot
	SiteID   uint32 // Return site_id
	RetSlot  uint32 // Return slot
	RetFlags uint8  // Return flags
}

// OpIR is one abstract-interpretation transfer instruction inside a block.
type OpIR struct {
	Kind         uint8
	SiteID       uint32
	TupleID      uint32
	SlotA        uint32 // ErrSlot / DstSlot / Slot / InnerSlot / PtrSlot / ResSlot
	SlotB        uint32 // SrcSlot / CalleeIdx / OuterSlot
	Companions   []uint32
	NilHint      uint8
	WritableHint uint8
	IsDefer      bool
}

// BlockIR is one basic block of a function's lowered CFG.
type BlockIR struct {
	Phis []PhiIR
	Ops  []OpIR
	Term Terminator
}

// FuncIR is one function's lowered CFG and metadata for the batch package solver.
type FuncIR struct {
	Flags     uint8
	SlotFlags []uint8
	Tuples    []TupleMeta
	Blocks    []BlockIR
}

// Finding is one deduplicated solver diagnostic in canonical order.
type Finding struct {
	FuncIdx  uint32
	SiteID   uint32
	RuleCode uint8
	SubKind  uint8
}

// PackageResult holds the converged interprocedural summaries and diagnostics.
type PackageResult struct {
	MayReturnNil []bool
	Findings     []Finding
}

// EncodePackage serializes funcs into a GSE1 binary request frame.
func EncodePackage(funcs []FuncIR) ([]byte, error) {
	return encodePackage(funcs, MaxFrameBytes)
}

// funcWireSize is the number of bytes encodePackage appends for fn, computed without allocating
// so a function that would pass the frame bound is refused before any of it is written. An
// invalid terminator or op kind has no size here; encodePackage reports it.
func funcWireSize(fn *FuncIR) uint64 {
	size := 13 + uint64(len(fn.SlotFlags)) + 9*uint64(len(fn.Tuples))
	for bi := range fn.Blocks {
		b := &fn.Blocks[bi]
		switch b.Term.Kind {
		case TermJump:
			size += 5
		case TermCond:
			size += 14
		case TermReturn:
			size += 10
		case TermStop:
			size++
		}
		size += 4
		for _, phi := range b.Phis {
			size += 6 + 8*uint64(len(phi.Incoming))
		}
		size += 4
		for oi := range b.Ops {
			op := &b.Ops[oi]
			size += 5
			switch op.Kind {
			case OpBindTuple:
				size += 10 + 4*uint64(len(op.Companions))
			case OpBindErr, OpObserveErr, OpUseCompanion, OpBoxConcreteErr, OpMarkWritable:
				size += 4
			case OpAssignSlot:
				size += 10
			case OpCallConcretePtr, OpShadowErr:
				size += 8
			case OpUnhandledClose:
				size += 5
			}
		}
	}
	return size
}

// encodePackage is EncodePackage with an explicit frame-size bound (tests pass a small one). Each
// function is sized before it is appended and the buffer is grown to exactly what it needs, never
// past the bound, so an oversized package is refused before the frame passes the bound and the
// encoder never allocates beyond it.
func encodePackage(funcs []FuncIR, maxFrameBytes int) ([]byte, error) {
	if len(funcs) > MaxFuncs {
		return nil, fmt.Errorf("gusseterr: func count %d exceeds %d", len(funcs), MaxFuncs)
	}
	if maxFrameBytes < 8 {
		return nil, fmt.Errorf("%w: frame bytes 8 over the bound of %d", ErrLimitExceeded, maxFrameBytes)
	}
	buf := make([]byte, 0, min(64+len(funcs)*128, maxFrameBytes))
	buf = append(buf, reqMagic[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(funcs)))

	for fi, fn := range funcs {
		if len(fn.SlotFlags) > MaxSlots {
			return nil, fmt.Errorf("gusseterr: func %d slots %d exceeds %d", fi, len(fn.SlotFlags), MaxSlots)
		}
		if len(fn.Tuples) > MaxTuples {
			return nil, fmt.Errorf("gusseterr: func %d tuples %d exceeds %d", fi, len(fn.Tuples), MaxTuples)
		}
		if len(fn.Blocks) > MaxBlocks {
			return nil, fmt.Errorf("gusseterr: func %d blocks %d exceeds %d", fi, len(fn.Blocks), MaxBlocks)
		}
		if err := checkStateCells(uint64(len(fn.Blocks)), uint64(len(fn.SlotFlags))); err != nil {
			return nil, fmt.Errorf("func %d: %w", fi, err)
		}
		size := funcWireSize(&fn)
		if uint64(len(buf))+size > uint64(maxFrameBytes) {
			return nil, fmt.Errorf("%w: frame bytes %d with func %d over the bound of %d", ErrLimitExceeded, uint64(len(buf))+size, fi, maxFrameBytes)
		}
		if need := len(buf) + int(size); need > cap(buf) {
			grown := make([]byte, len(buf), min(max(need, 2*cap(buf)), maxFrameBytes))
			copy(grown, buf)
			buf = grown
		}
		buf = append(buf, fn.Flags)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(fn.SlotFlags)))
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(fn.Tuples)))
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(fn.Blocks)))
		buf = append(buf, fn.SlotFlags...)

		for _, t := range fn.Tuples {
			buf = binary.LittleEndian.AppendUint32(buf, t.ErrSlot)
			buf = binary.LittleEndian.AppendUint32(buf, t.CallSiteID)
			buf = append(buf, t.Flags)
		}

		for bi, b := range fn.Blocks {
			switch b.Term.Kind {
			case TermJump:
				buf = append(buf, TermJump)
				buf = binary.LittleEndian.AppendUint32(buf, b.Term.Target)
			case TermCond:
				buf = append(buf, TermCond)
				buf = binary.LittleEndian.AppendUint32(buf, b.Term.Target)
				buf = binary.LittleEndian.AppendUint32(buf, b.Term.FalseBB)
				buf = append(buf, b.Term.CondKind)
				buf = binary.LittleEndian.AppendUint32(buf, b.Term.CondSlot)
			case TermReturn:
				buf = append(buf, TermReturn)
				buf = binary.LittleEndian.AppendUint32(buf, b.Term.SiteID)
				buf = binary.LittleEndian.AppendUint32(buf, b.Term.RetSlot)
				buf = append(buf, b.Term.RetFlags)
			case TermStop:
				buf = append(buf, TermStop)
			default:
				return nil, fmt.Errorf("gusseterr: func %d block %d invalid terminator %d", fi, bi, b.Term.Kind)
			}

			if len(b.Phis) > MaxPhisPerBlock {
				return nil, fmt.Errorf("gusseterr: func %d block %d phis %d exceeds %d", fi, bi, len(b.Phis), MaxPhisPerBlock)
			}
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(b.Phis)))
			for _, phi := range b.Phis {
				if len(phi.Incoming) > MaxPhiArity {
					return nil, fmt.Errorf("gusseterr: func %d block %d phi arity %d exceeds %d", fi, bi, len(phi.Incoming), MaxPhiArity)
				}
				buf = binary.LittleEndian.AppendUint32(buf, phi.DstSlot)
				buf = binary.LittleEndian.AppendUint16(buf, uint16(len(phi.Incoming)))
				for _, inc := range phi.Incoming {
					buf = binary.LittleEndian.AppendUint32(buf, inc.PredBB)
					buf = binary.LittleEndian.AppendUint32(buf, inc.SrcSlot)
				}
			}

			if len(b.Ops) > MaxOpsPerBlock {
				return nil, fmt.Errorf("gusseterr: func %d block %d ops %d exceeds %d", fi, bi, len(b.Ops), MaxOpsPerBlock)
			}
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(b.Ops)))
			for oi, op := range b.Ops {
				buf = append(buf, op.Kind)
				buf = binary.LittleEndian.AppendUint32(buf, op.SiteID)
				switch op.Kind {
				case OpUnhandledErr:
					// No extra payload.
				case OpBindTuple:
					if len(op.Companions) > MaxCompanions {
						return nil, fmt.Errorf("gusseterr: func %d block %d op %d companions %d exceeds %d", fi, bi, oi, len(op.Companions), MaxCompanions)
					}
					buf = binary.LittleEndian.AppendUint32(buf, op.TupleID)
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotA)
					buf = binary.LittleEndian.AppendUint16(buf, uint16(len(op.Companions)))
					for _, cs := range op.Companions {
						buf = binary.LittleEndian.AppendUint32(buf, cs)
					}
				case OpBindErr, OpObserveErr, OpUseCompanion, OpBoxConcreteErr, OpMarkWritable:
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotA)
				case OpAssignSlot:
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotA)
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotB)
					buf = append(buf, op.NilHint, op.WritableHint)
				case OpCallConcretePtr, OpShadowErr:
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotA)
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotB)
				case OpUnhandledClose:
					buf = binary.LittleEndian.AppendUint32(buf, op.SlotA)
					if op.IsDefer {
						buf = append(buf, 1)
					} else {
						buf = append(buf, 0)
					}
				default:
					return nil, fmt.Errorf("gusseterr: func %d block %d op %d invalid kind %d", fi, bi, oi, op.Kind)
				}
			}
		}
	}
	return buf, nil
}

// encodedResultLen is the length of the GSR1 frame EncodeResult produces for res.
func encodedResultLen(res PackageResult) int {
	return 12 + len(res.MayReturnNil) + len(res.Findings)*12
}

// EncodeResult serializes res into a GSR1 binary response frame.
func EncodeResult(res PackageResult) []byte {
	buf := make([]byte, 0, encodedResultLen(res))
	buf = append(buf, respMagic[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(res.MayReturnNil)))
	for _, b := range res.MayReturnNil {
		if b {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(res.Findings)))
	for _, f := range res.Findings {
		buf = binary.LittleEndian.AppendUint32(buf, f.FuncIdx)
		buf = binary.LittleEndian.AppendUint32(buf, f.SiteID)
		buf = append(buf, f.RuleCode, f.SubKind, 0, 0)
	}
	return buf
}

// DecodeResult parses a GSR1 binary response frame returned by the solver.
func DecodeResult(frame []byte) (PackageResult, error) {
	if len(frame) < 12 {
		return PackageResult{}, errors.New("gusseterr: truncated GSR1 response header")
	}
	if !bytes.Equal(frame[:4], respMagic[:]) {
		return PackageResult{}, fmt.Errorf("gusseterr: invalid GSR1 response magic %q", frame[:4])
	}
	nFuncs := int(binary.LittleEndian.Uint32(frame[4:8]))
	if nFuncs > MaxFuncs || 8+nFuncs+4 > len(frame) {
		return PackageResult{}, errors.New("gusseterr: invalid GSR1 func summary section")
	}
	mayNil := make([]bool, nFuncs)
	for i := range nFuncs {
		switch frame[8+i] {
		case 0:
			mayNil[i] = false
		case 1:
			mayNil[i] = true
		default:
			return PackageResult{}, fmt.Errorf("gusseterr: invalid may_return_nil byte %d", frame[8+i])
		}
	}
	off := 8 + nFuncs
	nFindings := int(binary.LittleEndian.Uint32(frame[off : off+4]))
	off += 4
	if nFindings < 0 || nFindings > (len(frame)-off)/12 || off+nFindings*12 != len(frame) {
		return PackageResult{}, errors.New("gusseterr: invalid GSR1 findings section length")
	}
	findings := make([]Finding, nFindings)
	for i := range nFindings {
		base := off + i*12
		findings[i] = Finding{
			FuncIdx:  binary.LittleEndian.Uint32(frame[base : base+4]),
			SiteID:   binary.LittleEndian.Uint32(frame[base+4 : base+8]),
			RuleCode: frame[base+8],
			SubKind:  frame[base+9],
		}
	}
	return PackageResult{MayReturnNil: mayNil, Findings: findings}, nil
}

// engineKinds maps the code the Rust engine puts in front of its message (SolveError::code,
// "<code>: <text>") to the sentinel a caller matches with errors.Is. A nil kind is a refusal with
// no sentinel (a malformed frame).
var engineKinds = map[string]error{
	"work_budget_exceeded":      ErrWorkBudgetExceeded,
	"decoded_bytes_exceeded":    ErrDecodedBytesExceeded,
	"callgraph_budget_exceeded": ErrCallgraphBudgetExceeded,
	"iteration_budget_exceeded": ErrIterationBudgetExceeded,
	"response_too_large":        ErrResponseTooLarge,
	"limit_exceeded":            ErrLimitExceeded,
	"bad_magic":                 nil,
	"truncated":                 nil,
	"trailing_bytes":            nil,
	"invalid_enum":              nil,
	"index_out_of_bounds":       nil,
	"allocation_failed":         nil,
}

// engineRefusal turns the message a Rust engine error carried (cause is the transport error that
// carried it) into the error both engines return: the text after the code, which is what the
// oracle prints for the same refusal, matching the code's sentinel and the transport error. A
// message with no known code is not an engine refusal and returns nil.
func engineRefusal(msg string, cause error) error {
	code, text, ok := strings.Cut(msg, ": ")
	if !ok {
		return nil
	}
	kind, known := engineKinds[code]
	if !known {
		return nil
	}
	if kind == nil {
		return &engineText{msg: text, cause: cause}
	}
	return &refusal{kind: kind, msg: text, cause: cause}
}

type engineText struct {
	msg   string
	cause error
}

func (e *engineText) Error() string { return e.msg }
func (e *engineText) Unwrap() error { return e.cause }
