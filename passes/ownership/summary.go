package ownership

import (
	"cmp"
	"encoding/binary"
	"errors"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/kinds"
)

// FuncFact summarises how a function treats its arguments, so callers in other packages can check
// moves across the call. Index i follows SSA parameter order: the receiver, when there is one, is 0.
type FuncFact struct {
	// Leaks[i]: the function may retain, share or move argument i, or mix it with memory the caller
	// does not own, other than by linking it to another argument or a result (Links).
	Leaks []bool
	// Iso[i]: parameter i is //gosree:iso, so passing an argument to it moves the argument.
	Iso []bool
	// Spawns[i]: parameter i is a function value that the callee starts on another goroutine or sends
	// on a channel, so passing a callback to it is a goroutine boundary.
	Spawns []bool
	// Fresh: every pointer-bearing result is a fresh allocation that nothing else references, or
	// memory of the arguments it is linked to (Links).
	Fresh bool
	// FreshResult[k]: result k is a fresh allocation that nothing else references, or memory of the
	// arguments it is linked to (Links).
	FreshResult []bool
	// Writes[i]: the function may write memory reachable from argument i (through its pointers, map
	// or slice), itself or through functions it calls.
	Writes []bool
	// Links are the parameters and results whose memory the function joins, as root.Add(child) or
	// root.First() do (see Link).
	Links []Link
	// Acquires are the parameter mutexes the function returns holding (lockset.go).
	Acquires []acquire
	// Unknown: gosree could not analyse the function (it has no body, or depends on one that could
	// not be), so Leaks and Writes are all set by assumption, not because it was seen to do either.
	Unknown bool

	// Extension trailer fields (encoded only when flags & (1 << 2) != 0):
	// WaitParamPlusOne is i+1 when parameter i (*sync.WaitGroup or *errgroup.Group) joins all
	// goroutines spawned by this wrapper on non-nil paths; 0 means none.
	WaitParamPlusOne int
	// WaitKind: 1 = *sync.WaitGroup, 2 = *errgroup.Group.
	WaitKind uint8
	// ParamAutoSync[i] / ResAutoSync[k]: parameter i / result k has a proven AutoSync struct type
	// declared in the callee's package (nil when none are AutoSync).
	ParamAutoSync []bool
	ResAutoSync   []bool
}

func (f *FuncFact) hasExt() bool {
	return f.WaitParamPlusOne > 0 || f.WaitKind != 0 || anyTrue(f.ParamAutoSync) || anyTrue(f.ResAutoSync)
}

func anyTrue(xs []bool) bool {
	for _, v := range xs {
		if v {
			return true
		}
	}
	return false
}

// AFact marks FuncFact as an analysis fact.
func (*FuncFact) AFact() {}

func (f *FuncFact) String() string { return "ownership summary" }

func appendBoolBits(buf []byte, xs []bool) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(xs)))
	if len(xs) == 0 {
		return buf
	}
	nBytes := (len(xs) + 7) / 8
	start := len(buf)
	buf = append(buf, make([]byte, nBytes)...)
	for i, v := range xs {
		if v {
			buf[start+i/8] |= 1 << (uint(i) & 7)
		}
	}
	return buf
}

func readBoolBits(data []byte) ([]bool, []byte, error) {
	n, read := binary.Uvarint(data)
	if read <= 0 || n > 1<<20 {
		return nil, nil, errors.New("corrupt FuncFact bool slice length")
	}
	data = data[read:]
	if n == 0 {
		return nil, data, nil
	}
	nBytes := int((n + 7) / 8)
	if len(data) < nBytes {
		return nil, nil, errors.New("truncated FuncFact bool slice")
	}
	out := make([]bool, int(n))
	for i := range out {
		out[i] = (data[i/8] & (1 << (uint(i) & 7))) != 0
	}
	return out, data[nBytes:], nil
}

// GobEncode serialises FuncFact into a compact reflection-free binary payload.
func (f *FuncFact) GobEncode() ([]byte, error) {
	var flags byte
	if f.Fresh {
		flags |= 1 << 0
	}
	if f.Unknown {
		flags |= 1 << 1
	}
	ext := f.hasExt()
	if ext {
		flags |= 1 << 2
	}
	buf := make([]byte, 0, 16+len(f.Links)*8+len(f.Acquires)*12)
	buf = append(buf, flags)
	buf = appendBoolBits(buf, f.Leaks)
	buf = appendBoolBits(buf, f.Iso)
	buf = appendBoolBits(buf, f.Spawns)
	buf = appendBoolBits(buf, f.FreshResult)
	buf = appendBoolBits(buf, f.Writes)
	buf = binary.AppendUvarint(buf, uint64(len(f.Links)))
	for _, l := range f.Links {
		buf = binary.AppendVarint(buf, int64(l.A))
		buf = binary.AppendVarint(buf, int64(l.DepthA))
		buf = binary.AppendVarint(buf, int64(l.B))
		buf = binary.AppendVarint(buf, int64(l.DepthB))
	}
	buf = binary.AppendUvarint(buf, uint64(len(f.Acquires)))
	for _, a := range f.Acquires {
		buf = binary.AppendVarint(buf, int64(a.Param))
		buf = binary.AppendVarint(buf, int64(a.Free))
		buf = binary.AppendUvarint(buf, uint64(len(a.Steps)))
		buf = append(buf, a.Steps...)
		buf = append(buf, byte(a.Mode))
	}
	if ext {
		buf = binary.AppendUvarint(buf, uint64(f.WaitParamPlusOne))
		buf = append(buf, f.WaitKind)
		if anyTrue(f.ParamAutoSync) {
			buf = appendBoolBits(buf, f.ParamAutoSync)
		} else {
			buf = appendBoolBits(buf, nil)
		}
		if anyTrue(f.ResAutoSync) {
			buf = appendBoolBits(buf, f.ResAutoSync)
		} else {
			buf = appendBoolBits(buf, nil)
		}
	}
	return buf, nil
}

// GobDecode deserialises a compact binary FuncFact payload.
func (f *FuncFact) GobDecode(data []byte) error {
	if len(data) < 1 {
		return errors.New("empty FuncFact payload")
	}
	flags := data[0]
	f.Fresh = (flags & (1 << 0)) != 0
	f.Unknown = (flags & (1 << 1)) != 0
	rest := data[1:]
	var err error
	if f.Leaks, rest, err = readBoolBits(rest); err != nil {
		return err
	}
	if f.Iso, rest, err = readBoolBits(rest); err != nil {
		return err
	}
	if f.Spawns, rest, err = readBoolBits(rest); err != nil {
		return err
	}
	if f.FreshResult, rest, err = readBoolBits(rest); err != nil {
		return err
	}
	if f.Writes, rest, err = readBoolBits(rest); err != nil {
		return err
	}
	// Every link takes at least four bytes and every acquire four, so a count larger than the
	// remaining bytes allow is corrupt, and no allocation is sized from an unchecked count.
	nLinks, r := binary.Uvarint(rest)
	if r <= 0 || nLinks > 1<<20 || nLinks > uint64(len(rest)-r)/4 {
		return errors.New("corrupt FuncFact links length")
	}
	rest = rest[r:]
	if nLinks > 0 {
		f.Links = make([]Link, int(nLinks))
		for i := range f.Links {
			var vals [4]int64
			for k := range 4 {
				v, nr := binary.Varint(rest)
				if nr <= 0 {
					return errors.New("truncated FuncFact link")
				}
				vals[k] = v
				rest = rest[nr:]
			}
			f.Links[i] = Link{A: int(vals[0]), DepthA: int(vals[1]), B: int(vals[2]), DepthB: int(vals[3])}
		}
	} else {
		f.Links = nil
	}
	nAcq, r := binary.Uvarint(rest)
	if r <= 0 || nAcq > 1<<20 || nAcq > uint64(len(rest)-r)/4 {
		return errors.New("corrupt FuncFact acquires length")
	}
	rest = rest[r:]
	if nAcq > 0 {
		f.Acquires = make([]acquire, int(nAcq))
		for i := range f.Acquires {
			param, nr := binary.Varint(rest)
			if nr <= 0 {
				return errors.New("truncated FuncFact acquire param")
			}
			rest = rest[nr:]
			free, nr := binary.Varint(rest)
			if nr <= 0 {
				return errors.New("truncated FuncFact acquire free")
			}
			rest = rest[nr:]
			sLen, nr := binary.Uvarint(rest)
			// The steps and the mode byte after them must both fit in what is left.
			if nr <= 0 || len(rest)-nr < 1 || sLen > uint64(len(rest)-nr-1) {
				return errors.New("truncated FuncFact acquire steps")
			}
			rest = rest[nr:]
			steps := string(rest[:sLen])
			rest = rest[sLen:]
			mode := lockMode(rest[0])
			rest = rest[1:]
			f.Acquires[i] = acquire{Param: int(param), Free: int(free), Steps: steps, Mode: mode}
		}
	} else {
		f.Acquires = nil
	}
	if (flags & (1 << 2)) != 0 {
		wp, nr := binary.Uvarint(rest)
		if nr <= 0 || wp > 1<<20 || len(rest)-nr < 1 {
			return errors.New("corrupt FuncFact wait param")
		}
		rest = rest[nr:]
		wk := rest[0]
		if (wk&3) > 2 || (wk&^uint8(7)) != 0 {
			return errors.New("corrupt FuncFact wait kind")
		}
		rest = rest[1:]
		if f.ParamAutoSync, rest, err = readBoolBits(rest); err != nil {
			return err
		}
		if len(f.ParamAutoSync) > 0 && !anyTrue(f.ParamAutoSync) {
			return errors.New("non-canonical FuncFact ParamAutoSync")
		}
		if f.ResAutoSync, rest, err = readBoolBits(rest); err != nil {
			return err
		}
		if len(f.ResAutoSync) > 0 && !anyTrue(f.ResAutoSync) {
			return errors.New("non-canonical FuncFact ResAutoSync")
		}
		f.WaitParamPlusOne = int(wp)
		f.WaitKind = wk
		if !f.hasExt() {
			return errors.New("empty FuncFact extension")
		}
	} else {
		f.WaitParamPlusOne = 0
		f.WaitKind = 0
		f.ParamAutoSync = nil
		f.ResAutoSync = nil
	}
	return nil
}

// ImmFact marks a named type whose //gosree:imm annotation passed validation.
type ImmFact struct{ Imm bool }

// AFact marks ImmFact as an analysis fact.
func (*ImmFact) AFact() {}

func (f *ImmFact) String() string { return "imm" }

func (f *ImmFact) GobEncode() ([]byte, error) {
	if f.Imm {
		return []byte{1}, nil
	}
	return []byte{0}, nil
}

func (f *ImmFact) GobDecode(data []byte) error {
	if len(data) != 1 {
		return errors.New("invalid ImmFact payload")
	}
	f.Imm = data[0] != 0
	return nil
}

type summary struct {
	leakParam []bool
	// leakBesides[i]: the function does something with argument i that leaks it and that its
	// contract at a call does not account for (event.byContract). Only a function of this package
	// has it: no other function has a contract worked out from its body.
	leakBesides []bool
	isoParam    []bool
	spawnParam  []bool
	leakFree    []bool
	// writeParam[i] / writeFree[j]: the function may write memory reachable from parameter i or free
	// variable j. nonRegWriteParam[i] omits writes charged solely by net/http handler registration
	// calls so route-registrar helpers do not masquerade as post-registration mutators in Mode H.
	writeParam       []bool
	nonRegWriteParam []bool
	writeFree        []bool
	fresh            bool
	freshResult      []bool
	// links are the parameters and results the function joins (FuncFact.Links).
	links         []Link
	acquires      []acquire
	waitParam     int
	waitKind      uint8
	paramAutoSync []bool
	resAutoSync   []bool
	// pessimistic marks a summary for a function gosree could not analyse.
	pessimistic bool
}

func (s *summary) leaks(i int) bool {
	if s.pessimistic || i >= len(s.leakParam) {
		return true
	}
	return s.leakParam[i]
}

// leaksBesidesContract reports whether the function leaks argument i by something other than
// what its contract at a call accounts for: the publish, and the forwards a caller resolves. A
// summary that was not worked out from a body of this package answers no, also the pessimistic
// one: such a callee has a contract only from the table, and the table speaks for all it does.
func (s *summary) leaksBesidesContract(i int) bool {
	return i < len(s.leakBesides) && s.leakBesides[i]
}

const waitSingleSpawn uint8 = 1 << 2

func (s *summary) isIso(i int) bool { return i < len(s.isoParam) && s.isoParam[i] }

func (s *summary) spawns(i int) bool { return i < len(s.spawnParam) && s.spawnParam[i] }

func (s *summary) waitGroupKind() uint8 { return s.waitKind & 3 }

func (s *summary) singleSpawn() bool { return !s.pessimistic && (s.waitKind&waitSingleSpawn) != 0 }

func (s *summary) freeLeaks(j int) bool {
	if s.pessimistic || j >= len(s.leakFree) {
		return true
	}
	return s.leakFree[j]
}

// writes reports whether calling the function may write memory reachable from argument i.
func (s *summary) writes(i int) bool {
	if s.pessimistic || i >= len(s.writeParam) {
		return true
	}
	return s.writeParam[i]
}

func (s *summary) writesNonReg(i int) bool {
	if s.pessimistic {
		return true
	}
	if s.nonRegWriteParam != nil {
		return i >= len(s.nonRegWriteParam) || s.nonRegWriteParam[i]
	}
	return s.writes(i)
}

func (s *summary) writesFree(j int) bool {
	if s.pessimistic || j >= len(s.writeFree) {
		return true
	}
	return s.writeFree[j]
}

func (s *summary) isFreshResult(k int) bool {
	if s.pessimistic {
		return false
	}
	if k >= 0 && k < len(s.freshResult) {
		return s.freshResult[k]
	}
	return s.fresh
}

// unavailable is the note diagnostics add when a callee's behaviour is assumed rather than known, so
// a reader can tell a callee gosree could not analyse from one it saw retain or write.
func (s *summary) unavailable() string {
	if s.pessimistic {
		return " (summary unavailable)"
	}
	return ""
}

var pessimistic = &summary{pessimistic: true, waitParam: -1}

func optimistic(fn *ssa.Function, st *state) *summary {
	nRes := 0
	if fn.Signature != nil && fn.Signature.Results() != nil {
		nRes = fn.Signature.Results().Len()
	}
	fr := make([]bool, nRes)
	for k := range fr {
		fr[k] = true
	}
	s := &summary{
		leakParam: make([]bool, len(fn.Params)),
		isoParam:  make([]bool, len(fn.Params)),

		leakBesides: make([]bool, len(fn.Params)),
		spawnParam:  make([]bool, len(fn.Params)),
		leakFree:    make([]bool, len(fn.FreeVars)),

		writeParam:       make([]bool, len(fn.Params)),
		nonRegWriteParam: make([]bool, len(fn.Params)),
		writeFree:        make([]bool, len(fn.FreeVars)),
		fresh:            true,
		freshResult:      fr,
		waitParam:        -1,
	}
	for i, p := range fn.Params {
		s.isoParam[i] = st.isIsoParam(p)
	}
	return s
}

// summary returns what gosree knows about calling fn.
func (st *state) summary(fn *ssa.Function) *summary {
	if fn == nil {
		return pessimistic
	}
	if o := fn.Origin(); o != nil {
		fn = o
	}
	if s, ok := st.sums[fn]; ok {
		return s
	}
	obj, ok := fn.Object().(*types.Func)
	if ok && (isBoundMethodWrapper(fn) || isMethodThunk(fn)) && obj.Signature().Recv() != nil && !types.IsInterface(obj.Signature().Recv().Type()) {
		origObj := obj.Origin()
		nParams := len(fn.Params)
		nFree := len(fn.FreeVars)
		if c, has := capContracts[origObj.FullName()]; has {
			if c.freshResult {
				leaks := make([]bool, nParams)
				writes := make([]bool, nParams)
				for i := range leaks {
					leaks[i] = true
					writes[i] = true
				}
				freeLeaks := make([]bool, nFree)
				freeWrites := make([]bool, nFree)
				for j := range freeLeaks {
					freeLeaks[j] = true
					freeWrites[j] = true
				}
				var freshRes []bool
				if fn.Signature != nil && fn.Signature.Results() != nil && fn.Signature.Results().Len() > 0 {
					freshRes = make([]bool, fn.Signature.Results().Len())
					for k := range freshRes {
						freshRes[k] = true
					}
				}
				return &summary{leakParam: leaks, writeParam: writes, leakFree: freeLeaks, writeFree: freeWrites, fresh: true, freshResult: freshRes, waitParam: -1}
			}
			return pessimistic
		}
		if origObj.Pkg() != nil && origObj.Pkg().Path() == atomicPkg {
			leaks := make([]bool, nParams)
			for i := range leaks {
				leaks[i] = true
			}
			freeLeaks := make([]bool, nFree)
			for j := range freeLeaks {
				freeLeaks[j] = true
			}
			return &summary{leakParam: leaks, writeParam: make([]bool, nParams), leakFree: freeLeaks, writeFree: make([]bool, nFree), waitParam: -1}
		}
		if ms := st.summaryOfFuncObj(origObj); !ms.pessimistic {
			if isMethodThunk(fn) {
				return ms
			}
			return adaptBoundMethodSummary(fn, ms)
		}
	}
	// Functions of other packages are built from type information; wrappers, thunks and bound
	// methods that did not resolve above have their own parameter layout and get no summary.
	if !ok || (fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information")) {
		return pessimistic
	}
	return st.summaryOfFuncObj(obj)
}

func (st *state) summaryOfFuncObj(obj *types.Func) *summary {
	if obj == nil {
		return pessimistic
	}
	obj = obj.Origin()
	if st.ssa != nil && st.ssa.Pkg != nil && st.ssa.Pkg.Prog != nil {
		if fn := st.ssa.Pkg.Prog.FuncValue(obj); fn != nil {
			if s, ok := st.sums[fn]; ok {
				return s
			}
		}
	}
	if st.pass == nil || obj.Pkg() == nil || obj.Pkg() == st.pass.Pkg {
		return pessimistic
	}
	if o, ok := kinds.FuncOverrideFor(obj.FullName()); ok {
		// An assumption replaces the computed summary (internal/kinds/funcs.go).
		return &summary{leakParam: o.Leaks, writeParam: o.Writes, fresh: o.Fresh, waitParam: -1}
	}
	var f FuncFact
	if !st.pass.ImportObjectFact(obj, &f) || f.Unknown {
		if c, ok := capContracts[obj.FullName()]; ok && c.freshResult {
			leaks := make([]bool, kinds.Params(obj))
			writes := make([]bool, len(leaks))
			for i := range leaks {
				leaks[i] = true
				writes[i] = true
			}
			return &summary{leakParam: leaks, writeParam: writes, fresh: true, waitParam: -1}
		}
		if obj.Pkg().Path() == atomicPkg {
			return atomicSummary(kinds.Params(obj))
		}
		return pessimistic
	}
	s := &summary{
		leakParam:     f.Leaks,
		isoParam:      f.Iso,
		spawnParam:    f.Spawns,
		writeParam:    f.Writes,
		fresh:         f.Fresh,
		freshResult:   f.FreshResult,
		links:         f.Links,
		acquires:      f.Acquires,
		waitParam:     f.WaitParamPlusOne - 1,
		waitKind:      f.WaitKind,
		paramAutoSync: f.ParamAutoSync,
		resAutoSync:   f.ResAutoSync,
	}
	if c, ok := capContracts[obj.FullName()]; ok && c.freshResult {
		s.fresh = true
		freshResult := make([]bool, len(f.FreshResult))
		for i := range freshResult {
			freshResult[i] = true
		}
		s.freshResult = freshResult
	}
	if obj.Pkg().Path() == atomicPkg {
		s.writeParam = make([]bool, len(f.Writes))
	}
	return s
}

func adaptBoundMethodSummary(fn *ssa.Function, ms *summary) *summary {
	nParams := len(fn.Params)
	nFree := len(fn.FreeVars)
	bs := &summary{
		leakParam:        make([]bool, nParams),
		isoParam:         make([]bool, nParams),
		spawnParam:       make([]bool, nParams),
		writeParam:       make([]bool, nParams),
		nonRegWriteParam: make([]bool, nParams),
		leakFree:         make([]bool, nFree),
		writeFree:        make([]bool, nFree),
		fresh:            ms.fresh,
		freshResult:      slices.Clone(ms.freshResult),
		waitParam:        -1,
	}
	for i := range nParams {
		bs.leakParam[i] = ms.leaks(i + 1)
		bs.isoParam[i] = ms.isIso(i + 1)
		bs.spawnParam[i] = ms.spawns(i + 1)
		bs.writeParam[i] = ms.writes(i + 1)
		bs.nonRegWriteParam[i] = ms.writesNonReg(i + 1)
	}
	if nFree > 0 {
		bs.leakFree[0] = ms.leaks(0)
		bs.writeFree[0] = ms.writes(0)
	}
	if ms.waitParam >= 1 {
		bs.waitParam = ms.waitParam - 1
		bs.waitKind = ms.waitKind
	}
	for _, l := range ms.links {
		if l.A == 0 {
			if l.B == 0 {
				continue
			}
			if nFree > 0 {
				bs.leakFree[0] = true
			}
			if l.B >= 1+nParams {
				bs.fresh = false
				resIdx := l.B - (1 + nParams)
				if resIdx >= 0 && resIdx < len(bs.freshResult) {
					bs.freshResult[resIdx] = false
				}
			} else if l.B-1 < nParams {
				bs.leakParam[l.B-1] = true
			}
			continue
		}
		if l.A >= 1 && l.B >= 1 {
			bs.links = append(bs.links, newLink(l.A-1, l.DepthA, l.B-1, l.DepthB))
		}
	}
	bs.links = mergeLinks(nil, bs.links)
	for _, a := range ms.acquires {
		if a.Param == 0 && nFree > 0 {
			bs.acquires = append(bs.acquires, acquire{Param: -1, Free: 0, Steps: a.Steps, Mode: a.Mode})
		} else if a.Param >= 1 {
			bs.acquires = append(bs.acquires, acquire{Param: a.Param - 1, Free: -1, Steps: a.Steps, Mode: a.Mode})
		}
	}
	return bs
}

// atomicSummary is the summary of a sync/atomic function with n parameters and no summary (a
// function without a Go body has none; see tables.go): it may keep
// what it is given and its result is not fresh, as for any function gosree cannot see, but it writes
// nothing a race check counts. A sync/atomic function with a summary keeps it, with its writes
// cleared.
func atomicSummary(n int) *summary {
	leaks := make([]bool, n)
	for i := range leaks {
		leaks[i] = true
	}
	return &summary{leakParam: leaks, writeParam: make([]bool, n), waitParam: -1}
}

func (st *state) isIsoParam(p *ssa.Parameter) bool {
	v, ok := p.Object().(*types.Var)
	return ok && st.ix.IsIso(v)
}

// summarise computes a function's summary from its graph.
//
// A parameter whose region holds another parameter's foreign event is linked to it, not leaked: the
// function joined their memory (root.Add(child) stores child into root). A result whose region holds
// a parameter's foreign event is linked to that parameter (root.First() returns memory of root). A
// link records the depth at which the two values' pointee chains meet in the callee's graph, so the
// caller joins the same memory and no more (graph.link): a copy `cp := *c; return &cp` shares *c's
// pointees, not c itself. Two owned arguments then stay owned, as one region, and a foreign one makes
// the other foreign. Anything else in a parameter's region is a leak, and anything else in a result's
// region makes it not fresh.
//
// A write is a write event on the forward pointee chain (fwdHas), not the region: storing a pointer
// to a parameter somewhere does not write it.
func summarise(fn *ssa.Function, g *graph, st *state) *summary {
	s := optimistic(fn, st)
	np := len(fn.Params)
	var links []Link
	// linkTo records that the value at pos (with class n) holds memory of a parameter, and reports
	// whether that is explained: a parameter's own memory, or a link at the depth where the two meet.
	// Chains that do not meet cannot be linked exactly, so that stays a leak (or a result that is not
	// fresh), never a dropped fact.
	linkTo := func(pos int, n *node, param int) bool {
		if pos == param {
			return true
		}
		pn := g.val(fn.Params[param])
		if pn == nil {
			return false
		}
		d, e, ok := g.meetDepth(n, pn)
		if ok {
			links = append(links, newLink(pos, d, param, e))
		}
		return ok
	}
	dirParam, dirNonRegParam, dirFree := computeDirectWrites(fn, st)
	for i, p := range fn.Params {
		if n := g.val(p); n != nil {
			leak := func(e event) bool {
				switch {
				case e.kind == evWrite:
					return false
				case e.kind == evForeign && e.param >= 0:
					return !linkTo(i, n, e.param)
				case e.kind == evReturn:
					// Returned memory of the parameter links it to the result (the result's region
					// holds the parameter's foreign event). An owned parameter has no foreign event to
					// link by, so returning it stays a leak.
					return s.isoParam[i]
				}
				return true
			}
			// What a publishing call hands back (a container returns what it holds) joins the
			// memory of what was published. That is the publish too, not something else done
			// with the parameter.
			publishedAt := map[ssa.Instruction]bool{}
			for c := range g.region(n) {
				for _, e := range c.events {
					if e.kind == evTransfer && e.accountedTo(i) && e.instr != nil {
						publishedAt[e.instr] = true
					}
				}
			}
			// One scan: leak records links as it goes, so it must see each event once.
			s.leakParam[i] = regionScan(g, n, func(e event) bool {
				if !leak(e) {
					return false
				}
				if !e.accountedTo(i) && !(e.kind == evForeign && publishedAt[e.instr]) {
					s.leakBesides[i] = true
				}
				return true
			})
			s.writeParam[i] = dirParam[i]
			s.nonRegWriteParam[i] = dirNonRegParam[i]
			if _, isSig := p.Type().Underlying().(*types.Signature); isSig {
				s.spawnParam[i] = regionHas(g, n, func(e event) bool { return e.kind == evTransfer })
			}
		}
	}
	for j, fv := range fn.FreeVars {
		if n := g.val(fv); n != nil {
			s.leakFree[j] = regionHas(g, n, func(e event) bool {
				switch e.kind {
				case evWrite:
					return false
				case evForeign:
					return e.free != j
				}
				return true
			})
			s.writeFree[j] = dirFree[j]
		}
	}
	for _, b := range fn.Blocks {
		ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return)
		if !ok {
			continue
		}
		for k, r := range ret.Results {
			n := g.val(r)
			if n == nil {
				continue
			}
			for k2 := k + 1; k2 < len(ret.Results); k2++ {
				if n2 := g.val(ret.Results[k2]); n2 != nil {
					if d, e, ok := g.meetDepth(n, n2); ok {
						links = append(links, newLink(np+k, d, np+k2, e))
					}
				}
			}
			if regionScan(g, n, func(e event) bool {
				switch {
				case e.kind == evReturn || e.kind == evWrite:
					return false
				case e.kind == evForeign && e.param >= 0:
					return !linkTo(np+k, n, e.param)
				}
				return true
			}) {
				s.fresh = false
				if k < len(s.freshResult) {
					s.freshResult[k] = false
				}
			}
		}
	}
	s.links = mergeLinks(nil, links)
	s.waitParam, s.waitKind = inferSpawnerWait(fn, s, st)
	if inferSingleSpawn(fn, s, st) {
		s.waitKind |= waitSingleSpawn
	}
	return s
}

// Link says a function joins the memory of two of its parameters or results: positions index the
// parameters followed by the results, and the memory DepthA pointers below A is the memory DepthB
// pointers below B. A is the smaller position.
type Link struct {
	A, DepthA, B, DepthB int
}

func newLink(a, da, b, db int) Link {
	if a > b {
		a, da, b, db = b, db, a, da
	}
	return Link{a, da, b, db}
}

// mergeLinks returns the links of a and b, sorted and without duplicates.
func mergeLinks(a, b []Link) []Link {
	out := slices.Concat(a, b)
	slices.SortFunc(out, func(x, y Link) int {
		return cmp.Or(cmp.Compare(x.A, y.A), cmp.Compare(x.B, y.B), cmp.Compare(x.DepthA, y.DepthA), cmp.Compare(x.DepthB, y.DepthB))
	})
	return slices.Compact(out)
}

// regionScan applies f to every event in n's region and reports whether f returned true for any.
// Unlike regionHas it never stops early: f records links as it goes, and the region is a map, so
// stopping at the first bad event would record a different subset of links on every run.
func regionScan(g *graph, n *node, f func(event) bool) bool {
	found := false
	for c := range g.region(n) {
		for _, e := range c.events {
			if f(e) {
				found = true
			}
		}
	}
	return found
}

func regionHas(g *graph, n *node, bad func(event) bool) bool {
	for c := range g.region(n) {
		for _, e := range c.events {
			if bad(e) {
				return true
			}
		}
	}
	return false
}

// fwdHas reports whether an event bad lies on n's forward pointee chain (pointsTo).
func fwdHas(g *graph, n *node, bad func(event) bool) bool {
	for _, c := range g.pointsTo(n) {
		for _, e := range c.events {
			if bad(e) {
				return true
			}
		}
	}
	return false
}

func sameSummary(a, b *summary) bool {
	return a.fresh == b.fresh && slices.Equal(a.freshResult, b.freshResult) &&
		slices.Equal(a.leakParam, b.leakParam) && slices.Equal(a.spawnParam, b.spawnParam) &&
		slices.Equal(a.leakBesides, b.leakBesides) &&
		slices.Equal(a.leakFree, b.leakFree) &&
		slices.Equal(a.writeParam, b.writeParam) && slices.Equal(a.nonRegWriteParam, b.nonRegWriteParam) &&
		slices.Equal(a.writeFree, b.writeFree) &&
		slices.Equal(a.links, b.links) &&
		a.waitParam == b.waitParam && a.waitKind == b.waitKind
}

// orInto returns next with every flag prev set also set.
func orInto(prev, next []bool) []bool {
	out := append([]bool(nil), next...)
	for i := range out {
		out[i] = out[i] || prev[i]
	}
	return out
}

func andInto(prev, next []bool) []bool {
	out := append([]bool(nil), next...)
	for i := range out {
		if i < len(prev) {
			out[i] = out[i] && prev[i]
		}
	}
	return out
}

// computeSummaries iterates to the greatest fixpoint: every function starts optimistic and only
// ever becomes more pessimistic, so the loop terminates. Functions are evaluated in bottom-up
// post-order and re-queued along intra-package caller edges when a summary changes.
func (st *state) computeSummaries(fns []*ssa.Function) {
	inPkg := make(map[*ssa.Function]bool, len(fns))
	for _, fn := range fns {
		inPkg[fn] = true
		if len(fn.Blocks) == 0 {
			st.sums[fn] = pessimistic
		} else {
			st.sums[fn] = optimistic(fn, st)
		}
	}
	callees := make(map[*ssa.Function][]*ssa.Function, len(fns))
	callers := make(map[*ssa.Function][]*ssa.Function, len(fns))
	var opBuf [8]*ssa.Value
	for _, fn := range fns {
		if len(fn.Blocks) == 0 {
			continue
		}
		seen := map[*ssa.Function]bool{fn: true}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				for _, op := range ins.Operands(opBuf[:0]) {
					if op == nil {
						continue
					}
					var target *ssa.Function
					switch x := (*op).(type) {
					case *ssa.Function:
						target = x
					case *ssa.MakeClosure:
						target, _ = x.Fn.(*ssa.Function)
					}
					if target != nil && strings.HasPrefix(target.Synthetic, "bound method wrapper") {
						if m, ok := target.Object().(*types.Func); ok && !types.IsInterface(m.Signature().Recv().Type()) {
							target = target.Prog.FuncValue(m)
						}
					}
					if target != nil && target.Origin() != nil {
						target = target.Origin()
					}
					if target != nil && inPkg[target] && !seen[target] {
						seen[target] = true
						callees[fn] = append(callees[fn], target)
						callers[target] = append(callers[target], fn)
					}
				}
			}
		}
	}

	order := make([]*ssa.Function, 0, len(fns))
	visited := make(map[*ssa.Function]bool, len(fns))
	var postOrder func(fn *ssa.Function)
	postOrder = func(fn *ssa.Function) {
		if visited[fn] {
			return
		}
		visited[fn] = true
		for _, callee := range callees[fn] {
			postOrder(callee)
		}
		if len(fn.Blocks) > 0 {
			order = append(order, fn)
		}
	}
	for _, fn := range fns {
		postOrder(fn)
	}

	inQueue := make(map[*ssa.Function]bool, len(order))
	queue := make([]*ssa.Function, len(order))
	copy(queue, order)
	for _, fn := range queue {
		inQueue[fn] = true
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		delete(inQueue, fn)
		next := summarise(fn, st.buildGraph(fn), st)
		if !sameSummary(st.sums[fn], next) {
			merged := mergePessimistic(st.sums[fn], next)
			if !sameSummary(st.sums[fn], merged) {
				st.sums[fn] = merged
				for _, caller := range callers[fn] {
					if !inQueue[caller] {
						inQueue[caller] = true
						queue = append(queue, caller)
					}
				}
			} else {
				st.sums[fn] = merged
			}
		}
	}
}

// mergePessimistic keeps the iteration monotone: a fact once lost is never regained.
func mergePessimistic(prev, next *summary) *summary {
	if prev.pessimistic || next.pessimistic {
		return pessimistic
	}
	out := *next
	out.fresh = prev.fresh && next.fresh
	out.freshResult = andInto(prev.freshResult, next.freshResult)
	out.leakParam = orInto(prev.leakParam, next.leakParam)
	out.leakBesides = orInto(prev.leakBesides, next.leakBesides)
	out.spawnParam = orInto(prev.spawnParam, next.spawnParam)
	out.leakFree = orInto(prev.leakFree, next.leakFree)
	out.writeParam = orInto(prev.writeParam, next.writeParam)
	if prev.nonRegWriteParam != nil || next.nonRegWriteParam != nil {
		out.nonRegWriteParam = orInto(prev.nonRegWriteParam, next.nonRegWriteParam)
	}
	out.writeFree = orInto(prev.writeFree, next.writeFree)
	out.links = mergeLinks(prev.links, next.links)
	if anyTrue(prev.spawnParam) {
		if prev.waitParam == -1 {
			out.waitParam = -1
			out.waitKind &^= 3
		}
		if !prev.singleSpawn() {
			out.waitKind &^= waitSingleSpawn
		}
	}
	return &out
}

type originSet struct {
	lo    uint64
	extra []uint64
}

func (o *originSet) set(idx int) bool {
	if idx < 0 {
		return false
	}
	if idx < 64 {
		bit := uint64(1) << uint(idx)
		if (o.lo & bit) != 0 {
			return false
		}
		o.lo |= bit
		return true
	}
	word := (idx - 64) / 64
	bit := uint64(1) << uint((idx-64)%64)
	for len(o.extra) <= word {
		o.extra = append(o.extra, 0)
	}
	if (o.extra[word] & bit) != 0 {
		return false
	}
	o.extra[word] |= bit
	return true
}

func (o originSet) has(idx int) bool {
	if idx < 0 {
		return false
	}
	if idx < 64 {
		return (o.lo & (uint64(1) << uint(idx))) != 0
	}
	word := (idx - 64) / 64
	if word >= len(o.extra) {
		return false
	}
	return (o.extra[word] & (uint64(1) << uint((idx-64)%64))) != 0
}

func (o originSet) isEmpty() bool {
	if o.lo != 0 {
		return false
	}
	for _, w := range o.extra {
		if w != 0 {
			return false
		}
	}
	return true
}

func (o originSet) unionWith(other originSet) (originSet, bool) {
	changed := (other.lo &^ o.lo) != 0
	o.lo |= other.lo
	if len(other.extra) > 0 {
		if len(o.extra) < len(other.extra) {
			cp := make([]uint64, len(other.extra))
			copy(cp, o.extra)
			o.extra = cp
		} else {
			o.extra = slices.Clone(o.extra)
		}
		for i, w := range other.extra {
			if (w &^ o.extra[i]) != 0 {
				changed = true
			}
			o.extra[i] |= w
		}
	}
	return o, changed
}

func isValueStructOrArray(t types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

func isStringByteRuneCopy(dst, src types.Type) bool {
	if dst == nil || src == nil {
		return false
	}
	isStr := func(t types.Type) bool {
		b, ok := t.Underlying().(*types.Basic)
		return ok && b.Info()&types.IsString != 0
	}
	isByteOrRuneSlice := func(t types.Type) bool {
		sl, ok := t.Underlying().(*types.Slice)
		if !ok {
			return false
		}
		b, ok := sl.Elem().Underlying().(*types.Basic)
		return ok && (b.Kind() == types.Byte || b.Kind() == types.Rune)
	}
	return (isStr(dst) && isByteOrRuneSlice(src)) || (isByteOrRuneSlice(dst) && isStr(src))
}

// computeDirectWrites computes directional 2-level (d1, dPlus) write summaries for fn's
// parameters and free variables, eliminating symmetric Steensgaard pollution across unrelated
// closure captures, slice/struct containers, and value-copied structs.
func computeDirectWrites(fn *ssa.Function, st *state) ([]bool, []bool, []bool) {
	np := len(fn.Params)
	nf := len(fn.FreeVars)
	writeParam := make([]bool, np)
	nonRegWriteParam := make([]bool, np)
	writeFree := make([]bool, nf)
	if np+nf == 0 || len(fn.Blocks) == 0 {
		return writeParam, nonRegWriteParam, writeFree
	}

	trackable := func(v ssa.Value) bool {
		if v == nil {
			return false
		}
		if _, guarded := st.localGuards[v]; guarded {
			return false
		}
		switch x := v.(type) {
		case *ssa.Const, *ssa.Function, *ssa.Builtin, *ssa.Global:
			return false
		case *ssa.MakeInterface:
			return !st.cls.Shareable(x.Type()) || !st.cls.Shareable(x.X.Type())
		default:
			return !st.cls.Shareable(v.Type())
		}
	}

	d1 := map[ssa.Value]originSet{}
	dPlus := map[ssa.Value]originSet{}

	for i, p := range fn.Params {
		if !trackable(p) {
			continue
		}
		var o originSet
		o.set(i)
		if isValueStructOrArray(p.Type()) {
			dPlus[p] = o
		} else {
			d1[p] = o
			dPlus[p] = o
		}
	}
	for j, fv := range fn.FreeVars {
		if !trackable(fv) {
			continue
		}
		var o originSet
		o.set(np + j)
		if isValueStructOrArray(fv.Type()) {
			dPlus[fv] = o
		} else {
			d1[fv] = o
			dPlus[fv] = o
		}
	}

	addD1 := func(dst ssa.Value, bits originSet) bool {
		if bits.isEmpty() || !trackable(dst) {
			return false
		}
		next, ch := d1[dst].unionWith(bits)
		if ch {
			d1[dst] = next
		}
		return ch
	}
	addDPlus := func(dst ssa.Value, bits originSet) bool {
		if bits.isEmpty() || !trackable(dst) {
			return false
		}
		next, ch := dPlus[dst].unionWith(bits)
		if ch {
			dPlus[dst] = next
		}
		return ch
	}
	bothOf := func(v ssa.Value) originSet {
		res, _ := d1[v].unionWith(dPlus[v])
		return res
	}
	propSame := func(dst, src ssa.Value) bool {
		c1 := addD1(dst, d1[src])
		c2 := addDPlus(dst, dPlus[src])
		return c1 || c2
	}
	propBaseDPlus := func(addr ssa.Value, bits originSet) bool {
		if bits.isEmpty() {
			return false
		}
		changed := false
		cur := addr
		for cur != nil {
			if addDPlus(cur, bits) {
				changed = true
			}
			switch x := cur.(type) {
			case *ssa.FieldAddr:
				cur = x.X
			case *ssa.IndexAddr:
				cur = x.X
			case *ssa.Slice:
				cur = x.X
			case *ssa.ChangeType:
				cur = x.X
			default:
				cur = nil
			}
		}
		return changed
	}

	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				switch x := ins.(type) {
				case *ssa.Phi:
					for _, e := range x.Edges {
						if propSame(x, e) {
							changed = true
						}
					}
				case *ssa.FieldAddr:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.IndexAddr:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.Field:
					if isValueStructOrArray(x.Type()) {
						if addDPlus(x, bothOf(x.X)) {
							changed = true
						}
					} else {
						if addD1(x, bothOf(x.X)) {
							changed = true
						}
						if addDPlus(x, dPlus[x.X]) {
							changed = true
						}
					}
				case *ssa.Index:
					if isValueStructOrArray(x.Type()) {
						if addDPlus(x, bothOf(x.X)) {
							changed = true
						}
					} else {
						if addD1(x, bothOf(x.X)) {
							changed = true
						}
						if addDPlus(x, dPlus[x.X]) {
							changed = true
						}
					}
				case *ssa.Slice:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.ChangeType:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.SliceToArrayPointer:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.Range:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.MakeInterface:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.ChangeInterface:
					if propSame(x, x.X) {
						changed = true
					}
				case *ssa.TypeAssert:
					if x.CommaOk {
						if addD1(x, d1[x.X]) {
							changed = true
						}
						if addDPlus(x, dPlus[x.X]) {
							changed = true
						}
					} else if isValueStructOrArray(x.Type()) {
						if addDPlus(x, bothOf(x.X)) {
							changed = true
						}
					} else if propSame(x, x.X) {
						changed = true
					}
				case *ssa.Convert:
					if !isStringByteRuneCopy(x.Type(), x.X.Type()) {
						if propSame(x, x.X) {
							changed = true
						}
					}
				case *ssa.MultiConvert:
					if !isStringByteRuneCopy(x.Type(), x.X.Type()) {
						if propSame(x, x.X) {
							changed = true
						}
					}
				case *ssa.Extract:
					if c, ok := x.Tuple.(*ssa.Call); ok && !c.Common().IsInvoke() && c.Common().StaticCallee() != nil {
						continue
					}
					switch tup := x.Tuple.(type) {
					case *ssa.Lookup, *ssa.Next:
						if isValueStructOrArray(x.Type()) {
							if addDPlus(x, bothOf(tup)) {
								changed = true
							}
						} else {
							if addD1(x, bothOf(tup)) {
								changed = true
							}
							if addDPlus(x, dPlus[tup]) {
								changed = true
							}
						}
					case *ssa.TypeAssert:
						if isValueStructOrArray(x.Type()) {
							if addDPlus(x, bothOf(tup)) {
								changed = true
							}
						} else if propSame(x, tup) {
							changed = true
						}
					default:
						if propSame(x, x.Tuple) {
							changed = true
						}
					}
				case *ssa.UnOp:
					if x.Op == token.MUL {
						if _, guarded := st.slotOf(x.X); !guarded {
							if isValueStructOrArray(x.Type()) {
								if addDPlus(x, bothOf(x.X)) {
									changed = true
								}
							} else {
								if addD1(x, bothOf(x.X)) {
									changed = true
								}
								if addDPlus(x, dPlus[x.X]) {
									changed = true
								}
							}
						}
					}
				case *ssa.Lookup:
					if _, guarded := st.slotOf(x.X); !guarded {
						if x.CommaOk {
							if propSame(x, x.X) {
								changed = true
							}
						} else if isValueStructOrArray(x.Type()) {
							if addDPlus(x, bothOf(x.X)) {
								changed = true
							}
						} else {
							if addD1(x, bothOf(x.X)) {
								changed = true
							}
							if addDPlus(x, dPlus[x.X]) {
								changed = true
							}
						}
					}
				case *ssa.Next:
					if propSame(x, x.Iter) {
						changed = true
					}
				case *ssa.Store:
					if trackable(x.Val) {
						if propBaseDPlus(x.Addr, bothOf(x.Val)) {
							changed = true
						}
					}
				case *ssa.MapUpdate:
					for _, mv := range []ssa.Value{x.Key, x.Value} {
						if trackable(mv) {
							if propBaseDPlus(x.Map, bothOf(mv)) {
								changed = true
							}
						}
					}
				case *ssa.MakeClosure:
					mcFn, _ := x.Fn.(*ssa.Function)
					if mcFn == nil {
						for _, bind := range x.Bindings {
							if addDPlus(x, bothOf(bind)) {
								changed = true
							}
						}
					} else {
						cs := st.summary(mcFn)
						for j, bind := range x.Bindings {
							if cs.writesFree(j) {
								if addDPlus(x, bothOf(bind)) {
									changed = true
								}
							}
						}
					}
				case *ssa.Call:
					if propCallDirectFlow(x, &x.Call, x, st, dPlus, addD1, addDPlus, bothOf, propSame, propBaseDPlus) {
						changed = true
					}
				case *ssa.Defer:
					if propCallDirectFlow(x, &x.Call, nil, st, dPlus, addD1, addDPlus, bothOf, propSame, propBaseDPlus) {
						changed = true
					}
				}
			}
		}
	}

	var written originSet
	var writtenNonReg originSet
	markVal := func(v ssa.Value, deep bool) {
		if v == nil {
			return
		}
		if _, guarded := st.slotOf(v); guarded {
			return
		}
		if deep {
			written, _ = written.unionWith(bothOf(v))
			writtenNonReg, _ = writtenNonReg.unionWith(bothOf(v))
		} else {
			written, _ = written.unionWith(d1[v])
			writtenNonReg, _ = writtenNonReg.unionWith(d1[v])
		}
	}
	markValRegOnly := func(v ssa.Value) {
		if v == nil {
			return
		}
		if _, guarded := st.slotOf(v); guarded {
			return
		}
		written, _ = written.unionWith(bothOf(v))
	}

	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if w, ok := writeOf(ins); ok {
				markVal(w.addr, false)
			}
			switch x := ins.(type) {
			case *ssa.Go:
				cc := x.Common()
				callee := cc.StaticCallee()
				var gSum *summary
				if callee != nil && !cc.IsInvoke() {
					gSum = st.summary(callee)
				}
				for i, a := range cc.Args {
					if gSum == nil || gSum.writes(i) {
						markVal(a, true)
					}
				}
				markVal(cc.Value, true)
			case *ssa.Call:
				collectCallWrites(fn, x, &x.Call, st, markVal, markValRegOnly)
			case *ssa.Defer:
				collectCallWrites(fn, x, &x.Call, st, markVal, markValRegOnly)
			}
		}
	}

	for i := range np {
		writeParam[i] = written.has(i)
		nonRegWriteParam[i] = writtenNonReg.has(i)
	}
	for j := range nf {
		writeFree[j] = written.has(np + j)
	}
	return writeParam, nonRegWriteParam, writeFree
}

func propCallDirectFlow(
	ins ssa.Instruction,
	cc *ssa.CallCommon,
	result ssa.Value,
	st *state,
	dPlus map[ssa.Value]originSet,
	addD1 func(ssa.Value, originSet) bool,
	addDPlus func(ssa.Value, originSet) bool,
	bothOf func(ssa.Value) originSet,
	propSame func(ssa.Value, ssa.Value) bool,
	propBaseDPlus func(ssa.Value, originSet) bool,
) bool {
	if cc.IsInvoke() {
		return false
	}
	if b, ok := cc.Value.(*ssa.Builtin); ok {
		changed := false
		switch b.Name() {
		case "append":
			if result != nil && len(cc.Args) >= 1 {
				if propSame(result, cc.Args[0]) {
					changed = true
				}
				if len(cc.Args) == 2 && !st.cls.Shareable(elemType(cc.Args[0].Type())) {
					if addDPlus(result, bothOf(cc.Args[1])) {
						changed = true
					}
				}
			}
		case "copy":
			if len(cc.Args) == 2 && !st.cls.Shareable(elemType(cc.Args[0].Type())) {
				if propBaseDPlus(cc.Args[0], bothOf(cc.Args[1])) {
					changed = true
				}
			}
		case "ssa:wrapnilchk":
			if result != nil && len(cc.Args) > 0 {
				if propSame(result, cc.Args[0]) {
					changed = true
				}
			}
		}
		return changed
	}
	callee := cc.StaticCallee()
	if callee == nil {
		return false
	}
	if isCapBorrowHelper(callee) && result != nil && len(cc.Args) > 0 {
		return propSame(result, cc.Args[0])
	}
	sum := st.summary(callee)
	if len(sum.links) == 0 {
		return false
	}
	spawned := -1
	if sp, ok := spawnOf(ins); ok && sp.via != "" {
		spawned = sp.arg
	}
	nArgs := len(cc.Args)
	nTotal := nArgs + cc.Signature().Results().Len()
	valAt := func(pos int) ssa.Value {
		if pos == spawned || pos < 0 || pos >= nTotal || (pos < nArgs && sum.spawns(pos)) {
			return nil
		}
		if pos < nArgs {
			return cc.Args[pos]
		}
		if result == nil {
			return nil
		}
		if cc.Signature().Results().Len() > 1 {
			return extractOfResult(result, pos-nArgs)
		}
		return result
	}
	changed := false
	for _, l := range sum.links {
		va, vb := valAt(l.A), valAt(l.B)
		if va == nil || vb == nil {
			continue
		}
		switch {
		case l.A < nArgs && l.B >= nArgs:
			// Argument linked into result (e.g. s.clone(), s.First(), append-like wrapper).
			srcA := bothOf(va)
			if l.DepthA >= 1 {
				srcA = dPlus[va]
			}
			if l.DepthB >= 1 || isValueStructOrArray(vb.Type()) {
				if addDPlus(vb, srcA) {
					changed = true
				}
			} else {
				if addD1(vb, srcA) {
					changed = true
				}
				if addDPlus(vb, srcA) {
					changed = true
				}
			}
		case l.A < nArgs && l.B < nArgs:
			if l.DepthA == 0 && l.DepthB == 0 {
				if propSame(va, vb) {
					changed = true
				}
				if propSame(vb, va) {
					changed = true
				}
			} else {
				srcB := bothOf(vb)
				if l.DepthB >= 1 {
					srcB = dPlus[vb]
				}
				srcA := bothOf(va)
				if l.DepthA >= 1 {
					srcA = dPlus[va]
				}
				if l.DepthA >= 1 {
					if propBaseDPlus(va, srcB) {
						changed = true
					}
				} else {
					if addD1(va, srcB) {
						changed = true
					}
					if addDPlus(va, srcB) {
						changed = true
					}
				}
				if l.DepthB >= 1 {
					if propBaseDPlus(vb, srcA) {
						changed = true
					}
				} else {
					if addD1(vb, srcA) {
						changed = true
					}
					if addDPlus(vb, srcA) {
						changed = true
					}
				}
			}
		case l.A >= nArgs && l.B >= nArgs:
			if propSame(va, vb) {
				changed = true
			}
			if propSame(vb, va) {
				changed = true
			}
		}
	}
	return changed
}

func collectCallWrites(
	caller *ssa.Function,
	ins ssa.Instruction,
	cc *ssa.CallCommon,
	st *state,
	markVal func(ssa.Value, bool),
	markValRegOnly func(ssa.Value),
) {
	if _, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
		return
	}
	if cc.IsInvoke() {
		// Try resolving concrete receiver types first (either in-function or closed struct field).
		if methodSums, resolved := st.resolveInvokeMethodSummaries(caller, cc); resolved {
			for _, ms := range methodSums {
				if ms.writes(0) {
					markVal(cc.Value, true)
				}
				for i, a := range cc.Args {
					if ms.writes(i + 1) {
						markVal(a, true)
					}
				}
			}
			return
		}
		rc, hasContract, cut := st.resolveCallContractCut(caller, cc)
		_ = cut
		markVal(cc.Value, true)
		if !hasContract || !rc.suppressWrite {
			for _, a := range cc.Args {
				markVal(a, true)
			}
		}
		return
	}
	callee := cc.StaticCallee()
	if callee == nil {
		if mc := rangeOverFuncClosure(caller, ins, cc); mc != nil {
			if mcFn, ok := mc.Fn.(*ssa.Function); ok && mcFn != nil {
				ys := st.summary(mcFn)
				for j, b := range mc.Bindings {
					if ys.writesFree(j) {
						markVal(b, true)
					}
				}
			}
			markVal(cc.Value, true)
			return
		}
		targets, rest := callTargets(cc.Value)
		if len(targets) == 0 && len(rest) == 1 && rest[0] == cc.Value {
			if fvTargets, ok := st.resolveFreeVarCallables(cc.Value); ok {
				targets = fvTargets
				rest = nil
			}
		}
		if len(rest) == 0 && len(targets) > 0 {
			for _, t := range targets {
				ts := st.summary(t.fn)
				if mfn, recv := methodValue(t); mfn != nil {
					ms := st.summary(mfn)
					if ms.writes(0) {
						markVal(recv, true)
					}
					for i, a := range cc.Args {
						if ms.writes(i + 1) {
							markVal(a, true)
						}
					}
					continue
				}
				if t.mc != nil {
					for j, b := range t.mc.Bindings {
						if ts.writesFree(j) {
							markVal(b, true)
						}
					}
				}
				for i, a := range cc.Args {
					if ts.writes(i) {
						markVal(a, true)
					}
				}
			}
			return
		}
		if fTargets, ok := st.resolveLoadedFieldCallables(cc.Value); ok {
			for _, tfn := range fTargets {
				ts := st.summary(tfn)
				for i, a := range cc.Args {
					if ts.writes(i) {
						markVal(a, true)
					}
				}
			}
			return
		}
		rc, hasContract, cut := st.resolveCallContractCut(caller, cc)
		_ = cut
		if !hasContract || !rc.suppressWrite {
			for _, a := range cc.Args {
				markVal(a, true)
			}
		}
		markVal(cc.Value, true)
		return
	}
	if isCapMove(callee) || isCapBorrowHelper(callee) {
		return
	}
	rc, hasContract, cut := st.resolveCallContractCut(caller, cc)
	_ = cut
	if hasContract && rc.suppressWrite {
		return
	}
	spawned := -1
	isHTTPReg := false
	if sp, ok := spawnOf(ins); ok && sp.via != "" {
		spawned = sp.arg
		isHTTPReg = sp.repeated && strings.Contains(sp.via, "net/http.")
	}
	sum := st.summary(callee)
	if fname := calleeFullName(callee); formatCallees[fname] {
		for i, a := range cc.Args {
			if i == spawned || sum.spawns(i) {
				markVal(a, true)
				continue
			}
			if !sum.writes(i) {
				continue
			}
			if i == 0 && strings.HasPrefix(fname, "fmt.Append") {
				markVal(a, true)
				continue
			}
			for _, op := range formatOperands(a, ins) {
				if typeCallsFormatMethod(op.Type(), map[types.Type]bool{}) {
					markVal(op, true)
				}
			}
		}
		return
	}
	for i, a := range cc.Args {
		if i == spawned {
			if isHTTPReg {
				markValRegOnly(a)
			} else {
				markVal(a, true)
			}
			continue
		}
		if sum.spawns(i) || sum.writesNonReg(i) {
			markVal(a, true)
		} else if sum.writes(i) {
			markValRegOnly(a)
		}
	}
}

// inferSpawnerWait detects wrapper spawners such as SafeGoWG(ctx, wg, name, fn) that take a
// *sync.WaitGroup (waitKind=1) or *errgroup.Group (waitKind=2) parameter w and run every spawned
// goroutine on w!=nil paths under w's Wait() join contract.
func inferSpawnerWait(fn *ssa.Function, s *summary, st *state) (int, uint8) {
	if !anyTrue(s.spawnParam) || len(fn.Blocks) == 0 {
		return -1, 0
	}
	for wIdx, wp := range fn.Params {
		wk := waitGroupParamKind(wp.Type())
		if wk == 0 {
			continue
		}
		nilBlocks := nilGuardedBlocks(fn, wp)
		joinedSpawns := 0
		allJoined := true
		for _, b := range fn.Blocks {
			if nilBlocks[b.Index] {
				continue
			}
			for _, ins := range b.Instrs {
				switch x := ins.(type) {
				case *ssa.Go:
					allJoined = false
				case *ssa.Call:
					if sp, ok := spawnOf(x); ok && sp.via != "" {
						if isDirectGroupSpawnOn(x, wp, wk) {
							joinedSpawns++
						} else {
							allJoined = false
						}
						continue
					}
					if callee := x.Call.StaticCallee(); callee != nil && !x.Call.IsInvoke() {
						cs := st.summary(callee)
						if anyTrue(cs.spawnParam) {
							if cs.waitParam >= 0 && cs.waitParam < len(x.Call.Args) && cs.waitGroupKind() == wk && x.Call.Args[cs.waitParam] == wp {
								joinedSpawns++
							} else {
								allJoined = false
							}
						}
					}
				}
			}
			if !allJoined {
				break
			}
		}
		if allJoined && joinedSpawns > 0 {
			return wIdx, wk
		}
	}
	return -1, 0
}

// inferSingleSpawn reports whether fn starts each of its spawned callback parameters at most once
// per call to fn (never in a loop, never twice on one path, and never via a repeated spawner).
func inferSingleSpawn(fn *ssa.Function, s *summary, st *state) bool {
	if len(fn.Blocks) == 0 || !anyTrue(s.spawnParam) {
		return false
	}
	var sites []ssa.Instruction
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			switch x := ins.(type) {
			case *ssa.Go:
				sites = append(sites, x)
			case *ssa.Call:
				if sp, ok := spawnOf(x); ok && sp.via != "" {
					if sp.repeated {
						return false
					}
					sites = append(sites, x)
					continue
				}
				if callee := x.Call.StaticCallee(); callee != nil && !x.Call.IsInvoke() {
					cs := st.summary(callee)
					if anyTrue(cs.spawnParam) {
						if !cs.singleSpawn() {
							return false
						}
						sites = append(sites, x)
					}
				}
			}
		}
	}
	if len(sites) == 0 {
		return false
	}
	for _, a := range sites {
		after := reachableAfter(a)
		for _, b := range sites {
			if after(b) {
				return false
			}
		}
	}
	isSite := func(ins ssa.Instruction) bool {
		return slices.Contains(sites, ins)
	}
	for cbIdx, isSp := range s.spawnParam {
		if !isSp {
			continue
		}
		if cbIdx >= len(fn.Params) || !paramSpawnedAtMostOnce(fn.Params[cbIdx], isSite, st) {
			return false
		}
	}
	return true
}

func paramSpawnedAtMostOnce(param *ssa.Parameter, isSite func(ssa.Instruction) bool, st *state) bool {
	if param.Referrers() == nil {
		return false
	}
	for _, r := range *param.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return false
			}
		case *ssa.Go:
			if !isSite(x) {
				return false
			}
			if x.Call.Value == param {
				if usedAsArg(x, param, -1) {
					return false
				}
				continue
			}
			callee := x.Call.StaticCallee()
			if callee == nil || x.Call.IsInvoke() {
				return false
			}
			nPassed := 0
			for k, a := range x.Call.Args {
				if a == param {
					if !synchronousCallsAtMostOnce(callee, k, st, map[*ssa.Function]bool{}) {
						return false
					}
					nPassed++
				}
			}
			if nPassed != 1 {
				return false
			}
		case *ssa.Call:
			if !isSite(x) {
				return false
			}
			if sp, ok := spawnOf(x); ok && sp.via != "" {
				if sp.fn != param || usedAsArg(x, param, sp.arg) {
					return false
				}
				continue
			}
			callee := x.Call.StaticCallee()
			if callee == nil {
				return false
			}
			cs := st.summary(callee)
			nPassed := 0
			for k, a := range x.Call.Args {
				if a == param {
					if !cs.spawns(k) {
						return false
					}
					nPassed++
				}
			}
			if nPassed != 1 || x.Call.Value == param {
				return false
			}
		case *ssa.MakeClosure:
			if !singleSpawnClosureOK(x, param, isSite, st) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func singleSpawnClosureOK(mc *ssa.MakeClosure, param ssa.Value, isSite func(ssa.Instruction) bool, st *state) bool {
	if mc.Referrers() == nil {
		return false
	}
	var start ssa.Instruction
	for _, r := range *mc.Referrers() {
		if _, dbg := r.(*ssa.DebugRef); dbg {
			continue
		}
		if start != nil || !isSite(r) {
			return false
		}
		switch x := r.(type) {
		case *ssa.Go:
			if x.Call.Value != mc || usedAsArg(x, mc, -1) {
				return false
			}
		case *ssa.Call:
			if sp, ok := spawnOf(x); ok && sp.via != "" {
				if sp.fn != mc || usedAsArg(x, mc, sp.arg) {
					return false
				}
			} else {
				callee := x.Call.StaticCallee()
				if callee == nil {
					return false
				}
				cs := st.summary(callee)
				nPassed := 0
				for k, a := range x.Call.Args {
					if a == mc {
						if !cs.spawns(k) {
							return false
						}
						nPassed++
					}
				}
				if nPassed != 1 || x.Call.Value == mc {
					return false
				}
			}
		default:
			return false
		}
		start = r
	}
	if start == nil {
		return false
	}
	subFn, ok := mc.Fn.(*ssa.Function)
	if !ok || len(subFn.Blocks) == 0 {
		return false
	}
	for j, bind := range mc.Bindings {
		if bind != param {
			continue
		}
		if j >= len(subFn.FreeVars) || !valCalledSyncAtMostOnce(subFn, subFn.FreeVars[j], st, map[*ssa.Function]bool{}) {
			return false
		}
	}
	return true
}

func synchronousCallsAtMostOnce(fn *ssa.Function, paramIdx int, st *state, seen map[*ssa.Function]bool) bool {
	if fn == nil || len(fn.Blocks) == 0 || paramIdx < 0 || paramIdx >= len(fn.Params) || seen[fn] {
		return false
	}
	seen[fn] = true
	defer delete(seen, fn)
	return valCalledSyncAtMostOnce(fn, fn.Params[paramIdx], st, seen)
}

func valCalledSyncAtMostOnce(fn *ssa.Function, v ssa.Value, st *state, seen map[*ssa.Function]bool) bool {
	if v == nil || v.Referrers() == nil {
		return false
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			switch x := ins.(type) {
			case *ssa.Go:
				return false
			case *ssa.Call:
				if _, ok := spawnOf(x); ok {
					return false
				}
				if callee := x.Call.StaticCallee(); callee != nil && !x.Call.IsInvoke() && anyTrue(st.summary(callee).spawnParam) {
					return false
				}
			}
		}
	}
	var actionSites []ssa.Instruction
	for _, r := range *v.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return false
			}
		case *ssa.Call:
			if x.Call.Value == v {
				if usedAsArg(x, v, -1) {
					return false
				}
				actionSites = append(actionSites, x)
				continue
			}
			callee := x.Call.StaticCallee()
			if callee == nil || x.Call.IsInvoke() {
				return false
			}
			nPassed := 0
			for k, a := range x.Call.Args {
				if a == v {
					if !synchronousCallsAtMostOnce(callee, k, st, seen) {
						return false
					}
					nPassed++
				}
			}
			if nPassed != 1 {
				return false
			}
			actionSites = append(actionSites, x)
		case *ssa.Defer:
			if x.Call.Value == v {
				if usedAsArg(x, v, -1) {
					return false
				}
				actionSites = append(actionSites, x)
				continue
			}
			callee := x.Call.StaticCallee()
			if callee == nil || x.Call.IsInvoke() {
				return false
			}
			nPassed := 0
			for k, a := range x.Call.Args {
				if a == v {
					if !synchronousCallsAtMostOnce(callee, k, st, seen) {
						return false
					}
					nPassed++
				}
			}
			if nPassed != 1 {
				return false
			}
			actionSites = append(actionSites, x)
		case *ssa.MakeClosure:
			if x.Referrers() == nil {
				return false
			}
			var callSite ssa.Instruction
			for _, cr := range *x.Referrers() {
				if _, dbg := cr.(*ssa.DebugRef); dbg {
					continue
				}
				if callSite != nil {
					return false
				}
				switch cx := cr.(type) {
				case *ssa.Call:
					if cx.Call.Value != x || usedAsArg(cx, x, -1) {
						return false
					}
					callSite = cx
				case *ssa.Defer:
					if cx.Call.Value != x || usedAsArg(cx, x, -1) {
						return false
					}
					callSite = cx
				default:
					return false
				}
			}
			subFn, ok := x.Fn.(*ssa.Function)
			if callSite == nil || !ok || len(subFn.Blocks) == 0 || seen[subFn] {
				return false
			}
			seen[subFn] = true
			for j, bind := range x.Bindings {
				if bind == v {
					if j >= len(subFn.FreeVars) || !valCalledSyncAtMostOnce(subFn, subFn.FreeVars[j], st, seen) {
						delete(seen, subFn)
						return false
					}
				}
			}
			delete(seen, subFn)
			actionSites = append(actionSites, callSite)
		default:
			return false
		}
	}
	for _, a := range actionSites {
		after := reachableAfter(a)
		for _, b := range actionSites {
			if after(b) {
				return false
			}
		}
	}
	return true
}

func waitGroupParamKind(t types.Type) uint8 {
	ptr, ok := t.Underlying().(*types.Pointer)
	if !ok {
		return 0
	}
	named, ok := types.Unalias(ptr.Elem()).(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return 0
	}
	pkgPath := named.Obj().Pkg().Path()
	name := named.Obj().Name()
	if pkgPath == "sync" && name == "WaitGroup" {
		return 1
	}
	if pkgPath == "golang.org/x/sync/errgroup" && name == "Group" {
		return 2
	}
	return 0
}

func isDirectGroupSpawnOn(call *ssa.Call, wp *ssa.Parameter, wk uint8) bool {
	callee := call.Call.StaticCallee()
	if callee == nil || call.Call.IsInvoke() || len(call.Call.Args) < 2 || call.Call.Args[0] != wp {
		return false
	}
	obj, ok := callee.Object().(*types.Func)
	if !ok {
		return false
	}
	switch obj.Origin().FullName() {
	case "(*sync.WaitGroup).Go":
		return wk == 1
	case "(*golang.org/x/sync/errgroup.Group).Go", "(*golang.org/x/sync/errgroup.Group).TryGo":
		return wk == 2
	}
	return false
}

// nilGuardedBlocks marks blocks reachable ONLY when param == nil (`if wg == nil { ...; return }`).
func nilGuardedBlocks(fn *ssa.Function, param *ssa.Parameter) []bool {
	nonNilReach := make([]bool, len(fn.Blocks))
	var walk func(b *ssa.BasicBlock)
	walk = func(b *ssa.BasicBlock) {
		if nonNilReach[b.Index] {
			return
		}
		nonNilReach[b.Index] = true
		if len(b.Instrs) > 0 && len(b.Succs) == 2 {
			if br, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If); ok {
				if nilSucc := nilBranchSuccIdx(br.Cond, param); nilSucc >= 0 {
					walk(b.Succs[1-nilSucc])
					return
				}
			}
		}
		for _, succ := range b.Succs {
			walk(succ)
		}
	}
	walk(fn.Blocks[0])
	onlyNil := make([]bool, len(fn.Blocks))
	for i := range onlyNil {
		onlyNil[i] = !nonNilReach[i]
	}
	return onlyNil
}

func nilBranchSuccIdx(cond ssa.Value, param *ssa.Parameter) int {
	base, neg := unwrapBoolCond(cond)
	bin, ok := base.(*ssa.BinOp)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
		return -1
	}
	isNilConst := func(v ssa.Value) bool {
		c, ok := v.(*ssa.Const)
		return ok && c.IsNil()
	}
	matched := (bin.X == param && isNilConst(bin.Y)) || (bin.Y == param && isNilConst(bin.X))
	if !matched {
		return -1
	}
	isEq := bin.Op == token.EQL
	if neg {
		isEq = !isEq
	}
	if isEq {
		return 0 // Succs[0] is true branch (`param == nil`)
	}
	return 1 // Succs[1] is false branch (`param == nil`)
}
