package gusseterr

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// DecodePackage decodes and validates a GSE1 binary request frame (matching
// gosree_errsolve::decode_package).
func DecodePackage(frame []byte) ([]FuncIR, error) {
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
		return nil, errors.New("GSE1 limit exceeded: num_funcs")
	}
	numFuncs := int(numFuncsU32)
	funcs := make([]FuncIR, 0, min(numFuncs, 256))

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
			return nil, errors.New("GSE1 limit exceeded: num_slots")
		}
		numTuples, err := readU32()
		if err != nil {
			return nil, err
		}
		if numTuples > MaxTuples {
			return nil, errors.New("GSE1 limit exceeded: num_tuples")
		}
		numBlocks, err := readU32()
		if err != nil {
			return nil, err
		}
		if numBlocks > MaxBlocks {
			return nil, errors.New("GSE1 limit exceeded: num_blocks")
		}
		if err := checkStateCells(uint64(numBlocks), uint64(numSlots)); err != nil {
			return nil, err
		}

		if pos+int(numSlots) > len(frame) {
			return nil, errors.New("truncated GSE1 frame")
		}
		slotFlags := append([]uint8(nil), frame[pos:pos+int(numSlots)]...)
		pos += int(numSlots)

		tuples := make([]TupleMeta, 0, min(int(numTuples), 128))
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

		blocks := make([]BlockIR, 0, min(int(numBlocks), 128))
		for range int(numBlocks) {
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
				return nil, errors.New("GSE1 limit exceeded: num_phis")
			}
			phis := make([]PhiIR, 0, min(int(nPhis), 64))
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
					return nil, errors.New("GSE1 limit exceeded: phi_arity")
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
				return nil, errors.New("GSE1 limit exceeded: num_ops")
			}
			ops := make([]OpIR, 0, min(int(nOps), 128))
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
						return nil, errors.New("GSE1 limit exceeded: companions")
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
	funcs, err := DecodePackage(frame)
	if err != nil {
		return nil, err
	}
	res, err := SolveOracle(ctx, funcs)
	if err != nil {
		return nil, err
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
	return solveOracle(ctx, funcs, MaxCallgraphIters)
}

// maxCanonClass mirrors MAX_CANON_CLASS in rust/gosree-errsolve/src/lib.rs.
const maxCanonClass = 64

// canonicalTuples mirrors canonical_tuples in rust/gosree-errsolve/src/lib.rs: it maps every
// tuple to the smallest id of its logical call result. Tuples merge only when their error
// slots are connected through phis, their flags agree, and their bind blocks are pairwise
// unreachable from one another, so one check after a join promotes the companions of whichever
// call ran, but two binds on one path (`a, err := f(); if retry { _, err = g() }`) stay
// separate. It returns nil when nothing changes.
func canonicalTuples(fn *FuncIR) []uint32 {
	nTuples := len(fn.Tuples)
	if nTuples < 2 {
		return nil
	}
	anyPhi := false
	for i := range fn.Blocks {
		if len(fn.Blocks[i].Phis) > 0 {
			anyPhi = true
			break
		}
	}
	if !anyPhi {
		return nil
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
		reach := make(map[uint32][]bool)
		for _, t := range members {
			for _, bb := range bindBlocks[t] {
				if _, ok := reach[bb]; !ok {
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
		return nil
	}
	return canon
}

// withCanonicalTuples returns fn with every BindTuple rewritten to its canonical tuple id,
// copying only the blocks it changes; fn itself is never modified.
func withCanonicalTuples(fn FuncIR) FuncIR {
	canon := canonicalTuples(&fn)
	if canon == nil {
		return fn
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
	return fn
}

// solveOracle is SolveOracle with an explicit call-graph iteration cap (tests pass a small cap
// to reach it).
func solveOracle(ctx context.Context, funcsIn []FuncIR, maxCallgraphIters int) (PackageResult, error) {
	if ctx == nil {
		return PackageResult{}, errors.New("gusseterr: nil context")
	}
	funcs := make([]FuncIR, len(funcsIn))
	for i := range funcsIn {
		funcs[i] = withCanonicalTuples(funcsIn[i])
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
	// The summaries only flip false -> true, so this converges in at most one sweep per
	// function; a chain longer than the cap fails the package rather than exporting
	// optimistic summaries that would hide ERR004 findings.
	converged := false
	for range maxCallgraphIters {
		if err := ctx.Err(); err != nil {
			return PackageResult{}, err
		}
		anyChanged := false
		for idx := range funcs {
			if mayReturnNil[idx] || funcs[idx].Flags&FuncReturnsConcreteErrPtr == 0 {
				continue
			}
			if len(funcs[idx].Blocks) == 0 {
				mayReturnNil[idx] = true
				anyChanged = true
				continue
			}
			fnMayNil, _, err := solveFuncOracle(ctx, uint32(idx), &funcs[idx], mayReturnNil, false)
			if err != nil {
				return PackageResult{}, err
			}
			if fnMayNil {
				mayReturnNil[idx] = true
				anyChanged = true
			}
		}
		if !anyChanged {
			converged = true
			break
		}
	}
	if !converged {
		return PackageResult{}, fmt.Errorf("gusseterr: call-graph summary fixpoint did not converge within %d iterations", maxCallgraphIters)
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
			return false, nil, fmt.Errorf("CFG worklist iteration budget exceeded in func %d", funcIdx)
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
