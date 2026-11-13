package ownership

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// checkTransfers checks every place a value crosses to another goroutine (or to an owned parameter):
// the value must be owned (SAFE002), must not also escape elsewhere (SAFE004), and must not be used
// again by the sender (SAFE003).
func (st *state) checkTransfers(g *graph) {
	for _, ins := range g.cutCalls {
		st.report(posOf(ins, ins), TransferCut, fmt.Sprintf(
			"what this call may run could not be followed to the end (%s), so whether something not reached publishes or keeps an argument passed here is not known",
			originCutNote()))
	}
	for _, t := range g.transfers {
		st.stats.Transfers++
		// A //gosree:unsafe on the transfer covers everything the transfer causes, including uses
		// after it that lie outside the scope.
		muted := st.ix.Suppressed(t.pos)
		before, raised := st.mutedFindings, st.findings
		st.muted, st.transferAt = muted, t.pos
		st.checkTransfer(g, t)
		st.muted, st.transferAt = false, token.NoPos
		if st.findings > raised {
			st.stats.FlaggedTransfers++
		}
		if muted && st.mutedFindings > before {
			st.stats.SuppressedTransfers++
			st.ix.Mute(t.pos)
		}
	}
}

func (st *state) checkTransfer(g *graph, t transfer) {
	borrowEscaped := st.checkBorrowEscape(g, t)
	handlerFlagged := false
	if sp, ok := spawnOf(t.instr); ok && sp.repeated && t.v == sp.fn && !borrowEscaped {
		handlerFlagged = st.checkHandlerWrites(sp, t.pos)
	}
	if borrowEscaped && t.what != "captured by a goroutine" {
		return
	}
	if !borrowEscaped && (st.isSpawnTransfer(t) || isSharedContainerPublish(t)) && st.isAutoSyncType(unwrapTransferVal(t.v).Type()) {
		return
	}
	n := g.val(t.v)
	if n == nil {
		return
	}
	reg := g.region(n)
	reachMask := reachableBlocksForTransfer(t)
	after := reachableAfterTransferWithMask(g, t, reachMask)

	if !borrowEscaped && st.isVerifiedHandlerReg(g, t, reg, after, handlerFlagged) {
		return
	}
	if !borrowEscaped && st.isVerifiedScopedForkJoin(g, t, after, reachMask) {
		return
	}
	if !borrowEscaped && st.isSpawnTransfer(t) && !handlerFlagged && st.isProjectedSyncTransfer(g, t, after) {
		return
	}

	var foreign, escape []event
	for c := range reg {
		for _, e := range c.events {
			if t.freezesToImm && e.global != nil && st.isInitOnlyImmutableGlobal(e.global) {
				continue
			}
			switch e.kind {
			case evForeign:
				foreign = append(foreign, e)
			case evEscape:
				// An escape after the move is itself a use after the move; SAFE003 reports it.
				if e.instr != nil && after(e.instr) {
					continue
				}
				escape = append(escape, e)
			}
		}
	}
	what := describe(t.v)
	isForwardedCallback := st.isForwardedSpawnerTransfer(g, t, foreign)
	if !borrowEscaped && !isForwardedCallback && len(foreign) > 0 {
		e := st.earliest(foreign)
		why := "it may alias " + e.desc + st.at(e.pos)
		if e.desc == what {
			why = "the caller still holds it (mark the parameter //gosree:iso to take ownership)"
		}
		st.report(t.pos, NotOwned, fmt.Sprintf("%s is %s but is not owned here: %s", what, t.what, why))
	}
	if !borrowEscaped && len(escape) > 0 {
		e := st.earliest(escape)
		st.report(t.pos, Escapes, fmt.Sprintf("%s is %s but also escapes: %s%s",
			what, t.what, e.desc, st.at(e.pos)))
	}
	st.checkUseAfter(g, t, n, reg, reachMask, after)
}

func (st *state) checkUseAfter(g *graph, t transfer, root *node, reg map[*node]bool, reachMask []bool, after func(ssa.Instruction) bool) {
	root = root.find()
	vals, cached := g.regionVals[root]
	if !cached {
		for v, n := range g.nodes {
			if n == nil || !reg[n.find()] {
				continue
			}
			switch v.(type) {
			case *ssa.Global, *ssa.Const, *ssa.Function:
				continue
			}
			vals = append(vals, v)
		}
		sort.Slice(vals, func(i, j int) bool { return compareSSAValOrder(g, vals[i], vals[j]) < 0 })
		if g.regionVals != nil {
			g.regionVals[root] = vals
		}
	}
	joins := st.joins(g, t)
	type found struct {
		w ssa.Value
		u use
	}
	var uses []found
	for _, w := range vals {
		if t.freezesToImm && st.directInitOnlyGlobal(w) {
			continue
		}
		wj := joins
		if len(wj) == 0 {
			wj = st.joinsForValue(g, t, w)
		}
		u, ok := useAfterForGraph(g, t.instr, t.caseBlock, w, wj, reachMask)
		if !ok {
			continue
		}
		uses = append(uses, found{w, u})
	}
	// A use after the move races with the goroutine only if the two touch an overlapping place,
	// one of them writes, and no mutex both hold excludes them (footprint.go). When that is proved
	// for every access the sender makes, a use of memory the proof followed is not reported.
	var verdict moveVerdict
	if len(uses) > 0 || len(g.defers) > 0 {
		verdict = st.refineMove(g, t, joins, vals)
	}
	for _, f := range uses {
		w, u := f.w, f.u
		if verdict.benign && verdict.covered[w] {
			continue
		}
		cat := UseAfter
		note := ""
		if u.synced {
			cat = UseAfterSynced
			note = " (after a synchronisation point, but not one that joins the goroutine it was moved to)"
		}
		st.report(u.pos, cat, fmt.Sprintf("%s is used after it was %s%s%s",
			describe(w), t.what, st.at(t.pos), note))
	}
	st.checkDeferredUse(g, t, reg, after, verdict)
}

func (st *state) isSpawnTransfer(t transfer) bool {
	if _, isGo := t.instr.(*ssa.Go); isGo {
		return true
	}
	_, ok := st.spawnSiteAt(t.instr, t.index)
	return ok
}

func isSharedContainerPublish(t transfer) bool {
	return strings.HasPrefix(t.what, "published via ") || strings.HasPrefix(t.what, "stored in shared ")
}

func unwrapTransferVal(v ssa.Value) ssa.Value {
	for v != nil {
		switch x := v.(type) {
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		default:
			return v
		}
	}
	return nil
}

func pointsToSet(g *graph, n *node) map[*node]bool {
	out := map[*node]bool{}
	if n == nil {
		return out
	}
	for _, p := range g.pointsTo(n) {
		out[p] = true
	}
	return out
}

func pointsToIntersects(g *graph, c *node, targetPts map[*node]bool) bool {
	if c == nil {
		return false
	}
	for _, p := range g.pointsTo(c) {
		if targetPts[p] {
			return true
		}
	}
	return false
}

func (st *state) nodeWriteMayTouchTarget(g *graph, targetPts map[*node]bool, c *node, ins ssa.Instruction, allowSiblingRepeatedReg bool) bool {
	if allowSiblingRepeatedReg {
		if sp2, ok := spawnOf(ins); ok && sp2.repeated && strings.Contains(sp2.via, "net/http.") {
			return false
		}
		if call, isCall := ins.(*ssa.Call); isCall && !call.Call.IsInvoke() {
			if callee := call.Call.StaticCallee(); callee != nil {
				sum := st.summary(callee)
				if !sum.pessimistic {
					mayNonReg := false
					for ai, arg := range call.Call.Args {
						if an := g.nodes[arg]; an != nil && pointsToIntersects(g, an.find(), targetPts) && sum.writesNonReg(ai) {
							mayNonReg = true
							break
						}
					}
					if !mayNonReg {
						return false
					}
				}
			}
		}
	}
	if targetPts[c] {
		return true
	}
	if !pointsToIntersects(g, c, targetPts) {
		return false
	}
	if _, isDirect := writeOf(ins); isDirect {
		return false
	}
	return true
}

func (st *state) deferMayWriteTarget(g *graph, d *ssa.Defer, targetPts map[*node]bool) bool {
	cc := d.Common()
	if !cc.IsInvoke() {
		if callee := cc.StaticCallee(); callee != nil {
			sum := st.summary(callee)
			if !sum.pessimistic {
				for ai, arg := range cc.Args {
					if an := g.nodes[arg]; an != nil && pointsToIntersects(g, an.find(), targetPts) && (sum.writes(ai) || sum.spawns(ai)) {
						return true
					}
				}
				return false
			}
		}
		if mc, isMC := cc.Value.(*ssa.MakeClosure); isMC {
			if fn, ok := mc.Fn.(*ssa.Function); ok && len(fn.Blocks) > 0 {
				sum := st.summary(fn)
				if !sum.pessimistic {
					fg := st.graphOf(fn)
					for bi, bnd := range mc.Bindings {
						if bn := g.nodes[bnd]; bn != nil && pointsToIntersects(g, bn.find(), targetPts) {
							if sum.writesFree(bi) || (bi < len(fn.FreeVars) && st.childLeaksOrTransfersFreeVar(fg, fn.FreeVars[bi])) {
								return true
							}
						}
					}
					for ai, arg := range cc.Args {
						if an := g.nodes[arg]; an != nil && pointsToIntersects(g, an.find(), targetPts) && (sum.writes(ai) || sum.spawns(ai)) {
							return true
						}
					}
					return false
				}
			}
		}
	}
	var opBuf [8]*ssa.Value
	for _, op := range d.Operands(opBuf[:0]) {
		if op != nil && *op != nil {
			if dn := g.nodes[*op]; dn != nil && pointsToIntersects(g, dn.find(), targetPts) {
				return true
			}
		}
	}
	return false
}

func (st *state) isForwardedSpawnerTransfer(g *graph, t transfer, foreign []event) bool {
	if !st.isSpawnTransfer(t) || len(foreign) == 0 || g == nil || g.fn == nil {
		return false
	}
	sum := st.summary(g.fn)
	for _, e := range foreign {
		if e.param < 0 || !sum.spawns(e.param) {
			return false
		}
	}
	return true
}

func (st *state) isGuardedGlobal(gl *ssa.Global) bool {
	if gl == nil {
		return false
	}
	v, ok := gl.Object().(*types.Var)
	if !ok {
		return false
	}
	return st.ix.Guarded[v] != nil || st.autoGuarded[v] != nil
}

func (st *state) handlerHasUnguardedHiddenWrite(hb handlerBody) bool {
	seen := map[*ssa.Function]bool{}
	var visit func(fn *ssa.Function, isAnonInHandler bool) bool
	visit = func(fn *ssa.Function, isAnonInHandler bool) bool {
		if fn == nil || len(fn.Blocks) == 0 || seen[fn] {
			return false
		}
		seen[fn] = true
		if len(seen) > 128 {
			return true
		}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if _, isGo := ins.(*ssa.Go); isGo {
					fg := st.graphOf(fn)
					for _, tr := range fg.transfers {
						if tr.instr != ins {
							continue
						}
						if st.isAutoSyncType(unwrapTransferVal(tr.v).Type()) {
							continue
						}
						rm := reachableBlocksForTransfer(tr)
						af := reachableAfterTransferWithMask(fg, tr, rm)
						if !st.isVerifiedScopedForkJoin(fg, tr, af, rm) {
							return true
						}
					}
					continue
				}
				if sp2, isSp := st.spawnSiteOf(ins); isSp {
					if sp2.repeated {
						if !strings.Contains(sp2.via, "net/http.") {
							return true
						}
						continue
					}
					fg := st.graphOf(fn)
					for _, tr := range fg.transfers {
						if tr.instr != ins {
							continue
						}
						if st.isAutoSyncType(unwrapTransferVal(tr.v).Type()) {
							continue
						}
						rm := reachableBlocksForTransfer(tr)
						af := reachableAfterTransferWithMask(fg, tr, rm)
						if !st.isVerifiedScopedForkJoin(fg, tr, af, rm) {
							return true
						}
					}
					continue
				}
				if isAnonInHandler {
					if w, isW := writeOf(ins); isW {
						if _, guarded := st.slotOf(w.addr); !guarded && !isFreshInFn(fn, w.addr) {
							return true
						}
					}
				}
				if mc, isMC := ins.(*ssa.MakeClosure); isMC {
					if anon, ok := mc.Fn.(*ssa.Function); ok && st.isAnalyzableClosureFn(anon) && !st.isProjectedSyncClosureInFn(fn, mc) {
						hasSharedBind := false
						for _, bnd := range mc.Bindings {
							if !st.cls.Shareable(bnd.Type()) && !st.isAutoSyncType(bnd.Type()) && !isFreshInFn(fn, bnd) {
								hasSharedBind = true
								break
							}
						}
						if hasSharedBind && visit(anon, true) {
							return true
						}
					}
				}
				if call, isCall := ins.(ssa.CallInstruction); isCall && !call.Common().IsInvoke() {
					if callee := call.Common().StaticCallee(); callee != nil && callee.Pkg == st.ssa.Pkg && len(callee.Blocks) > 0 {
						if visit(callee, isAnonInHandler) {
							return true
						}
					} else if callee != nil {
						cs := st.summary(callee)
						if isAnonInHandler {
							for ai, arg := range call.Common().Args {
								if !st.cls.Shareable(arg.Type()) && !st.isAutoSyncType(arg.Type()) && !isFreshInFn(fn, arg) && cs.writes(ai) {
									return true
								}
							}
						} else if callee.Pkg != st.ssa.Pkg {
							for ai, arg := range call.Common().Args {
								if !st.cls.Shareable(arg.Type()) && !st.isAutoSyncType(arg.Type()) && !isFreshInFn(fn, arg) && (cs.pessimistic || cs.spawns(ai)) {
									return true
								}
							}
						}
					}
				}
			}
		}
		return false
	}
	return visit(hb.fn, false)
}

func isFreshInFn(fn *ssa.Function, v ssa.Value) bool {
	for depth := 0; depth < 8 && v != nil; depth++ {
		switch x := v.(type) {
		case *ssa.Alloc:
			if x.Parent() != fn {
				return false
			}
			// A heap box for a parameter (`t0 = new T (param); *t0 = param`) holds caller memory, not fresh memory.
			if x.Referrers() != nil {
				for _, r := range *x.Referrers() {
					if s, isStore := r.(*ssa.Store); isStore && s.Addr == x {
						if _, isParam := s.Val.(*ssa.Parameter); isParam {
							return false
						}
						if _, isFV := s.Val.(*ssa.FreeVar); isFV {
							return false
						}
					}
				}
			}
			return true
		case *ssa.MakeSlice, *ssa.MakeMap:
			return x.Parent() == fn
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		default:
			return false
		}
	}
	return false
}

func (st *state) handlerForeignCallersClean(g *graph, reg map[*node]bool, targetPts map[*node]bool) bool {
	var hasForeignParam bool
	paramSet := map[int]bool{}
	for c := range reg {
		for _, e := range c.events {
			if e.global != nil && (st.isInitOnlyImmutableGlobal(e.global) || st.isGuardedGlobal(e.global)) {
				continue
			}
			if e.kind == evReturn && pointsToIntersects(g, c, targetPts) && !st.isSafeReturnEvent(g, e, targetPts) {
				return false
			}
			if e.kind == evForeign {
				if e.param < 0 {
					return false
				}
				hasForeignParam = true
				paramSet[e.param] = true
			}
		}
	}
	if !hasForeignParam {
		return true
	}
	if st.allParamsSharedRead(g.fn, paramSet) {
		return true
	}
	if g.fn == nil || g.fn.Synthetic != "" {
		return false
	}
	if obj, ok := g.fn.Object().(*types.Func); ok && obj.Exported() && st.pass.Pkg.Name() != "main" {
		return false
	}
	// g.fn is unexported or in package main: verify all callers in st.allFns.
	if g.fn.Parent() != nil {
		return false
	}
	for _, caller := range st.allFns {
		if len(caller.Blocks) == 0 {
			continue
		}
		for _, b := range caller.Blocks {
			for _, ins := range b.Instrs {
				if mc, isMC := ins.(*ssa.MakeClosure); isMC && mc.Fn == g.fn {
					return false
				}
				if _, isGo := ins.(*ssa.Go); isGo && ins.(*ssa.Go).Call.StaticCallee() == g.fn {
					return false
				}
				if df, isDef := ins.(*ssa.Defer); isDef && df.Call.StaticCallee() == g.fn {
					return false
				}
				call, isCall := ins.(*ssa.Call)
				if !isCall || call.Call.IsInvoke() {
					continue
				}
				if call.Call.StaticCallee() != g.fn {
					for _, a := range call.Call.Args {
						if a == g.fn {
							return false
						}
					}
					continue
				}
				cg := st.graphOf(caller)
				afterCall := reachableAfter(call)
				for pIdx := range paramSet {
					if pIdx >= len(call.Call.Args) {
						return false
					}
					arg := call.Call.Args[pIdx]
					if st.cls.Shareable(arg.Type()) || cg.isAutoSyncCapture(call, arg) {
						continue
					}
					an := cg.val(arg)
					if an == nil {
						continue
					}
					aReg := cg.region(an)
					aPts := pointsToSet(cg, an)
					for cc := range aReg {
						for _, ce := range cc.events {
							if ce.global != nil && (st.isInitOnlyImmutableGlobal(ce.global) || st.isGuardedGlobal(ce.global)) {
								continue
							}
							if ce.kind == evForeign && pointsToIntersects(cg, cc, aPts) {
								if ce.param >= 0 && st.sums[caller] != nil && st.sums[caller].isSharedRead(ce.param) {
									continue
								}
								return false
							}
							if ce.kind == evReturn && pointsToIntersects(cg, cc, aPts) && !st.isSafeReturnEvent(cg, ce, aPts) {
								return false
							}
							if ce.kind == evWrite && ce.instr != nil && st.nodeWriteMayTouchTarget(cg, aPts, cc, ce.instr, true) {
								if afterCall(ce.instr) {
									return false
								}
								if _, isDefer := ce.instr.(*ssa.Defer); isDefer {
									return false
								}
								if st.isUnjoinedConcurrentSpawnBefore(cg, ce.instr, call) {
									return false
								}
							}
						}
					}
					for _, df := range cg.defers {
						if st.deferMayWriteTarget(cg, df, aPts) {
							return false
						}
					}
				}
			}
		}
	}
	return true
}

// isVerifiedHandlerReg (Mode H) reports whether t is a repeated HTTP handler registration whose
// handler bodies are completely resolved in this program, raised zero SAFE008 / incomplete
// findings, perform zero unguarded writes to their shared roots, and whose registering function
// performs no concurrent or post-registration writes to the shared region.
func (st *state) isVerifiedHandlerReg(g *graph, t transfer, reg map[*node]bool, after func(ssa.Instruction) bool, handlerFlagged bool) bool {
	if handlerFlagged || g == nil {
		return false
	}
	sp, ok := spawnOf(t.instr)
	if !ok || !sp.repeated || t.v != sp.fn {
		return false
	}
	prog := st.ssa.Pkg.Prog
	if !hasCompleteHandlerRoots(prog, sp) {
		return false
	}
	roots := handlerRoots(prog, sp)
	for _, hb := range roots {
		if len(hb.fn.Blocks) == 0 {
			return false
		}
		hbSum := st.summary(hb.fn)
		if hbSum.pessimistic {
			return false
		}
		if st.handlerRootsHaveCutCall(hb.fn, hb.roots) {
			return false
		}
		if st.handlerHasUnguardedHiddenWrite(hb) {
			return false
		}
	}
	targetPts := pointsToSet(g, g.val(t.v))
	if g.hasCutCallTouching(targetPts) {
		return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
	}
	if !st.handlerForeignCallersClean(g, reg, targetPts) {
		return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
	}
	for c := range reg {
		for _, e := range c.events {
			if e.global != nil && st.mutatedGlobals[e.global] && !st.isGuardedGlobal(e.global) {
				return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
			}
			if e.kind == evWrite && e.instr != nil && st.nodeWriteMayTouchTarget(g, targetPts, c, e.instr, true) {
				if after(e.instr) {
					return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
				}
				if _, isDefer := e.instr.(*ssa.Defer); isDefer {
					return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
				}
				if st.isUnjoinedConcurrentSpawnBefore(g, e.instr, t.instr) {
					return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
				}
			}
		}
	}
	for _, d := range g.defers {
		if st.deferMayWriteTarget(g, d, targetPts) {
			return st.isPerBindingVerifiedHandlerReg(g, t, sp, after)
		}
	}
	return true
}

func (st *state) isUnjoinedConcurrentSpawnBefore(g *graph, spawnIns, beforeIns ssa.Instruction) bool {
	_, isGo := spawnIns.(*ssa.Go)
	if !isGo {
		if _, ok := st.spawnSiteOf(spawnIns); !ok {
			return false
		}
	}
	for _, other := range g.transfers {
		if other.instr != spawnIns {
			continue
		}
		joins := st.joins(g, other)
		if !everyPathReachesJoinBefore(g, spawnIns, beforeIns, joins) {
			return true
		}
	}
	return false
}

func everyPathReachesJoinBefore(g *graph, startIns, targetIns ssa.Instruction, joins map[ssa.Instruction]bool) bool {
	if len(joins) == 0 || startIns.Parent() == nil || startIns.Parent() != targetIns.Parent() {
		return false
	}
	fn := startIns.Parent()
	visited := make([]bool, len(fn.Blocks))
	type item struct {
		b    *ssa.BasicBlock
		from int
	}
	queue := []item{{startIns.Block(), g.idxOf(startIns) + 1}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		stopped := false
		for i := it.from; i < len(it.b.Instrs); i++ {
			ins := it.b.Instrs[i]
			if ins == targetIns {
				return false
			}
			if joins[ins] {
				if _, isIf := ins.(*ssa.If); !isIf {
					stopped = true
					break
				}
			}
		}
		if stopped {
			continue
		}
		live := liveSuccs(it.b)
		for si, succ := range it.b.Succs {
			if iff, ok := lastInstr(it.b).(*ssa.If); ok && joins[iff] {
				_, negated := unwrapBoolCond(iff.Cond)
				if (si == 0) != negated {
					continue
				}
			}
			if len(live) == 1 && live[0] != succ {
				continue
			}
			if succ.Index >= 0 && succ.Index < len(visited) {
				if visited[succ.Index] {
					continue
				}
				visited[succ.Index] = true
			}
			queue = append(queue, item{succ, 0})
		}
	}
	return true
}

func everyPathJoinsBeforeReturn(g *graph, t transfer, joins map[ssa.Instruction]bool) bool {
	if len(joins) == 0 || g == nil || g.fn == nil {
		return false
	}
	inWindow := instrsAfter(g, t, joins, nil)
	hasReturn := false
	for _, b := range g.fn.Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		if ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return); ok {
			hasReturn = true
			if inWindow[ret] {
				return false
			}
		}
	}
	return hasReturn
}

// isVerifiedScopedForkJoin (Mode J) proves structured-concurrency safety for a single-shot
// goroutine closure whose parent joins it on every path before returning (WaitGroup.Go,
// errgroup.Group.Go, SafeGoWG, or channel join), checking each captured binding individually as
// either Shareable/AutoSync, scoped read-only, or scoped fresh-owned.
func (st *state) isVerifiedScopedForkJoin(g *graph, t transfer, after func(ssa.Instruction) bool, reachMask []bool) bool {
	if g == nil {
		return false
	}
	if goIns, isGo := t.instr.(*ssa.Go); isGo && (t.index >= 0 || len(goIns.Call.Args) > 0) {
		return false
	}
	sp, ok := st.spawnSiteAt(t.instr, t.index)
	if !ok || sp.repeated || t.v != sp.fn {
		return false
	}
	mc, ok := t.v.(*ssa.MakeClosure)
	if !ok {
		return false
	}
	fn, ok := mc.Fn.(*ssa.Function)
	if !ok || len(fn.Blocks) == 0 {
		return false
	}
	joins := st.joins(g, t)
	if len(joins) == 0 {
		joins = st.spawnerWaitJoins(g, t)
	}
	if !everyPathJoinsBeforeReturn(g, t, joins) {
		return false
	}
	cs := st.summary(fn)
	if cs.pessimistic {
		return false
	}
	fg := st.graphOf(fn)
	inWindow := instrsAfter(g, t, joins, nil)
	inWinFn := func(ins ssa.Instruction) bool { return inWindow[ins] }
	for j, b := range mc.Bindings {
		if j >= len(fn.FreeVars) {
			return false
		}
		fv := fn.FreeVars[j]
		if st.cls.Shareable(b.Type()) || g.isAutoSyncCapture(t.instr, b) {
			continue
		}
		if bn := g.val(b); bn != nil && g.hasCutCallTouching(pointsToSet(g, bn)) {
			return false
		}
		if fvn := fg.val(fv); fvn != nil && fg.hasCutCallTouching(pointsToSet(fg, fvn)) {
			return false
		}
		if st.isProjectedSyncBinding(g, t, fn, j, b, inWinFn, -1) {
			continue
		}
		if !st.childNonFormatLeaksFreeVar(fg, fv, j) {
			if !cs.writesFree(j) && st.isScopedReadOnlyInParent(g, t, b, inWindow) {
				continue
			}
			if st.isSingleRootMoveBenign(g, t, fn, mc, j, b, joins, after, reachMask) {
				continue
			}
		}
		if helperMC, isMC := b.(*ssa.MakeClosure); isMC && helperMC.Parent() == g.fn && freeVarOnlyCalledDirectly(fv) {
			if st.isHelperClosureBindingBenign(g, t, helperMC, joins, after, reachMask) {
				continue
			}
		}
		return false
	}
	return true
}

func (st *state) childLeaksOrTransfersFreeVar(fg *graph, fv *ssa.FreeVar) bool {
	n := fg.val(fv)
	if n == nil {
		return false
	}
	reg := fg.region(n)
	for c := range reg {
		for _, e := range c.events {
			if e.kind == evTransfer {
				return true
			}
			if e.kind == evEscape && !fg.formatsOnly(e, reg) {
				return true
			}
		}
	}
	return false
}

func (st *state) childNonFormatLeaksFreeVar(fg *graph, fv *ssa.FreeVar, fvIdx int) bool {
	n := fg.val(fv)
	if n == nil {
		return false
	}
	reg := fg.region(n)
	for c := range reg {
		for _, e := range c.events {
			switch e.kind {
			case evWrite:
				continue
			case evForeign:
				if e.free != fvIdx {
					return true
				}
			case evEscape:
				if !fg.formatsOnly(e, reg) {
					return true
				}
			default:
				return true
			}
		}
	}
	return false
}

func (st *state) isScopedReadOnlyInParent(g *graph, t transfer, b ssa.Value, inWindow map[ssa.Instruction]bool) bool {
	bn := g.val(b)
	if bn == nil {
		return true
	}
	bReg := g.region(bn)
	targetPts := pointsToSet(g, bn)
	for c := range bReg {
		for _, e := range c.events {
			if e.global != nil && st.mutatedGlobals[e.global] && !st.isGuardedGlobal(e.global) {
				return false
			}
			if e.kind == evWrite && e.instr != nil && st.nodeWriteMayTouchTarget(g, targetPts, c, e.instr, false) {
				if inWindow[e.instr] {
					return false
				}
				if _, isDefer := e.instr.(*ssa.Defer); isDefer {
					return false
				}
				if e.instr != t.instr && st.isUnjoinedConcurrentSpawnBefore(g, e.instr, t.instr) {
					return false
				}
			}
		}
	}
	for _, d := range g.defers {
		if st.deferMayWriteTarget(g, d, targetPts) {
			return false
		}
	}
	return true
}

func (st *state) isHelperClosureBindingBenign(g *graph, t transfer, helperMC *ssa.MakeClosure, joins map[ssa.Instruction]bool, after func(ssa.Instruction) bool, reachMask []bool) bool {
	helperFn, ok := helperMC.Fn.(*ssa.Function)
	if !ok || len(helperFn.Blocks) == 0 {
		return false
	}
	hs := st.summary(helperFn)
	if hs.pessimistic {
		return false
	}
	hg := st.graphOf(helperFn)
	inWindow := instrsAfter(g, t, joins, nil)
	for k, hb := range helperMC.Bindings {
		if k >= len(helperFn.FreeVars) {
			return false
		}
		if st.cls.Shareable(hb.Type()) || g.isAutoSyncCapture(t.instr, hb) {
			continue
		}
		hfv := helperFn.FreeVars[k]
		if hbn := g.val(hb); hbn != nil && g.hasCutCallTouching(pointsToSet(g, hbn)) {
			return false
		}
		if hfvn := hg.val(hfv); hfvn != nil && hg.hasCutCallTouching(pointsToSet(hg, hfvn)) {
			return false
		}
		if st.childLeaksOrTransfersFreeVar(hg, hfv) || hs.freeLeaks(k) {
			return false
		}
		if !hs.writesFree(k) && st.isScopedReadOnlyInParent(g, t, hb, inWindow) {
			continue
		}
		if st.isSingleRootMoveBenign(g, t, helperFn, helperMC, k, hb, joins, after, reachMask) {
			continue
		}
		return false
	}
	return true
}

func (st *state) isSingleRootMoveBenign(g *graph, t transfer, fn *ssa.Function, mc *ssa.MakeClosure, bindIdx int, b ssa.Value, joins map[ssa.Instruction]bool, after func(ssa.Instruction) bool, reachMask []bool) bool {
	if bn := g.val(b); bn != nil {
		for c := range g.region(bn) {
			for _, e := range c.events {
				if e.kind == evForeign {
					return false
				}
			}
		}
	}
	loopHdrs := map[int64]*ssa.BasicBlock{}
	fv := fn.FreeVars[bindIdx]
	d, cellLoads, unwrappedCell := unwrapDerivedPlaceCell(fv, b, t.instr, joins, loopHdrs)
	if !unwrappedCell {
		var ok bool
		d, ok = moveRootAt(b, t.instr, joins, loopHdrs)
		if !ok || d.root.Parent() != g.fn {
			return false
		}
	} else if d.root.Parent() != g.fn {
		return false
	}
	parent := &fpFrame{fn: g.fn}
	var gs *guardState
	if st.hasLockOps(st.allFns) {
		gs = st.ensureGuardSummaries(st.allFns)
	}
	child := &fpWalker{st: st, gs: gs, budget: fpBudget, spawnSite: t.instr, joins: joins, loopHdrs: loopHdrs, covered: map[ssa.Value]bool{}, seen: map[fpSeenKey]bool{}}
	inner := &fpFrame{fn: fn, parent: parent, mc: mc, site: t.instr}
	if ci, ok := t.instr.(ssa.CallInstruction); ok {
		inner.call = ci
	}
	if unwrappedCell {
		for _, ld := range cellLoads {
			child.walk(inner, ld, d)
			if child.top {
				return false
			}
		}
	} else {
		child.walk(inner, fv, d)
	}
	if child.top {
		return false
	}
	sender := &fpWalker{st: st, gs: gs, budget: fpBudget, allowReturn: true, spawnSite: t.instr, joins: joins, loopHdrs: loopHdrs, covered: map[ssa.Value]bool{}, seen: map[fpSeenKey]bool{}}
	sender.walk(parent, d.root, desig{root: d.root})
	if sender.top {
		return false
	}
	def, _ := d.root.(ssa.Instruction)
	afterRoot := instrsAfter(g, t, joins, def)
	if afterRoot[t.instr] {
		var selfLoopDisjoint func(int64) bool
		if len(loopHdrs) > 0 {
			selfLoopDisjoint = func(loopID int64) bool {
				hdr := loopHdrs[loopID]
				return hdr != nil && !canReachInstrWithoutBlockOrJoins(g.fn, t.instr, t.instr, hdr, joins)
			}
		}
		for _, c1 := range child.acc {
			for _, c2 := range child.acc {
				if conflictsWithLoop(c1, c2, selfLoopDisjoint) {
					return false
				}
			}
		}
	}
	for _, a := range sender.acc {
		if !a.exit && !afterRoot[a.site] && !(a.site != t.instr && st.isUnjoinedConcurrentSpawnBefore(g, a.site, t.instr)) {
			continue
		}
		var loopDisjoint func(int64) bool
		if !a.exit && a.site != nil && len(loopHdrs) > 0 &&
			!(a.site != t.instr && st.isUnjoinedConcurrentSpawnBefore(g, a.site, t.instr)) {
			loopDisjoint = func(loopID int64) bool {
				hdr := loopHdrs[loopID]
				return hdr != nil && !canReachInstrWithoutBlockOrJoins(g.fn, t.instr, a.site, hdr, joins)
			}
		}
		for _, c := range child.acc {
			if conflictsWithLoop(a, c, loopDisjoint) {
				return false
			}
		}
	}
	if bn := g.val(b); bn != nil {
		bReg := g.region(bn)
		for w, n := range g.nodes {
			if n == nil || !bReg[n.find()] {
				continue
			}
			switch w.(type) {
			case *ssa.Global, *ssa.Const, *ssa.Function:
				continue
			}
			if _, used := useAfterForGraph(g, t.instr, t.caseBlock, w, joins, reachMask); used && !sender.covered[w] {
				return false
			}
		}
		if len(g.defers) > 0 {
			var opBuf [8]*ssa.Value
			for _, df := range g.defers {
				if after(df) {
					continue
				}
				for _, op := range df.Operands(opBuf[:0]) {
					if op != nil && *op != nil {
						if dn := g.nodes[*op]; dn != nil && bReg[dn.find()] && !sender.covered[*op] {
							return false
						}
					}
				}
			}
		}
	}
	return true
}

func compareSSAValOrder(g *graph, a, b ssa.Value) int {
	if a == b {
		return 0
	}
	if c := cmp.Compare(a.Pos(), b.Pos()); c != 0 {
		return c
	}
	kindRank := func(v ssa.Value) (int, int, int) {
		switch x := v.(type) {
		case *ssa.Parameter:
			if g != nil && g.fn != nil {
				return 0, slices.Index(g.fn.Params, x), 0
			}
			return 0, 0, 0
		case *ssa.FreeVar:
			if g != nil && g.fn != nil {
				return 1, slices.Index(g.fn.FreeVars, x), 0
			}
			return 1, 0, 0
		case ssa.Instruction:
			blkIdx := -1
			if x.Block() != nil {
				blkIdx = x.Block().Index
			}
			return 2, blkIdx, g.idxOf(x)
		default:
			return 3, 0, 0
		}
	}
	ka, b1, i1 := kindRank(a)
	kb, b2, i2 := kindRank(b)
	return cmp.Or(cmp.Compare(ka, kb), cmp.Compare(b1, b2), cmp.Compare(i1, i2), cmp.Compare(a.Name(), b.Name()))
}

// checkDeferredUse reports deferred calls registered before a move that touch the moved memory: they
// run when the function returns, which is after the move. (A defer registered after the move is an
// ordinary use and checkUseAfter reports it.)
func (st *state) checkDeferredUse(g *graph, t transfer, reg map[*node]bool, after func(ssa.Instruction) bool, verdict moveVerdict) {
	if len(g.defers) == 0 {
		return
	}
	var opBuf [8]*ssa.Value
	for _, d := range g.defers {
		if after(d) {
			continue
		}
		for _, op := range d.Operands(opBuf[:0]) {
			if op == nil || *op == nil {
				continue
			}
			// A deferred access the footprint followed was checked against the goroutine, as
			// running at exit.
			if verdict.benign && verdict.covered[*op] {
				continue
			}
			if n := g.nodes[*op]; n != nil && reg[n.find()] {
				st.report(d.Pos(), UseAfter, fmt.Sprintf("this deferred call uses %s, which runs when the function returns, after it was %s%s",
					describe(*op), t.what, st.at(t.pos)))
				break
			}
		}
	}
}

// earliest returns the event a message names where several explain a finding: the first in source
// order (comparePos), and of two at one position the one whose description sorts first. An event
// with a position comes before one without.
func (st *state) earliest(es []event) event {
	sort.SliceStable(es, func(i, j int) bool {
		return cmp.Or(st.comparePos(es[i].pos, es[j].pos), strings.Compare(es[i].desc, es[j].desc)) < 0
	})
	return es[0]
}

// comparePos orders two positions by file name and then by place in the file, with a position that
// is not valid last. The values of token.Pos order files by when they were added to the file set,
// which follows the order the files were loaded in and differs from run to run, so "the first" of
// several positions in different files must not be chosen by them.
func (st *state) comparePos(a, b token.Pos) int {
	if a == b {
		return 0
	}
	if !a.IsValid() || !b.IsValid() {
		if a.IsValid() {
			return -1
		}
		return 1
	}
	name := func(p token.Pos) string {
		if f := st.pass.Fset.File(p); f != nil {
			return f.Name()
		}
		return ""
	}
	return cmp.Or(strings.Compare(name(a), name(b)), cmp.Compare(a, b))
}

func (st *state) at(pos token.Pos) string {
	if !pos.IsValid() {
		return ""
	}
	return fmt.Sprintf(" (line %d)", st.pass.Fset.Position(pos).Line)
}

// describe names an SSA value the way its source would.
func describe(v ssa.Value) string {
	switch v := v.(type) {
	case *ssa.Alloc:
		switch v.Comment {
		case "complit":
			return "composite literal"
		case "makeslice":
			return "slice made here"
		case "slicelit":
			return "slice literal"
		case "varargs":
			return "variadic argument slice"
		case "new":
			return "value from new"
		case "rangeindex", "":
			return "this value"
		}
		return "variable " + v.Comment
	case *ssa.Parameter:
		return "parameter " + v.Name()
	case *ssa.FreeVar:
		return "captured variable " + v.Name()
	case *ssa.MakeClosure:
		return "the closure"
	case *ssa.Global:
		return "package variable " + v.Name()
	case *ssa.UnOp:
		if v.Op == token.ARROW {
			return "the received value"
		}
		return describe(v.X)
	case *ssa.FieldAddr:
		return describe(v.X)
	case *ssa.IndexAddr:
		return describe(v.X)
	case *ssa.Field:
		return describe(v.X)
	case *ssa.Slice:
		return describe(v.X)
	case *ssa.ChangeType:
		return describe(v.X)
	case *ssa.MakeInterface:
		return describe(v.X)
	case *ssa.MakeSlice:
		return "slice made here"
	case *ssa.MakeMap:
		return "map made here"
	case *ssa.Call:
		if fn := v.Call.StaticCallee(); fn != nil {
			return "the result of " + fn.Name()
		}
		return "the call result"
	}
	return "this value"
}

func (st *state) checkBorrowEscape(g *graph, t transfer) bool {
	// 1. Channel sends (both normal and select)
	if t.what == "sent on a channel" || t.what == "sent on a channel in select" {
		if vr, name, kindStr := st.identifyBorrow(g, t); vr != nil {
			msg := fmt.Sprintf("stack-scoped borrow '%s' (%s) cannot cross goroutine boundary: cannot be sent on a channel", name, kindStr)
			st.report(t.pos, BorrowEscape, msg)
			return true
		}
	}

	// 2. Goroutine closure capture (go statement or spawner callback)
	if sp, ok := spawnOf(t.instr); ok {
		if t.what == "captured by a goroutine" || t.what == "started as a goroutine" || sp.via != "" {
			borrows := st.identifyCapturedBorrowsSpawn(g, sp)
			if len(borrows) > 0 {
				for _, b := range borrows {
					msg := fmt.Sprintf("stack-scoped borrow '%s' (%s) cannot cross goroutine boundary: cannot be captured by a goroutine", b.name, b.kindStr)
					st.report(t.pos, BorrowEscape, msg)
				}
				return true
			}
		}
	} else if callIns, ok := t.instr.(*ssa.Call); ok {
		if len(closureCandidates(t.v)) > 0 {
			borrows := st.identifyCapturedBorrowsSpawn(g, spawnSite{ins: callIns, fn: t.v, arg: t.index, via: t.what})
			if len(borrows) > 0 {
				for _, b := range borrows {
					msg := fmt.Sprintf("stack-scoped borrow '%s' (%s) cannot cross goroutine boundary: cannot be captured by a goroutine", b.name, b.kindStr)
					st.report(t.pos, BorrowEscape, msg)
				}
				return true
			}
		}
	}

	// 3. Goroutine argument or method receiver
	if t.what == "passed to a goroutine" || t.what == "receiver of a method started as a goroutine" || t.what == "started as a goroutine" {
		if vr, name, kindStr := st.identifyBorrow(g, t); vr != nil {
			msg := fmt.Sprintf("stack-scoped borrow '%s' (%s) cannot cross goroutine boundary: cannot be passed as argument to go statement", name, kindStr)
			st.report(t.pos, BorrowEscape, msg)
			return true
		}
	}

	// 4. Moving a local borrow via cap.Move, passing to an owned (iso) parameter, or publishing via a capability contract
	if _, isParam := t.v.(*ssa.Parameter); !isParam || isCapContractTransferWhat(t.what) {
		if t.what == "moved by cap.Move" || strings.HasPrefix(t.what, "passed to owned parameter ") || isCapContractTransferWhat(t.what) {
			if vr, name, kindStr := st.identifyBorrow(g, t); vr != nil {
				msg := fmt.Sprintf("stack-scoped borrow '%s' (%s) cannot cross goroutine boundary: cannot be %s", name, kindStr, t.what)
				st.report(t.pos, BorrowEscape, msg)
				return true
			}
		}
	}

	return false
}

// varAtPos returns the variable of set declared at pos, the one with the smallest name if several
// are, so a map walk cannot choose between them.
func varAtPos(set map[*types.Var]bool, pos token.Pos) *types.Var {
	var best *types.Var
	for v := range set {
		if v.Pos().IsValid() && v.Pos() == pos && (best == nil || v.Name() < best.Name()) {
			best = v
		}
	}
	return best
}

func (st *state) borrowKind(v *types.Var) (string, bool) {
	if v == nil {
		return "", false
	}
	if st.ix.IsMub(v) {
		return `\mub`, true
	}
	if st.ix.IsRob(v) {
		return `\rob`, true
	}
	return "", false
}

func (st *state) borrowVarFromIdent(id *ast.Ident) *types.Var {
	if id == nil {
		return nil
	}
	var vr *types.Var
	if v, ok := st.pass.TypesInfo.Uses[id].(*types.Var); ok {
		vr = v
	} else if v, ok := st.pass.TypesInfo.Defs[id].(*types.Var); ok {
		vr = v
	}
	if vr != nil {
		if _, isB := st.borrowKind(vr); isB {
			return vr
		}
	}
	return nil
}

func (st *state) borrowFromExpr(expr ast.Expr) (*types.Var, string, string) {
	return st.borrowFromExprInSyntax(expr, nil, nil)
}

// borrowFromExprInSyntax finds the borrow expr carries: a borrow variable at its root, one a
// variable at its root was assigned from (looked up in syntax), or one passed to a call that hands
// a parameter back. resolved holds the variables whose assignments have been looked at; nil starts
// a search. Each variable is looked at once, which is what ends the search; it used to end five
// steps from where it started, and a borrow further back than that was not found.
func (st *state) borrowFromExprInSyntax(expr ast.Expr, syntax ast.Node, resolved map[*types.Var]bool) (*types.Var, string, string) {
	if expr == nil {
		return nil, "", ""
	}
	if resolved == nil {
		resolved = map[*types.Var]bool{}
	}
	if id := rootIdent(expr); id != nil {
		if vr := st.borrowVarFromIdent(id); vr != nil {
			if kStr, isB := st.borrowKind(vr); isB {
				return vr, vr.Name(), kStr
			}
		}
		if syntax != nil {
			var vr *types.Var
			if v, ok := st.pass.TypesInfo.Uses[id].(*types.Var); ok {
				vr = v
			} else if v, ok := st.pass.TypesInfo.Defs[id].(*types.Var); ok {
				vr = v
			}
			if vr != nil {
				if bVr, bName, kStr := st.resolveBorrowVarInSyntax(vr, syntax, resolved); bVr != nil {
					return bVr, bName, kStr
				}
			}
		}
	}
	unwrapped := unparen(expr)
	if call, ok := unwrapped.(*ast.CallExpr); ok && len(call.Args) > 0 {
		if k := st.ix.BorrowAt(call.Pos()); k != annot.BorrowNone {
			kStr := `\rob`
			if k == annot.BorrowMut {
				kStr = `\mub`
			}
			arg := unparen(call.Args[0])
			if ue, ok := arg.(*ast.UnaryExpr); ok && ue.Op == token.AND {
				arg = unparen(ue.X)
			}
			if id := rootIdent(arg); id != nil {
				if v, ok := st.pass.TypesInfo.Uses[id].(*types.Var); ok && v != nil {
					return v, id.Name, kStr
				}
				if v, ok := st.pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
					return v, id.Name, kStr
				}
			}
		}
		if funId, ok := unparen(call.Fun).(*ast.Ident); ok {
			if fnObj, ok := st.pass.TypesInfo.Uses[funId].(*types.Func); ok && st.ssa != nil && st.ssa.Pkg != nil && st.ssa.Pkg.Prog != nil {
				if callee := st.ssa.Pkg.Prog.FuncValue(fnObj); callee != nil && len(callee.Blocks) > 0 {
					for _, b := range callee.Blocks {
						for _, ins := range b.Instrs {
							if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
								if pi := paramRootIndex(ret.Results[0], callee); pi >= 0 && pi < len(call.Args) {
									if bVr, bName, kStr := st.borrowFromExprInSyntax(call.Args[pi], syntax, resolved); bVr != nil {
										return bVr, bName, kStr
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return nil, "", ""
}

func (st *state) resolveBorrowVarInSyntax(vr *types.Var, syntax ast.Node, resolved map[*types.Var]bool) (*types.Var, string, string) {
	if vr == nil {
		return nil, "", ""
	}
	if kStr, isB := st.borrowKind(vr); isB {
		return vr, vr.Name(), kStr
	}
	if syntax == nil || resolved[vr] {
		return nil, "", ""
	}
	resolved[vr] = true
	var foundVr *types.Var
	var foundName, foundKind string
	ast.Inspect(syntax, func(n ast.Node) bool {
		if n == nil || foundVr != nil {
			return false
		}
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				if i >= len(stmt.Rhs) {
					break
				}
				if lid, ok := unparen(lhs).(*ast.Ident); ok {
					var lv *types.Var
					if v, ok := st.pass.TypesInfo.Defs[lid].(*types.Var); ok {
						lv = v
					} else if v, ok := st.pass.TypesInfo.Uses[lid].(*types.Var); ok {
						lv = v
					}
					if lv == vr {
						if bVr, bName, kStr := st.borrowFromExprInSyntax(stmt.Rhs[i], syntax, resolved); bVr != nil {
							foundVr, foundName, foundKind = bVr, bName, kStr
							return false
						}
					}
				}
			}
		case *ast.ValueSpec:
			for i, nameId := range stmt.Names {
				if i >= len(stmt.Values) {
					break
				}
				if lv, ok := st.pass.TypesInfo.Defs[nameId].(*types.Var); ok && lv == vr {
					if bVr, bName, kStr := st.borrowFromExprInSyntax(stmt.Values[i], syntax, resolved); bVr != nil {
						foundVr, foundName, foundKind = bVr, bName, kStr
						return false
					}
				}
			}
		}
		return true
	})
	return foundVr, foundName, foundKind
}

func (st *state) extractBorrowFromSend(snd *ast.SendStmt) (*types.Var, string, string) {
	return st.extractBorrowFromSendInSyntax(snd, nil)
}

func (st *state) extractBorrowFromSendInSyntax(snd *ast.SendStmt, syntax ast.Node) (*types.Var, string, string) {
	if snd == nil {
		return nil, "", ""
	}
	return st.borrowFromExprInSyntax(snd.Value, syntax, nil)
}

func (st *state) borrowFromVal(v ssa.Value) (*types.Var, string, string) {
	return st.borrowFromValSeen(v, map[ssa.Value]bool{})
}

func (st *state) borrowFromValSeen(v ssa.Value, seen map[ssa.Value]bool) (*types.Var, string, string) {
	// Each step goes to a value of this function not seen before (a call is answered from the
	// parameter its callee hands back, not walked into), so the walk ends without a budget.
	for v != nil && !seen[v] {
		seen[v] = true
		switch x := v.(type) {
		case *ssa.Parameter:
			if vr, ok := x.Object().(*types.Var); ok {
				if kStr, isB := st.borrowKind(vr); isB {
					return vr, vr.Name(), kStr
				}
			}
			return nil, "", ""
		case *ssa.Phi:
			for _, e := range x.Edges {
				if vr, name, kStr := st.borrowFromValSeen(e, seen); vr != nil {
					return vr, name, kStr
				}
			}
			return nil, "", ""
		case *ssa.Alloc:
			if mv := varAtPos(st.ix.MubVars, x.Pos()); mv != nil {
				return mv, mv.Name(), `\mub`
			}
			if rv := varAtPos(st.ix.RobVars, x.Pos()); rv != nil {
				return rv, rv.Name(), `\rob`
			}
			if refs := x.Referrers(); refs != nil {
				for _, ref := range *refs {
					if store, ok := ref.(*ssa.Store); ok && store.Addr == x {
						if vr, name, kStr := st.borrowFromValSeen(store.Val, seen); vr != nil {
							return vr, name, kStr
						}
					}
				}
			}
			return nil, "", ""
		case *ssa.Call:
			if callee := x.Call.StaticCallee(); callee != nil {
				if isCapBorrowHelper(callee) && len(x.Call.Args) > 0 {
					kStr := `\rob`
					if strings.Contains(callee.Name(), "Mut") || strings.Contains(callee.Name(), "Mub") {
						kStr = `\mub`
					}
					if alloc, ok := x.Call.Args[0].(*ssa.Alloc); ok {
						if mv := varAtPos(st.ix.MubVars, x.Pos()); mv != nil {
							return mv, mv.Name(), kStr
						}
						if rv := varAtPos(st.ix.RobVars, x.Pos()); rv != nil {
							return rv, rv.Name(), kStr
						}
						if alloc.Comment != "" {
							dummyVar := types.NewVar(x.Pos(), st.pass.Pkg, alloc.Comment, x.Type())
							return dummyVar, alloc.Comment, kStr
						}
					}
				} else if len(callee.Blocks) > 0 {
					for _, b := range callee.Blocks {
						for _, ins := range b.Instrs {
							if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
								if pi := paramRootIndex(ret.Results[0], callee); pi >= 0 && pi < len(x.Call.Args) {
									if vr, name, kStr := st.borrowFromValSeen(x.Call.Args[pi], seen); vr != nil {
										return vr, name, kStr
									}
								}
							}
						}
					}
				}
			}
			return nil, "", ""
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.UnOp:
			if x.Op == token.MUL {
				v = x.X
			} else {
				v = nil
			}
		default:
			v = nil
		}
	}
	return nil, "", ""
}

func (st *state) identifyBorrow(g *graph, t transfer) (*types.Var, string, string) {
	syntax := g.fn.Syntax()
	if syntax == nil {
		return st.borrowFromVal(t.v)
	}
	targetLine := st.pass.Fset.Position(t.pos).Line

	// 1. Select statement send: select { case ch <- x: ... }
	if _, ok := t.instr.(*ssa.Select); ok {
		var matchedSend *ast.SendStmt
		ast.Inspect(syntax, func(n ast.Node) bool {
			if n == nil || matchedSend != nil {
				return false
			}
			sel, ok := n.(*ast.SelectStmt)
			if !ok || (sel.Pos() != t.pos && st.pass.Fset.Position(sel.Pos()).Line != targetLine) {
				return true
			}
			if sel.Body != nil {
				stateIdx := 0
				for _, stmt := range sel.Body.List {
					cc, ok := stmt.(*ast.CommClause)
					if !ok || cc.Comm == nil {
						// default: clause
						continue
					}
					snd, ok := cc.Comm.(*ast.SendStmt)
					if !ok {
						// receive clause
						stateIdx++
						continue
					}
					matched := false
					if t.opPos.IsValid() && snd.Arrow == t.opPos {
						matched = true
					} else if t.index >= 0 && stateIdx == t.index {
						matched = true
					}
					if matched {
						matchedSend = snd
						return false
					}
					stateIdx++
				}
			}
			return false
		})

		if matchedSend != nil {
			if vr, name, kStr := st.extractBorrowFromSendInSyntax(matchedSend, syntax); vr != nil {
				return vr, name, kStr
			}
		}
		return st.borrowFromVal(t.v)
	}

	// 2. Direct channel send statement: ch <- x
	if _, ok := t.instr.(*ssa.Send); ok {
		var targetSend *ast.SendStmt
		ast.Inspect(syntax, func(n ast.Node) bool {
			if n == nil || targetSend != nil {
				return false
			}
			if snd, ok := n.(*ast.SendStmt); ok {
				if (t.opPos.IsValid() && (snd.Arrow == t.opPos || snd.Pos() == t.opPos)) ||
					st.pass.Fset.Position(snd.Pos()).Line == targetLine ||
					st.pass.Fset.Position(snd.Arrow).Line == targetLine {
					targetSend = snd
					return false
				}
			}
			return true
		})
		if targetSend != nil {
			if vr, name, kStr := st.extractBorrowFromSendInSyntax(targetSend, syntax); vr != nil {
				return vr, name, kStr
			}
		}
		return st.borrowFromVal(t.v)
	}

	// 3. Goroutine spawn: go fn(args...)
	if goIns, ok := t.instr.(*ssa.Go); ok {
		var targetGo *ast.GoStmt
		ast.Inspect(syntax, func(n ast.Node) bool {
			if n == nil || targetGo != nil {
				return false
			}
			if gs, ok := n.(*ast.GoStmt); ok && (gs.Pos() == t.pos || st.pass.Fset.Position(gs.Pos()).Line == targetLine) {
				targetGo = gs
				return false
			}
			return true
		})

		if targetGo != nil && targetGo.Call != nil {
			if t.what == "passed to a goroutine" {
				var targetArg ast.Expr
				cc := goIns.Common()
				if cc.IsInvoke() {
					if t.index >= 0 && t.index < len(targetGo.Call.Args) {
						targetArg = targetGo.Call.Args[t.index]
					}
				} else {
					if sel, ok := unparen(targetGo.Call.Fun).(*ast.SelectorExpr); ok && (cc.Method != nil || len(cc.Args) == len(targetGo.Call.Args)+1) {
						if t.index == 0 {
							targetArg = sel.X
						} else if t.index > 0 && t.index-1 < len(targetGo.Call.Args) {
							targetArg = targetGo.Call.Args[t.index-1]
						}
					} else {
						if t.index >= 0 && t.index < len(targetGo.Call.Args) {
							targetArg = targetGo.Call.Args[t.index]
						}
					}
				}
				if targetArg != nil {
					if vr, name, kStr := st.borrowFromExprInSyntax(targetArg, syntax, nil); vr != nil {
						return vr, name, kStr
					}
				}
				return st.borrowFromVal(t.v)
			} else if t.what == "receiver of a method started as a goroutine" {
				if sel, ok := unparen(targetGo.Call.Fun).(*ast.SelectorExpr); ok {
					if vr, name, kStr := st.borrowFromExprInSyntax(sel.X, syntax, nil); vr != nil {
						return vr, name, kStr
					}
				}
				return st.borrowFromVal(t.v)
			} else if t.what == "started as a goroutine" {
				if vr, name, kStr := st.borrowFromExprInSyntax(targetGo.Call.Fun, syntax, nil); vr != nil {
					return vr, name, kStr
				}
				return st.borrowFromVal(t.v)
			}
		}
	}

	// 4. Function/method call (cap.Move or owned //gosree:iso / cap.Iso parameter)
	if callIns, ok := t.instr.(*ssa.Call); ok {
		var targetCall *ast.CallExpr
		ast.Inspect(syntax, func(n ast.Node) bool {
			if n == nil || targetCall != nil {
				return false
			}
			if ce, ok := n.(*ast.CallExpr); ok && (ce.Pos() == t.pos || ce.Lparen == t.pos || st.pass.Fset.Position(ce.Pos()).Line == targetLine) {
				if st.ix.BorrowAt(ce.Pos()) == annot.BorrowNone {
					targetCall = ce
					return false
				}
			}
			return true
		})
		if targetCall != nil {
			var targetArg ast.Expr
			cc := callIns.Common()
			if !cc.IsInvoke() {
				if sel, ok := unparen(targetCall.Fun).(*ast.SelectorExpr); ok && (cc.Method != nil || len(cc.Args) == len(targetCall.Args)+1) {
					if t.index == 0 {
						targetArg = sel.X
					} else if t.index > 0 && t.index-1 < len(targetCall.Args) {
						targetArg = targetCall.Args[t.index-1]
					}
				} else if t.index >= 0 && t.index < len(targetCall.Args) {
					targetArg = targetCall.Args[t.index]
				}
			} else if t.index >= 0 && t.index < len(targetCall.Args) {
				targetArg = targetCall.Args[t.index]
			}
			if targetArg != nil {
				if vr, name, kStr := st.borrowFromExprInSyntax(targetArg, syntax, nil); vr != nil {
					return vr, name, kStr
				}
			}
		}
	}

	return st.borrowFromVal(t.v)
}

type capturedBorrow struct {
	vr      *types.Var
	name    string
	kindStr string
}

func (st *state) findBorrowsInLit(fl *ast.FuncLit) []capturedBorrow {
	return st.findBorrowsInLitInSyntax(fl, nil, nil)
}

// litKey identifies one findBorrowsInLitInSyntax search from the top.
type litKey struct {
	fl    *ast.FuncLit
	outer ast.Node
}

// closureBindings maps each variable of the function syntax to the function literals assigned to
// it (`f := func(){...}`, `f = func(){...}`, `var f = func(){...}`), in source order. Variables
// are identified by their *types.Var, so a shadowing variable of the same name and a struct field
// selector are different keys. The syntax is walked once and the result memoised.
func (st *state) closureBindings(syntax ast.Node) map[*types.Var][]*ast.FuncLit {
	if m, ok := st.litBindings[syntax]; ok {
		return m
	}
	m := map[*types.Var][]*ast.FuncLit{}
	bind := func(id *ast.Ident, rhs ast.Expr) {
		fl, ok := unparen(rhs).(*ast.FuncLit)
		if !ok {
			return
		}
		if v, ok := st.pass.TypesInfo.ObjectOf(id).(*types.Var); ok && v != nil && !v.IsField() {
			m[v] = append(m[v], fl)
		}
	}
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				if id, ok := unparen(lhs).(*ast.Ident); ok && i < len(stmt.Rhs) {
					bind(id, stmt.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range stmt.Names {
				if i < len(stmt.Values) {
					bind(name, stmt.Values[i])
				}
			}
		}
		return true
	})
	if st.litBindings == nil {
		st.litBindings = map[ast.Node]map[*types.Var][]*ast.FuncLit{}
	}
	st.litBindings[syntax] = m
	return m
}

// findBorrowsInLitInSyntax lists the borrows fl captures, and those captured by the function
// literals bound to the variables it uses (looked up in outerSyntax), to any depth. visited holds
// the literals already listed; nil starts a search. Each literal is listed once, which is what ends
// the search; it used to end five literals deep, and what a deeper one captures was left out.
// A search from the top is memoised. A call inside a search is not: what it lists depends on
// what the search has listed already.
func (st *state) findBorrowsInLitInSyntax(fl *ast.FuncLit, outerSyntax ast.Node, visited map[*ast.FuncLit]bool) []capturedBorrow {
	if fl == nil || fl.Body == nil || visited[fl] {
		return nil
	}
	top := visited == nil
	key := litKey{fl: fl, outer: outerSyntax}
	if top {
		if res, ok := st.litBorrows[key]; ok {
			return res
		}
		visited = map[*ast.FuncLit]bool{}
	}
	visited[fl] = true
	var res []capturedBorrow
	seen := map[*types.Var]bool{}
	ast.Inspect(fl.Body, func(inner ast.Node) bool {
		id, ok := inner.(*ast.Ident)
		if !ok {
			return true
		}
		vr, ok := st.pass.TypesInfo.Uses[id].(*types.Var)
		if !ok || vr == nil {
			return true
		}
		// Variable must be defined outside the closure
		if vr.Pos() < fl.Pos() || vr.Pos() > fl.End() {
			if kStr, isB := st.borrowKind(vr); isB {
				if !seen[vr] {
					seen[vr] = true
					res = append(res, capturedBorrow{vr: vr, name: vr.Name(), kindStr: kStr})
				}
			} else if outerSyntax != nil {
				// vr is bound to a FuncLit in outerSyntax (nested closure capture)
				for _, innerFl := range st.closureBindings(outerSyntax)[vr] {
					if innerFl == fl {
						continue
					}
					for _, b := range st.findBorrowsInLitInSyntax(innerFl, outerSyntax, visited) {
						if !seen[b.vr] {
							seen[b.vr] = true
							res = append(res, b)
						}
					}
				}
			}
		}
		return true
	})
	sort.Slice(res, func(i, j int) bool {
		return res[i].vr.Pos() < res[j].vr.Pos()
	})
	if top {
		if st.litBorrows == nil {
			st.litBorrows = map[litKey][]capturedBorrow{}
		}
		st.litBorrows[key] = res
	}
	return res
}

func (st *state) identifyCapturedBorrows(g *graph, goIns *ssa.Go) []capturedBorrow {
	sp, ok := spawnOf(goIns)
	if !ok {
		return nil
	}
	return st.identifyCapturedBorrowsSpawn(g, sp)
}

func (st *state) identifyCapturedBorrowsSpawn(g *graph, sp spawnSite) []capturedBorrow {
	syntax := g.fn.Syntax()
	if syntax == nil || sp.ins == nil {
		return nil
	}
	targetLine := st.pass.Fset.Position(sp.ins.Pos()).Line

	var foundBorrows []capturedBorrow
	seen := map[*types.Var]bool{}
	addBorrows := func(borrows []capturedBorrow) {
		for _, b := range borrows {
			if !seen[b.vr] {
				seen[b.vr] = true
				foundBorrows = append(foundBorrows, b)
			}
		}
	}

	// 1. Try SSA resolution: every closure sp.fn may be. The goroutine runs whichever it is, so what
	// any of them borrows crosses; taking the first one found left the others' borrows out.
	for _, mc := range closureCandidates(sp.fn) {
		if fn, ok := mc.Fn.(*ssa.Function); ok && fn != nil {
			if fl, ok := fn.Syntax().(*ast.FuncLit); ok {
				addBorrows(st.findBorrowsInLitInSyntax(fl, syntax, nil))
			}
		}
	}
	if len(foundBorrows) > 0 {
		return foundBorrows
	}

	// 2. Try AST walk at targetLine

	ast.Inspect(syntax, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			if st.pass.Fset.Position(node.Pos()).Line != targetLine {
				return true
			}
			// Case 2a: Direct FuncLit (go func() { ... }())
			if fl, ok := unparen(node.Call.Fun).(*ast.FuncLit); ok {
				if borrows := st.findBorrowsInLitInSyntax(fl, syntax, nil); len(borrows) > 0 {
					addBorrows(borrows)
					return false
				}
			}
			// Case 2b: Variable-bound closure (go fn())
			if id, ok := unparen(node.Call.Fun).(*ast.Ident); ok {
				if v, ok := st.pass.TypesInfo.ObjectOf(id).(*types.Var); ok {
					// Every closure the variable may hold: the goroutine runs whichever it holds then.
					for _, fl := range st.closureBindings(syntax)[v] {
						if borrows := st.findBorrowsInLitInSyntax(fl, syntax, nil); len(borrows) > 0 {
							addBorrows(borrows)
						}
					}
				}
			}
			return false
		case *ast.CallExpr:
			if st.pass.Fset.Position(node.Pos()).Line != targetLine {
				return true
			}
			for _, arg := range node.Args {
				if fl, ok := unparen(arg).(*ast.FuncLit); ok {
					if borrows := st.findBorrowsInLitInSyntax(fl, syntax, nil); len(borrows) > 0 {
						addBorrows(borrows)
					}
				}
			}
		}
		return true
	})

	if len(foundBorrows) > 0 {
		sort.Slice(foundBorrows, func(i, j int) bool {
			return foundBorrows[i].vr.Pos() < foundBorrows[j].vr.Pos()
		})
	}
	return foundBorrows
}

func (st *state) identifyCapturedBorrow(g *graph, goIns *ssa.Go) (*types.Var, string, string) {
	borrows := st.identifyCapturedBorrows(g, goIns)
	if len(borrows) > 0 {
		return borrows[0].vr, borrows[0].name, borrows[0].kindStr
	}
	return nil, "", ""
}

func rootIdent(e ast.Expr) *ast.Ident {
	for e != nil {
		switch x := unparen(e).(type) {
		case *ast.Ident:
			return x
		case *ast.StarExpr:
			e = x.X
		case *ast.UnaryExpr:
			e = x.X
		case *ast.SelectorExpr:
			return x.Sel
		default:
			return nil
		}
	}
	return nil
}
