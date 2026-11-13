package errcheck

import (
	"cmp"
	"go/ast"
	"go/token"
	"slices"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/gusseterr"
)

// A closure and the function that creates it are lowered separately, so a write in one and the
// Close in the other never meet on one written flag. For a closure the enclosing function runs at
// exactly one call that runs on every path to a return, and whose value goes nowhere else (the
// conditions of closureCallFor, shared with the closure pair plan), running the closure there is
// the same as running its body in place. The enclosing function then replays, at that call, what
// the body does to the variables it captures:
//
//   - a write through a captured variable marks the variable written;
//   - a discarded Close of a captured variable, deferred or not, closes it. A Close the body defers
//     runs when the body returns, which from the creator is the call, not the creator's exit.
//
// A closure that is not tracked is replayed in no way, so it behaves exactly as without this file:
// replay adds a report only on a shape it tracks and never removes one. Two kinds of captured
// variable are left alone for the same reason. One the closure stores to or leaks (reassigned,
// address taken or stored) may hold another resource after the body than before it. One the closure
// itself writes keeps its Close in the closure's own flow, which already judges it with the write
// in view; replaying the Close too would report that site twice (once as a Close, once as a
// deferred Close) and would answer for a Close that may come before the write.

// closureFx is what one tracked closure does to the variables it captures, by free-variable index.
type closureFx struct {
	writes   []int     // variables the body (or a tracked closure inside it) writes through
	closes   []fxClose // discarded Closes of variables the body does not write
	unstable []int     // variables the body stores to or leaks; never replayed, but passed outward
}

type fxClose struct {
	idx  int
	site closeCallAST
}

// closureEffectsAt returns the effects to replay at call, when call is the one dominating call of a
// tracked closure, and nil otherwise.
func (b *packageBuilder) closureEffectsAt(call *ssa.Call) *closureFx {
	mc, ok := call.Call.Value.(*ssa.MakeClosure)
	if !ok || call.Call.IsInvoke() {
		return nil
	}
	fn, ok := mc.Fn.(*ssa.Function)
	if !ok {
		return nil
	}
	if cc := b.closureCallFor(fn); cc == nil || !cc.domLowered || cc.call != call {
		return nil
	}
	if closureDepth(fn) > maxReplayDepth {
		return nil
	}
	return b.closureFxFor(fn)
}

// maxReplayDepth is the deepest closure nesting the replay follows: a closure written directly in
// a declared function is at depth 1, one in that closure at depth 2. A closure deeper than this is
// not replayed, so it behaves as before ft-fe6e and its creator never sees its effects.
//
// The bound exists because the cost is not linear in the depth. A closure at depth i names every
// captured variable the closures inside it use, so with one variable per layer the layers carry
// d, d-1, ... 1 names, and a summary lists the variables written below it: the work is the size of
// those lists, d(d+1)/2 entries for d layers (1,617 plan-work units at depth 25 and 65,492 at depth
// 200 when every variable is closed, against 494 and 3,994 without the replay). Under the cap that
// stays quadratic in the depth, which is at most maxReplayDepth layers each naming at most the
// variables of the function, so the work is bounded by the cap times the captured-variable lists
// and is linear in the size of the input. 16 is three times the deepest nesting of function
// literals in this repository, its fixtures and the Go standard library (5, measured with a parser
// walk over all three), and a power of two.
const maxReplayDepth = 16

// closureDepth is the number of functions enclosing fn, counted up to maxReplayDepth+1: it stops
// there, since only whether fn is past the cap is asked.
func closureDepth(fn *ssa.Function) int {
	n := 0
	for p := fn.Parent(); p != nil && n <= maxReplayDepth; p = p.Parent() {
		n++
	}
	return n
}

// closureFxFor summarises closure fn once. Each closure is scanned once and a closure nested in it
// contributes its own memoized summary, so the work is the size of the bodies plus the captured
// variables they name, not the nesting depth times either.
func (b *packageBuilder) closureFxFor(fn *ssa.Function) *closureFx {
	if fx, ok := b.closureFxs[fn]; ok {
		return fx
	}
	fx := b.buildClosureFx(fn)
	b.closureFxs[fn] = fx
	return fx
}

// closedNameSet is the names of the variables the package calls Close on. A captured variable that
// no code closes by name cannot take part in a report, so the summary skips it (a superset: two
// variables sharing a name only cost work).
func (b *packageBuilder) closedNameSet() map[string]bool {
	if b.closedNames != nil {
		return b.closedNames
	}
	b.closedNames = make(map[string]bool)
	for _, f := range b.pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Close" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				b.closedNames[id.Name] = true
			}
			return true
		})
	}
	return b.closedNames
}

// astInfoOf returns the AST facts of fn, scanning it once even when a creator asks before fn is
// lowered; lowering takes the entry out so the facts of a lowered function are not kept.
func (b *packageBuilder) astInfoOf(fn *ssa.Function) *funcASTInfo {
	if ai, ok := b.astInfos[fn]; ok {
		return ai
	}
	ai := scanFuncAST(b.pass, fn.Syntax())
	b.astInfos[fn] = ai
	return ai
}

func (b *packageBuilder) takeASTInfo(fn *ssa.Function) *funcASTInfo {
	if ai, ok := b.astInfos[fn]; ok {
		delete(b.astInfos, fn)
		return ai
	}
	return scanFuncAST(b.pass, fn.Syntax())
}

// loadedFreeVar returns the captured variable v is a load of, as resourceSlot sees a receiver.
func loadedFreeVar(v ssa.Value) *ssa.FreeVar {
	if load, ok := v.(*ssa.UnOp); ok && load.Op == token.MUL {
		if fv, ok := load.X.(*ssa.FreeVar); ok {
			return fv
		}
	}
	return nil
}

func (b *packageBuilder) buildClosureFx(fn *ssa.Function) *closureFx {
	if len(b.closedNameSet()) == 0 {
		return nil
	}
	// rel holds the captured variables some code closes by name. unstable holds those among them the
	// closure stores to or leaks.
	rel := make(map[*ssa.FreeVar]int)
	for i, fv := range fn.FreeVars {
		b.planWork++
		if b.closedNames[fv.Name()] {
			rel[fv] = i
		}
	}
	if len(rel) == 0 {
		return nil
	}
	unstable := make(map[int]bool)
	for fv, i := range rel {
		for _, ref := range liveReferrers(fv) {
			b.planWork++
			switch r := ref.(type) {
			case *ssa.UnOp:
				if r.Op != token.MUL {
					unstable[i] = true
				}
			case *ssa.MakeClosure:
			case *ssa.Store:
				if r.Addr == ssa.Value(fv) || r.Val == ssa.Value(fv) {
					unstable[i] = true
				}
			default:
				unstable[i] = true
			}
		}
	}

	info := b.astInfoOf(fn)
	var deferRuns []bool
	writes := make(map[int]bool)
	type closeKey struct {
		idx int
		pos token.Pos
	}
	var closes []fxClose
	seenClose := make(map[closeKey]bool)
	addClose := func(idx int, site closeCallAST) {
		if k := (closeKey{idx, site.pos}); !seenClose[k] {
			seenClose[k] = true
			closes = append(closes, fxClose{idx: idx, site: site})
		}
	}
	for bi, blk := range fn.Blocks {
		if blk == fn.Recover {
			continue
		}
		for _, ins := range blk.Instrs {
			b.planWork++
			switch x := ins.(type) {
			case *ssa.Call:
				cc := &x.Call
				if fv := loadedFreeVar(methodReceiverVal(cc)); fv != nil {
					if i, ok := rel[fv]; ok {
						if isWriteMutatingMethod(cc) {
							writes[i] = true
						} else if cl, ok := info.closes[cc.Pos()]; ok && !cl.isDefer && isCloseMethodCall(cc) {
							addClose(i, cl)
						}
					}
				}
				if inner := b.closureEffectsAt(x); inner != nil {
					mc := x.Call.Value.(*ssa.MakeClosure)
					lift := func(k int) (int, bool) {
						b.planWork++
						fv, ok := mc.Bindings[k].(*ssa.FreeVar)
						if !ok {
							return 0, false
						}
						i, ok := rel[fv]
						return i, ok
					}
					for _, k := range inner.unstable {
						if i, ok := lift(k); ok {
							unstable[i] = true
						}
					}
					for _, k := range inner.writes {
						if i, ok := lift(k); ok {
							writes[i] = true
						}
					}
					for _, c := range inner.closes {
						if i, ok := lift(c.idx); ok {
							addClose(i, c.site)
						}
					}
				}
			case *ssa.Defer:
				cc := &x.Call
				if fv := loadedFreeVar(methodReceiverVal(cc)); fv != nil {
					if i, ok := rel[fv]; ok && isCloseMethodCall(cc) {
						if cl, ok := info.closes[cc.Pos()]; ok && cl.isDefer {
							if deferRuns == nil {
								deferRuns = blocksWhereDefersRun(fn)
							}
							if deferRuns[bi] {
								addClose(i, cl)
							}
						}
					}
				}
			}
		}
	}
	fx := &closureFx{}
	for i := range writes {
		if !unstable[i] {
			fx.writes = append(fx.writes, i)
		}
	}
	for i := range unstable {
		fx.unstable = append(fx.unstable, i)
	}
	slices.Sort(fx.writes)
	slices.Sort(fx.unstable)
	for _, c := range closes {
		if !unstable[c.idx] && !writes[c.idx] {
			fx.closes = append(fx.closes, c)
		}
	}
	slices.SortFunc(fx.closes, func(a, c fxClose) int {
		if r := cmp.Compare(a.site.pos, c.site.pos); r != 0 {
			return r
		}
		return cmp.Compare(a.idx, c.idx)
	})
	if len(fx.writes) == 0 && len(fx.closes) == 0 && len(fx.unstable) == 0 {
		return nil
	}
	return fx
}

// replayClosureEffects emits, right after the call that runs a tracked closure, the closure's
// effects on the creator's own variables: its writes mark them written and its discarded Closes
// close them, each at the Close's site in the closure.
func (fl *funcLowerer) replayClosureEffects(call *ssa.Call, ops *[]gusseterr.OpIR) {
	fx := fl.pkg.closureEffectsAt(call)
	if fx == nil {
		return
	}
	mc := call.Call.Value.(*ssa.MakeClosure)
	for _, i := range fx.writes {
		if slot, ok := fl.capturedSlot(mc.Bindings[i]); ok {
			*ops = append(*ops, gusseterr.OpIR{Kind: gusseterr.OpMarkWritable, SiteID: 0, SlotA: slot})
		}
	}
	for _, c := range fx.closes {
		if slot, ok := fl.capturedSlot(mc.Bindings[c.idx]); ok {
			sid := fl.pkg.addSite(c.site.pos, c.site.recvDesc, "")
			*ops = append(*ops, gusseterr.OpIR{Kind: gusseterr.OpUnhandledClose, SiteID: sid, SlotA: slot, IsDefer: false})
		}
	}
}

// capturedSlot is the slot a load of the variable bound to a closure's free variable reads in fl:
// the variable's cell, or the creator's own captured variable.
func (fl *funcLowerer) capturedSlot(binding ssa.Value) (uint32, bool) {
	switch v := binding.(type) {
	case *ssa.Alloc:
		if fl.localCells[v] {
			return fl.slot(v), true
		}
	case *ssa.FreeVar:
		return fl.slot(v), true
	}
	return 0, false
}
