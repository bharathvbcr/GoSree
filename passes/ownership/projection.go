package ownership

import (
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// fieldSyncInfo returns the proven Firewall and Frozen Sync flags for field fieldIdx of named.
func (st *state) fieldSyncInfo(named *types.Named, fieldIdx int) (firewall, frozen bool) {
	if named == nil || fieldIdx < 0 {
		return false, false
	}
	orig := named.Origin()
	if orig.TypeParams() != nil && orig.TypeParams().Len() > 0 {
		return false, false
	}
	s, ok := orig.Underlying().(*types.Struct)
	if !ok || fieldIdx >= s.NumFields() {
		return false, false
	}
	tn := orig.Obj()
	if tn == nil || tn.Pkg() == nil {
		return false, false
	}
	if st.pass != nil && tn.Pkg() == st.pass.Pkg {
		fv := s.Field(fieldIdx).Origin()
		return st.firewallFields[fv], st.frozenFields[fv]
	}
	if st.pass == nil {
		return false, false
	}
	for i := range orig.NumMethods() {
		m := orig.Method(i)
		if m == nil || !m.Exported() || m.Pkg() != tn.Pkg() {
			continue
		}
		var ff FuncFact
		if st.pass.ImportObjectFact(m.Origin(), &ff) && !ff.Unknown {
			fw := fieldIdx < len(ff.RecvFirewall) && ff.RecvFirewall[fieldIdx]
			fr := fieldIdx < len(ff.RecvFrozen) && ff.RecvFrozen[fieldIdx]
			return fw, fr
		}
	}
	return false, false
}

func isSafeHTTPHandlerReturnType(t types.Type) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
		if named, ok := t.(*types.Named); ok && named.Obj() != nil && named.Obj().Pkg() != nil {
			return named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == "ServeMux"
		}
		return false
	}
	if named, ok := t.(*types.Named); ok && named.Obj() != nil && named.Obj().Pkg() != nil {
		if named.Obj().Pkg().Path() == "net/http" {
			switch named.Obj().Name() {
			case "Handler", "HandlerFunc":
				return true
			}
		}
	}
	return false
}

func (g *graph) hasCutCallTouching(targetPts map[*node]bool) bool {
	if g == nil || len(g.cutCalls) == 0 || len(targetPts) == 0 {
		return false
	}
	touchesVal := func(v ssa.Value) bool {
		if v == nil {
			return false
		}
		if _, isConst := v.(*ssa.Const); isConst || g.st.cls.Shareable(v.Type()) || g.st.isAutoSyncType(v.Type()) {
			return false
		}
		if vn := g.val(v); vn != nil && pointsToIntersects(g, vn, targetPts) {
			return true
		}
		if mc, isMC := unwrapClosureValue(v); isMC {
			for _, b := range mc.Bindings {
				if bn := g.val(b); bn != nil && pointsToIntersects(g, bn, targetPts) && !g.st.cls.Shareable(b.Type()) && !g.st.isAutoSyncType(b.Type()) {
					return true
				}
			}
		}
		return false
	}
	for _, ins := range g.cutCalls {
		call, ok := ins.(ssa.CallInstruction)
		if !ok {
			return true
		}
		cc := call.Common()
		if touchesVal(cc.Value) {
			return true
		}
		for _, a := range cc.Args {
			if touchesVal(a) || touchesVal(unwrapPublishedArg(a)) {
				return true
			}
		}
	}
	return false
}

func (st *state) handlerRootsHaveCutCall(fn *ssa.Function, roots map[ssa.Value]string) bool {
	hg := st.graphOf(fn)
	if hg == nil || len(hg.cutCalls) == 0 {
		return false
	}
	pts := map[*node]bool{}
	for rv := range roots {
		if st.isSafeHandlerValue(rv) {
			continue
		}
		if rn := hg.val(rv); rn != nil {
			for c := range pointsToSet(hg, rn) {
				pts[c] = true
			}
		}
	}
	return hg.hasCutCallTouching(pts)
}

func (st *state) hasCutCallInSlice(g *graph, ds *directionalSlice) bool {
	if g == nil || len(g.cutCalls) == 0 || ds == nil {
		return false
	}
	inSlice := func(v ssa.Value) bool {
		if v == nil {
			return false
		}
		if ds.alias[v] || ds.up[v] {
			return true
		}
		if mc, isMC := unwrapClosureValue(v); isMC {
			for _, b := range mc.Bindings {
				if ds.alias[b] || ds.up[b] {
					return true
				}
			}
		}
		return false
	}
	for _, ins := range g.cutCalls {
		call, ok := ins.(ssa.CallInstruction)
		if !ok {
			return true
		}
		cc := call.Common()
		if inSlice(cc.Value) {
			return true
		}
		for _, a := range cc.Args {
			if inSlice(a) || inSlice(unwrapPublishedArg(a)) {
				return true
			}
		}
	}
	return false
}

// isSafeReturnEvent reports whether a Return event e in g only returns HTTP handlers / ServeMux or
// opaque closures whose execution neither mutates nor leaks any binding intersecting targetPts.
func (st *state) isSafeReturnEvent(g *graph, e event, targetPts map[*node]bool) bool {
	ret, ok := e.instr.(*ssa.Return)
	if !ok || g == nil {
		return false
	}
	for _, res := range ret.Results {
		rn := g.val(res)
		if rn == nil || !pointsToIntersects(g, rn, targetPts) {
			continue
		}
		if isSafeHTTPHandlerReturnType(res.Type()) {
			continue
		}
		mc, isMC := unwrapClosureValue(res)
		if !isMC {
			return false
		}
		retFn, ok := mc.Fn.(*ssa.Function)
		if !ok || len(retFn.Blocks) == 0 || retFn.Pkg != st.ssa.Pkg {
			return false
		}
		retSum := st.summary(retFn)
		if retSum.pessimistic {
			return false
		}
		retG := st.graphOf(retFn)
		for m, rb := range mc.Bindings {
			if m >= len(retFn.FreeVars) {
				return false
			}
			if st.cls.Shareable(rb.Type()) || g.isAutoSyncCapture(ret, rb) {
				continue
			}
			rbn := g.val(rb)
			if rbn == nil || !pointsToIntersects(g, rbn, targetPts) {
				continue
			}
			if fvn := retG.val(retFn.FreeVars[m]); fvn != nil && retG.hasCutCallTouching(pointsToSet(retG, fvn)) {
				return false
			}
			if retSum.writesFree(m) || retSum.freeLeaks(m) || st.fnHasUnjoinedSpawnWritingFreeVar(retFn, m) {
				return false
			}
		}
	}
	return true
}

func (st *state) fnHasUnjoinedSpawnWritingFreeVar(fn *ssa.Function, freeIdx int) bool {
	if fn == nil || freeIdx < 0 || freeIdx >= len(fn.FreeVars) || len(fn.Blocks) == 0 {
		return false
	}
	fg := st.graphOf(fn)
	fv := fn.FreeVars[freeIdx]
	fvn := fg.val(fv)
	if fvn == nil {
		return false
	}
	fvPts := pointsToSet(fg, fvn)
	if fg.hasCutCallTouching(fvPts) {
		return true
	}
	for _, tr := range fg.transfers {
		joins := st.joins(fg, tr)
		if len(joins) == 0 {
			joins = st.spawnerWaitJoins(fg, tr)
		}
		if everyPathJoinsBeforeReturn(fg, tr, joins) {
			continue
		}
		tn := fg.val(tr.v)
		if tn != nil && pointsToIntersects(fg, tn, fvPts) {
			return true
		}
	}
	return false
}

func (st *state) allParamsSharedRead(fn *ssa.Function, paramSet map[int]bool) bool {
	if fn == nil || len(paramSet) == 0 {
		return false
	}
	s := st.sums[fn]
	if s == nil || s.pessimistic {
		return false
	}
	for pIdx := range paramSet {
		if !s.isSharedRead(pIdx) {
			return false
		}
	}
	return true
}

func (st *state) collectFirstClassFuncsInPkg(fns []*ssa.Function) map[*ssa.Function]bool {
	used := map[*ssa.Function]bool{}
	var opBuf [8]*ssa.Value
	for _, caller := range fns {
		if len(caller.Blocks) == 0 {
			continue
		}
		for _, b := range caller.Blocks {
			for _, ins := range b.Instrs {
				if mc, isMC := ins.(*ssa.MakeClosure); isMC {
					if fn, ok := mc.Fn.(*ssa.Function); ok {
						used[fn] = true
					}
				}
				if goIns, isGo := ins.(*ssa.Go); isGo {
					if callee := goIns.Call.StaticCallee(); callee != nil {
						used[callee] = true
					}
				}
				if df, isDef := ins.(*ssa.Defer); isDef {
					if callee := df.Call.StaticCallee(); callee != nil {
						used[callee] = true
					}
				}
				ops := ins.Operands(opBuf[:0])
				call, isCall := ins.(*ssa.Call)
				for _, op := range ops {
					if op == nil {
						continue
					}
					target, ok := (*op).(*ssa.Function)
					if !ok || target == nil {
						continue
					}
					if isCall && !call.Call.IsInvoke() && call.Call.Value == target {
						passedAsArg := false
						for _, a := range call.Call.Args {
							if a == target {
								passedAsArg = true
								break
							}
						}
						if !passedAsArg {
							continue
						}
					}
					if st.isClosedCallOnlyGlobalFuncStore(ins, target) {
						continue
					}
					used[target] = true
				}
			}
		}
	}
	return used
}

// handlerRegRootsClean checks that sp is a complete, cut-free repeated HTTP handler registration
// with zero SAFE008 direct or hidden unguarded writes.
func (st *state) handlerRegRootsClean(sp spawnSite) bool {
	if !sp.repeated || st.ssa == nil || st.ssa.Pkg == nil {
		return false
	}
	prog := st.ssa.Pkg.Prog
	if !hasCompleteHandlerRoots(prog, sp) {
		return false
	}
	for _, hb := range handlerRoots(prog, sp) {
		if len(hb.fn.Blocks) == 0 {
			return false
		}
		if st.summary(hb.fn).pessimistic || st.handlerRootsHaveCutCall(hb.fn, hb.roots) {
			return false
		}
		if st.handlerHasUnguardedHiddenWrite(hb) {
			return false
		}
	}
	prevDry := st.dryRun
	st.dryRun = true
	flagged := st.checkHandlerWrites(sp, token.NoPos)
	st.dryRun = prevDry
	return !flagged
}

// inferSharedReadSummaries computes FuncFact.ProjSync and FuncFact.SharedRead to a monotone
// fixpoint for top-level functions in this package.
func (st *state) inferSharedReadSummaries(fns []*ssa.Function) {
	for _, fn := range fns {
		if fn == nil || fn.Synthetic != "" || fn.Parent() != nil || len(fn.Blocks) == 0 {
			continue
		}
		s := st.sums[fn]
		if s == nil || s.pessimistic || len(fn.Params) == 0 {
			continue
		}
		for pIdx, p := range fn.Params {
			if st.isIsoParam(p) || st.cls.Shareable(p.Type()) || st.isAutoSyncType(p.Type()) || namedStructOfValOrPtr(p.Type()) == nil {
				continue
			}
			ctx := &projCheckCtx{
				st:   st,
				seen: map[projVisitKey]bool{},
			}
			if ctx.verifyStructVal(fn, p) && ctx.sawProjectedField {
				if s.projSync == nil {
					s.projSync = make([]bool, len(fn.Params))
				}
				s.projSync[pIdx] = true
			}
		}
	}

	firstClass := st.collectFirstClassFuncsInPkg(fns)
	st.firstClassFuncs = firstClass
	var cands []*ssa.Function
	for _, fn := range fns {
		if fn == nil || fn.Synthetic != "" || fn.Parent() != nil || len(fn.Blocks) == 0 {
			continue
		}
		s := st.sums[fn]
		if s == nil || s.pessimistic || len(fn.Params) == 0 {
			continue
		}
		if firstClass[fn] {
			continue
		}
		cands = append(cands, fn)
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range cands {
			s := st.sums[fn]
			g := st.graphOf(fn)
			for pIdx, p := range fn.Params {
				if s.isSharedRead(pIdx) {
					continue
				}
				if st.isIsoParam(p) || st.cls.Shareable(p.Type()) || st.isAutoSyncType(p.Type()) || s.spawns(pIdx) {
					continue
				}
				if (!s.writes(pIdx) && st.paramQualifiesSharedRead(g, fn, pIdx, p)) || st.paramQualifiesSharedReadDirectional(g, fn, pIdx, p) {
					if s.sharedRead == nil {
						s.sharedRead = make([]bool, len(fn.Params))
					}
					s.sharedRead[pIdx] = true
					changed = true
				}
			}
		}
	}
}

func (st *state) paramQualifiesSharedReadDirectional(g *graph, fn *ssa.Function, pIdx int, p *ssa.Parameter) bool {
	if len(g.defers) > 0 {
		pn := g.val(p)
		if pn == nil {
			return false
		}
		pPts := pointsToSet(g, pn)
		for _, d := range g.defers {
			if st.deferMayWriteTarget(g, d, pPts) {
				return false
			}
		}
	}
	slice, ok := st.collectDirectionalSlice(g, p, nil)
	if !ok || slice == nil {
		return false
	}
	pn := g.val(p)
	if pn == nil {
		return false
	}
	pPts := pointsToSet(g, pn)
	for v := range slice.alias {
		if vn := g.val(v); vn != nil {
			for _, e := range vn.events {
				if e.global != nil && st.mutatedGlobals[e.global] && !st.isGuardedGlobal(e.global) {
					return false
				}
				if e.kind == evReturn && !st.isSafeReturnEvent(g, e, pPts) {
					return false
				}
				if e.kind == evForeign && e.param != pIdx {
					return false
				}
			}
		}
	}
	sawSharedSink := false
	for _, tr := range g.transfers {
		if !slice.alias[tr.v] {
			mc, isMC := tr.v.(*ssa.MakeClosure)
			if !isMC {
				continue
			}
			captured := false
			for _, bind := range mc.Bindings {
				if slice.alias[bind] {
					captured = true
					break
				}
			}
			if !captured {
				continue
			}
		}
		reachMask := reachableBlocksForTransfer(tr)
		after := reachableAfterTransferWithMask(g, tr, reachMask)
		if sp, ok := spawnOf(tr.instr); ok && sp.repeated && tr.v == sp.fn {
			if !st.noSlicePostSiteOrConcurrentWrites(g, slice, tr.instr, after) {
				return false
			}
			if st.handlerRegRootsClean(sp) || st.isProjectedSyncTransferAssumingParam(g, tr, after, pIdx) {
				sawSharedSink = true
			}
			continue
		}
		if st.isSpawnTransfer(tr) && st.isProjectedSyncTransferAssumingParam(g, tr, after, pIdx) {
			if !st.noSlicePostSiteOrConcurrentWrites(g, slice, tr.instr, after) {
				return false
			}
			sawSharedSink = true
			continue
		}
		if st.isVerifiedScopedForkJoin(g, tr, after, reachMask) {
			continue
		}
		return false
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			call, isCall := ins.(*ssa.Call)
			if !isCall || call.Call.IsInvoke() {
				continue
			}
			callee := call.Call.StaticCallee()
			if callee == nil {
				continue
			}
			cs := st.summary(callee)
			if cs.pessimistic {
				continue
			}
			afterCall := reachableAfter(call)
			for k, arg := range call.Call.Args {
				if !cs.isSharedRead(k) || !slice.alias[arg] {
					continue
				}
				if !st.noSlicePostSiteOrConcurrentWrites(g, slice, call, afterCall) {
					return false
				}
				sawSharedSink = true
			}
		}
	}
	return sawSharedSink
}

func (st *state) noSlicePostSiteOrConcurrentWrites(g *graph, slice *directionalSlice, site ssa.Instruction, after func(ssa.Instruction) bool) bool {
	for _, b := range g.fn.Blocks {
		for _, ins := range b.Instrs {
			if ins == site {
				continue
			}
			if !st.sliceInstructionWrites(g, slice, ins) {
				continue
			}
			if after(ins) {
				return false
			}
			if _, isDefer := ins.(*ssa.Defer); isDefer {
				return false
			}
			if st.isUnjoinedConcurrentSpawnBefore(g, ins, site) {
				return false
			}
		}
	}
	return true
}

func (st *state) paramQualifiesSharedRead(g *graph, fn *ssa.Function, pIdx int, p *ssa.Parameter) bool {
	pn := g.val(p)
	if pn == nil {
		return false
	}
	pPts := pointsToSet(g, pn)
	if g.hasCutCallTouching(pPts) {
		return false
	}
	pReg := g.region(pn)
	for c := range pReg {
		for _, e := range c.events {
			if e.global != nil && st.mutatedGlobals[e.global] && !st.isGuardedGlobal(e.global) {
				return false
			}
			if e.kind == evReturn && pointsToIntersects(g, c, pPts) && !st.isSafeReturnEvent(g, e, pPts) {
				return false
			}
			if e.kind == evForeign && e.param != pIdx && pointsToIntersects(g, c, pPts) {
				return false
			}
		}
	}
	for _, d := range g.defers {
		if st.deferMayWriteTarget(g, d, pPts) {
			return false
		}
	}

	sawSharedSink := false
	for _, tr := range g.transfers {
		tn := g.val(tr.v)
		if tn == nil || !pointsToIntersects(g, tn, pPts) {
			continue
		}
		reachMask := reachableBlocksForTransfer(tr)
		after := reachableAfterTransferWithMask(g, tr, reachMask)
		if sp, ok := spawnOf(tr.instr); ok && sp.repeated && tr.v == sp.fn {
			if !st.noPostSiteOrConcurrentWrites(g, pReg, pPts, tr.instr, after) {
				return false
			}
			if st.handlerRegRootsClean(sp) || st.isProjectedSyncTransferAssumingParam(g, tr, after, pIdx) {
				sawSharedSink = true
			}
			continue
		}
		if st.isSpawnTransfer(tr) && st.isProjectedSyncTransferAssumingParam(g, tr, after, pIdx) {
			if !st.noPostSiteOrConcurrentWrites(g, pReg, pPts, tr.instr, after) {
				return false
			}
			sawSharedSink = true
			continue
		}
		if st.isVerifiedScopedForkJoin(g, tr, after, reachMask) {
			continue
		}
		return false
	}

	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			call, isCall := ins.(*ssa.Call)
			if !isCall || call.Call.IsInvoke() {
				continue
			}
			callee := call.Call.StaticCallee()
			if callee == nil {
				continue
			}
			cs := st.summary(callee)
			if cs.pessimistic {
				continue
			}
			afterCall := reachableAfter(call)
			for k, arg := range call.Call.Args {
				if !cs.isSharedRead(k) {
					continue
				}
				an := g.val(arg)
				if an == nil || !pointsToIntersects(g, an, pPts) {
					continue
				}
				if !st.noPostSiteOrConcurrentWrites(g, pReg, pPts, call, afterCall) {
					return false
				}
				sawSharedSink = true
			}
		}
	}
	return sawSharedSink
}

func (st *state) noPostSiteOrConcurrentWrites(g *graph, reg map[*node]bool, targetPts map[*node]bool, site ssa.Instruction, after func(ssa.Instruction) bool) bool {
	for c := range reg {
		for _, e := range c.events {
			if e.kind == evWrite && e.instr != nil && st.nodeWriteMayTouchTarget(g, targetPts, c, e.instr, true) {
				if after(e.instr) {
					return false
				}
				if _, isDefer := e.instr.(*ssa.Defer); isDefer {
					return false
				}
				if st.isUnjoinedConcurrentSpawnBefore(g, e.instr, site) {
					return false
				}
			}
		}
	}
	return true
}

// checkSharedReadCalls verifies caller-side ownership and post-call immutability for every call to a
// function with SharedRead parameters, and rejects taking a SharedRead function as a first-class value.
func (st *state) checkSharedReadCalls(g *graph) {
	if g == nil || g.fn == nil || len(g.fn.Blocks) == 0 {
		return
	}
	callerSum := st.sums[g.fn]
	var opBuf [8]*ssa.Value
	for _, b := range g.fn.Blocks {
		for _, ins := range b.Instrs {
			ops := ins.Operands(opBuf[:0])
			call, isCall := ins.(*ssa.Call)
			for _, op := range ops {
				if op == nil {
					continue
				}
				fnVal, isFn := (*op).(*ssa.Function)
				if !isFn || fnVal == nil {
					continue
				}
				if isCall && !call.Call.IsInvoke() && call.Call.Value == fnVal {
					passedAsArg := false
					for _, a := range call.Call.Args {
						if a == fnVal {
							passedAsArg = true
							break
						}
					}
					if !passedAsArg {
						continue
					}
				}
				fs := st.summary(fnVal)
				if fs == nil || !anyTrue(fs.sharedRead) {
					continue
				}
				if st.isClosedCallOnlyGlobalFuncStore(ins, fnVal) {
					continue
				}
				pos := posOf(ins, ins)
				st.report(pos, Escapes, "function "+fnVal.Name()+" shares its parameter across concurrent readers and must be called directly")
			}

			if !isCall || call.Call.IsInvoke() {
				continue
			}
			var targets []*ssa.Function
			if callee := call.Call.StaticCallee(); callee != nil {
				targets = []*ssa.Function{callee}
			} else if load, isLoad := call.Call.Value.(*ssa.UnOp); isLoad && load.Op == token.MUL {
				if gl, isGlobal := load.X.(*ssa.Global); isGlobal {
					if resolved, ok := st.closedCallOnlyGlobalFuncs(gl); ok {
						targets = resolved
					}
				}
			}
			if len(targets) == 0 {
				continue
			}
			afterCall := reachableAfter(call)
			callPos := posOf(call, call)
			for _, callee := range targets {
				cs := st.summary(callee)
				if cs == nil || cs.pessimistic || !anyTrue(cs.sharedRead) {
					continue
				}
				for k, arg := range call.Call.Args {
					if !cs.isSharedRead(k) {
						continue
					}
					if c, isConst := arg.(*ssa.Const); isConst && c.IsNil() {
						continue
					}
					if st.cls.Shareable(arg.Type()) || g.isAutoSyncCapture(call, arg) {
						continue
					}
					st.verifySharedReadCallArg(g, callerSum, call, callPos, callee, arg, afterCall)
				}
			}
		}
	}
}

func (st *state) isClosedCallOnlyGlobalFuncStore(ins ssa.Instruction, fnVal *ssa.Function) bool {
	stIns, ok := ins.(*ssa.Store)
	if !ok || stIns.Val != fnVal {
		return false
	}
	gl, ok := stIns.Addr.(*ssa.Global)
	if !ok {
		return false
	}
	_, closed := st.closedCallOnlyGlobalFuncs(gl)
	return closed
}

// closedCallOnlyGlobalFuncs reports whether gl is an unexported (or main-package) function variable
// whose address never escapes, every value stored into gl across the package is a static function
// (or nil), and every load from gl is invoked directly as the callee of a *ssa.Call.
func (st *state) closedCallOnlyGlobalFuncs(gl *ssa.Global) ([]*ssa.Function, bool) {
	if gl == nil || st.contracts == nil || gl.Referrers() == nil {
		return nil, false
	}
	isMain := st.pass != nil && st.pass.Pkg != nil && st.pass.Pkg.Name() == "main"
	if !isMain && token.IsExported(gl.Name()) {
		return nil, false
	}
	for _, r := range *gl.Referrers() {
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.Store:
			if x.Addr != gl {
				return nil, false
			}
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != gl || x.Referrers() == nil {
				return nil, false
			}
			for _, lr := range *x.Referrers() {
				switch lx := lr.(type) {
				case *ssa.DebugRef:
				case *ssa.Call:
					if lx.Call.IsInvoke() || lx.Call.Value != x {
						return nil, false
					}
					for _, a := range lx.Call.Args {
						if a == x {
							return nil, false
						}
					}
				default:
					return nil, false
				}
			}
		default:
			return nil, false
		}
	}
	stores := st.contracts.globalStores[gl]
	if len(stores) == 0 {
		return nil, false
	}
	var out []*ssa.Function
	for _, sv := range stores {
		switch x := sv.(type) {
		case *ssa.Const:
			if !x.IsNil() {
				return nil, false
			}
		case *ssa.Function:
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		case *ssa.MakeClosure:
			fn, ok := x.Fn.(*ssa.Function)
			if !ok || fn == nil || len(x.Bindings) > 0 {
				return nil, false
			}
			if !slices.Contains(out, fn) {
				out = append(out, fn)
			}
		default:
			return nil, false
		}
	}
	return out, len(out) > 0
}

func (st *state) verifySharedReadCallArg(g *graph, callerSum *summary, call *ssa.Call, callPos token.Pos, callee *ssa.Function, arg ssa.Value, afterCall func(ssa.Instruction) bool) {
	if slice, ok := st.collectDirectionalSlice(g, arg, call); ok {
		if st.checkDirectionalSliceForSharedRead(g, callerSum, call, callPos, callee, arg, slice, afterCall, true) {
			return
		}
		return
	}
	an := g.val(arg)
	if an == nil {
		return
	}
	aReg := g.region(an)
	aPts := pointsToSet(g, an)
	what := describe(arg)
	prevTransfer := st.transferAt
	st.transferAt = callPos
	defer func() { st.transferAt = prevTransfer }()

	for cc := range aReg {
		for _, ce := range cc.events {
			if ce.global != nil && (st.isInitOnlyImmutableGlobal(ce.global) || st.isGuardedGlobal(ce.global)) {
				continue
			}
			if ce.global != nil && st.mutatedGlobals[ce.global] {
				st.report(callPos, NotOwned, what+" is shared across concurrent readers by "+callee.Name()+" but aliases mutable package variable "+ce.global.Name())
				return
			}
			if ce.kind == evForeign && pointsToIntersects(g, cc, aPts) {
				if ce.param >= 0 && callerSum != nil && callerSum.isSharedRead(ce.param) {
					continue
				}
				st.report(callPos, NotOwned, what+" is shared across concurrent readers by "+callee.Name()+" but is not owned here")
				return
			}
			if ce.kind == evReturn && pointsToIntersects(g, cc, aPts) && !st.isSafeReturnEvent(g, ce, aPts) {
				st.report(callPos, Escapes, what+" is shared across concurrent readers by "+callee.Name()+" and also returned")
				return
			}
			if ce.kind == evWrite && ce.instr != nil && st.nodeWriteMayTouchTarget(g, aPts, cc, ce.instr, true) {
				if afterCall(ce.instr) {
					wPos := posOf(ce.instr, ce.instr)
					st.report(wPos, UseAfter, what+" is written after being shared across concurrent readers by "+callee.Name()+st.at(callPos))
					return
				}
				if _, isDefer := ce.instr.(*ssa.Defer); isDefer {
					wPos := posOf(ce.instr, ce.instr)
					st.report(wPos, UseAfter, what+" is written by a deferred call after being shared by "+callee.Name()+st.at(callPos))
					return
				}
				if st.isUnjoinedConcurrentSpawnBefore(g, ce.instr, call) {
					st.report(callPos, UseAfter, what+" has an unjoined concurrent writer when shared by "+callee.Name())
					return
				}
			}
		}
	}
	for _, df := range g.defers {
		if st.deferMayWriteTarget(g, df, aPts) {
			st.report(df.Pos(), UseAfter, what+" is written by a deferred call after being shared by "+callee.Name()+st.at(callPos))
			return
		}
	}
}

type closureCaptureRef struct {
	mc  *ssa.MakeClosure
	idx int
}

type directionalSlice struct {
	alias     map[ssa.Value]bool
	up        map[ssa.Value]bool
	params    map[int]bool
	globals   map[*ssa.Global]bool
	closures  []closureCaptureRef
	returns   []*ssa.Return
	hasFree   bool
}

// collectDirectionalSlice traces the directional alias and interior-subobject slice of start inside
// g.fn without collapsing unrelated sibling bindings of MakeClosure instructions (such as a
// RouterShutdown closure returned alongside an http.Handler).
func (st *state) collectDirectionalSlice(g *graph, start ssa.Value, selfInstr ssa.Instruction) (*directionalSlice, bool) {
	if g == nil || g.fn == nil || start == nil {
		return nil, false
	}
	ds := &directionalSlice{
		alias:   map[ssa.Value]bool{},
		up:      map[ssa.Value]bool{},
		params:  map[int]bool{},
		globals: map[*ssa.Global]bool{},
	}
	seenMC := map[closureCaptureRef]bool{}
	seenRet := map[*ssa.Return]bool{}

	var queue []ssa.Value
	addAlias := func(v ssa.Value) {
		if v == nil || ds.alias[v] {
			return
		}
		if c, isConst := v.(*ssa.Const); isConst && c.IsNil() {
			return
		}
		if st.cls.Shareable(v.Type()) || st.isAutoSyncType(v.Type()) {
			return
		}
		ds.alias[v] = true
		queue = append(queue, v)
	}
	var addUp func(v ssa.Value)
	addUp = func(v ssa.Value) {
		if v == nil || ds.up[v] {
			return
		}
		ds.up[v] = true
		switch x := v.(type) {
		case *ssa.Parameter:
			for i, p := range g.fn.Params {
				if p == x {
					ds.params[i] = true
				}
			}
		case *ssa.FreeVar:
			ds.hasFree = true
		case *ssa.Global:
			ds.globals[x] = true
		case *ssa.FieldAddr:
			addUp(x.X)
		case *ssa.Field:
			addUp(x.X)
		case *ssa.IndexAddr:
			addUp(x.X)
		case *ssa.Index:
			addUp(x.X)
		case *ssa.UnOp:
			if x.Op == token.MUL {
				addUp(x.X)
			}
		case *ssa.ChangeType:
			addUp(x.X)
		case *ssa.Phi:
			for _, edge := range x.Edges {
				addUp(edge)
			}
		}
	}

	addAlias(start)
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]

		switch x := v.(type) {
		case *ssa.Parameter:
			for i, p := range g.fn.Params {
				if p == x {
					ds.params[i] = true
				}
			}
		case *ssa.FreeVar:
			ds.hasFree = true
		case *ssa.Global:
			ds.globals[x] = true
		case *ssa.Phi:
			for _, edge := range x.Edges {
				addAlias(edge)
			}
		case *ssa.ChangeType:
			addAlias(x.X)
		case *ssa.Convert:
			addAlias(x.X)
		case *ssa.MakeInterface:
			addAlias(x.X)
		case *ssa.ChangeInterface:
			addAlias(x.X)
		case *ssa.Slice:
			addAlias(x.X)
		case *ssa.SliceToArrayPointer:
			addAlias(x.X)
		case *ssa.TypeAssert:
			addAlias(x.X)
		case *ssa.Field:
			addUp(x.X)
		case *ssa.Index:
			addUp(x.X)
		case *ssa.Lookup:
			addUp(x.X)
		case *ssa.FieldAddr:
			addUp(x.X)
		case *ssa.IndexAddr:
			addUp(x.X)
		case *ssa.UnOp:
			if x.Op == token.MUL {
				switch base := x.X.(type) {
				case *ssa.Alloc:
					if base.Parent() != g.fn {
						return nil, false
					}
					addAlias(base)
				case *ssa.FieldAddr:
					addAlias(base)
					addUp(base.X)
				case *ssa.IndexAddr:
					addAlias(base)
					addUp(base.X)
				default:
					addAlias(x.X)
				}
			}
		case *ssa.Extract:
			switch tup := x.Tuple.(type) {
			case *ssa.TypeAssert:
				addAlias(tup.X)
			case *ssa.Lookup:
				addUp(tup.X)
			case *ssa.Call:
				if tup.Call.IsInvoke() {
					return nil, false
				}
				callee := tup.Call.StaticCallee()
				if callee == nil {
					return nil, false
				}
				cs := st.summary(callee)
				if cs.pessimistic {
					return nil, false
				}
				resPos := 1 + len(tup.Call.Args) + x.Index
				linked := false
				for _, l := range cs.links {
					if l.A == resPos && l.B < len(tup.Call.Args) {
						addAlias(tup.Call.Args[l.B])
						linked = true
					} else if l.B == resPos && l.A < len(tup.Call.Args) {
						addAlias(tup.Call.Args[l.A])
						linked = true
					}
				}
				if !cs.isFreshResult(x.Index) && !linked {
					return nil, false
				}
			default:
				return nil, false
			}
		case *ssa.Call:
			if b, isBuiltin := x.Call.Value.(*ssa.Builtin); isBuiltin {
				if b.Name() == "append" && len(x.Call.Args) == 2 {
					addAlias(x.Call.Args[0])
					addAlias(x.Call.Args[1])
				} else {
					return nil, false
				}
			} else {
				if x.Call.IsInvoke() {
					return nil, false
				}
				callee := x.Call.StaticCallee()
				if callee == nil {
					return nil, false
				}
				cs := st.summary(callee)
				if cs.pessimistic {
					return nil, false
				}
				resPos := 1 + len(x.Call.Args)
				linked := false
				for _, l := range cs.links {
					if l.A == resPos && l.B < len(x.Call.Args) {
						addAlias(x.Call.Args[l.B])
						linked = true
					} else if l.B == resPos && l.A < len(x.Call.Args) {
						addAlias(x.Call.Args[l.A])
						linked = true
					}
				}
				if !cs.isFreshResult(0) && !linked {
					return nil, false
				}
			}
		case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeMap, *ssa.MakeChan, *ssa.Function, *ssa.MakeClosure:
		default:
			return nil, false
		}

		if v.Referrers() == nil {
			continue
		}
		for _, r := range *v.Referrers() {
			if r.Parent() != g.fn {
				return nil, false
			}
			switch rx := r.(type) {
			case *ssa.DebugRef, *ssa.BinOp:
			case *ssa.Phi, *ssa.ChangeType, *ssa.Convert, *ssa.MakeInterface, *ssa.ChangeInterface,
				*ssa.FieldAddr, *ssa.Field, *ssa.IndexAddr, *ssa.Index, *ssa.Lookup,
				*ssa.Slice, *ssa.SliceToArrayPointer, *ssa.TypeAssert, *ssa.Extract, *ssa.Range, *ssa.Next:
				if rv, ok := r.(ssa.Value); ok {
					addAlias(rv)
				}
			case *ssa.UnOp:
				if rx.Op == token.MUL && rx.X == v {
					addAlias(rx)
				} else if rx.Op == token.ARROW {
					return nil, false
				}
			case *ssa.Store:
				if rx.Val == v {
					addAlias(rx.Addr)
				}
				if rx.Addr == v {
					addAlias(rx.Val)
				}
			case *ssa.MapUpdate:
				if rx.Key == v || rx.Value == v {
					addAlias(rx.Map)
				}
				if rx.Map == v {
					addAlias(rx.Value)
				}
			case *ssa.Return:
				if !seenRet[rx] {
					seenRet[rx] = true
					ds.returns = append(ds.returns, rx)
				}
			case *ssa.Send:
				if rx != selfInstr {
					return nil, false
				}
			case *ssa.MakeClosure:
				for bi, bnd := range rx.Bindings {
					if bnd == v {
						ref := closureCaptureRef{mc: rx, idx: bi}
						if !seenMC[ref] {
							seenMC[ref] = true
							ds.closures = append(ds.closures, ref)
						}
					}
				}
			case ssa.CallInstruction:
				cc := rx.Common()
				if b, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
					if b.Name() == "append" && len(cc.Args) == 2 {
						if rv, ok := r.(ssa.Value); ok {
							addAlias(rv)
						}
						if cc.Args[1] == v {
							addAlias(cc.Args[0])
						}
					} else if b.Name() == "copy" && len(cc.Args) == 2 && cc.Args[1] == v {
						addAlias(cc.Args[0])
					}
					continue
				}
				if rx == selfInstr {
					continue
				}
				if cc.IsInvoke() || cc.Value == v {
					return nil, false
				}
				callee := cc.StaticCallee()
				if callee == nil {
					return nil, false
				}
				cs := st.summary(callee)
				if cs.pessimistic {
					return nil, false
				}
				for ai, arg := range cc.Args {
					if arg != v {
						continue
					}
					for _, l := range cs.links {
						if l.A == ai {
							if l.B < len(cc.Args) {
								addAlias(cc.Args[l.B])
							} else if rv, ok := r.(ssa.Value); ok {
								addAlias(rv)
							}
						} else if l.B == ai {
							if l.A < len(cc.Args) {
								addAlias(cc.Args[l.A])
							} else if rv, ok := r.(ssa.Value); ok {
								addAlias(rv)
							}
						}
					}
					if cs.leaks(ai) {
						if sp, isSp := spawnOf(rx); isSp && sp.repeated {
							continue
						}
						if cs.isSharedRead(ai) {
							continue
						}
						if callee.Pkg == st.ssa.Pkg && len(callee.Blocks) > 0 && st.samePkgCalleeSafeForSharedSlice(callee, ai, map[*ssa.Function]bool{}) {
							continue
						}
						vn := g.val(v)
						if vn != nil && g.formatsOnly(event{kind: evEscape, instr: rx}, g.region(vn)) {
							continue
						}
						return nil, false
					}
				}
			default:
				return nil, false
			}
		}
	}
	if st.hasCutCallInSlice(g, ds) {
		return nil, false
	}
	return ds, true
}

func (st *state) sliceInstructionWrites(g *graph, ds *directionalSlice, ins ssa.Instruction) bool {
	if w, isW := writeOf(ins); isW {
		if ds.alias[w.addr] || ds.up[w.addr] {
			return true
		}
	}
	call, isCall := ins.(ssa.CallInstruction)
	if !isCall {
		return false
	}
	cc := call.Common()
	if b, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
		switch b.Name() {
		case "append", "copy", "delete", "clear":
			if len(cc.Args) > 0 && (ds.alias[cc.Args[0]] || ds.up[cc.Args[0]]) {
				return true
			}
		}
		return false
	}
	if cc.IsInvoke() {
		for _, a := range cc.Args {
			if ds.alias[a] || ds.up[a] {
				return true
			}
		}
		if cc.Value != nil && (ds.alias[cc.Value] || ds.up[cc.Value]) {
			return true
		}
		return false
	}
	if callee := cc.StaticCallee(); callee != nil {
		if callee.Pkg != nil && callee.Pkg.Pkg != nil && callee.Pkg.Pkg.Path() == atomicPkg {
			return false
		}
		if _, isLock := lockOps[calleeFullName(callee)]; isLock {
			return false
		}
		cs := st.summary(callee)
		for ai, a := range cc.Args {
			if (ds.alias[a] || ds.up[a]) && (cs.pessimistic || cs.writesNonReg(ai) || cs.spawns(ai)) {
				return true
			}
		}
		return false
	}
	if mc, isMC := unwrapClosureValue(cc.Value); isMC {
		if anon, ok := mc.Fn.(*ssa.Function); ok && st.isAnalyzableClosureFn(anon) {
			cs := st.summary(anon)
			for ai, a := range cc.Args {
				if (ds.alias[a] || ds.up[a]) && (cs.pessimistic || cs.writesNonReg(ai) || cs.spawns(ai)) {
					return true
				}
			}
			for bi, bnd := range mc.Bindings {
				if (ds.alias[bnd] || ds.up[bnd]) && (cs.pessimistic || cs.writesFree(bi)) {
					return true
				}
			}
			return false
		}
	}
	for _, a := range cc.Args {
		if ds.alias[a] || ds.up[a] {
			return true
		}
	}
	return false
}

func (st *state) verifyDirectionalClosuresAndReturns(g *graph, ds *directionalSlice, site ssa.Instruction, after func(ssa.Instruction) bool) (clean bool, retEscape bool) {
	if len(ds.returns) > 0 {
		for _, ret := range ds.returns {
			for _, res := range ret.Results {
				if !ds.alias[res] {
					continue
				}
				if isSafeHTTPHandlerReturnType(res.Type()) {
					continue
				}
				return false, true
			}
		}
	}
	for _, ref := range ds.closures {
		anon, ok := ref.mc.Fn.(*ssa.Function)
		if !ok || !st.isAnalyzableClosureFn(anon) || ref.idx >= len(anon.FreeVars) {
			return false, false
		}
		as := st.summary(anon)
		if as.pessimistic {
			return false, false
		}
		ag := st.graphOf(anon)
		if fvn := ag.val(anon.FreeVars[ref.idx]); fvn != nil && ag.hasCutCallTouching(pointsToSet(ag, fvn)) {
			return false, false
		}
		if (!as.writesFree(ref.idx) && !as.freeLeaks(ref.idx) && !st.fnHasUnjoinedSpawnWritingFreeVar(anon, ref.idx)) || st.isProjectedSyncClosureInFn(g.fn, ref.mc) {
			continue
		}
		// This sibling closure may write or leak the captured binding; check where ref.mc goes!
		if ref.mc.Referrers() == nil {
			continue
		}
		for _, r := range *ref.mc.Referrers() {
			if r == site {
				continue
			}
			switch rx := r.(type) {
			case *ssa.DebugRef:
			case *ssa.Return:
				return false, true
			case *ssa.Defer:
				return false, false
			case *ssa.Go:
				if after(rx) || st.isUnjoinedConcurrentSpawnBefore(g, rx, site) {
					return false, false
				}
			case *ssa.Call:
				if rx.Call.Value == ref.mc && !rx.Call.IsInvoke() {
					passedAsArg := false
					for _, a := range rx.Call.Args {
						if a == ref.mc {
							passedAsArg = true
							break
						}
					}
					if !passedAsArg && !after(rx) {
						continue
					}
				}
				if sp, isSp := spawnOf(rx); isSp && sp.repeated && sp.fn == ref.mc {
					continue
				}
				if _, isSp := st.spawnSiteOf(rx); isSp {
					if after(rx) || st.isUnjoinedConcurrentSpawnBefore(g, rx, site) {
						return false, false
					}
					continue
				}
				return false, false
			default:
				return false, false
			}
		}
	}
	return true, false
}

func (st *state) checkDirectionalSliceForSharedRead(g *graph, callerSum *summary, site ssa.Instruction, reportPos token.Pos, callee *ssa.Function, arg ssa.Value, ds *directionalSlice, after func(ssa.Instruction) bool, emit bool) bool {
	what := describe(arg)
	calleeName := "handler"
	if callee != nil {
		calleeName = callee.Name()
	}
	prevTransfer := st.transferAt
	if emit {
		st.transferAt = reportPos
		defer func() { st.transferAt = prevTransfer }()
	}

	if ds.hasFree {
		if emit {
			st.report(reportPos, NotOwned, what+" is shared across concurrent readers by "+calleeName+" but captures an outer variable")
		}
		return false
	}
	for gl := range ds.globals {
		if st.isInitOnlyImmutableGlobal(gl) || st.isGuardedGlobal(gl) {
			continue
		}
		if emit {
			st.report(reportPos, NotOwned, what+" is shared across concurrent readers by "+calleeName+" but aliases mutable package variable "+gl.Name())
		}
		return false
	}
	for pIdx := range ds.params {
		if callerSum != nil && callerSum.isSharedRead(pIdx) {
			continue
		}
		if !st.unexportedFuncCallersCleanForParam(g.fn, pIdx) {
			if emit {
				st.report(reportPos, NotOwned, what+" is shared across concurrent readers by "+calleeName+" but is not owned here")
			}
			return false
		}
	}
	okClosures, retEsc := st.verifyDirectionalClosuresAndReturns(g, ds, site, after)
	if !okClosures {
		if emit {
			cat := UseAfter
			msg := what + " may be written concurrently after being shared by " + calleeName
			if retEsc {
				cat = Escapes
				msg = what + " is shared across concurrent readers by " + calleeName + " and also returned to caller"
			}
			st.report(reportPos, cat, msg)
		}
		return false
	}

	for _, b := range g.fn.Blocks {
		for _, ins := range b.Instrs {
			if ins == site && !after(site) {
				continue
			}
			if _, isDef := ins.(*ssa.Defer); isDef && st.sliceInstructionWrites(g, ds, ins) {
				if emit {
					st.report(posOf(ins, ins), UseAfter, what+" is written by a deferred call after being shared by "+calleeName+st.at(reportPos))
				}
				return false
			}
			if st.sliceInstructionWrites(g, ds, ins) {
				if after(ins) {
					if emit {
						st.report(posOf(ins, ins), UseAfter, what+" is written after being shared across concurrent readers by "+calleeName+st.at(reportPos))
					}
					return false
				}
				if st.isUnjoinedConcurrentSpawnBefore(g, ins, site) {
					if emit {
						st.report(reportPos, UseAfter, what+" has an unjoined concurrent writer when shared by "+calleeName)
					}
					return false
				}
			}
		}
	}
	return true
}

func (st *state) unexportedFuncCallersCleanForParam(fn *ssa.Function, paramIdx int) bool {
	if fn == nil || fn.Synthetic != "" || fn.Parent() != nil {
		return false
	}
	if obj, ok := fn.Object().(*types.Func); ok && obj.Exported() && st.pass.Pkg.Name() != "main" {
		return false
	}
	if st.firstClassFuncs[fn] {
		return false
	}
	for _, caller := range st.allFns {
		if len(caller.Blocks) == 0 {
			continue
		}
		cg := st.graphOf(caller)
		callerSum := st.sums[caller]
		for _, b := range caller.Blocks {
			for _, ins := range b.Instrs {
				call, isCall := ins.(*ssa.Call)
				if !isCall || call.Call.IsInvoke() || call.Call.StaticCallee() != fn {
					continue
				}
				if paramIdx >= len(call.Call.Args) {
					return false
				}
				arg := call.Call.Args[paramIdx]
				if c, isConst := arg.(*ssa.Const); isConst && c.IsNil() {
					continue
				}
				if st.cls.Shareable(arg.Type()) || cg.isAutoSyncCapture(call, arg) {
					continue
				}
				afterCall := reachableAfter(call)
				ds, ok := st.collectDirectionalSlice(cg, arg, call)
				if !ok || !st.checkDirectionalSliceForSharedRead(cg, callerSum, call, token.NoPos, fn, arg, ds, afterCall, false) {
					return false
				}
			}
		}
	}
	return true
}

func isConcreteBoundWrapper(fn *ssa.Function) bool {
	if !isBoundMethodWrapper(fn) || len(fn.Blocks) == 0 || len(fn.FreeVars) != 1 {
		return false
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok || obj.Signature() == nil || obj.Signature().Recv() == nil {
		return false
	}
	return !types.IsInterface(obj.Signature().Recv().Type())
}

func (st *state) isAnalyzableClosureFn(fn *ssa.Function) bool {
	if fn == nil || len(fn.Blocks) == 0 {
		return false
	}
	if fn.Pkg == st.ssa.Pkg {
		return true
	}
	return isConcreteBoundWrapper(fn)
}

func (st *state) samePkgCalleeSafeForSharedSlice(callee *ssa.Function, paramIdx int, visited map[*ssa.Function]bool) bool {
	if callee == nil || callee.Pkg != st.ssa.Pkg || len(callee.Blocks) == 0 || paramIdx < 0 || paramIdx >= len(callee.Params) {
		return false
	}
	if visited[callee] {
		return true
	}
	visited[callee] = true
	cs := st.summary(callee)
	if cs.pessimistic || cs.spawns(paramIdx) {
		return false
	}
	cg := st.graphOf(callee)
	pn := cg.val(callee.Params[paramIdx])
	if pn == nil {
		return true
	}
	pReg := cg.region(pn)
	pPts := pointsToSet(cg, pn)
	if cg.hasCutCallTouching(pPts) {
		return false
	}
	for _, tr := range cg.transfers {
		tn := cg.val(tr.v)
		if tn == nil || !pointsToIntersects(cg, tn, pPts) {
			continue
		}
		rm := reachableBlocksForTransfer(tr)
		af := reachableAfterTransferWithMask(cg, tr, rm)
		if sp, isSp := spawnOf(tr.instr); isSp && sp.repeated && tr.v == sp.fn {
			if !st.noPostSiteOrConcurrentWrites(cg, pReg, pPts, tr.instr, af) {
				return false
			}
			continue
		}
		if st.isSpawnTransfer(tr) && st.isProjectedSyncTransfer(cg, tr, af) {
			continue
		}
		if st.isVerifiedScopedForkJoin(cg, tr, af, rm) {
			continue
		}
		return false
	}
	for c := range pReg {
		for _, e := range c.events {
			if e.global != nil && !st.isInitOnlyImmutableGlobal(e.global) && !st.isGuardedGlobal(e.global) {
				return false
			}
			if e.kind != evEscape || e.instr == nil {
				continue
			}
			if _, isRet := e.instr.(*ssa.Return); isRet {
				continue
			}
			if sp, isSp := spawnOf(e.instr); isSp && sp.repeated {
				if fnVal := cg.val(sp.fn); fnVal != nil && pointsToIntersects(cg, fnVal, pPts) {
					continue
				}
				if !st.handlerRegRootsClean(sp) {
					return false
				}
				continue
			}
			if cg.formatsOnly(e, pReg) {
				continue
			}
			if call, isCall := e.instr.(ssa.CallInstruction); isCall && !call.Common().IsInvoke() {
				if sub := call.Common().StaticCallee(); sub != nil {
					subSum := st.summary(sub)
					allArgsOK := true
					sawArg := false
					for ai, arg := range call.Common().Args {
						an := cg.val(arg)
						if an == nil || !pointsToIntersects(cg, an, pPts) {
							continue
						}
						sawArg = true
						if !subSum.leaks(ai) || subSum.isSharedRead(ai) {
							continue
						}
						if sub.Pkg == st.ssa.Pkg && len(sub.Blocks) > 0 && st.samePkgCalleeSafeForSharedSlice(sub, ai, visited) {
							continue
						}
						allArgsOK = false
						break
					}
					if sawArg && allArgsOK {
						continue
					}
				}
			}
			return false
		}
	}
	return true
}

// isPerBindingVerifiedHandlerReg verifies a repeated HTTP handler registration binding-by-binding
// using directional alias slices so a sibling RouterShutdown closure does not merge disjoint
// handlers into one Steensgaard equivalence class.
func (st *state) isPerBindingVerifiedHandlerReg(g *graph, t transfer, sp spawnSite, after func(ssa.Instruction) bool) bool {
	mc, isMC := unwrapClosureValue(sp.fn)
	if !isMC {
		return false
	}
	fn, ok := mc.Fn.(*ssa.Function)
	if !ok || !st.isAnalyzableClosureFn(fn) {
		return false
	}
	callerSum := st.sums[g.fn]
	for j, b := range mc.Bindings {
		if j >= len(fn.FreeVars) {
			return false
		}
		fv := fn.FreeVars[j]
		if c, isConst := b.(*ssa.Const); isConst && c.IsNil() {
			continue
		}
		if st.cls.Shareable(b.Type()) || g.isAutoSyncCapture(t.instr, b) || st.isFrozenCellBinding(g, b, fv, t.instr, after) {
			continue
		}
		if st.isProjectedSyncBinding(g, t, fn, j, b, after, -1) {
			continue
		}
		ds, ok := st.collectDirectionalSlice(g, b, t.instr)
		if !ok || !st.checkDirectionalSliceForSharedRead(g, callerSum, t.instr, token.NoPos, fn, b, ds, after, false) {
			return false
		}
	}
	return true
}

func (st *state) isFrozenCellBinding(g *graph, b ssa.Value, fv *ssa.FreeVar, site ssa.Instruction, after func(ssa.Instruction) bool) bool {
	if !st.isShareableCellBinding(b, fv) {
		return false
	}
	alloc, ok := b.(*ssa.Alloc)
	if !ok || alloc.Parent() != g.fn {
		return false
	}
	return st.cellAddrNeverWrittenAfter(g, alloc, site, after, map[ssa.Value]bool{})
}

func (st *state) cellAddrNeverWrittenAfter(g *graph, addr ssa.Value, site ssa.Instruction, after func(ssa.Instruction) bool, seen map[ssa.Value]bool) bool {
	if seen[addr] {
		return true
	}
	seen[addr] = true
	if addr.Referrers() == nil {
		return true
	}
	for _, r := range *addr.Referrers() {
		if r.Parent() != g.fn {
			return false
		}
		switch rx := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if rx.Op != token.MUL || rx.X != addr {
				return false
			}
		case *ssa.FieldAddr:
			if rx.X != addr || !st.cellAddrNeverWrittenAfter(g, rx, site, after, seen) {
				return false
			}
		case *ssa.IndexAddr:
			if rx.X != addr || !st.cellAddrNeverWrittenAfter(g, rx, site, after, seen) {
				return false
			}
		case *ssa.Store:
			if rx.Addr != addr || after(rx) {
				return false
			}
		case *ssa.MakeClosure:
			anon, ok := rx.Fn.(*ssa.Function)
			if !ok || !st.isAnalyzableClosureFn(anon) {
				return false
			}
			as := st.summary(anon)
			if as.pessimistic {
				return false
			}
			for bi, bnd := range rx.Bindings {
				if bnd == addr && (as.writesFree(bi) || as.freeLeaks(bi)) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// isProjectedSyncTransfer proves that a spawned closure captures only Shareable/AutoSync values or
// struct pointers/values whose projected fields inside the closure (and its callees) are strictly
// Firewall (mutex-encapsulated / sync primitive) or Frozen (read-only post-construction), never
// touching any unsynchronized sibling field of the struct.
func (st *state) isProjectedSyncTransfer(g *graph, t transfer, after func(ssa.Instruction) bool) bool {
	return st.isProjectedSyncTransferAssumingParam(g, t, after, -1)
}

func (st *state) isProjectedSyncTransferAssumingParam(g *graph, t transfer, after func(ssa.Instruction) bool, assumedSharedParam int) bool {
	if g == nil {
		return false
	}
	mc, isMC := unwrapClosureValue(t.v)
	if !isMC {
		return false
	}
	fn, ok := mc.Fn.(*ssa.Function)
	if !ok || !st.isAnalyzableClosureFn(fn) {
		return false
	}
	if sp, isSp := spawnOf(t.instr); isSp && sp.repeated && t.v == sp.fn {
		if !st.handlerRegRootsClean(sp) {
			return false
		}
	}
	if st.summary(fn).pessimistic {
		return false
	}
	sawProjected := false
	for j, b := range mc.Bindings {
		if j >= len(fn.FreeVars) {
			return false
		}
		fv := fn.FreeVars[j]
		if c, isConst := b.(*ssa.Const); isConst && c.IsNil() {
			continue
		}
		if st.cls.Shareable(b.Type()) || g.isAutoSyncCapture(t.instr, b) || st.isFrozenCellBinding(g, b, fv, t.instr, after) {
			continue
		}
		if !st.isProjectedSyncBinding(g, t, fn, j, b, after, assumedSharedParam) {
			return false
		}
		sawProjected = true
	}
	return sawProjected
}

func (st *state) isProjectedSyncClosureInFn(parent *ssa.Function, mc *ssa.MakeClosure) bool {
	if parent == nil || mc == nil {
		return false
	}
	anon, ok := mc.Fn.(*ssa.Function)
	if !ok || !st.isAnalyzableClosureFn(anon) {
		return false
	}
	if st.summary(anon).pessimistic {
		return false
	}
	pg := st.graphOf(parent)
	afterMC := reachableAfter(mc)
	sawProjected := false
	for j, b := range mc.Bindings {
		if j >= len(anon.FreeVars) {
			return false
		}
		fv := anon.FreeVars[j]
		if c, isConst := b.(*ssa.Const); isConst && c.IsNil() {
			continue
		}
		if st.cls.Shareable(b.Type()) || st.isAutoSyncType(b.Type()) || isFreshInFn(parent, b) || st.isFrozenCellBinding(pg, b, fv, mc, afterMC) {
			continue
		}
		if namedStructOfValOrPtr(fv.Type()) == nil {
			return false
		}
		ctx := &projCheckCtx{
			st:   st,
			seen: map[projVisitKey]bool{},
		}
		if !ctx.verifyStructVal(anon, fv) || !ctx.sawProjectedField {
			return false
		}
		sawProjected = true
	}
	return sawProjected
}

func namedStructOfValOrPtr(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	if p, isPtr := t.(*types.Pointer); isPtr {
		t = types.Unalias(p.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	if _, isStruct := named.Origin().Underlying().(*types.Struct); !isStruct {
		return nil
	}
	return named
}

type projVisitKey struct {
	fn  *ssa.Function
	val ssa.Value
}

type projCheckCtx struct {
	st                   *state
	seen                 map[projVisitKey]bool
	sawExportedField     bool
	sawProjectedField    bool
	ctorIfaceFrozen      map[*types.Var]bool
	ctorIfaceNeedsCaller map[*types.Var]bool
}

func (st *state) buildCtorIfaceFrozenForTransfer(t transfer) (frozen, needsCaller map[*types.Var]bool) {
	sp, ok := spawnOf(t.instr)
	if !ok || !sp.repeated || t.v != sp.fn {
		return nil, nil
	}
	spec := st.specializeHandlerCtorFields(sp)
	if len(spec) == 0 {
		return nil, nil
	}
	frozen = map[*types.Var]bool{}
	needsCaller = map[*types.Var]bool{}
	for fOrigin, concretes := range spec {
		iface, isIface := fOrigin.Type().Underlying().(*types.Interface)
		if !isIface {
			continue
		}
		allOK := true
		anyNonAutoSync := false
		for _, ct := range concretes {
			if !st.concreteTypeReadOnlyForInterface(ct, iface) {
				allOK = false
				break
			}
			if !st.cls.Shareable(ct) && !st.isAutoSyncType(ct) {
				anyNonAutoSync = true
			}
		}
		if allOK {
			frozen[fOrigin] = true
			if anyNonAutoSync {
				needsCaller[fOrigin] = true
			}
		}
	}
	return frozen, needsCaller
}

func (st *state) isProjectedSyncBinding(g *graph, t transfer, fn *ssa.Function, fvIdx int, b ssa.Value, after func(ssa.Instruction) bool, assumedSharedParam int) bool {
	if fvIdx < 0 || fvIdx >= len(fn.FreeVars) {
		return false
	}
	fv := fn.FreeVars[fvIdx]
	actual := fv
	if cellAlloc, isAlloc := b.(*ssa.Alloc); isAlloc && isPtrToStructOrPtr(fv.Type()) {
		if !st.localVarNeverReassignedAfter(g, cellAlloc, after) {
			return false
		}
	}
	_ = actual
	ctorFrozen, ctorNeedsCaller := st.buildCtorIfaceFrozenForTransfer(t)
	ctx := &projCheckCtx{
		st:                   st,
		seen:                 map[projVisitKey]bool{},
		ctorIfaceFrozen:      ctorFrozen,
		ctorIfaceNeedsCaller: ctorNeedsCaller,
	}
	if !ctx.verifyBindingEntry(fn, fv, b) {
		return false
	}

	// Verify no post-spawn or concurrent writes to b in g.fn, and if an exported frozen field was
	// projected, verify b's foreign/caller origins are also read-only!
	ds, ok := st.collectDirectionalSlice(g, b, t.instr)
	if ok {
		for _, blk := range g.fn.Blocks {
			for _, ins := range blk.Instrs {
				if ins == t.instr && !after(t.instr) {
					continue
				}
				if _, isDef := ins.(*ssa.Defer); isDef && st.sliceInstructionWrites(g, ds, ins) {
					return false
				}
				if st.sliceInstructionWrites(g, ds, ins) {
					if after(ins) || st.isUnjoinedConcurrentSpawnBefore(g, ins, t.instr) {
						return false
					}
				}
			}
		}
		if ctx.sawExportedField {
			if ds.hasFree {
				return false
			}
			for gl := range ds.globals {
				if !st.isInitOnlyImmutableGlobal(gl) && !st.isGuardedGlobal(gl) {
					return false
				}
			}
			callerSum := st.sums[g.fn]
			for pIdx := range ds.params {
				if pIdx == assumedSharedParam || (callerSum != nil && callerSum.isSharedRead(pIdx)) {
					continue
				}
				if !st.unexportedFuncCallersCleanForParam(g.fn, pIdx) {
					return false
				}
			}
			okClos, _ := st.verifyDirectionalClosuresAndReturns(g, ds, t.instr, after)
			if !okClos {
				return false
			}
		}
		return true
	}

	bn := g.val(b)
	if bn == nil {
		return false
	}
	bReg := g.region(bn)
	bPts := pointsToSet(g, bn)
	if g.hasCutCallTouching(bPts) {
		return false
	}
	if !st.noPostSiteOrConcurrentWrites(g, bReg, bPts, t.instr, after) {
		return false
	}
	for _, df := range g.defers {
		if st.deferMayWriteTarget(g, df, bPts) {
			return false
		}
	}
	if ctx.sawExportedField && !st.handlerForeignCallersClean(g, bReg, bPts) {
		return false
	}
	return true
}

func isPtrToStructOrPtr(t types.Type) bool {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	return namedStructOfValOrPtr(p.Elem()) != nil
}

func (st *state) localVarNeverReassignedAfter(g *graph, alloc *ssa.Alloc, after func(ssa.Instruction) bool) bool {
	if alloc.Referrers() == nil {
		return true
	}
	for _, r := range *alloc.Referrers() {
		switch rx := r.(type) {
		case *ssa.DebugRef, *ssa.MakeClosure:
		case *ssa.UnOp:
			if rx.Op != token.MUL || rx.X != alloc {
				return false
			}
		case *ssa.Store:
			if rx.Addr != alloc || after(rx) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (ctx *projCheckCtx) verifyBindingEntry(fn *ssa.Function, fv *ssa.FreeVar, b ssa.Value) bool {
	if namedStructOfValOrPtr(fv.Type()) != nil {
		return ctx.verifyStructVal(fn, fv) && ctx.sawProjectedField
	}
	// If Go heap-boxed a local variable `s := &Server{...}` (`b` is `*ssa.Alloc` of `*Server`),
	// `fv` has type `**Server` and closures load `*fv` before projecting fields.
	if _, isAlloc := b.(*ssa.Alloc); isAlloc && isPtrToStructOrPtr(fv.Type()) {
		if fv.Referrers() == nil {
			return false
		}
		for _, r := range *fv.Referrers() {
			switch rx := r.(type) {
			case *ssa.DebugRef:
			case *ssa.UnOp:
				if rx.Op != token.MUL || rx.X != fv || !ctx.verifyStructVal(fn, rx) {
					return false
				}
			default:
				return false
			}
		}
		return ctx.sawProjectedField
	}
	return false
}

func (ctx *projCheckCtx) verifyStructVal(fn *ssa.Function, val ssa.Value) bool {
	if fn == nil || val == nil {
		return false
	}
	if ctx.st.cls.Shareable(val.Type()) || ctx.st.isAutoSyncType(val.Type()) {
		ctx.sawProjectedField = true
		return true
	}
	named := namedStructOfValOrPtr(val.Type())
	if named == nil {
		return false
	}
	key := projVisitKey{fn: fn, val: val}
	if ctx.seen[key] {
		return true
	}
	ctx.seen[key] = true
	if val.Referrers() == nil {
		return true
	}
	origStruct, ok := named.Origin().Underlying().(*types.Struct)
	if !ok {
		return false
	}
	_, valIsPtr := types.Unalias(val.Type()).(*types.Pointer)
	tn := named.Origin().Obj()
	samePkgAsStruct := tn != nil && ctx.st.pass != nil && tn.Pkg() == ctx.st.pass.Pkg

	for _, r := range *val.Referrers() {
		if r.Parent() != fn {
			return false
		}
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return false
			}
		case *ssa.Phi, *ssa.ChangeType:
			if !ctx.verifyStructVal(fn, r.(ssa.Value)) {
				return false
			}
		case *ssa.UnOp:
			if x.Op != token.MUL || x.X != val || !valIsPtr {
				return false
			}
			if structHasSyncPrimitiveField(origStruct, map[types.Type]bool{}) {
				return false
			}
			if !ctx.verifyStructVal(fn, x) {
				return false
			}
		case *ssa.Store:
			if x.Val != val {
				return false
			}
			a, isAlloc := x.Addr.(*ssa.Alloc)
			if !isAlloc || a.Heap || a.Parent() != fn || isResultSlot(a) || allocCaptured(a) || a.Referrers() == nil {
				return false
			}
			for _, ar := range *a.Referrers() {
				switch arx := ar.(type) {
				case *ssa.DebugRef:
				case *ssa.Store:
					if arx != x {
						return false
					}
				case *ssa.UnOp:
					if arx.Op != token.MUL || arx.X != a || !ctx.verifyStructVal(fn, arx) {
						return false
					}
				default:
					return false
				}
			}
		case *ssa.Field:
			if x.X != val || x.Field >= origStruct.NumFields() {
				return false
			}
			ctx.sawProjectedField = true
			sf := origStruct.Field(x.Field)
			if (valIsPtr || (!ctx.st.cls.Shareable(sf.Type()) && !ctx.st.isAutoSyncType(sf.Type()))) && (sf.Exported() || sf.Embedded()) {
				ctx.sawExportedField = true
			}
			if ctx.st.cls.Shareable(sf.Type()) || ctx.st.isAutoSyncType(sf.Type()) {
				continue
			}
			_, fr := ctx.st.fieldSyncInfo(named, x.Field)
			if !fr && samePkgAsStruct && ctx.ctorIfaceFrozen[sf.Origin()] {
				fr = true
				if ctx.ctorIfaceNeedsCaller[sf.Origin()] {
					ctx.sawExportedField = true
				}
			}
			if !fr {
				return false
			}
			if !ctx.verifyProjectedValue(fn, x) {
				return false
			}
		case *ssa.FieldAddr:
			if x.X != val || x.Field >= origStruct.NumFields() {
				return false
			}
			ctx.sawProjectedField = true
			sf := origStruct.Field(x.Field)
			if sf.Exported() || sf.Embedded() {
				ctx.sawExportedField = true
			}
			fw, fr := ctx.st.fieldSyncInfo(named, x.Field)
			if !fw && !fr && samePkgAsStruct && ctx.ctorIfaceFrozen[sf.Origin()] {
				fr = true
				if ctx.ctorIfaceNeedsCaller[sf.Origin()] {
					ctx.sawExportedField = true
				}
			}
			if !fw && !fr {
				return false
			}
			if !ctx.verifyFieldAddrUses(fn, x, sf, fw, fr, samePkgAsStruct) {
				return false
			}
		case *ssa.MakeClosure:
			anon, ok := x.Fn.(*ssa.Function)
			if !ok || !ctx.st.isAnalyzableClosureFn(anon) {
				return false
			}
			if ctx.st.summary(anon).pessimistic {
				return false
			}
			for bi, bnd := range x.Bindings {
				if bnd == val {
					if bi >= len(anon.FreeVars) || !ctx.verifyStructVal(anon, anon.FreeVars[bi]) {
						return false
					}
				}
			}
		case ssa.CallInstruction:
			cc := x.Common()
			if b, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
				if b.Name() == "ssa:wrapnilchk" {
					continue
				}
				return false
			}
			if cc.IsInvoke() || cc.Value == val {
				return false
			}
			callee := cc.StaticCallee()
			if callee == nil {
				return false
			}
			cs := ctx.st.summary(callee)
			if cs.pessimistic {
				return false
			}
			if callee.Pkg == ctx.st.ssa.Pkg && len(callee.Blocks) > 0 {
				for ai, arg := range cc.Args {
					if arg == val {
						if ai >= len(callee.Params) || !ctx.verifyStructVal(callee, callee.Params[ai]) {
							return false
						}
					}
				}
				continue
			}
			for ai, arg := range cc.Args {
				if arg == val {
					if !cs.isProjSync(ai) {
						return false
					}
					if hasExportedOrPromotedField(arg.Type(), map[types.Type]bool{}) {
						ctx.sawExportedField = true
					}
				}
			}
		default:
			return false
		}
	}
	return true
}

func (ctx *projCheckCtx) verifyFieldAddrUses(fn *ssa.Function, fa *ssa.FieldAddr, sf *types.Var, fw, fr, samePkgAsStruct bool) bool {
	if fa.Referrers() == nil {
		return true
	}
	fOrigin := sf.Origin()
	// If this field is proven autoGuarded in its declaring package, tryGuard already verified lock
	// dominance and interior containment across every source function and closure in that package!
	if samePkgAsStruct && fw && ctx.st.autoGuarded[fOrigin] != nil {
		return true
	}
	if fw && isSyncPrimitiveValueType(sf.Type()) {
		for _, r := range *fa.Referrers() {
			switch r.(type) {
			case *ssa.DebugRef:
			case *ssa.Call, *ssa.Defer:
				cc := r.(ssa.CallInstruction).Common()
				if cc.IsInvoke() || len(cc.Args) == 0 || cc.Args[0] != fa {
					return false
				}
				for k := 1; k < len(cc.Args); k++ {
					if cc.Args[k] == fa {
						return false
					}
				}
				callee := cc.StaticCallee()
				if callee == nil || callee.Pkg == nil || callee.Pkg.Pkg == nil {
					return false
				}
				pkgPath := callee.Pkg.Pkg.Path()
				if pkgPath != "sync" && pkgPath != "sync/atomic" {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !fr {
		return false
	}
	for _, r := range *fa.Referrers() {
		switch rx := r.(type) {
		case *ssa.DebugRef:
		case *ssa.UnOp:
			if rx.Op != token.MUL || rx.X != fa {
				return false
			}
			if !ctx.verifyProjectedValue(fn, rx) {
				return false
			}
		case *ssa.FieldAddr:
			if rx.X != fa || !ctx.verifyNestedFrozenAddr(fn, rx) {
				return false
			}
		case *ssa.IndexAddr:
			if rx.X != fa || !ctx.st.addrStrictlyReadOnly(fn, rx, map[ssa.Value]bool{}) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (ctx *projCheckCtx) verifyNestedFrozenAddr(fn *ssa.Function, fa *ssa.FieldAddr) bool {
	if named := namedStructOfValOrPtr(fa.X.Type()); named != nil {
		if s, isStruct := named.Origin().Underlying().(*types.Struct); isStruct && fa.Field < s.NumFields() {
			sf := s.Field(fa.Field)
			if sf.Exported() || sf.Embedded() {
				ctx.sawExportedField = true
			}
			fw, fr := ctx.st.fieldSyncInfo(named, fa.Field)
			tn := named.Origin().Obj()
			samePkg := tn != nil && ctx.st.pass != nil && tn.Pkg() == ctx.st.pass.Pkg
			if fw || fr {
				return ctx.verifyFieldAddrUses(fn, fa, sf, fw, fr, samePkg)
			}
		}
	}
	return ctx.st.addrStrictlyReadOnly(fn, fa, map[ssa.Value]bool{})
}

func (ctx *projCheckCtx) verifyProjectedValue(fn *ssa.Function, val ssa.Value) bool {
	if val == nil || ctx.st.cls.Shareable(val.Type()) || ctx.st.isAutoSyncType(val.Type()) {
		return true
	}
	if namedStructOfValOrPtr(val.Type()) != nil && ctx.verifyStructVal(fn, val) {
		return true
	}
	if isSignatureType(val.Type()) || isInterfaceType(val.Type()) {
		return ctx.callableOrIfaceProjectedReadOnly(fn, val, map[ssa.Value]bool{})
	}
	return ctx.st.containerValueStrictlyReadOnly(fn, val, map[ssa.Value]bool{})
}

func isInterfaceType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// callableOrIfaceProjectedReadOnly checks that a value loaded from an already-proven closedReadOnly
// func or interface field is only called with Shareable/AutoSync/fresh arguments (never stored into
// a mutable container or passed to a mutating sink).
func (ctx *projCheckCtx) callableOrIfaceProjectedReadOnly(fn *ssa.Function, val ssa.Value, seen map[ssa.Value]bool) bool {
	if seen[val] {
		return true
	}
	seen[val] = true
	if val.Referrers() == nil {
		return true
	}
	for _, r := range *val.Referrers() {
		if r.Parent() != fn {
			return false
		}
		switch x := r.(type) {
		case *ssa.DebugRef:
		case *ssa.BinOp:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return false
			}
		case *ssa.Phi, *ssa.ChangeInterface:
			if !ctx.callableOrIfaceProjectedReadOnly(fn, r.(ssa.Value), seen) {
				return false
			}
		case *ssa.Call:
			if x.Call.IsInvoke() {
				if x.Call.Value != val {
					return false
				}
				for _, a := range x.Call.Args {
					if !ctx.st.cls.Shareable(a.Type()) && !ctx.st.isAutoSyncType(a.Type()) && !isFreshInFn(fn, a) {
						return false
					}
				}
				continue
			}
			if x.Call.Value == val {
				for _, a := range x.Call.Args {
					if !ctx.st.cls.Shareable(a.Type()) && !ctx.st.isAutoSyncType(a.Type()) && !isFreshInFn(fn, a) {
						return false
					}
				}
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}
