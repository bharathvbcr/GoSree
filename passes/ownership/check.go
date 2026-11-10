package ownership

import (
	"fmt"
	"go/token"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// checkTransfers checks every place a value crosses to another goroutine (or to an owned parameter):
// the value must be owned (SAFE002), must not also escape elsewhere (SAFE004), and must not be used
// again by the sender (SAFE003).
func (st *state) checkTransfers(g *graph) {
	for _, t := range g.transfers {
		st.stats.Transfers++
		// A //safe:unsafe on the transfer covers everything the transfer causes, including uses
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
	if sp, ok := spawnOf(t.instr); ok && sp.repeated && t.v == sp.fn {
		st.checkHandlerWrites(sp, t.pos)
	}
	n := g.val(t.v)
	if n == nil {
		return
	}
	reg := g.region(n)
	after := reachableAfter(t.instr)

	var foreign, escape []event
	for c := range reg {
		for _, e := range c.events {
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
	if len(foreign) > 0 {
		e := earliest(foreign)
		why := "it may alias " + e.desc + st.at(e.pos)
		if e.desc == what {
			why = "the caller still holds it (mark the parameter //safe:iso to take ownership)"
		}
		st.report(t.pos, NotOwned, fmt.Sprintf("%s is %s but is not owned here: %s", what, t.what, why))
	}
	if len(escape) > 0 {
		e := earliest(escape)
		st.report(t.pos, Escapes, fmt.Sprintf("%s is %s but also escapes: %s%s",
			what, t.what, e.desc, st.at(e.pos)))
	}
	st.checkUseAfter(g, t, reg)
}

func (st *state) checkUseAfter(g *graph, t transfer, reg map[*node]bool) {
	var vals []ssa.Value
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
	sort.Slice(vals, func(i, j int) bool { return vals[i].Pos() < vals[j].Pos() })
	joins := st.joins(g, t)
	for _, w := range vals {
		u, ok := useAfter(t.instr, w, joins)
		if !ok {
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
	st.checkDeferredUse(g, t, reg)
}

// checkDeferredUse reports deferred calls registered before a move that touch the moved memory: they
// run when the function returns, which is after the move. (A defer registered after the move is an
// ordinary use and checkUseAfter reports it.)
func (st *state) checkDeferredUse(g *graph, t transfer, reg map[*node]bool) {
	after := reachableAfter(t.instr)
	forEachInstr(g.fn, func(ins ssa.Instruction) {
		d, ok := ins.(*ssa.Defer)
		if !ok || after(d) {
			return
		}
		for _, op := range d.Operands(nil) {
			if op == nil || *op == nil {
				continue
			}
			if n := g.nodes[*op]; n != nil && reg[n.find()] {
				st.report(d.Pos(), UseAfter, fmt.Sprintf("this deferred call uses %s, which runs when the function returns, after it was %s%s",
					describe(*op), t.what, st.at(t.pos)))
				return
			}
		}
	})
}

func earliest(es []event) event {
	sort.SliceStable(es, func(i, j int) bool { return es[i].pos < es[j].pos })
	for _, e := range es {
		if e.pos.IsValid() {
			return e
		}
	}
	return es[0]
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
