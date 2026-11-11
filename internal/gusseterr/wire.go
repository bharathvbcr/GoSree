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
)

const NoneU32 uint32 = ^uint32(0)

// Limits matching rust/gosree-errsolve/src/lib.rs.
const (
	MaxFuncs             = 16_384
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
)

// checkStateCells enforces MaxStateCells for one function; every encoder and decoder uses it so
// both engines apply the identical rule.
func checkStateCells(nBlocks, nSlots uint64) error {
	if nBlocks*nSlots > MaxStateCells {
		return fmt.Errorf("gusseterr: GSE1 limit exceeded: state cells (blocks x slots) %d x %d exceeds %d", nBlocks, nSlots, MaxStateCells)
	}
	return nil
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
	if len(funcs) > MaxFuncs {
		return nil, fmt.Errorf("gusseterr: func count %d exceeds %d", len(funcs), MaxFuncs)
	}
	buf := make([]byte, 0, 64+len(funcs)*128)
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

// EncodeResult serializes res into a GSR1 binary response frame.
func EncodeResult(res PackageResult) []byte {
	buf := make([]byte, 0, 12+len(res.MayReturnNil)+len(res.Findings)*12)
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
