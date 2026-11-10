package ownership

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// checkTransfers checks every place a value crosses to another goroutine (or to an owned parameter):
// the value must be owned (SAFE002), must not also escape elsewhere (SAFE004), and must not be used
// again by the sender (SAFE003).
func (st *state) checkTransfers(g *graph) {
	for _, t := range g.transfers {
		st.stats.Transfers++
		borrowEscaped := st.checkBorrowEscape(g, t)
		if borrowEscaped && t.what != "captured by a goroutine" {
			continue
		}
		n := g.val(t.v)
		if n == nil {
			continue
		}
		reg := g.region(n)
		after := reachableAfterTransfer(t)

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
		if !borrowEscaped && len(foreign) > 0 {
			e := earliest(foreign)
			why := "it may alias " + e.desc + st.at(e.pos)
			if e.desc == what {
				why = "the caller still holds it (mark the parameter //safe:iso to take ownership)"
			}
			st.report(t.pos, NotOwned, fmt.Sprintf("%s is %s but is not owned here: %s", what, t.what, why))
		}
		if !borrowEscaped && len(escape) > 0 {
			e := earliest(escape)
			st.report(t.pos, Escapes, fmt.Sprintf("%s is %s but also escapes: %s%s",
				what, t.what, e.desc, st.at(e.pos)))
		}
		st.checkUseAfter(g, t, reg)
	}
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
		u, ok := useAfter(t.instr, t.caseBlock, w, joins)
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
	after := reachableAfterTransfer(t)
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

func (st *state) checkBorrowEscape(g *graph, t transfer) bool {
	// 1. Channel sends (both normal and select)
	if t.what == "sent on a channel" || t.what == "sent on a channel in select" {
		if vr, name, kindStr := st.identifyBorrow(g, t); vr != nil {
			msg := fmt.Sprintf("stack-scoped borrow '%s' (%s) cannot cross goroutine boundary: cannot be sent on a channel", name, kindStr)
			st.report(t.pos, BorrowEscape, msg)
			return true
		}
	}

	// 2. Goroutine closure capture
	if t.what == "captured by a goroutine" || t.what == "started as a goroutine" {
		if goIns, ok := t.instr.(*ssa.Go); ok {
			borrows := st.identifyCapturedBorrows(g, goIns)
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

	return false
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

func (st *state) extractBorrowFromSend(snd *ast.SendStmt) *types.Var {
	if snd == nil {
		return nil
	}
	id := rootIdent(snd.Value)
	return st.borrowVarFromIdent(id)
}

func (st *state) borrowFromVal(v ssa.Value) (*types.Var, string, string) {
	for v != nil {
		switch x := v.(type) {
		case *ssa.Parameter:
			if vr, ok := x.Object().(*types.Var); ok {
				if kStr, isB := st.borrowKind(vr); isB {
					return vr, vr.Name(), kStr
				}
			}
			return nil, "", ""
		case *ssa.ChangeType:
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
			if vr := st.extractBorrowFromSend(matchedSend); vr != nil {
				if kStr, isB := st.borrowKind(vr); isB {
					return vr, vr.Name(), kStr
				}
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
			if vr := st.extractBorrowFromSend(targetSend); vr != nil {
				if kStr, isB := st.borrowKind(vr); isB {
					return vr, vr.Name(), kStr
				}
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
					if id := rootIdent(targetArg); id != nil {
						if vr := st.borrowVarFromIdent(id); vr != nil {
							if kStr, isB := st.borrowKind(vr); isB {
								return vr, vr.Name(), kStr
							}
						}
					}
				}
				return st.borrowFromVal(t.v)
			} else if t.what == "receiver of a method started as a goroutine" {
				if sel, ok := unparen(targetGo.Call.Fun).(*ast.SelectorExpr); ok {
					if id := rootIdent(sel.X); id != nil {
						if vr := st.borrowVarFromIdent(id); vr != nil {
							if kStr, isB := st.borrowKind(vr); isB {
								return vr, vr.Name(), kStr
							}
						}
					}
				}
				return st.borrowFromVal(t.v)
			} else if t.what == "started as a goroutine" {
				if id, ok := unparen(targetGo.Call.Fun).(*ast.Ident); ok {
					if vr := st.borrowVarFromIdent(id); vr != nil {
						if kStr, isB := st.borrowKind(vr); isB {
							return vr, vr.Name(), kStr
						}
					}
				}
				return st.borrowFromVal(t.v)
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
	if fl == nil || fl.Body == nil {
		return nil
	}
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
			}
		}
		return true
	})
	sort.Slice(res, func(i, j int) bool {
		return res[i].vr.Pos() < res[j].vr.Pos()
	})
	return res
}

func (st *state) identifyCapturedBorrows(g *graph, goIns *ssa.Go) []capturedBorrow {
	syntax := g.fn.Syntax()
	if syntax == nil {
		return nil
	}
	targetLine := st.pass.Fset.Position(goIns.Pos()).Line

	// 1. Try SSA resolution: if goIns.Common().Value resolves to MakeClosure
	if mc := resolveClosureVal(goIns.Common().Value, 0); mc != nil {
		if fn, ok := mc.Fn.(*ssa.Function); ok && fn != nil {
			if fl, ok := fn.Syntax().(*ast.FuncLit); ok {
				if borrows := st.findBorrowsInLit(fl); len(borrows) > 0 {
					return borrows
				}
			}
		}
	}

	// 2. Try AST walk at targetLine
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

	ast.Inspect(syntax, func(n ast.Node) bool {
		gs, ok := n.(*ast.GoStmt)
		if !ok || st.pass.Fset.Position(gs.Pos()).Line != targetLine {
			return true
		}

		// Case 2a: Direct FuncLit (go func() { ... }())
		if fl, ok := unparen(gs.Call.Fun).(*ast.FuncLit); ok {
			if borrows := st.findBorrowsInLit(fl); len(borrows) > 0 {
				addBorrows(borrows)
				return false
			}
		}

		// Case 2b: Variable-bound closure (go fn())
		if id, ok := unparen(gs.Call.Fun).(*ast.Ident); ok {
			ast.Inspect(syntax, func(node ast.Node) bool {
				switch stmt := node.(type) {
				case *ast.AssignStmt:
					for i, lhs := range stmt.Lhs {
						if lid := rootIdent(lhs); lid != nil && lid.Name == id.Name {
							if i < len(stmt.Rhs) {
								if fl, ok := unparen(stmt.Rhs[i]).(*ast.FuncLit); ok {
									if borrows := st.findBorrowsInLit(fl); len(borrows) > 0 {
										addBorrows(borrows)
										return false
									}
								}
							}
						}
					}
				case *ast.ValueSpec:
					for i, name := range stmt.Names {
						if name.Name == id.Name && i < len(stmt.Values) {
							if fl, ok := unparen(stmt.Values[i]).(*ast.FuncLit); ok {
								if borrows := st.findBorrowsInLit(fl); len(borrows) > 0 {
									addBorrows(borrows)
									return false
								}
							}
						}
					}
				}
				return true
			})
			if len(foundBorrows) > 0 {
				return false
			}
		}
		return false
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

