package gusseterr

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"slices"
)

// addSat and mulSat saturate at the largest uint64 instead of wrapping, so the phase-1 work
// budget can never be defeated by overflow. They mirror saturating_add and saturating_mul.
func addSat(a, b uint64) uint64 {
	s, carry := bits.Add64(a, b, 0)
	if carry != 0 {
		return ^uint64(0)
	}
	return s
}

func mulSat(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return ^uint64(0)
	}
	return lo
}

// DecodePackage decodes and validates a GSE1 binary request frame (matching
// gosree_errsolve::decode_package).
func DecodePackage(frame []byte) ([]FuncIR, error) {
	return decodePackage(frame, MaxDecodedBytes)
}

// decodeBudget charges decoded bytes against the decoded-size bound before the structure they
// pay for is allocated, so a hostile count is refused before it is acted on. Mirrors
// DecodeBudget in rust/gosree-errsolve/src/lib.rs.
type decodeBudget struct {
	used, max uint64
}

func (b *decodeBudget) charge(count, unit uint64) error {
	hi, lo := bits.Mul64(count, unit)
	total, carry := bits.Add64(lo, b.used, 0)
	if hi != 0 || carry != 0 || total > b.max {
		return limitErr("decoded_bytes")
	}
	b.used = total
	return nil
}

// decodePackage is DecodePackage with an explicit decoded-size bound (tests pass a small one);
// it mirrors decode_package_bounded in rust/gosree-errsolve/src/lib.rs.
func decodePackage(frame []byte, maxDecodedBytes uint64) ([]FuncIR, error) {
	budget := decodeBudget{max: maxDecodedBytes}
	if len(frame) > MaxFrameBytes {
		return nil, limitErr("frame_bytes")
	}
	if len(frame) < 8 {
		return nil, errors.New("truncated GSE1 frame")
	}
	if !bytes.Equal(frame[:4], reqMagic[:]) {
		return nil, errors.New("invalid GSE1 frame magic")
	}
	pos := 4
	readU8 := func() (uint8, error) {
		if pos+1 > len(frame) {
			return 0, errors.New("truncated GSE1 frame")
		}
		v := frame[pos]
		pos++
		return v, nil
	}
	readU16 := func() (uint16, error) {
		if pos+2 > len(frame) {
			return 0, errors.New("truncated GSE1 frame")
		}
		v := binary.LittleEndian.Uint16(frame[pos : pos+2])
		pos += 2
		return v, nil
	}
	readU32 := func() (uint32, error) {
		if pos+4 > len(frame) {
			return 0, errors.New("truncated GSE1 frame")
		}
		v := binary.LittleEndian.Uint32(frame[pos : pos+4])
		pos += 4
		return v, nil
	}

	numFuncsU32, err := readU32()
	if err != nil {
		return nil, err
	}
	if numFuncsU32 > MaxFuncs {
		return nil, limitErr("num_funcs")
	}
	numFuncs := int(numFuncsU32)
	// need fails with a truncation error unless count records of at least unit wire bytes each
	// could still be in the frame: collections are allocated at exactly their claimed count, so a
	// count the remaining bytes cannot back is refused before anything is allocated.
	need := func(count int, unit int) error {
		if len(frame)-pos < count*unit {
			return errors.New("truncated GSE1 frame")
		}
		return nil
	}
	if err := need(numFuncs, minFuncWireBytes); err != nil {
		return nil, err
	}
	funcs := make([]FuncIR, 0, numFuncs)

	checkSlot := func(slot, nSlots uint32, allowNone bool) error {
		if allowNone && slot == NoneU32 {
			return nil
		}
		if slot >= nSlots {
			return fmt.Errorf("slot index %d out of bounds (len %d)", slot, nSlots)
		}
		return nil
	}
	checkBlock := func(bb, nBlocks uint32) error {
		if bb >= nBlocks {
			return fmt.Errorf("block index %d out of bounds (len %d)", bb, nBlocks)
		}
		return nil
	}

	for range numFuncs {
		flags, err := readU8()
		if err != nil {
			return nil, err
		}
		numSlots, err := readU32()
		if err != nil {
			return nil, err
		}
		if numSlots > MaxSlots {
			return nil, limitErr("num_slots")
		}
		numTuples, err := readU32()
		if err != nil {
			return nil, err
		}
		if numTuples > MaxTuples {
			return nil, limitErr("num_tuples")
		}
		numBlocks, err := readU32()
		if err != nil {
			return nil, err
		}
		if numBlocks > MaxBlocks {
			return nil, limitErr("num_blocks")
		}
		if err := checkStateCells(uint64(numBlocks), uint64(numSlots)); err != nil {
			return nil, err
		}
		for _, c := range [][2]uint64{{1, DecodedFuncBytes}, {uint64(numSlots), 1}, {uint64(numTuples), DecodedTupleBytes}} {
			if err := budget.charge(c[0], c[1]); err != nil {
				return nil, err
			}
		}

		if pos+int(numSlots) > len(frame) {
			return nil, errors.New("truncated GSE1 frame")
		}
		slotFlags := append([]uint8(nil), frame[pos:pos+int(numSlots)]...)
		pos += int(numSlots)

		if err := need(int(numTuples), tupleWireBytes); err != nil {
			return nil, err
		}
		tuples := make([]TupleMeta, 0, int(numTuples))
		for range int(numTuples) {
			errSlot, err := readU32()
			if err != nil {
				return nil, err
			}
			if err := checkSlot(errSlot, numSlots, true); err != nil {
				return nil, err
			}
			callSite, err := readU32()
			if err != nil {
				return nil, err
			}
			tf, err := readU8()
			if err != nil {
				return nil, err
			}
			tuples = append(tuples, TupleMeta{ErrSlot: errSlot, CallSiteID: callSite, Flags: tf})
		}

		if err := need(int(numBlocks), minBlockWireBytes); err != nil {
			return nil, err
		}
		blocks := make([]BlockIR, 0, int(numBlocks))
		for range int(numBlocks) {
			if err := budget.charge(1, DecodedBlockBytes); err != nil {
				return nil, err
			}
			tk, err := readU8()
			if err != nil {
				return nil, err
			}
			var term Terminator
			term.Kind = tk
			switch tk {
			case TermJump:
				target, err := readU32()
				if err != nil {
					return nil, err
				}
				if err := checkBlock(target, numBlocks); err != nil {
					return nil, err
				}
				term.Target = target
			case TermCond:
				trueBB, err := readU32()
				if err != nil {
					return nil, err
				}
				falseBB, err := readU32()
				if err != nil {
					return nil, err
				}
				if err := checkBlock(trueBB, numBlocks); err != nil {
					return nil, err
				}
				if err := checkBlock(falseBB, numBlocks); err != nil {
					return nil, err
				}
				ck, err := readU8()
				if err != nil {
					return nil, err
				}
				if ck > CondGtZero {
					return nil, fmt.Errorf("invalid cond_kind tag: %d", ck)
				}
				cs, err := readU32()
				if err != nil {
					return nil, err
				}
				if err := checkSlot(cs, numSlots, ck == CondOpaque); err != nil {
					return nil, err
				}
				term.Target = trueBB
				term.FalseBB = falseBB
				term.CondKind = ck
				term.CondSlot = cs
			case TermReturn:
				sid, err := readU32()
				if err != nil {
					return nil, err
				}
				rs, err := readU32()
				if err != nil {
					return nil, err
				}
				if err := checkSlot(rs, numSlots, true); err != nil {
					return nil, err
				}
				rf, err := readU8()
				if err != nil {
					return nil, err
				}
				term.SiteID = sid
				term.RetSlot = rs
				term.RetFlags = rf
			case TermStop:
			default:
				return nil, fmt.Errorf("invalid term_kind tag: %d", tk)
			}

			nPhis, err := readU32()
			if err != nil {
				return nil, err
			}
			if nPhis > MaxPhisPerBlock {
				return nil, limitErr("num_phis")
			}
			if err := budget.charge(uint64(nPhis), DecodedPhiBytes); err != nil {
				return nil, err
			}
			if err := need(int(nPhis), minPhiWireBytes); err != nil {
				return nil, err
			}
			phis := make([]PhiIR, 0, int(nPhis))
			for range int(nPhis) {
				dst, err := readU32()
				if err != nil {
					return nil, err
				}
				if err := checkSlot(dst, numSlots, false); err != nil {
					return nil, err
				}
				arity, err := readU16()
				if err != nil {
					return nil, err
				}
				if arity > MaxPhiArity {
					return nil, limitErr("phi_arity")
				}
				if err := budget.charge(uint64(arity), DecodedPhiEdgeBytes); err != nil {
					return nil, err
				}
				if err := need(int(arity), phiEdgeWireBytes); err != nil {
					return nil, err
				}
				inc := make([]PhiEdge, 0, int(arity))
				for range int(arity) {
					pred, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkBlock(pred, numBlocks); err != nil {
						return nil, err
					}
					src, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(src, numSlots, false); err != nil {
						return nil, err
					}
					inc = append(inc, PhiEdge{PredBB: pred, SrcSlot: src})
				}
				phis = append(phis, PhiIR{DstSlot: dst, Incoming: inc})
			}

			nOps, err := readU32()
			if err != nil {
				return nil, err
			}
			if nOps > MaxOpsPerBlock {
				return nil, limitErr("num_ops")
			}
			if err := budget.charge(uint64(nOps), DecodedOpBytes); err != nil {
				return nil, err
			}
			if err := need(int(nOps), minOpWireBytes); err != nil {
				return nil, err
			}
			ops := make([]OpIR, 0, int(nOps))
			for range int(nOps) {
				okind, err := readU8()
				if err != nil {
					return nil, err
				}
				sid, err := readU32()
				if err != nil {
					return nil, err
				}
				op := OpIR{Kind: okind, SiteID: sid}
				switch okind {
				case OpUnhandledErr:
				case OpBindTuple:
					tid, err := readU32()
					if err != nil {
						return nil, err
					}
					if tid >= numTuples {
						return nil, fmt.Errorf("tuple index %d out of bounds", tid)
					}
					es, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(es, numSlots, true); err != nil {
						return nil, err
					}
					cc, err := readU16()
					if err != nil {
						return nil, err
					}
					if cc > MaxCompanions {
						return nil, limitErr("companions")
					}
					if err := budget.charge(uint64(cc), DecodedCompanionBytes); err != nil {
						return nil, err
					}
					if err := need(int(cc), companionWireBytes); err != nil {
						return nil, err
					}
					comps := make([]uint32, 0, int(cc))
					for range int(cc) {
						cs, err := readU32()
						if err != nil {
							return nil, err
						}
						if err := checkSlot(cs, numSlots, false); err != nil {
							return nil, err
						}
						comps = append(comps, cs)
					}
					op.TupleID = tid
					op.SlotA = es
					op.Companions = comps
				case OpBindErr, OpObserveErr, OpUseCompanion, OpBoxConcreteErr, OpMarkWritable:
					s, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(s, numSlots, false); err != nil {
						return nil, err
					}
					op.SlotA = s
				case OpAssignSlot:
					dst, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(dst, numSlots, false); err != nil {
						return nil, err
					}
					src, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(src, numSlots, true); err != nil {
						return nil, err
					}
					nh, err := readU8()
					if err != nil {
						return nil, err
					}
					if nh > AssignNilMustNil {
						return nil, fmt.Errorf("invalid nil_hint tag: %d", nh)
					}
					wh, err := readU8()
					if err != nil {
						return nil, err
					}
					if wh > 1 {
						return nil, fmt.Errorf("invalid writable_hint tag: %d", wh)
					}
					op.SlotA = dst
					op.SlotB = src
					op.NilHint = nh
					op.WritableHint = wh
				case OpCallConcretePtr:
					dst, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(dst, numSlots, false); err != nil {
						return nil, err
					}
					callee, err := readU32()
					if err != nil {
						return nil, err
					}
					if callee >= numFuncsU32 {
						return nil, fmt.Errorf("callee_func index %d out of bounds", callee)
					}
					op.SlotA = dst
					op.SlotB = callee
				case OpShadowErr:
					inS, err := readU32()
					if err != nil {
						return nil, err
					}
					outS, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(inS, numSlots, false); err != nil {
						return nil, err
					}
					if err := checkSlot(outS, numSlots, false); err != nil {
						return nil, err
					}
					op.SlotA = inS
					op.SlotB = outS
				case OpUnhandledClose:
					rs, err := readU32()
					if err != nil {
						return nil, err
					}
					if err := checkSlot(rs, numSlots, false); err != nil {
						return nil, err
					}
					df, err := readU8()
					if err != nil {
						return nil, err
					}
					if df > 1 {
						return nil, fmt.Errorf("invalid is_defer tag: %d", df)
					}
					op.SlotA = rs
					op.IsDefer = df != 0
				default:
					return nil, fmt.Errorf("invalid op_kind tag: %d", okind)
				}
				ops = append(ops, op)
			}
			blocks = append(blocks, BlockIR{Phis: phis, Ops: ops, Term: term})
		}
		funcs = append(funcs, FuncIR{Flags: flags, SlotFlags: slotFlags, Tuples: tuples, Blocks: blocks})
	}

	if pos != len(frame) {
		return nil, fmt.Errorf("GSE1 frame has %d trailing bytes", len(frame)-pos)
	}
	return funcs, nil
}

// SolveFrameOracle decodes frame, runs the pure-Go reference oracle, and
// encodes the resulting GSR1 response bytes.
func SolveFrameOracle(ctx context.Context, frame []byte) ([]byte, error) {
	return solveFrameOracle(ctx, frame, MaxResponseBytes)
}

// solveFrameOracle is SolveFrameOracle with an explicit response-size bound (tests pass a small
// one). The response length is known from the result before anything is encoded, so an
// over-large one is refused without allocating it, with the text the native path's refusal has
// (gusset: "output N bytes exceeds maximum M bytes").
func solveFrameOracle(ctx context.Context, frame []byte, maxResponseBytes int) ([]byte, error) {
	funcs, err := DecodePackage(frame)
	if err != nil {
		return nil, err
	}
	res, err := SolveOracle(ctx, funcs)
	if err != nil {
		return nil, err
	}
	if n := encodedResultLen(res); n > maxResponseBytes {
		return nil, refuse(ErrResponseTooLarge, "output %d bytes exceeds maximum %d bytes", n, maxResponseBytes)
	}
	return EncodeResult(res), nil
}

// Abstract lattice types matching rust/gosree-errsolve/src/lib.rs.

type originKind uint8

const (
	originBot originKind = iota
	originSafe
	originSingle
	originUncheckedMix
	originBlanked
)

type originVal struct {
	kind  originKind
	tuple uint32
}

func (a originVal) join(b originVal) originVal {
	if a == b {
		return a
	}
	if a.kind == originBot {
		return b
	}
	if b.kind == originBot {
		return a
	}
	if a.kind == originBlanked && b.kind == originBlanked {
		return originVal{originBlanked, min(a.tuple, b.tuple)}
	}
	if a.kind == originBlanked {
		return a
	}
	if b.kind == originBlanked {
		return b
	}
	if a.kind == originUncheckedMix && b.kind == originUncheckedMix {
		return originVal{originUncheckedMix, min(a.tuple, b.tuple)}
	}
	if a.kind == originUncheckedMix && b.kind == originSingle {
		return originVal{originUncheckedMix, min(a.tuple, b.tuple)}
	}
	if a.kind == originSingle && b.kind == originUncheckedMix {
		return originVal{originUncheckedMix, min(a.tuple, b.tuple)}
	}
	if a.kind == originUncheckedMix && b.kind == originSafe {
		return a
	}
	if a.kind == originSafe && b.kind == originUncheckedMix {
		return b
	}
	if a.kind == originSingle && b.kind == originSingle {
		return originVal{originUncheckedMix, min(a.tuple, b.tuple)}
	}
	if a.kind == originSingle && b.kind == originSafe {
		return originVal{originUncheckedMix, a.tuple}
	}
	if a.kind == originSafe && b.kind == originSingle {
		return originVal{originUncheckedMix, b.tuple}
	}
	return originVal{kind: originSafe}
}

type nilKind uint8

const (
	nilBot nilKind = iota
	nilMustNil
	nilNonNilChecked
	nilNonNil
	nilMayNil
)

func (a nilKind) join(b nilKind) nilKind {
	if a == b {
		return a
	}
	if a == nilBot {
		return b
	}
	if b == nilBot {
		return a
	}
	if (a == nilNonNilChecked && b == nilNonNil) || (a == nilNonNil && b == nilNonNilChecked) {
		return nilNonNil
	}
	return nilMayNil
}

func (a nilKind) relaxChecked() nilKind {
	if a == nilNonNilChecked {
		return nilNonNil
	}
	return a
}

type errVarKind uint8

const (
	errVarBot errVarKind = iota
	errVarClean
	errVarUnread
)

type errVarVal struct {
	kind errVarKind
	site uint32
}

func (a errVarVal) join(b errVarVal) errVarVal {
	if a.kind == errVarBot {
		return b
	}
	if b.kind == errVarBot {
		return a
	}
	if a.kind == errVarClean && b.kind == errVarClean {
		return errVarVal{kind: errVarClean}
	}
	if a.kind == errVarUnread && b.kind == errVarUnread {
		return errVarVal{errVarUnread, min(a.site, b.site)}
	}
	if a.kind == errVarUnread {
		return a
	}
	return b
}

type writableKind uint8

const (
	writableBot writableKind = iota
	writableReadOnly
	writableWritable
)

func (a writableKind) join(b writableKind) writableKind {
	if a == writableBot {
		return b
	}
	if b == writableBot {
		return a
	}
	if a == writableWritable || b == writableWritable {
		return writableWritable
	}
	return writableReadOnly
}

type errLinkKind uint8

const (
	errLinkBot errLinkKind = iota
	errLinkTuple
	errLinkNone
)

type errLinkVal struct {
	kind  errLinkKind
	tuple uint32
}

func (a errLinkVal) join(b errLinkVal) errLinkVal {
	if a == b {
		return a
	}
	if a.kind == errLinkBot {
		return b
	}
	if b.kind == errLinkBot {
		return a
	}
	return errLinkVal{kind: errLinkNone}
}

type absState struct {
	reachable bool
	origin    []originVal
	nilv      []nilKind
	errVar    []errVarVal
	writable  []writableKind
	errLink   []errLinkVal
}

func bottomState(n int) absState {
	return absState{
		origin:   make([]originVal, n),
		nilv:     make([]nilKind, n),
		errVar:   make([]errVarVal, n),
		writable: make([]writableKind, n),
		errLink:  make([]errLinkVal, n),
	}
}

func entryState(slotFlags []uint8) absState {
	n := len(slotFlags)
	st := absState{
		reachable: true,
		origin:    make([]originVal, n),
		nilv:      make([]nilKind, n),
		errVar:    make([]errVarVal, n),
		writable:  make([]writableKind, n),
		errLink:   make([]errLinkVal, n),
	}
	for i, sf := range slotFlags {
		st.origin[i] = originVal{kind: originSafe}
		st.nilv[i] = nilMayNil
		st.errVar[i] = errVarVal{kind: errVarClean}
		if sf&SlotInitWritable != 0 {
			st.writable[i] = writableWritable
		} else {
			st.writable[i] = writableReadOnly
		}
		st.errLink[i] = errLinkVal{kind: errLinkNone}
	}
	return st
}

func (st absState) clone() absState {
	return absState{
		reachable: st.reachable,
		origin:    slices.Clone(st.origin),
		nilv:      slices.Clone(st.nilv),
		errVar:    slices.Clone(st.errVar),
		writable:  slices.Clone(st.writable),
		errLink:   slices.Clone(st.errLink),
	}
}

func (st *absState) joinFrom(inc *absState) bool {
	if !inc.reachable {
		return false
	}
	changed := false
	if !st.reachable {
		st.reachable = true
		changed = true
	}
	for i := range st.origin {
		if o := st.origin[i].join(inc.origin[i]); o != st.origin[i] {
			st.origin[i] = o
			changed = true
		}
		if nv := st.nilv[i].join(inc.nilv[i]); nv != st.nilv[i] {
			st.nilv[i] = nv
			changed = true
		}
		if ev := st.errVar[i].join(inc.errVar[i]); ev != st.errVar[i] {
			st.errVar[i] = ev
			changed = true
		}
		if w := st.writable[i].join(inc.writable[i]); w != st.writable[i] {
			st.writable[i] = w
			changed = true
		}
		if el := st.errLink[i].join(inc.errLink[i]); el != st.errLink[i] {
			st.errLink[i] = el
			changed = true
		}
	}
	return changed
}

func (st *absState) relaxAllCheckedNils() {
	for i := range st.nilv {
		st.nilv[i] = st.nilv[i].relaxChecked()
	}
}

func (st *absState) promoteTuple(tid uint32) {
	for i := range st.origin {
		if st.origin[i] == (originVal{originSingle, tid}) {
			st.origin[i] = originVal{kind: originSafe}
		}
	}
}

type edgePair struct {
	target uint32
	state  absState
}

type deferCloseRec struct {
	bb            uint32
	siteID        uint32
	resSlot       uint32
	writableAtReg bool
}

// SolveOracle runs the pure-Go reference abstract-interpretation solver,
// matching rust/gosree-errsolve bit-for-bit.
func SolveOracle(ctx context.Context, funcs []FuncIR) (PackageResult, error) {
	// Summaries only flip false -> true, so at most len(funcs) sweeps change one and one more
	// confirms: len(funcs)+1 is a proven bound and the cap in solveOracle is an invariant check.
	return solveOracle(ctx, funcs, len(funcs)+1)
}

// maxCanonClass mirrors MAX_CANON_CLASS in rust/gosree-errsolve/src/lib.rs.
const maxCanonClass = 64

// canonicalTuples mirrors canonical_tuples in rust/gosree-errsolve/src/lib.rs: it maps every
// tuple to the smallest id of its logical call result. Tuples merge only when their error
// slots are connected through phis, their flags agree, and their bind blocks are pairwise
// unreachable from one another, so one check after a join promotes the companions of whichever
// call ran, but two binds on one path (`a, err := f(); if retry { _, err = g() }`) stay
// separate. It returns nil when nothing changes.
func canonicalTuples(ctx context.Context, fn *FuncIR) ([]uint32, error) {
	nTuples := len(fn.Tuples)
	if nTuples < 2 {
		return nil, nil
	}
	anyPhi := false
	for i := range fn.Blocks {
		if len(fn.Blocks[i].Phis) > 0 {
			anyPhi = true
			break
		}
	}
	if !anyPhi {
		return nil, nil
	}
	parent := make([]uint32, len(fn.SlotFlags))
	for i := range parent {
		parent[i] = uint32(i)
	}
	find := func(x uint32) uint32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for i := range fn.Blocks {
		for _, phi := range fn.Blocks[i].Phis {
			for _, inc := range phi.Incoming {
				a, b := find(phi.DstSlot), find(inc.SrcSlot)
				if a != b {
					parent[max(a, b)] = min(a, b)
				}
			}
		}
	}
	bindBlocks := make([][]uint32, nTuples)
	for bb := range fn.Blocks {
		for oi := range fn.Blocks[bb].Ops {
			op := &fn.Blocks[bb].Ops[oi]
			if op.Kind != OpBindTuple {
				continue
			}
			v := bindBlocks[op.TupleID]
			if n := len(v); n == 0 || v[n-1] != uint32(bb) {
				bindBlocks[op.TupleID] = append(v, uint32(bb))
			}
		}
	}
	type classKey struct {
		root  uint32
		flags uint8
	}
	classes := make(map[classKey][]uint32)
	for t, meta := range fn.Tuples {
		if meta.ErrSlot != NoneU32 && len(bindBlocks[t]) > 0 {
			key := classKey{find(meta.ErrSlot), meta.Flags}
			classes[key] = append(classes[key], uint32(t))
		}
	}
	canon := make([]uint32, nTuples)
	for t := range canon {
		canon[t] = uint32(t)
	}
	changed := false
	for _, members := range classes {
		if len(members) < 2 || len(members) > maxCanonClass {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reach := make(map[uint32][]bool)
		for _, t := range members {
			for _, bb := range bindBlocks[t] {
				if _, ok := reach[bb]; !ok {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					reach[bb] = reachableBlocksFromOracle(fn, bb)
				}
			}
		}
		exclusive := func(t, u uint32) bool {
			for _, bt := range bindBlocks[t] {
				for _, bu := range bindBlocks[u] {
					if bt == bu || reach[bt][bu] || reach[bu][bt] {
						return false
					}
				}
			}
			return true
		}
		var groups [][]uint32
		for _, t := range members {
			placed := false
			for gi, g := range groups {
				if slices.ContainsFunc(g, func(u uint32) bool { return !exclusive(t, u) }) {
					continue
				}
				canon[t] = g[0]
				changed = true
				groups[gi] = append(g, t)
				placed = true
				break
			}
			if !placed {
				groups = append(groups, []uint32{t})
			}
		}
	}
	if !changed {
		return nil, nil
	}
	return canon, nil
}

// withCanonicalTuples returns fn with every BindTuple rewritten to its canonical tuple id,
// copying only the blocks it changes; fn itself is never modified.
func withCanonicalTuples(ctx context.Context, fn FuncIR) (FuncIR, error) {
	canon, err := canonicalTuples(ctx, &fn)
	if err != nil || canon == nil {
		return fn, err
	}
	blocks := slices.Clone(fn.Blocks)
	for bi := range blocks {
		ops := blocks[bi].Ops
		cloned := false
		for oi := range ops {
			if ops[oi].Kind != OpBindTuple || canon[ops[oi].TupleID] == ops[oi].TupleID {
				continue
			}
			if !cloned {
				ops = slices.Clone(ops)
				blocks[bi].Ops = ops
				cloned = true
			}
			ops[oi].TupleID = canon[ops[oi].TupleID]
		}
	}
	fn.Blocks = blocks
	return fn, nil
}

// u32MinHeap is a binary min-heap of function indices: the phase-1 sweep queues of solveOracle.
type u32MinHeap []uint32

func (h *u32MinHeap) push(v uint32) {
	*h = append(*h, v)
	q := *h
	for i := len(q) - 1; i > 0; {
		p := (i - 1) / 2
		if q[p] <= q[i] {
			break
		}
		q[p], q[i] = q[i], q[p]
		i = p
	}
}

// pop removes and returns the smallest index; the heap must not be empty.
func (h *u32MinHeap) pop() uint32 {
	q := *h
	top := q[0]
	last := len(q) - 1
	q[0] = q[last]
	q = q[:last]
	for i := 0; ; {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(q) && q[l] < q[m] {
			m = l
		}
		if r < len(q) && q[r] < q[m] {
			m = r
		}
		if m == i {
			break
		}
		q[i], q[m] = q[m], q[i]
		i = m
	}
	*h = q
	return top
}

// phase1Stats counts the phase-1 work of solveOracleLimited; tests read it. It is the
// load-independent measure of phase-1 cost. Mirrors Phase1Stats in rust/gosree-errsolve/src/lib.rs.
type phase1Stats struct {
	solves uint64 // solveFuncOracle calls made by phase 1
	work   uint64 // work units of those solves (ops plus blocks x max(slots, 1) of each function solved, per solve)
	sccs   uint64 // strongly connected components of the call graph that phase 1 visited
	sweeps uint64 // sweeps run over all components, each component's confirming sweep included
}

// sccPollSteps mirrors SCC_POLL_STEPS in rust/gosree-errsolve/src/lib.rs: the component search
// polls the context once per this many steps.
const sccPollSteps = 1024

// callSCCs mirrors call_sccs in rust/gosree-errsolve/src/lib.rs: the strongly connected
// components of the graph whose edge v -> w means w calls v (callers[v] lists the w), restricted
// to active nodes, found by an iterative Tarjan search (a call chain of MaxFuncs functions would
// overflow the stack recursively). Tarjan emits a component after every component reachable from
// it, which here is every component of its callers, so the emission order is callers first. The
// result is the reverse: one flat list of the nodes in solve order (components callees first) and
// the exclusive end offset of each component in it. Inside a component the nodes are in reverse
// finish order of the depth-first search, which for the search over callers lists every function
// after the functions it calls that were found through it, so a summary that flips travels to its
// callers within one sweep wherever the search found them: the callee-first order used between
// components, applied inside the cycle. A cycle can still contain back edges against that order;
// each costs one more sweep, and the work budget bounds the total.
func callSCCs(ctx context.Context, callers [][]uint32, active []bool) (order, orderEnds []uint32, err error) {
	var members, ends []uint32
	const unvisited = ^uint32(0)
	n := len(callers)
	index := make([]uint32, n)
	for i := range index {
		index[i] = unvisited
	}
	finish := make([]uint32, n)
	nextFinish := uint32(0)
	low := make([]uint32, n)
	onStack := make([]bool, n)
	var stack []uint32
	type frame struct {
		node uint32
		pos  int
	}
	var frames []frame
	nextIndex := uint32(0)
	steps := 0
	for root := range n {
		if !active[root] || index[root] != unvisited {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		index[root], low[root] = nextIndex, nextIndex
		nextIndex++
		stack = append(stack, uint32(root))
		onStack[root] = true
		frames = append(frames, frame{uint32(root), 0})
		for len(frames) > 0 {
			steps++
			if steps%sccPollSteps == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			top := &frames[len(frames)-1]
			v := top.node
			if top.pos < len(callers[v]) {
				w := callers[v][top.pos]
				top.pos++
				switch {
				case !active[w]:
				case index[w] == unvisited:
					index[w], low[w] = nextIndex, nextIndex
					nextIndex++
					stack = append(stack, w)
					onStack[w] = true
					frames = append(frames, frame{w, 0})
				case onStack[w]:
					low[v] = min(low[v], index[w])
				}
				continue
			}
			frames = frames[:len(frames)-1]
			finish[v] = nextFinish
			nextFinish++
			if low[v] == index[v] {
				for {
					m := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[m] = false
					members = append(members, m)
					if m == v {
						break
					}
				}
				ends = append(ends, uint32(len(members)))
			}
			if len(frames) > 0 {
				p := frames[len(frames)-1].node
				low[p] = min(low[p], low[v])
			}
		}
	}
	order = make([]uint32, 0, len(members))
	orderEnds = make([]uint32, 0, len(ends))
	for k := len(ends) - 1; k >= 0; k-- {
		start := uint32(0)
		if k > 0 {
			start = ends[k-1]
		}
		from := len(order)
		order = append(order, members[start:ends[k]]...)
		slices.SortFunc(order[from:], func(a, b uint32) int { return cmp.Compare(finish[b], finish[a]) })
		orderEnds = append(orderEnds, uint32(len(order)))
	}
	return order, orderEnds, nil
}

// solveOracle is SolveOracle with an explicit call-graph sweep cap (tests pass a small cap to
// reach the error). The cap applies to each strongly connected component on its own.
func solveOracle(ctx context.Context, funcsIn []FuncIR, maxCallgraphIters int) (PackageResult, error) {
	return solveOracleLimited(ctx, funcsIn, maxCallgraphIters, Phase1WorkFactor, Phase1WorkFloor, &phase1Stats{})
}

// solveOracleLimited is solveOracle with an explicit phase-1 work budget (tests pass a small one
// to reach the error) and a counter; it mirrors solve_package_limited in
// rust/gosree-errsolve/src/lib.rs.
func solveOracleLimited(ctx context.Context, funcsIn []FuncIR, maxCallgraphIters int, workFactor, workFloor uint64, stats *phase1Stats) (PackageResult, error) {
	if ctx == nil {
		return PackageResult{}, errors.New("gusseterr: nil context")
	}
	funcs := make([]FuncIR, len(funcsIn))
	for i := range funcsIn {
		var err error
		if funcs[i], err = withCanonicalTuples(ctx, funcsIn[i]); err != nil {
			return PackageResult{}, err
		}
	}
	nFuncs := len(funcs)
	mayReturnNil := make([]bool, nFuncs)
	for i, fn := range funcs {
		if fn.Flags&FuncExternalMayNil != 0 {
			mayReturnNil[i] = true
		} else if fn.Flags&FuncReturnsConcreteErrPtr != 0 && len(fn.Blocks) == 0 {
			mayReturnNil[i] = true
		}
	}

	// Phase 1: Interprocedural call-graph fixpoint.
	// The summaries only flip false -> true, and a function's result depends on nothing but its
	// callees' summaries, so the least fixpoint is unique and any order that ends with every
	// function stable under the final summaries reaches it. Missing the sweep cap would export
	// optimistic summaries that hide ERR004 findings, so it is an error.
	//
	// The order is callee-first: the call graph's strongly connected components are solved in
	// reverse topological order, so every callee outside a component is final before any of its
	// callers is first solved, and a function outside a cycle is solved exactly once. (In index
	// order a function calling every member of a caller-first chain was re-solved after each
	// member flipped: O(n^2), 186 s at 262,144 functions.) Inside a component the dirty-worklist
	// sweeps of the index-order phase are kept: thisSweep holds the dirty members still ahead of
	// the sweep's position and nextSweep those behind it, both min-heaps, so a sweep visits them
	// in index order and costs O((solves + call edges) x log functions). dirty[i] is true while i
	// is queued, when i can never be solved again (its summary is already may-nil or it returns
	// no concrete error pointer), and while i has not been reached yet (a flipped callee's
	// callers in later components are solved when their turn comes), so no index is queued
	// twice. Only functions that can still flip are graph nodes: an inactive function is never
	// solved, so an edge through it carries no information.
	//
	// A component that is itself a cycle can still cost O(n^2) (a hub calling every member of a
	// chain that closes back on the hub), so the solves are also bounded by a whole-package work
	// budget in a deterministic unit: the ops plus state cells (blocks x max(slots, 1)) of each
	// function per solve, against workFactor x the package's total of that (at least workFloor). The unit is the same in
	// both engines, so both refuse the same packages at the same solve. Cap and budget are
	// checked after every solve, and the context is polled per component and per sweep. Mirrors
	// solve_package_limited in rust/gosree-errsolve/src/lib.rs.
	callers := make([][]uint32, nFuncs)
	unitWork := make([]uint64, nFuncs)
	var size uint64
	for idx := range funcs {
		// Solve cost scales with the state cells (blocks x slots) the solver holds, which is what
		// MaxStateCells bounds, plus one step per op.
		work := uint64(len(funcs[idx].Blocks)) * max(uint64(len(funcs[idx].SlotFlags)), 1)
		for bi := range funcs[idx].Blocks {
			work += uint64(len(funcs[idx].Blocks[bi].Ops))
			for oi := range funcs[idx].Blocks[bi].Ops {
				op := &funcs[idx].Blocks[bi].Ops[oi]
				if op.Kind != OpCallConcretePtr {
					continue
				}
				c := op.SlotB
				if n := len(callers[c]); n == 0 || callers[c][n-1] != uint32(idx) {
					callers[c] = append(callers[c], uint32(idx))
				}
			}
		}
		unitWork[idx] = work
		size += work
	}
	budget := max(workFloor, mulSat(size, workFactor))
	active := make([]bool, nFuncs)
	for i := range funcs {
		active[i] = !mayReturnNil[i] && funcs[i].Flags&FuncReturnsConcreteErrPtr != 0
	}
	order, sccEnds, err := callSCCs(ctx, callers, active)
	if err != nil {
		return PackageResult{}, err
	}
	// rank[f] is f's position in the solve order; the sweep heaps hold ranks, so a sweep visits
	// the dirty members of a component in that order and not in function-index order.
	rank := make([]uint32, nFuncs)
	for pos, f := range order {
		rank[f] = uint32(pos)
	}

	dirty := make([]bool, nFuncs)
	for i := range dirty {
		dirty[i] = true
	}
	var thisSweep, nextSweep u32MinHeap
	var workDone uint64
	start := uint32(0)
	for _, end := range sccEnds {
		stats.sccs++
		for pos := start; pos < end; pos++ {
			thisSweep.push(pos)
		}
		start = end
		converged := false
		for range maxCallgraphIters {
			if err := ctx.Err(); err != nil {
				return PackageResult{}, err
			}
			stats.sweeps++
			anyChanged := false
			for len(thisSweep) > 0 {
				pos := thisSweep.pop()
				idx := order[pos]
				if mayReturnNil[idx] || !dirty[idx] {
					continue
				}
				if err := ctx.Err(); err != nil {
					return PackageResult{}, err
				}
				dirty[idx] = false
				fnMayNil, _, err := solveFuncOracle(ctx, idx, &funcs[idx], mayReturnNil, false)
				if err != nil {
					return PackageResult{}, err
				}
				stats.solves++
				workDone = addSat(workDone, unitWork[idx])
				stats.work = workDone
				if workDone > budget {
					return PackageResult{}, refuse(ErrWorkBudgetExceeded, "phase-1 work budget exceeded: %d work units against a budget of %d (ops plus blocks x max(slots, 1) of every function solved; the budget is %d times the package's total of that, at least %d)", workDone, budget, Phase1WorkFactor, Phase1WorkFloor)
				}
				if fnMayNil {
					mayReturnNil[idx] = true
					anyChanged = true
					for _, c := range callers[idx] {
						if dirty[c] {
							continue
						}
						dirty[c] = true
						if r := rank[c]; r > pos {
							thisSweep.push(r)
						} else {
							nextSweep.push(r)
						}
					}
				}
			}
			if !anyChanged {
				converged = true
				break
			}
			thisSweep, nextSweep = nextSweep, thisSweep
		}
		if !converged {
			return PackageResult{}, refuse(ErrCallgraphBudgetExceeded, "call-graph summary fixpoint did not converge within %d iterations", maxCallgraphIters)
		}
	}

	// Phase 2: Intraprocedural CFG fixpoint + diagnostic extraction.
	var findings []Finding
	for idx := range funcs {
		if err := ctx.Err(); err != nil {
			return PackageResult{}, err
		}
		if len(funcs[idx].Blocks) == 0 {
			continue
		}
		_, fds, err := solveFuncOracle(ctx, uint32(idx), &funcs[idx], mayReturnNil, true)
		if err != nil {
			return PackageResult{}, err
		}
		findings = append(findings, fds...)
	}

	slices.SortFunc(findings, func(a, b Finding) int {
		if c := cmp.Compare(a.FuncIdx, b.FuncIdx); c != 0 {
			return c
		}
		if c := cmp.Compare(a.SiteID, b.SiteID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.RuleCode, b.RuleCode); c != 0 {
			return c
		}
		return cmp.Compare(a.SubKind, b.SubKind)
	})
	findings = slices.Compact(findings)

	return PackageResult{MayReturnNil: mayReturnNil, Findings: findings}, nil
}

func applyPhisOracle(block *BlockIR, base *absState, outEdges [][]edgePair, bb uint32) absState {
	st := base.clone()
	if !st.reachable || len(block.Phis) == 0 {
		return st
	}
	for _, phi := range block.Phis {
		var o originVal
		var nv nilKind
		var ev errVarVal
		var w writableKind
		var el errLinkVal
		for _, inc := range phi.Incoming {
			s := int(inc.SrcSlot)
			if int(inc.PredBB) < len(outEdges) {
				for _, edge := range outEdges[inc.PredBB] {
					if edge.target == bb && edge.state.reachable {
						o = o.join(edge.state.origin[s])
						nv = nv.join(edge.state.nilv[s])
						ev = ev.join(edge.state.errVar[s])
						w = w.join(edge.state.writable[s])
						el = el.join(edge.state.errLink[s])
					}
				}
			}
		}
		d := int(phi.DstSlot)
		if o.kind != originBot {
			st.origin[d] = o
		}
		if nv != nilBot {
			st.nilv[d] = nv
		}
		if ev.kind != errVarBot {
			st.errVar[d] = ev
		}
		if w != writableBot {
			st.writable[d] = w
		}
		if el.kind != errLinkBot {
			st.errLink[d] = el
		}
	}
	return st
}

func stepOpOracle(
	op *OpIR,
	st *absState,
	fn *FuncIR,
	mayReturnNil []bool,
	funcIdx uint32,
	emit *[]Finding,
	defers *[]deferCloseRec,
	bb uint32,
) {
	switch op.Kind {
	case OpUnhandledErr:
		if emit != nil {
			*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR001, SubKind: SubDefault})
		}
	case OpBindTuple:
		st.relaxAllCheckedNils()
		tmeta := fn.Tuples[op.TupleID]
		blanked := (tmeta.Flags&TupleErrBlanked) != 0 || op.SlotA == NoneU32
		compOrigin := originVal{originSingle, op.TupleID}
		if blanked {
			compOrigin = originVal{originBlanked, op.TupleID}
		}
		if op.SlotA != NoneU32 {
			es := int(op.SlotA)
			if st.errVar[es].kind == errVarUnread && emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR003, SubKind: SubERR003Overwritten})
			}
			st.errVar[es] = errVarVal{errVarUnread, op.SiteID}
			st.nilv[es] = nilMayNil
			st.errLink[es] = errLinkVal{errLinkTuple, op.TupleID}
			st.origin[es] = originVal{kind: originSafe}
		}
		for _, cs := range op.Companions {
			c := int(cs)
			st.origin[c] = compOrigin
			st.nilv[c] = nilMayNil
			st.errVar[c] = errVarVal{kind: errVarClean}
			st.errLink[c] = errLinkVal{kind: errLinkNone}
		}
	case OpBindErr:
		st.relaxAllCheckedNils()
		es := int(op.SlotA)
		if st.errVar[es].kind == errVarUnread && emit != nil {
			*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR003, SubKind: SubERR003Overwritten})
		}
		st.errVar[es] = errVarVal{errVarUnread, op.SiteID}
		st.nilv[es] = nilMayNil
		st.errLink[es] = errLinkVal{kind: errLinkNone}
		st.origin[es] = originVal{kind: originSafe}
	case OpAssignSlot:
		d := int(op.SlotA)
		if op.SlotB == NoneU32 {
			if st.errVar[d].kind == errVarUnread && emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR003, SubKind: SubERR003Overwritten})
			}
			st.origin[d] = originVal{kind: originSafe}
			st.errVar[d] = errVarVal{kind: errVarClean}
			st.errLink[d] = errLinkVal{kind: errLinkNone}
			switch op.NilHint {
			case AssignNilNonNil:
				st.nilv[d] = nilNonNil
			case AssignNilMustNil:
				st.nilv[d] = nilMustNil
			default:
				st.nilv[d] = nilMayNil
			}
			if op.WritableHint == 1 {
				st.writable[d] = writableWritable
			} else {
				st.writable[d] = writableReadOnly
			}
		} else {
			s := int(op.SlotB)
			srcOrigin := st.origin[s]
			srcNil := st.nilv[s]
			srcEv := st.errVar[s]
			srcW := st.writable[s]
			srcEl := st.errLink[s]
			if d != s && st.errVar[d].kind == errVarUnread && emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR003, SubKind: SubERR003Overwritten})
			}
			if d != s {
				st.errVar[s] = errVarVal{kind: errVarClean}
			}
			st.origin[d] = srcOrigin
			switch op.NilHint {
			case AssignNilCopy:
				st.nilv[d] = srcNil
			case AssignNilNonNil:
				st.nilv[d] = nilNonNil
			case AssignNilMustNil:
				st.nilv[d] = nilMustNil
			default:
				st.nilv[d] = nilMayNil
			}
			st.errVar[d] = srcEv
			if op.WritableHint == 1 {
				st.writable[d] = writableWritable
			} else {
				st.writable[d] = srcW
			}
			st.errLink[d] = srcEl
		}
	case OpCallConcretePtr:
		d := int(op.SlotA)
		st.origin[d] = originVal{kind: originSafe}
		st.errVar[d] = errVarVal{kind: errVarClean}
		st.errLink[d] = errLinkVal{kind: errLinkNone}
		st.writable[d] = writableReadOnly
		if mayReturnNil[op.SlotB] {
			st.nilv[d] = nilMayNil
		} else {
			st.nilv[d] = nilNonNil
		}
	case OpObserveErr:
		s := int(op.SlotA)
		st.errVar[s] = errVarVal{kind: errVarClean}
		st.nilv[s] = st.nilv[s].relaxChecked()
	case OpUseCompanion:
		s := int(op.SlotA)
		switch st.origin[s].kind {
		case originBlanked:
			if emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR002, SubKind: SubERR002Blanked})
			}
		case originSingle, originUncheckedMix:
			if emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR002, SubKind: SubERR002Unchecked})
			}
		}
	case OpShadowErr:
		if emit != nil {
			*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR003, SubKind: SubERR003Shadowed})
		}
	case OpBoxConcreteErr:
		p := int(op.SlotA)
		switch st.nilv[p] {
		case nilMustNil:
			if emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR004, SubKind: SubERR004MustNil})
			}
		case nilMayNil:
			if emit != nil {
				*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR004, SubKind: SubERR004MayNil})
			}
		}
	case OpMarkWritable:
		st.writable[op.SlotA] = writableWritable
	case OpUnhandledClose:
		r := int(op.SlotA)
		nowWritable := st.writable[r] == writableWritable
		if op.IsDefer {
			if defers != nil {
				*defers = append(*defers, deferCloseRec{bb: bb, siteID: op.SiteID, resSlot: op.SlotA, writableAtReg: nowWritable})
			}
		} else if nowWritable && emit != nil {
			*emit = append(*emit, Finding{FuncIdx: funcIdx, SiteID: op.SiteID, RuleCode: RuleERR005, SubKind: SubERR005SyncClose})
		}
	}
}

func outgoingEdgesOracle(block *BlockIR, end *absState, fn *FuncIR) []edgePair {
	if !end.reachable {
		return nil
	}
	switch block.Term.Kind {
	case TermJump:
		return []edgePair{{target: block.Term.Target, state: end.clone()}}
	case TermCond:
		tState := end.clone()
		fState := end.clone()
		switch block.Term.CondKind {
		case CondIsNil:
			cs := int(block.Term.CondSlot)
			tState.errVar[cs] = errVarVal{kind: errVarClean}
			fState.errVar[cs] = errVarVal{kind: errVarClean}
			tState.nilv[cs] = nilMustNil
			if end.errLink[cs].kind == errLinkTuple {
				tState.promoteTuple(end.errLink[cs].tuple)
			}
			if fn.SlotFlags[cs]&SlotIsError != 0 {
				fState.nilv[cs] = nilNonNilChecked
			} else {
				fState.nilv[cs] = nilNonNil
			}
		case CondNotNil:
			cs := int(block.Term.CondSlot)
			tState.errVar[cs] = errVarVal{kind: errVarClean}
			fState.errVar[cs] = errVarVal{kind: errVarClean}
			if fn.SlotFlags[cs]&SlotIsError != 0 {
				tState.nilv[cs] = nilNonNilChecked
			} else {
				tState.nilv[cs] = nilNonNil
			}
			fState.nilv[cs] = nilMustNil
			if end.errLink[cs].kind == errLinkTuple {
				fState.promoteTuple(end.errLink[cs].tuple)
			}
		case CondGtZero:
			cs := int(block.Term.CondSlot)
			if tState.origin[cs].kind == originSingle {
				tid := tState.origin[cs].tuple
				if fn.Tuples[tid].Flags&TupleIORead != 0 {
					tState.origin[cs] = originVal{kind: originSafe}
				}
			}
		}
		return []edgePair{
			{target: block.Term.Target, state: tState},
			{target: block.Term.FalseBB, state: fState},
		}
	default:
		return nil
	}
}

func reachableBlocksFromOracle(fn *FuncIR, start uint32) []bool {
	seen := make([]bool, len(fn.Blocks))
	seen[start] = true
	q := []uint32{start}
	for len(q) > 0 {
		bb := q[0]
		q = q[1:]
		term := fn.Blocks[bb].Term
		switch term.Kind {
		case TermJump:
			if !seen[term.Target] {
				seen[term.Target] = true
				q = append(q, term.Target)
			}
		case TermCond:
			if !seen[term.Target] {
				seen[term.Target] = true
				q = append(q, term.Target)
			}
			if !seen[term.FalseBB] {
				seen[term.FalseBB] = true
				q = append(q, term.FalseBB)
			}
		}
	}
	return seen
}

func solveFuncOracle(
	ctx context.Context,
	funcIdx uint32,
	fn *FuncIR,
	mayReturnNil []bool,
	collectFindings bool,
) (bool, []Finding, error) {
	nBlocks := len(fn.Blocks)
	nSlots := len(fn.SlotFlags)

	inStates := make([]absState, nBlocks)
	endStates := make([]absState, nBlocks)
	for i := range nBlocks {
		inStates[i] = bottomState(nSlots)
		endStates[i] = bottomState(nSlots)
	}
	outEdges := make([][]edgePair, nBlocks)
	for i := range nBlocks {
		switch fn.Blocks[i].Term.Kind {
		case TermJump:
			outEdges[i] = []edgePair{{target: fn.Blocks[i].Term.Target, state: bottomState(nSlots)}}
		case TermCond:
			outEdges[i] = []edgePair{
				{target: fn.Blocks[i].Term.Target, state: bottomState(nSlots)},
				{target: fn.Blocks[i].Term.FalseBB, state: bottomState(nSlots)},
			}
		}
	}

	inStates[0] = entryState(fn.SlotFlags)
	inQueue := make([]bool, nBlocks)
	worklist := []uint32{0}
	inQueue[0] = true

	steps := 0
	for len(worklist) > 0 {
		bb := worklist[0]
		worklist = worklist[1:]
		inQueue[bb] = false
		steps++
		if steps > MaxIterationsPerFunc {
			return false, nil, refuse(ErrIterationBudgetExceeded, "CFG worklist iteration budget exceeded in func %d (bound %d steps)", funcIdx, MaxIterationsPerFunc)
		}
		if (steps & 63) == 0 {
			if err := ctx.Err(); err != nil {
				return false, nil, err
			}
		}

		block := &fn.Blocks[bb]
		st := applyPhisOracle(block, &inStates[bb], outEdges, bb)
		if !st.reachable {
			continue
		}
		for i := range block.Ops {
			stepOpOracle(&block.Ops[i], &st, fn, mayReturnNil, funcIdx, nil, nil, bb)
		}
		edges := outgoingEdgesOracle(block, &st, fn)
		endStates[bb] = st
		for i := range edges {
			succ := edges[i].target
			outEdges[bb][i].target = succ
			edgeChanged := outEdges[bb][i].state.joinFrom(&edges[i].state)
			inChanged := inStates[succ].joinFrom(&outEdges[bb][i].state)
			if (edgeChanged || inChanged) && !inQueue[succ] {
				inQueue[succ] = true
				worklist = append(worklist, succ)
			}
		}
	}

	funcMayNil := false
	if fn.Flags&FuncReturnsConcreteErrPtr != 0 {
		for bbIdx := range fn.Blocks {
			st := &endStates[bbIdx]
			if !st.reachable {
				continue
			}
			term := fn.Blocks[bbIdx].Term
			if term.Kind == TermReturn {
				if term.RetFlags&RetConcreteLiteralNil != 0 {
					funcMayNil = true
					break
				}
				if term.RetSlot != NoneU32 {
					nv := st.nilv[term.RetSlot]
					if nv == nilMustNil || nv == nilMayNil {
						funcMayNil = true
						break
					}
				}
			}
		}
	}

	if !collectFindings {
		return funcMayNil, nil, nil
	}

	var findings []Finding
	var defers []deferCloseRec

	for bbIdx := range fn.Blocks {
		if !inStates[bbIdx].reachable {
			continue
		}
		bb := uint32(bbIdx)
		block := &fn.Blocks[bbIdx]
		st := applyPhisOracle(block, &inStates[bbIdx], outEdges, bb)
		for i := range block.Ops {
			stepOpOracle(&block.Ops[i], &st, fn, mayReturnNil, funcIdx, &findings, &defers, bb)
		}
		if block.Term.Kind == TermReturn && (fn.Flags&FuncReturnsError) != 0 && (block.Term.RetFlags&RetLiteralNilErr) != 0 {
			activeChecked := false
			for sIdx, nv := range st.nilv {
				if nv == nilNonNilChecked && (fn.SlotFlags[sIdx]&SlotIsError) != 0 {
					activeChecked = true
					break
				}
			}
			if activeChecked {
				findings = append(findings, Finding{
					FuncIdx:  funcIdx,
					SiteID:   block.Term.SiteID,
					RuleCode: RuleERR003,
					SubKind:  SubERR003Nilerr,
				})
			}
		}
	}

	for _, dc := range defers {
		writableOnExit := dc.writableAtReg
		if !writableOnExit {
			reach := reachableBlocksFromOracle(fn, dc.bb)
			for bbIdx, canReach := range reach {
				if !canReach || !endStates[bbIdx].reachable {
					continue
				}
				if fn.Blocks[bbIdx].Term.Kind == TermReturn && endStates[bbIdx].writable[dc.resSlot] == writableWritable {
					writableOnExit = true
					break
				}
			}
		}
		if writableOnExit {
			findings = append(findings, Finding{
				FuncIdx:  funcIdx,
				SiteID:   dc.siteID,
				RuleCode: RuleERR005,
				SubKind:  SubERR005DeferClose,
			})
		}
	}

	return funcMayNil, findings, nil
}
