package ownership

import (
	"cmp"
	"encoding/binary"
	"errors"
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
	// variable j.
	writeParam  []bool
	writeFree   []bool
	fresh       bool
	freshResult []bool
	// links are the parameters and results the function joins (FuncFact.Links).
	links    []Link
	acquires []acquire
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

func (s *summary) isIso(i int) bool { return i < len(s.isoParam) && s.isoParam[i] }

func (s *summary) spawns(i int) bool { return i < len(s.spawnParam) && s.spawnParam[i] }

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

var pessimistic = &summary{pessimistic: true}

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

		writeParam:  make([]bool, len(fn.Params)),
		writeFree:   make([]bool, len(fn.FreeVars)),
		fresh:       true,
		freshResult: fr,
	}
	for i, p := range fn.Params {
		s.isoParam[i] = st.isIsoParam(p)
	}
	return s
}

// summary returns what gosree knows about calling fn.
func (st *state) summary(fn *ssa.Function) *summary {
	if o := fn.Origin(); o != nil {
		fn = o
	}
	if s := st.summaryRead(fn); s != nil {
		return s
	}
	obj, ok := fn.Object().(*types.Func)
	if ok && (isBoundMethodWrapper(fn) || isMethodThunk(fn)) && obj.Signature().Recv() != nil && !types.IsInterface(obj.Signature().Recv().Type()) {
		origObj := obj.Origin()
		nParams := len(fn.Params)
		nFree := len(fn.FreeVars)
		if c, has := capContracts[origObj.FullName()]; has && c.freshResult {
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
			return &summary{leakParam: leaks, writeParam: writes, leakFree: freeLeaks, writeFree: freeWrites, fresh: true}
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
			return &summary{leakParam: leaks, writeParam: make([]bool, nParams), leakFree: freeLeaks, writeFree: make([]bool, nFree)}
		}
	}
	if m := methodBehind(fn); m != nil {
		return wrapperSummary(fn, m, st.summary(m))
	}
	// Functions of other packages are built from type information; other synthetic functions
	// have their own parameter layout and get no summary.
	if !ok || (fn.Synthetic != "" && !strings.HasPrefix(fn.Synthetic, "from type information")) {
		return pessimistic
	}
	obj = obj.Origin()
	if obj.Pkg() == st.pass.Pkg {
		return pessimistic
	}
	if o, ok := kinds.FuncOverrideFor(obj.FullName()); ok {
		// An assumption replaces the computed summary (internal/kinds/funcs.go).
		return &summary{leakParam: o.Leaks, writeParam: o.Writes, fresh: o.Fresh}
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
			return &summary{leakParam: leaks, writeParam: writes, fresh: true}
		}
		if obj.Pkg().Path() == atomicPkg {
			return atomicSummary(kinds.Params(obj))
		}
		return pessimistic
	}
	s := &summary{
		leakParam:   f.Leaks,
		isoParam:    f.Iso,
		spawnParam:  f.Spawns,
		writeParam:  f.Writes,
		fresh:       f.Fresh,
		freshResult: f.FreshResult,
		links:       f.Links,
		acquires:    f.Acquires,
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

// wrapperSummary is the summary of a method value's wrapper or a method expression's thunk w, from
// the summary ms of its method: the same facts, with the parameters lined up. A thunk has the
// method's parameters. A wrapper has them without the receiver, which is its one free variable.
//
// A link that involves the receiver is kept only for a thunk whose first parameter is the
// method's receiver as it stands. A wrapper has no position for its receiver, and a wrapper or
// thunk of a promoted method, or of a value method reached through a pointer, gets to the
// method's receiver from its own by a field or a load, so the depths would not be the method's.
// What such a link joined is then taken the safe way: a parameter joined to the receiver leaks, a
// result joined to it is not fresh, and the receiver itself is retained.
func wrapperSummary(w, method *ssa.Function, ms *summary) *summary {
	if ms.pessimistic {
		return pessimistic
	}
	shift := 0
	if isBoundMethodWrapper(w) {
		shift = 1
	}
	n := len(w.Params)
	s := &summary{
		leakParam:   make([]bool, n),
		leakBesides: make([]bool, n),
		isoParam:    make([]bool, n),
		spawnParam:  make([]bool, n),
		writeParam:  make([]bool, n),
		leakFree:    make([]bool, len(w.FreeVars)),
		writeFree:   make([]bool, len(w.FreeVars)),
		fresh:       ms.fresh,
		freshResult: slices.Clone(ms.freshResult),
	}
	for i := range n {
		s.leakParam[i] = ms.leaks(i + shift)
		s.leakBesides[i] = ms.leaksBesidesContract(i + shift)
		s.isoParam[i] = ms.isIso(i + shift)
		s.spawnParam[i] = ms.spawns(i + shift)
		s.writeParam[i] = ms.writes(i + shift)
	}
	keepReceiver := func() {
		switch {
		case shift == 1 && len(s.leakFree) > 0:
			s.leakFree[0] = true
		case shift == 0 && n > 0:
			s.leakParam[0] = true
		}
	}
	if shift == 1 && len(s.leakFree) > 0 {
		// A receiver the method owns or starts a goroutine with is handed over when the value is
		// called; a captured variable has no such fact, so it is retained.
		s.leakFree[0] = ms.leaks(0) || ms.isIso(0) || ms.spawns(0)
		s.writeFree[0] = ms.writes(0)
	}
	sameReceiver := shift == 0 && n > 0 && len(method.Params) > 0 && types.Identical(w.Params[0].Type(), method.Params[0].Type())
	var links []Link
	for _, l := range ms.links {
		if l.A != 0 || sameReceiver {
			links = append(links, newLink(l.A-shift, l.DepthA, l.B-shift, l.DepthB))
			continue
		}
		// l.A is the receiver, and A is the smaller position: l.B is another parameter or a result.
		keepReceiver()
		if other := l.B - shift; other < n {
			if other >= 0 {
				s.leakParam[other] = true
			}
		} else {
			s.fresh = false
			if k := other - n; k < len(s.freshResult) {
				s.freshResult[k] = false
			}
		}
	}
	s.links = mergeLinks(nil, links)
	return s
}

// summaryRead returns the summary worked out for a function of this package, or nil, and records
// who read it. While the summaries are worked out a function's summary depends on every summary
// its graph read, and that is not only those of the functions its body names: a call of a
// variable is answered by the functions the variable may hold, which may be named in another
// function or in a package variable's initialiser. Whoever read a summary is worked out again
// when it changes.
func (st *state) summaryRead(fn *ssa.Function) *summary {
	s, ok := st.sums[fn]
	if !ok {
		return nil
	}
	if st.sumFor != nil && st.sumFor != fn {
		readers := st.sumReaders[fn]
		if readers == nil {
			readers = map[*ssa.Function]bool{}
			st.sumReaders[fn] = readers
		}
		readers[st.sumFor] = true
	}
	return s
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
	return &summary{leakParam: leaks, writeParam: make([]bool, n)}
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
	isWrite := func(e event) bool { return e.kind == evWrite }
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
			//
			// Nor is what a forward does with another of its arguments: the call that hands this
			// parameter to a function this function was given hands the others to it too, each
			// accounted to its own parameter and decided at each call of this function. Where the
			// called function joins them (a key and the value stored under it), the other
			// argument's event is in this parameter's region.
			publishedAt := map[ssa.Instruction]bool{}
			forwardedAt := map[ssa.Instruction]bool{}
			for c := range g.region(n) {
				for _, e := range c.events {
					if e.accountedTo(i) && e.instr != nil {
						switch e.kind {
						case evTransfer:
							publishedAt[e.instr] = true
						case evEscape:
							forwardedAt[e.instr] = true
						}
					}
				}
			}
			// One scan: leak records links as it goes, so it must see each event once.
			s.leakParam[i] = regionScan(g, n, func(e event) bool {
				if !leak(e) {
					return false
				}
				if !e.accountedTo(i) && !(e.kind == evForeign && publishedAt[e.instr]) && !(e.kind == evEscape && e.byContract != 0 && forwardedAt[e.instr]) {
					s.leakBesides[i] = true
				}
				return true
			})
			s.writeParam[i] = fwdHas(g, n, isWrite)
			if _, isSig := p.Type().Underlying().(*types.Signature); isSig && !s.isoParam[i] {
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
			s.writeFree[j] = fwdHas(g, n, isWrite)
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
		slices.Equal(a.writeParam, b.writeParam) && slices.Equal(a.writeFree, b.writeFree) && slices.Equal(a.links, b.links)
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
// post-order and re-queued when a summary changes: along intra-package caller edges, and to every
// function that read the summary while its own was worked out (summaryRead).
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
					if m := methodBehind(target); m != nil {
						target = m
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
		st.sumFor = fn
		next := summarise(fn, st.buildGraph(fn), st)
		st.sumFor = nil
		if !sameSummary(st.sums[fn], next) {
			merged := mergePessimistic(st.sums[fn], next)
			if !sameSummary(st.sums[fn], merged) {
				st.sums[fn] = merged
				again := func(reader *ssa.Function) {
					if !inQueue[reader] && len(reader.Blocks) > 0 {
						inQueue[reader] = true
						queue = append(queue, reader)
					}
				}
				for _, caller := range callers[fn] {
					again(caller)
				}
				// In the order they come in fns: the order of a map differs from run to run, and
				// so would the order the summaries are worked out in.
				for _, reader := range fns {
					if st.sumReaders[fn][reader] {
						again(reader)
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
	out.writeFree = orInto(prev.writeFree, next.writeFree)
	out.links = mergeLinks(prev.links, next.links)
	return &out
}
