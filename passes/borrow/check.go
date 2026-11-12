package borrow

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// Diagnostic Categories for Borrow Checking
const (
	ConflictingBorrow = "BORROW002"
	OwnerInvalidation = "BORROW003"
	DanglingReturn    = "BORROW004"
)

// node represents an equivalence class in Steensgaard alias analysis.
type node struct {
	parent  *node
	pointee *node
}

func (n *node) find() *node {
	for n.parent != nil {
		if n.parent.parent != nil {
			n.parent = n.parent.parent
		}
		n = n.parent
	}
	return n
}

type graph struct {
	fn    *ssa.Function
	nodes map[ssa.Value]*node
}

func newGraph(fn *ssa.Function) *graph {
	g := &graph{fn: fn, nodes: map[ssa.Value]*node{}}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			g.instr(ins)
		}
	}
	return g
}

func (g *graph) val(v ssa.Value) *node {
	if v == nil {
		return nil
	}
	if n, ok := g.nodes[v]; ok {
		if n == nil {
			return nil
		}
		return n.find()
	}
	n := &node{}
	g.nodes[v] = n
	return n
}

func (g *graph) pointee(n *node) *node {
	n = n.find()
	if n.pointee == nil {
		n.pointee = &node{}
	}
	return n.pointee.find()
}

func (g *graph) union(a, b *node) {
	if a == nil || b == nil {
		return
	}
	a, b = a.find(), b.find()
	if a == b {
		return
	}
	b.parent = a
	pa, pb := a.pointee, b.pointee
	b.pointee = nil
	switch {
	case pa == nil:
		a.pointee = pb
	case pb != nil:
		g.union(pa, pb)
		a.pointee = pa.find()
	}
}

func (g *graph) join(v, x ssa.Value) {
	vn, xn := g.val(v), g.val(x)
	if vn != nil && xn != nil {
		g.union(vn, xn)
	}
}

func (g *graph) instr(ins ssa.Instruction) {
	switch ins := ins.(type) {
	case *ssa.Alloc:
		g.val(ins)
	case *ssa.Phi:
		for _, e := range ins.Edges {
			g.join(ins, e)
		}
	case *ssa.FieldAddr:
		g.join(ins, ins.X)
	case *ssa.IndexAddr:
		g.join(ins, ins.X)
	case *ssa.Slice:
		g.join(ins, ins.X)
	case *ssa.ChangeType:
		g.join(ins, ins.X)
	case *ssa.Convert:
		g.join(ins, ins.X)
	case *ssa.UnOp:
		if ins.Op == token.MUL {
			if xn := g.val(ins.X); xn != nil {
				g.union(g.val(ins), g.pointee(xn))
			}
		}
	case *ssa.Store:
		vn := g.val(ins.Val)
		an := g.val(ins.Addr)
		if an != nil && vn != nil {
			g.union(g.pointee(an), vn)
		}
	}
}

// Borrow represents an active or tracked second-class borrow.
type Borrow struct {
	ID        int
	Kind      annot.BorrowKind
	OwnerNode *node
	OwnerName string
	OwnerVar  *types.Var
	OwnerVal  ssa.Value
	Pos       token.Pos
	Line      int
	Site      ssa.Instruction
	Var       *types.Var
	Val       ssa.Value
	Uses      map[ssa.Instruction]bool
}

type borrowChecker struct {
	pass                  *analysis.Pass
	ix                    *annot.Index
	ssa                   *buildssa.SSA
	fn                    *ssa.Function
	graph                 *graph
	borrows               []*Borrow
	reports               map[reportKey]bool
	deferred              map[*types.Var]bool
	registeredBorrowExprs map[*ast.UnaryExpr]bool
}

// CheckBorrows performs local SSA borrow checking and NLL conflict analysis for all functions in the package.
func CheckBorrows(pass *analysis.Pass, ix *annot.Index, ssaProg *buildssa.SSA) {
	for _, fn := range allPackageFuncs(ssaProg) {
		if len(fn.Blocks) == 0 {
			continue
		}
		bc := &borrowChecker{
			pass:                  pass,
			ix:                    ix,
			ssa:                   ssaProg,
			fn:                    fn,
			reports:               make(map[reportKey]bool),
			deferred:              make(map[*types.Var]bool),
			registeredBorrowExprs: make(map[*ast.UnaryExpr]bool),
		}
		bc.run()
	}
}

func (bc *borrowChecker) fallbackPos() token.Pos {
	if bc.fn != nil && bc.fn.Pos().IsValid() {
		return bc.fn.Pos()
	}
	if bc.pass != nil {
		for _, f := range bc.pass.Files {
			if f.Pos().IsValid() {
				return f.Pos()
			}
			if f.Package.IsValid() {
				return f.Package
			}
		}
	}
	return token.NoPos
}

func (bc *borrowChecker) report(pos token.Pos, cat, msg string) {
	if !pos.IsValid() {
		pos = bc.fallbackPos()
	}
	if !pos.IsValid() || (bc.ix != nil && bc.ix.Mute(pos)) {
		return
	}
	k := reportKey{pos, cat}
	if bc.reports[k] {
		return
	}
	bc.reports[k] = true
	bc.pass.Report(analysis.Diagnostic{
		Pos:      pos,
		Category: cat,
		Message:  cat + ": " + msg,
	})
}

func (bc *borrowChecker) run() {
	bc.graph = newGraph(bc.fn)
	bc.findBorrows()
	bc.checkConflictingBorrows()
	bc.checkOwnerMutation()
	bc.checkDanglingReturn()
	bc.checkLoopEscapes()
}

func (bc *borrowChecker) findBorrows() {
	// 1. Parameter Borrows
	for _, p := range bc.fn.Params {
		if vr, ok := p.Object().(*types.Var); ok {
			var kind annot.BorrowKind
			if bc.ix.IsMub(vr) {
				kind = annot.BorrowMut
			} else if bc.ix.IsRob(vr) {
				kind = annot.BorrowRob
			}
			if kind != annot.BorrowNone {
				var site ssa.Instruction
				if len(bc.fn.Blocks) > 0 && len(bc.fn.Blocks[0].Instrs) > 0 {
					site = bc.fn.Blocks[0].Instrs[0]
				}
				b := &Borrow{
					ID:        len(bc.borrows) + 1,
					Kind:      kind,
					OwnerNode: bc.graph.val(p),
					OwnerName: vr.Name(),
					OwnerVar:  vr,
					OwnerVal:  p,
					Pos:       p.Pos(),
					Line:      bc.pass.Fset.Position(p.Pos()).Line,
					Site:      site,
					Var:       vr,
					Val:       p,
					Uses:      make(map[ssa.Instruction]bool),
				}
				bc.collectUses(b)
				bc.borrows = append(bc.borrows, b)
			}
		}
	}

	// 2. AST Local Assignments & Directives
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return
	}

	// Track deferred variables
	ast.Inspect(syntax, func(n ast.Node) bool {
		if def, ok := n.(*ast.DeferStmt); ok {
			ast.Inspect(def.Call, func(sub ast.Node) bool {
				if id, ok := sub.(*ast.Ident); ok {
					if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
						bc.deferred[v] = true
					}
				}
				return true
			})
		}
		return true
	})

	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				lhsIdent, ok := unparen(lhs).(*ast.Ident)
				if !ok {
					continue
				}
				var rhs ast.Expr
				if i < len(stmt.Rhs) {
					rhs = stmt.Rhs[i]
				}
				bc.registerBorrowCandidate(stmt.Pos(), lhsIdent, rhs)
			}
		case *ast.DeclStmt:
			if gd, ok := stmt.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for i, name := range vs.Names {
							var rhs ast.Expr
							if i < len(vs.Values) {
								rhs = vs.Values[i]
							}
							bc.registerBorrowCandidate(stmt.Pos(), name, rhs)
						}
					}
				}
			}
		case *ast.CallExpr:
			bc.registerInlineCallBorrows(stmt)
		}
		return true
	})
}

func (bc *borrowChecker) registerInlineCallBorrows(call *ast.CallExpr) {
	if call == nil {
		return
	}
	// If this CallExpr itself is cap.Borrow*(&x), skip here: we register it when visiting its parent CallExpr's Args.
	if bc.ix.BorrowAt(call.Pos()) != annot.BorrowNone {
		return
	}
	var sig *types.Signature
	if funType := bc.pass.TypesInfo.TypeOf(call.Fun); funType != nil {
		sig, _ = funType.Underlying().(*types.Signature)
	}
	site := bc.findInstructionAtOrAfter(call.Pos())
	seenBorrowVarsInCall := map[*types.Var]int{}
	for idx, arg := range call.Args {
		unwrapped := unparen(arg)
		var kind annot.BorrowKind
		if k := bc.ix.BorrowAt(arg.Pos()); k != annot.BorrowNone {
			kind = k
		} else if k := bc.ix.BorrowAt(unwrapped.Pos()); k != annot.BorrowNone {
			kind = k
		} else if ue, ok := unwrapped.(*ast.UnaryExpr); ok && ue.Op == token.AND {
			if k := bc.ix.BorrowAt(ue.OpPos); k != annot.BorrowNone {
				kind = k
			} else if sig != nil && sig.Params() != nil && idx < sig.Params().Len() {
				pVar := sig.Params().At(idx)
				if bc.ix.IsMub(pVar) {
					kind = annot.BorrowMut
				} else if bc.ix.IsRob(pVar) {
					kind = annot.BorrowRob
				}
			}
		} else if id, ok := unwrapped.(*ast.Ident); ok && sig != nil && sig.Params() != nil && idx < sig.Params().Len() {
			pVar := sig.Params().At(idx)
			isMubParam := bc.ix.IsMub(pVar)
			isRobParam := bc.ix.IsRob(pVar)
			if isMubParam || isRobParam {
				var idVar *types.Var
				if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
					idVar = v
				} else if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
					idVar = v
				}
				if idVar != nil {
					seenBorrowVarsInCall[idVar]++
					isExistingBorrowVar := false
					var existingKind annot.BorrowKind
					for _, prev := range bc.borrows {
						if prev.Var == idVar {
							isExistingBorrowVar = true
							existingKind = prev.Kind
							break
						}
					}
					if isMubParam {
						if !isExistingBorrowVar || existingKind == annot.BorrowRob || seenBorrowVarsInCall[idVar] > 1 {
							kind = annot.BorrowMut
						}
					} else if isRobParam {
						if !isExistingBorrowVar || (existingKind == annot.BorrowMut && seenBorrowVarsInCall[idVar] > 1) {
							kind = annot.BorrowRob
						}
					}
				}
			}
		}
		if kind == annot.BorrowNone {
			continue
		}
		ownerName, ownerVar, ownerVal := bc.resolveOwner(unwrapped)
		var ownerNode *node
		if ownerVal != nil {
			ownerNode = bc.graph.val(ownerVal)
		}
		pos := arg.Pos()
		line := bc.pass.Fset.Position(pos).Line
		b := &Borrow{
			ID:        len(bc.borrows) + 1,
			Kind:      kind,
			OwnerNode: ownerNode,
			OwnerName: ownerName,
			OwnerVar:  ownerVar,
			OwnerVal:  ownerVal,
			Pos:       pos,
			Line:      line,
			Site:      site,
			Uses:      map[ssa.Instruction]bool{},
		}
		if site != nil {
			b.Uses[site] = true
		}
		bc.markRegisteredBorrowExpr(arg)
		bc.borrows = append(bc.borrows, b)
	}
}

func (bc *borrowChecker) markRegisteredBorrowExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(n ast.Node) bool {
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		if ue, ok := n.(*ast.UnaryExpr); ok && ue.Op == token.AND {
			bc.registeredBorrowExprs[ue] = true
		}
		return true
	})
}

func (bc *borrowChecker) registerBorrowCandidate(stmtPos token.Pos, lhsIdent *ast.Ident, rhs ast.Expr) {
	if lhsIdent == nil || rhs == nil {
		return
	}
	v, _ := bc.pass.TypesInfo.Defs[lhsIdent].(*types.Var)
	if v == nil {
		v, _ = bc.pass.TypesInfo.Uses[lhsIdent].(*types.Var)
	}
	if v == nil {
		return
	}

	// Determine borrow kind strictly from annot.Index
	var kind annot.BorrowKind
	if bc.ix.IsMub(v) {
		kind = annot.BorrowMut
	} else if bc.ix.IsRob(v) {
		kind = annot.BorrowRob
	} else if k := bc.ix.BorrowAt(stmtPos); k != annot.BorrowNone {
		kind = k
	} else if k := bc.ix.BorrowAt(lhsIdent.Pos()); k != annot.BorrowNone {
		kind = k
	} else if rhs != nil {
		if k := bc.ix.BorrowAt(rhs.Pos()); k != annot.BorrowNone {
			kind = k
		} else {
			unwrapped := unparen(rhs)
			if ue, ok := unwrapped.(*ast.UnaryExpr); ok && bc.ix.BorrowAt(ue.OpPos) != annot.BorrowNone {
				kind = bc.ix.BorrowAt(ue.OpPos)
			}
			if call, ok := unwrapped.(*ast.CallExpr); ok && bc.ix.BorrowAt(call.Pos()) != annot.BorrowNone {
				kind = bc.ix.BorrowAt(call.Pos())
			}
		}
	}

	if kind == annot.BorrowNone {
		return
	}

	// Determine owner
	ownerName := "x"
	var ownerVar *types.Var
	var ownerVal ssa.Value
	if rhs != nil {
		ownerName, ownerVar, ownerVal = bc.resolveOwner(rhs)
	}

	var ownerNode *node
	if ownerVal != nil {
		ownerNode = bc.graph.val(ownerVal)
	}
	if ownerNode == nil {
		for _, l := range bc.fn.Locals {
			if l.Comment == ownerName {
				ownerVal = l
				ownerNode = bc.graph.val(l)
				break
			}
		}
	}

	pos := lhsIdent.Pos()
	line := bc.pass.Fset.Position(pos).Line
	site := bc.findInstructionAtOrAfter(pos)

	b := &Borrow{
		ID:        len(bc.borrows) + 1,
		Kind:      kind,
		OwnerNode: ownerNode,
		OwnerName: ownerName,
		OwnerVar:  ownerVar,
		OwnerVal:  ownerVal,
		Pos:       pos,
		Line:      line,
		Site:      site,
		Var:       v,
		Uses:      make(map[ssa.Instruction]bool),
	}
	bc.markRegisteredBorrowExpr(rhs)
	bc.collectUses(b)
	bc.borrows = append(bc.borrows, b)
}

func (bc *borrowChecker) resolveOwner(rhs ast.Expr) (string, *types.Var, ssa.Value) {
	switch e := rhs.(type) {
	case *ast.ParenExpr:
		return bc.resolveOwner(e.X)
	case *ast.StarExpr:
		return bc.resolveOwner(e.X)
	case *ast.SliceExpr:
		return bc.resolveOwner(e.X)
	case *ast.CompositeLit:
		return fmt.Sprintf("<lit@%d>", e.Pos()), nil, nil
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return bc.resolveOwner(e.X)
		}
	case *ast.CallExpr:
		if len(e.Args) > 0 {
			return bc.resolveOwner(e.Args[0])
		}
	case *ast.Ident:
		name := e.Name
		var vr *types.Var
		if v, ok := bc.pass.TypesInfo.Uses[e].(*types.Var); ok {
			vr = v
		} else if v, ok := bc.pass.TypesInfo.Defs[e].(*types.Var); ok {
			vr = v
		}

		for _, prev := range bc.borrows {
			if prev.Var != nil {
				if vr != nil {
					if prev.Var == vr {
						return prev.OwnerName, prev.OwnerVar, prev.OwnerVal
					}
				} else if prev.Var.Name() == name {
					return prev.OwnerName, prev.OwnerVar, prev.OwnerVal
				}
			}
		}
		// Trace earlier pointer alias, sub-slice, or type-assertion assignments in AST across multiple hops:
		// e.g. p := &x; q := p; in := &o.In; t := s[1:]; p := i.(*Pair)
		if syntax := bc.fn.Syntax(); syntax != nil {
			extractAliasBase := func(rhsExpr ast.Expr) *ast.Ident {
				rhsVal := unparen(rhsExpr)
				if call, ok := rhsVal.(*ast.CallExpr); ok && len(call.Args) > 0 {
					rhsVal = unparen(call.Args[0])
				}
				switch rv := rhsVal.(type) {
				case *ast.UnaryExpr:
					if rv.Op == token.AND {
						return rootIdent(rv.X)
					}
				case *ast.Ident:
					if tv := bc.pass.TypesInfo.TypeOf(rv); tv != nil {
						if _, isPtr := tv.Underlying().(*types.Pointer); isPtr {
							return rv
						}
					}
				case *ast.SliceExpr:
					return rootIdent(rv.X)
				case *ast.TypeAssertExpr:
					return rootIdent(rv.X)
				}
				return nil
			}
			for range 8 {
				var origName string
				var origVar *types.Var
				ast.Inspect(syntax, func(n ast.Node) bool {
					if asgn, ok := n.(*ast.AssignStmt); ok {
						for idx, lhs := range asgn.Lhs {
							if id, ok := unparen(lhs).(*ast.Ident); ok {
								var idVar *types.Var
								if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
									idVar = v
								} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
									idVar = v
								}
								isTarget := false
								if vr != nil && idVar != nil {
									isTarget = (vr == idVar)
								} else if id.Name == name {
									isTarget = true
								}
								if isTarget && idx < len(asgn.Rhs) {
									if baseId := extractAliasBase(asgn.Rhs[idx]); baseId != nil {
										origName = baseId.Name
										if v, ok := bc.pass.TypesInfo.Uses[baseId].(*types.Var); ok {
											origVar = v
										} else if v, ok := bc.pass.TypesInfo.Defs[baseId].(*types.Var); ok {
											origVar = v
										}
										return false
									}
								}
							}
						}
					}
					if ds, ok := n.(*ast.DeclStmt); ok {
						if gd, ok := ds.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
							for _, spec := range gd.Specs {
								if vs, ok := spec.(*ast.ValueSpec); ok {
									for idx, nameId := range vs.Names {
										var idVar *types.Var
										if v, ok := bc.pass.TypesInfo.Defs[nameId].(*types.Var); ok {
											idVar = v
										}
										isTarget := false
										if vr != nil && idVar != nil {
											isTarget = (vr == idVar)
										} else if nameId.Name == name {
											isTarget = true
										}
										if isTarget && idx < len(vs.Values) {
											if baseId := extractAliasBase(vs.Values[idx]); baseId != nil {
												origName = baseId.Name
												if v, ok := bc.pass.TypesInfo.Uses[baseId].(*types.Var); ok {
													origVar = v
												} else if v, ok := bc.pass.TypesInfo.Defs[baseId].(*types.Var); ok {
													origVar = v
												}
												return false
											}
										}
									}
								}
							}
						}
					}
					return true
				})
				if origName == "" || (origName == name && origVar == vr) {
					break
				}
				name = origName
				if origVar != nil {
					vr = origVar
				}
			}
		}
		// 1. If variable identity is known, prefer address-taken allocation in basic blocks
		if vr != nil {
			for _, b := range bc.fn.Blocks {
				for _, ins := range b.Instrs {
					if alloc, ok := ins.(*ssa.Alloc); ok && alloc.Pos() == vr.Pos() {
						return name, vr, alloc
					}
				}
			}
			for _, l := range bc.fn.Locals {
				if l.Pos() == vr.Pos() {
					return name, vr, l
				}
			}
			for _, p := range bc.fn.Params {
				if p.Pos() == vr.Pos() || (p.Object() != nil && p.Object() == vr) {
					return name, vr, p
				}
			}
		}

		// 2. Fallback to lexical name matching only when vr is nil
		for _, b := range bc.fn.Blocks {
			for _, ins := range b.Instrs {
				if alloc, ok := ins.(*ssa.Alloc); ok && alloc.Comment == name {
					return name, vr, alloc
				}
			}
		}
		for _, l := range bc.fn.Locals {
			if l.Comment == name {
				return name, vr, l
			}
		}
		for _, p := range bc.fn.Params {
			if p.Name() == name {
				return name, vr, p
			}
		}
		return name, vr, nil
	case *ast.SelectorExpr:
		return bc.resolveOwner(e.X)
	case *ast.IndexExpr:
		return bc.resolveOwner(e.X)
	}
	return "x", nil, nil
}

func (bc *borrowChecker) findInstructionAtOrAfter(pos token.Pos) ssa.Instruction {
	line := bc.pass.Fset.Position(pos).Line
	var bestAtOrAfter ssa.Instruction
	var bestSameLine ssa.Instruction
	var bestAfterLine ssa.Instruction

	for _, b := range bc.fn.Blocks {
		for _, ins := range b.Instrs {
			insPos := ins.Pos()
			if insPos.IsValid() && insPos >= pos {
				if bestAtOrAfter == nil || insPos < bestAtOrAfter.Pos() {
					bestAtOrAfter = ins
				}
			}
			insLine := bc.pass.Fset.Position(insPos).Line
			if insLine == line && bestSameLine == nil {
				bestSameLine = ins
			}
			if insLine > line && (bestAfterLine == nil || insLine < bc.pass.Fset.Position(bestAfterLine.Pos()).Line) {
				bestAfterLine = ins
			}
		}
	}
	if bestAtOrAfter != nil {
		return bestAtOrAfter
	}
	if bestSameLine != nil {
		return bestSameLine
	}
	if bestAfterLine != nil {
		return bestAfterLine
	}
	if len(bc.fn.Blocks) > 0 && len(bc.fn.Blocks[0].Instrs) > 0 {
		return bc.fn.Blocks[0].Instrs[0]
	}
	return nil
}

func (bc *borrowChecker) varOfIdent(id *ast.Ident) *types.Var {
	if id == nil {
		return nil
	}
	if tv, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
		return tv
	}
	if tv, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
		return tv
	}
	return nil
}

func (bc *borrowChecker) borrowTargetVars(b *Borrow) map[*types.Var]bool {
	targetVars := map[*types.Var]bool{}
	if b == nil || b.Var == nil {
		return targetVars
	}
	targetVars[b.Var] = true
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return targetVars
	}

	// Track pointer-to-local-var relationships (e.g. pc := &c) so stores `*pc = b1`
	// mark `c` as holding a value derived from `b1`.
	addrOfVar := map[*types.Var]map[*types.Var]bool{}
	addAddr := func(ptr, target *types.Var) bool {
		if ptr == nil || target == nil {
			return false
		}
		m := addrOfVar[ptr]
		if m == nil {
			m = map[*types.Var]bool{}
			addrOfVar[ptr] = m
		}
		if m[target] {
			return false
		}
		m[target] = true
		return true
	}
	recordAddrBind := func(lhsVar *types.Var, rhs ast.Expr) bool {
		if lhsVar == nil || rhs == nil {
			return false
		}
		switch r := unparen(rhs).(type) {
		case *ast.UnaryExpr:
			if r.Op == token.AND {
				if id, ok := unparen(r.X).(*ast.Ident); ok {
					return addAddr(lhsVar, bc.varOfIdent(id))
				}
			}
		case *ast.Ident:
			rv := bc.varOfIdent(r)
			updated := false
			for tgt := range addrOfVar[rv] {
				if addAddr(lhsVar, tgt) {
					updated = true
				}
			}
			return updated
		}
		return false
	}
	addrChanged := true
	for addrChanged {
		addrChanged = false
		ast.Inspect(syntax, func(n ast.Node) bool {
			switch stmt := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range stmt.Rhs {
					if i < len(stmt.Lhs) {
						if lhsId, ok := unparen(stmt.Lhs[i]).(*ast.Ident); ok {
							if recordAddrBind(bc.varOfIdent(lhsId), rhs) {
								addrChanged = true
							}
						}
					}
				}
			case *ast.DeclStmt:
				if gd, ok := stmt.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
					for _, spec := range gd.Specs {
						if vs, ok := spec.(*ast.ValueSpec); ok {
							for i, val := range vs.Values {
								if i < len(vs.Names) {
									if recordAddrBind(bc.varOfIdent(vs.Names[i]), val) {
										addrChanged = true
									}
								}
							}
						}
					}
				}
			}
			return true
		})
	}

	changed := true
	for changed {
		changed = false
		ast.Inspect(syntax, func(n ast.Node) bool {
			switch stmt := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range stmt.Rhs {
					if i >= len(stmt.Lhs) {
						continue
					}
					rhsHasTarget := false
					ast.Inspect(rhs, func(sub ast.Node) bool {
						if id, ok := sub.(*ast.Ident); ok {
							if v := bc.varOfIdent(id); v != nil && targetVars[v] {
								rhsHasTarget = true
								return false
							}
						}
						return true
					})
					if rhsHasTarget {
						switch lhs := unparen(stmt.Lhs[i]).(type) {
						case *ast.Ident:
							if lhsVar := bc.varOfIdent(lhs); lhsVar != nil && !targetVars[lhsVar] {
								targetVars[lhsVar] = true
								changed = true
							}
						case *ast.StarExpr:
							if ptrId, ok := unparen(lhs.X).(*ast.Ident); ok {
								if ptrVar := bc.varOfIdent(ptrId); ptrVar != nil {
									for tgt := range addrOfVar[ptrVar] {
										if !targetVars[tgt] {
											targetVars[tgt] = true
											changed = true
										}
									}
								}
							}
						}
					}
				}
			case *ast.DeclStmt:
				if gd, ok := stmt.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
					for _, spec := range gd.Specs {
						if vs, ok := spec.(*ast.ValueSpec); ok {
							for i, val := range vs.Values {
								if i >= len(vs.Names) {
									continue
								}
								rhsHasTarget := false
								ast.Inspect(val, func(sub ast.Node) bool {
									if id, ok := sub.(*ast.Ident); ok {
										if v := bc.varOfIdent(id); v != nil && targetVars[v] {
											rhsHasTarget = true
											return false
										}
									}
									return true
								})
								if rhsHasTarget {
									if lhsVar := bc.varOfIdent(vs.Names[i]); lhsVar != nil && !targetVars[lhsVar] {
										targetVars[lhsVar] = true
										changed = true
									}
								}
							}
						}
					}
				}
			}
			return true
		})
	}
	return targetVars
}

func (bc *borrowChecker) collectUses(b *Borrow) {
	syntax := bc.fn.Syntax()
	if syntax == nil || b.Var == nil {
		return
	}

	// 1. Collect all AST lines where b.Var (or any variable aliasing or pointing to it) is used
	targetVars := bc.borrowTargetVars(b)

	useLines := make(map[int]bool)
	isDeferred := bc.deferred[b.Var]

	lhsAssignIdents := make(map[*ast.Ident]bool)
	ast.Inspect(syntax, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && (as.Tok == token.ASSIGN || as.Tok == token.DEFINE) {
			for _, lhs := range as.Lhs {
				if id, ok := unparen(lhs).(*ast.Ident); ok {
					lhsAssignIdents[id] = true
				}
			}
		}
		return true
	})

	ast.Inspect(syntax, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		var v *types.Var
		isUse := false
		if tv, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
			v = tv
			isUse = true
		} else if tv, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
			v = tv
		}
		if v != nil && targetVars[v] {
			line := bc.pass.Fset.Position(id.Pos()).Line
			if line >= b.Line || (isUse && !lhsAssignIdents[id]) {
				useLines[line] = true
			}
		}
		return true
	})

	// 2. Map SSA instructions on those lines to b.Uses
	for _, blk := range bc.fn.Blocks {
		for _, ins := range blk.Instrs {
			insLine := bc.pass.Fset.Position(ins.Pos()).Line
			if useLines[insLine] {
				b.Uses[ins] = true
			}
			if isDeferred {
				if _, ok := ins.(*ssa.Return); ok {
					b.Uses[ins] = true
				}
			}
		}
	}
}

// borrowRecreatedAtSite reports whether re-entering b.Site's block along a back-edge
// re-executes b's borrow creation before b.Site. When `b := &x` emits no SSA instruction on
// its own line, findInstructionAtOrAfter slides b.Site forward; if it slid across a `for`,
// `range`, or label header, re-entering that loop/label does NOT recreate b.
func (bc *borrowChecker) borrowRecreatedAtSite(b *Borrow) bool {
	if b == nil || b.Site == nil {
		return false
	}
	sitePos := b.Site.Pos()
	if _, isPhi := b.Site.(*ssa.Phi); isPhi && b.Site.Block() != nil {
		for _, ins := range b.Site.Block().Instrs {
			if _, ok := ins.(*ssa.Phi); !ok && ins.Pos().IsValid() {
				sitePos = ins.Pos()
				break
			}
		}
	}
	syntax := bc.fn.Syntax()
	if syntax == nil || !b.Pos.IsValid() || !sitePos.IsValid() {
		return false
	}
	crossedHeader := false
	ast.Inspect(syntax, func(n ast.Node) bool {
		if crossedHeader || n == nil {
			return false
		}
		if n != syntax {
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false
			}
		}
		switch s := n.(type) {
		case *ast.ForStmt:
			if b.Pos < s.Pos() && s.Pos() <= sitePos {
				crossedHeader = true
				return false
			}
		case *ast.RangeStmt:
			if b.Pos < s.Pos() && s.Pos() <= sitePos {
				crossedHeader = true
				return false
			}
		case *ast.LabeledStmt:
			if b.Pos < s.Pos() && s.Pos() <= sitePos {
				crossedHeader = true
				return false
			}
		}
		return true
	})
	return !crossedHeader
}

// isBorrowActiveAt reports whether borrow b is active at (sitePos, t).
func (bc *borrowChecker) isBorrowActiveAt(b *Borrow, sitePos token.Pos, t ssa.Instruction) bool {
	if b == nil || t == nil || b.Site == nil {
		return false
	}
	if !bc.isReachable(b.Site, t) {
		return false
	}

	recreated := bc.borrowRecreatedAtSite(b)
	startBlock := t.Block()
	tIdx := indexIn(t)

	// Check remaining instructions in startBlock (stopping if b is recreated before any use;
	// when sitePos < b.Pos and t slid forward onto b.Site, b is recreated before b.Site executes).
	for i := tIdx; i < len(startBlock.Instrs); i++ {
		ins := startBlock.Instrs[i]
		if (i > tIdx || (sitePos.IsValid() && sitePos < b.Pos)) && ins == b.Site && recreated {
			return false
		}
		if b.Uses[ins] {
			return true
		}
	}

	// BFS across successor basic blocks (including cyclic paths back into startBlock).
	visited := map[*ssa.BasicBlock]bool{}
	queue := append([]*ssa.BasicBlock(nil), startBlock.Succs...)
	for len(queue) > 0 {
		blk := queue[0]
		queue = queue[1:]
		if visited[blk] {
			continue
		}
		visited[blk] = true

		killed := false
		for _, ins := range blk.Instrs {
			if ins == b.Site && recreated {
				killed = true
				break
			}
			if b.Uses[ins] {
				return true
			}
			if ins == t {
				killed = true
				break
			}
		}
		if !killed {
			queue = append(queue, blk.Succs...)
		}
	}

	return false
}

func (bc *borrowChecker) isReachable(from, to ssa.Instruction) bool {
	if from == nil || to == nil || from.Block() == nil || to.Block() == nil {
		return false
	}
	if from.Block() == to.Block() && indexIn(to) >= indexIn(from) {
		return true
	}
	visited := map[*ssa.BasicBlock]bool{}
	queue := append([]*ssa.BasicBlock(nil), from.Block().Succs...)
	for len(queue) > 0 {
		blk := queue[0]
		queue = queue[1:]
		if blk == to.Block() {
			return true
		}
		if visited[blk] {
			continue
		}
		visited[blk] = true
		queue = append(queue, blk.Succs...)
	}
	return false
}

func (bc *borrowChecker) mayExecuteAfterBorrow(brw *Borrow, sitePos token.Pos, ins ssa.Instruction) bool {
	if brw == nil {
		return false
	}
	if sitePos > brw.Pos {
		return true
	}
	if sitePos == brw.Pos || brw.Site == nil || ins == nil || brw.Site.Block() == nil || ins.Block() == nil {
		return false
	}
	visited := map[*ssa.BasicBlock]bool{}
	queue := append([]*ssa.BasicBlock(nil), brw.Site.Block().Succs...)
	for len(queue) > 0 {
		blk := queue[0]
		queue = queue[1:]
		if blk == ins.Block() {
			return true
		}
		if visited[blk] {
			continue
		}
		visited[blk] = true
		queue = append(queue, blk.Succs...)
	}
	return false
}

// Pass A: Conflicting Borrow Check (BORROW002)
func (bc *borrowChecker) checkConflictingBorrows() {
	seenPair := map[[2]int]bool{}
	for i, b2 := range bc.borrows {
		for j, b1 := range bc.borrows {
			if i == j {
				continue
			}
			pair := [2]int{min(i, j), max(i, j)}
			if seenPair[pair] {
				continue
			}
			if !bc.mayExecuteAfterBorrow(b1, b2.Pos, b2.Site) {
				continue
			}

			// Check if both borrows target the same owner
			sameOwner := false
			if b1.OwnerVar != nil && b2.OwnerVar != nil {
				sameOwner = (b1.OwnerVar == b2.OwnerVar)
			} else if b1.OwnerNode != nil && b2.OwnerNode != nil && b1.OwnerNode.find() == b2.OwnerNode.find() {
				sameOwner = true
			} else if b1.OwnerVal != nil && b2.OwnerVal != nil && b1.OwnerVal == b2.OwnerVal {
				sameOwner = true
			} else if b1.OwnerName != "" && b1.OwnerName == b2.OwnerName {
				sameOwner = true
			}

			if !sameOwner {
				continue
			}

			// Check if b1 is active at b2's creation site
			if !bc.isBorrowActiveAt(b1, b2.Pos, b2.Site) {
				continue
			}
			seenPair[pair] = true

			// Conflict rules:
			var msg string
			switch {
			case b1.Kind == annot.BorrowMut && b2.Kind == annot.BorrowMut:
				msg = fmt.Sprintf("cannot borrow '%s' as mutable more than once at a time: already borrowed as mutable at line %d",
					b2.OwnerName, b1.Line)
			case b1.Kind == annot.BorrowRob && b2.Kind == annot.BorrowMut:
				msg = fmt.Sprintf("cannot borrow '%s' as mutable because it is also borrowed as read-only at line %d",
					b2.OwnerName, b1.Line)
			case b1.Kind == annot.BorrowMut && b2.Kind == annot.BorrowRob:
				msg = fmt.Sprintf("cannot borrow '%s' as read-only because it is also borrowed as mutable at line %d",
					b2.OwnerName, b1.Line)
			default:
				continue
			}
			// An escape hatch on either borrow site mutes the conflict. Both are asked through Mute, so
			// each hatch that covers a site is recorded as used and is not reported as stale.
			if m1, m2 := bc.ix.Mute(b1.Pos), bc.ix.Mute(b2.Pos); m1 || m2 {
				continue
			}
			bc.report(b2.Pos, ConflictingBorrow, msg)
		}
	}
}

// Pass B: Owner Invalidation Check (BORROW003)
func (bc *borrowChecker) checkOwnerMutation() {
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return
	}

	// Map pre-existing pointer aliases (p := &x) to the owner variable they point to.
	ptrAliasOf := make(map[*types.Var]*types.Var)
	recordPtrAlias := func(lhsVar *types.Var, rhs ast.Expr) {
		if lhsVar == nil || rhs == nil {
			return
		}
		for _, brw := range bc.borrows {
			if brw.Var == lhsVar {
				return
			}
		}
		switch r := unparen(rhs).(type) {
		case *ast.UnaryExpr:
			if r.Op == token.AND {
				if id := rootIdent(r.X); id != nil {
					if ov, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && ov != nil {
						ptrAliasOf[lhsVar] = ov
					} else if ov, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok && ov != nil {
						ptrAliasOf[lhsVar] = ov
					}
				}
			}
		case *ast.Ident:
			if pv, ok := bc.pass.TypesInfo.Uses[r].(*types.Var); ok && pv != nil {
				if ov := ptrAliasOf[pv]; ov != nil {
					ptrAliasOf[lhsVar] = ov
				}
			}
		}
	}
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range stmt.Rhs {
				if i < len(stmt.Lhs) {
					if id, ok := unparen(stmt.Lhs[i]).(*ast.Ident); ok {
						var v *types.Var
						if tv, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
							v = tv
						} else if tv, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
							v = tv
						}
						recordPtrAlias(v, rhs)
					}
				}
			}
		case *ast.ValueSpec:
			for i, val := range stmt.Values {
				if i < len(stmt.Names) {
					if v, ok := bc.pass.TypesInfo.Defs[stmt.Names[i]].(*types.Var); ok {
						recordPtrAlias(v, val)
					}
				}
			}
		}
		return true
	})

	// Map local closure variables (mutator := func() { x = 99 } or h := hook{f: func() { x = 99 }}) to the variables they mutate.
	closureMutates := make(map[*types.Var][]*types.Var)
	extractMutatedVars := func(body ast.Node) []*types.Var {
		if body == nil {
			return nil
		}
		var out []*types.Var
		addMutVar := func(expr ast.Expr) {
			u := unparen(expr)
			_, isStar := u.(*ast.StarExpr)
			id := rootIdent(u)
			if id == nil && isStar {
				id = rootIdent(u.(*ast.StarExpr).X)
			}
			if id == nil {
				return
			}
			if mv, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && mv != nil {
				if isStar {
					if ptrAliasOf[mv] != nil {
						out = append(out, ptrAliasOf[mv])
					}
				} else {
					out = append(out, mv)
				}
			}
		}
		ast.Inspect(body, func(sub ast.Node) bool {
			switch st := sub.(type) {
			case *ast.AssignStmt:
				for _, l := range st.Lhs {
					addMutVar(l)
				}
			case *ast.IncDecStmt:
				addMutVar(st.X)
			case *ast.CallExpr:
				if funId, ok := unparen(st.Fun).(*ast.Ident); ok && len(st.Args) > 0 {
					switch funId.Name {
					case "clear", "copy", "delete", "append":
						addMutVar(st.Args[0])
					}
				}
			}
			return true
		})
		return out
	}
	recordClosureExpr := func(lhsVar *types.Var, rhs ast.Expr) {
		if lhsVar == nil || rhs == nil {
			return
		}
		ast.Inspect(rhs, func(sub ast.Node) bool {
			if fl, ok := sub.(*ast.FuncLit); ok && fl.Body != nil {
				closureMutates[lhsVar] = append(closureMutates[lhsVar], extractMutatedVars(fl.Body)...)
			}
			return true
		})
	}
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range stmt.Rhs {
				if i < len(stmt.Lhs) {
					if id := rootIdent(stmt.Lhs[i]); id != nil {
						var v *types.Var
						if tv, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
							v = tv
						} else if tv, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
							v = tv
						}
						recordClosureExpr(v, rhs)
					}
				}
			}
		case *ast.ValueSpec:
			for i, val := range stmt.Values {
				if i < len(stmt.Names) {
					if v, ok := bc.pass.TypesInfo.Defs[stmt.Names[i]].(*types.Var); ok {
						recordClosureExpr(v, val)
					}
				}
			}
		}
		return true
	})

	// Track address-of expressions (&x) that are passed to read-only (rob) parameters
	robArgExprs := make(map[*ast.UnaryExpr]bool)
	ast.Inspect(syntax, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if funType := bc.pass.TypesInfo.TypeOf(call.Fun); funType != nil {
			if sig, ok := funType.Underlying().(*types.Signature); ok && sig.Params() != nil {
				for idx, arg := range call.Args {
					if idx < sig.Params().Len() {
						pVar := sig.Params().At(idx)
						if bc.ix.IsRob(pVar) {
							if ue, ok := unparen(arg).(*ast.UnaryExpr); ok && ue.Op == token.AND {
								robArgExprs[ue] = true
							}
						}
					}
				}
			}
		}
		return true
	})

	// 1. Direct Owner Assignment, IncDec, Builtin Mutation, Closure Call, Move, and Unannotated Address-Take in AST
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range stmt.Lhs {
				id := rootIdent(lhs)
				_, isStar := unparen(lhs).(*ast.StarExpr)
				if id == nil && isStar {
					id = rootIdent(unparen(lhs).(*ast.StarExpr).X)
				}
				if id == nil {
					continue
				}
				var assignedVar *types.Var
				isDef := false
				if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
					assignedVar = v
					isDef = true
				} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
					assignedVar = v
				}
				name := id.Name
				ins := bc.findInstructionAtOrAfter(lhs.Pos())
				for _, brw := range bc.borrows {
					if assignedVar != nil && brw.Var == assignedVar {
						if brw.Kind == annot.BorrowRob {
							_, isStar := unparen(lhs).(*ast.StarExpr)
							_, isSel := unparen(lhs).(*ast.SelectorExpr)
							_, isIdx := unparen(lhs).(*ast.IndexExpr)
							if (isStar || isSel || isIdx) && bc.mayExecuteAfterBorrow(brw, id.Pos(), ins) {
								bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
									"cannot assign through read-only borrow '%s' (borrowed at line %d)",
									id.Name, brw.Line))
							}
						}
						continue
					}
					isOwner := false
					_, isStar := unparen(lhs).(*ast.StarExpr)
					if brw.OwnerVar != nil && assignedVar != nil {
						if !isStar && brw.OwnerVar == assignedVar {
							if isDef {
								continue
							}
							isOwner = true
						} else if isStar && ptrAliasOf[assignedVar] == brw.OwnerVar && assignedVar.Pos() < brw.Pos {
							isOwner = true
						}
					} else if !isStar && brw.OwnerName == name {
						isOwner = true
					}
					if isOwner && bc.mayExecuteAfterBorrow(brw, id.Pos(), ins) && bc.isBorrowActiveAt(brw, id.Pos(), ins) {
						bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
							"cannot assign to '%s' because it is borrowed at line %d",
							brw.OwnerName, brw.Line))
					}
				}
			}
		case *ast.IncDecStmt:
			id := rootIdent(stmt.X)
			_, isStar := unparen(stmt.X).(*ast.StarExpr)
			_, isSel := unparen(stmt.X).(*ast.SelectorExpr)
			_, isIdx := unparen(stmt.X).(*ast.IndexExpr)
			if id == nil && isStar {
				id = rootIdent(unparen(stmt.X).(*ast.StarExpr).X)
			}
			if id == nil {
				break
			}
			var assignedVar *types.Var
			if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
				assignedVar = v
			} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
				assignedVar = v
			}
			name := id.Name
			ins := bc.findInstructionAtOrAfter(stmt.Pos())
			for _, brw := range bc.borrows {
				if assignedVar != nil && brw.Var == assignedVar {
					if brw.Kind == annot.BorrowRob && (isStar || isSel || isIdx) && bc.mayExecuteAfterBorrow(brw, id.Pos(), ins) {
						bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
							"cannot assign through read-only borrow '%s' (borrowed at line %d)",
							id.Name, brw.Line))
					}
					continue
				}
				isOwner := false
				if brw.OwnerVar != nil && assignedVar != nil {
					if !isStar && brw.OwnerVar == assignedVar {
						isOwner = true
					} else if isStar && ptrAliasOf[assignedVar] == brw.OwnerVar && assignedVar.Pos() < brw.Pos {
						isOwner = true
					}
				} else if !isStar && brw.OwnerName == name {
					isOwner = true
				}
				if isOwner && bc.mayExecuteAfterBorrow(brw, id.Pos(), ins) && bc.isBorrowActiveAt(brw, id.Pos(), ins) {
					bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
						"cannot assign to '%s' because it is borrowed at line %d",
						brw.OwnerName, brw.Line))
				}
			}
		case *ast.RangeStmt:
			if stmt.Tok == token.ASSIGN {
				for _, rExpr := range []ast.Expr{stmt.Key, stmt.Value} {
					if rExpr == nil {
						continue
					}
					id := rootIdent(rExpr)
					if id == nil {
						continue
					}
					var assignedVar *types.Var
					if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
						assignedVar = v
					} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
						assignedVar = v
					}
					ins := bc.findInstructionAtOrAfter(rExpr.Pos())
					for _, brw := range bc.borrows {
						if assignedVar != nil && brw.OwnerVar == assignedVar && bc.mayExecuteAfterBorrow(brw, id.Pos(), ins) && bc.isBorrowActiveAt(brw, id.Pos(), ins) {
							bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
								"cannot assign to '%s' because it is borrowed at line %d",
								brw.OwnerName, brw.Line))
						}
					}
				}
			}
		case *ast.CallExpr:
			ins := bc.findInstructionAtOrAfter(stmt.Pos())
			// A. Built-in slice/map mutation: clear(s), copy(s, src), delete(m, k), append(s[:1], v)
			if funId, ok := unparen(stmt.Fun).(*ast.Ident); ok {
				switch funId.Name {
				case "clear", "copy", "delete", "append":
					if len(stmt.Args) > 0 {
						// For append, skip if the first arg is not a slice expression (e.g. s = append(s, v) is handled by AssignStmt on s).
						if funId.Name == "append" {
							if _, isSliceExpr := unparen(stmt.Args[0]).(*ast.SliceExpr); !isSliceExpr {
								break
							}
						}
						if argId := rootIdent(stmt.Args[0]); argId != nil {
							var argVar *types.Var
							if v, ok := bc.pass.TypesInfo.Uses[argId].(*types.Var); ok {
								argVar = v
							} else if v, ok := bc.pass.TypesInfo.Defs[argId].(*types.Var); ok {
								argVar = v
							}
							for _, brw := range bc.borrows {
								isOwner := false
								if brw.OwnerVar != nil && argVar != nil {
									isOwner = (brw.OwnerVar == argVar)
								} else if brw.OwnerName == argId.Name {
									isOwner = true
								}
								if isOwner && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
									bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
										"cannot mutate '%s' via %s() because it is borrowed at line %d",
										brw.OwnerName, funId.Name, brw.Line))
								}
							}
						}
					}
				}
			}
			// B. Call to or passing of a pre-defined mutating closure: mutator(), h.f(), runIt(mutator)
			checkClosureVar := func(cId *ast.Ident) {
				if cId == nil {
					return
				}
				if cVar, ok := bc.pass.TypesInfo.Uses[cId].(*types.Var); ok && cVar != nil {
					if mutatedVars := closureMutates[cVar]; len(mutatedVars) > 0 {
						for _, mv := range mutatedVars {
							for _, brw := range bc.borrows {
								if brw.OwnerVar == mv && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
									bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
										"cannot assign to '%s' (via closure '%s') because it is borrowed at line %d",
										brw.OwnerName, cId.Name, brw.Line))
								}
							}
						}
					}
				}
			}
			checkClosureVar(rootIdent(stmt.Fun))
			for _, arg := range stmt.Args {
				if argId, ok := unparen(arg).(*ast.Ident); ok {
					checkClosureVar(argId)
				}
			}
			// B2. Passing borrowed owner to an //gosree:iso parameter or passing pre-existing pointer alias to a non-rob function
			if bc.ix.BorrowAt(stmt.Pos()) == annot.BorrowNone {
				if funType := bc.pass.TypesInfo.TypeOf(stmt.Fun); funType != nil {
					if sig, ok := funType.Underlying().(*types.Signature); ok && sig.Params() != nil {
						for idx, arg := range stmt.Args {
							if idx >= sig.Params().Len() {
								break
							}
							pVar := sig.Params().At(idx)
							argId := rootIdent(arg)
							if argId == nil {
								continue
							}
							var argVar *types.Var
							if v, ok := bc.pass.TypesInfo.Uses[argId].(*types.Var); ok {
								argVar = v
							} else if v, ok := bc.pass.TypesInfo.Defs[argId].(*types.Var); ok {
								argVar = v
							}
							if argVar == nil {
								continue
							}
							if bc.ix.IsIso(pVar) {
								for _, brw := range bc.borrows {
									if brw.Var == argVar {
										continue
									}
									if brw.OwnerVar == argVar && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
										bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
											"cannot move '%s' because it is borrowed at line %d",
											brw.OwnerName, brw.Line))
									}
								}
							} else if _, isIdent := unparen(arg).(*ast.Ident); isIdent && !bc.ix.IsRob(pVar) && !bc.ix.IsMub(pVar) {
								if ownerOfAlias := ptrAliasOf[argVar]; ownerOfAlias != nil {
									for _, brw := range bc.borrows {
										if brw.Var == argVar {
											continue
										}
										if brw.OwnerVar == ownerOfAlias && argVar.Pos() < brw.Pos && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
											bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
												"cannot borrow or take address of '%s' while it is borrowed at line %d",
												brw.OwnerName, brw.Line))
										}
									}
								}
								for _, brw := range bc.borrows {
									if brw.Var == argVar && brw.Kind == annot.BorrowRob && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) {
										if funId, ok := unparen(stmt.Fun).(*ast.Ident); ok {
											if fnObj, ok := bc.pass.TypesInfo.Uses[funId].(*types.Func); ok && bc.ssa != nil && bc.ssa.Pkg != nil && bc.ssa.Pkg.Prog != nil {
												if callee := bc.ssa.Pkg.Prog.FuncValue(fnObj); calleeMutatesParam(callee, idx) {
													bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
														"cannot assign through read-only borrow '%s' (borrowed at line %d)",
														argId.Name, brw.Line))
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
			// C. cap.Move(x) while x is borrowed
			if sel, ok := unparen(stmt.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "Move" && len(stmt.Args) == 1 {
				if xId, ok := unparen(sel.X).(*ast.Ident); ok && xId.Name == "cap" {
					if argId := rootIdent(stmt.Args[0]); argId != nil {
						var argVar *types.Var
						if v, ok := bc.pass.TypesInfo.Uses[argId].(*types.Var); ok {
							argVar = v
						} else if v, ok := bc.pass.TypesInfo.Defs[argId].(*types.Var); ok {
							argVar = v
						}
						for _, brw := range bc.borrows {
							if argVar != nil && brw.Var == argVar {
								continue
							}
							isOwner := false
							if brw.OwnerVar != nil && argVar != nil {
								isOwner = (brw.OwnerVar == argVar)
							} else if brw.OwnerName == argId.Name {
								isOwner = true
							}
							if isOwner && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
								bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
									"cannot move '%s' because it is borrowed at line %d",
									brw.OwnerName, brw.Line))
							}
						}
					}
				}
			}
			// E. Implicit address-taking pointer-receiver method call on borrowed value owner: c.Inc()
			if sel, ok := unparen(stmt.Fun).(*ast.SelectorExpr); ok {
				if selection, ok := bc.pass.TypesInfo.Selections[sel]; ok && selection.Kind() == types.MethodVal {
					if fnObj, ok := selection.Obj().(*types.Func); ok && fnObj.Signature() != nil && fnObj.Signature().Recv() != nil {
						recvVar := fnObj.Signature().Recv()
						_, recvIsPtr := recvVar.Type().Underlying().(*types.Pointer)
						xType := bc.pass.TypesInfo.TypeOf(sel.X)
						_, xIsPtr := xType.Underlying().(*types.Pointer)
						if recvIsPtr && !xIsPtr && !bc.ix.IsRob(recvVar) {
							if recvId := rootIdent(sel.X); recvId != nil {
								var targetVar *types.Var
								if v, ok := bc.pass.TypesInfo.Uses[recvId].(*types.Var); ok {
									targetVar = v
								} else if v, ok := bc.pass.TypesInfo.Defs[recvId].(*types.Var); ok {
									targetVar = v
								}
								for _, brw := range bc.borrows {
									if targetVar != nil && brw.Var == targetVar {
										continue
									}
									isOwner := false
									if brw.OwnerVar != nil && targetVar != nil {
										isOwner = (brw.OwnerVar == targetVar)
									} else if brw.OwnerName == recvId.Name {
										isOwner = true
									}
									if isOwner && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
										bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
											"cannot call pointer-receiver method '%s' on '%s' while it is borrowed at line %d",
											sel.Sel.Name, brw.OwnerName, brw.Line))
									}
								}
							}
						}
					}
				}
			}
		case *ast.UnaryExpr:
			if stmt.Op == token.AND {
				// Skip if this &expr is itself an annotated or registered borrow site
				if bc.registeredBorrowExprs[stmt] || bc.ix.BorrowAt(stmt.Pos()) != annot.BorrowNone || bc.ix.BorrowAt(stmt.OpPos) != annot.BorrowNone {
					break
				}
				id := rootIdent(stmt.X)
				if id == nil {
					break
				}
				var targetVar *types.Var
				if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
					targetVar = v
				} else if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
					targetVar = v
				}
				ins := bc.findInstructionAtOrAfter(stmt.Pos())
				for _, brw := range bc.borrows {
					if targetVar != nil && brw.Var == targetVar {
						continue
					}
					if brw.Kind == annot.BorrowRob && robArgExprs[stmt] {
						continue
					}
					isOwner := false
					if brw.OwnerVar != nil && targetVar != nil {
						isOwner = (brw.OwnerVar == targetVar)
					} else if brw.OwnerName == id.Name {
						isOwner = true
					}
					if isOwner && bc.mayExecuteAfterBorrow(brw, stmt.Pos(), ins) && bc.isBorrowActiveAt(brw, stmt.Pos(), ins) {
						bc.report(stmt.Pos(), OwnerInvalidation, fmt.Sprintf(
							"cannot borrow or take address of '%s' while it is borrowed at line %d",
							brw.OwnerName, brw.Line))
					}
				}
			}
		}
		return true
	})

	// 2. Channel Send of Owner Allocation: ch <- x, ch <- pt.X, ch <- arr[0]
	for _, b := range bc.fn.Blocks {
		for _, ins := range b.Instrs {
			send, ok := ins.(*ssa.Send)
			if !ok {
				continue
			}
			// If what is being sent is a borrow variable, skip (handled as BORROW001 storage violation)
			line := bc.pass.Fset.Position(send.Pos()).Line
			isBorrowSend := false
			for _, brw := range bc.borrows {
				if brw.Val != nil && send.X == brw.Val {
					isBorrowSend = true
					break
				}
				if brw.Var != nil && syntax != nil {
					ast.Inspect(syntax, func(n ast.Node) bool {
						if snd, ok := n.(*ast.SendStmt); ok && bc.pass.Fset.Position(snd.Pos()).Line == line {
							if id := rootIdent(snd.Value); id != nil {
								if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && v == brw.Var {
									isBorrowSend = true
									return false
								}
								if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok && v == brw.Var {
									isBorrowSend = true
									return false
								}
							}
						}
						return true
					})
				}
				if isBorrowSend {
					break
				}
			}
			if isBorrowSend {
				continue
			}
			for _, brw := range bc.borrows {
				isOwnerSend := false
				if brw.OwnerVal != nil && (send.X == brw.OwnerVal || bc.isValOf(send.X, brw.OwnerVal)) {
					isOwnerSend = true
				}
				if !isOwnerSend {
					if alloc := baseAlloc(send.X); alloc != nil {
						if brw.OwnerVal != nil {
							if alloc == brw.OwnerVal {
								isOwnerSend = true
							}
						} else if brw.OwnerVar == nil && alloc.Comment == brw.OwnerName {
							isOwnerSend = true
						}
					}
				}
				if !isOwnerSend && syntax != nil {
					ast.Inspect(syntax, func(n ast.Node) bool {
						if snd, ok := n.(*ast.SendStmt); ok && bc.pass.Fset.Position(snd.Pos()).Line == line {
							if id := rootIdent(snd.Value); id != nil {
								if brw.OwnerVar != nil {
									if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && v == brw.OwnerVar {
										isOwnerSend = true
										return false
									}
									if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok && v == brw.OwnerVar {
										isOwnerSend = true
										return false
									}
								}
							}
						}
						return true
					})
				}
				if isOwnerSend && bc.mayExecuteAfterBorrow(brw, send.Pos(), ins) && bc.isBorrowActiveAt(brw, send.Pos(), ins) {
					bc.report(send.Pos(), OwnerInvalidation, fmt.Sprintf(
						"cannot move '%s' because it is borrowed at line %d",
						brw.OwnerName, brw.Line))
				}
			}
		}
	}
}

func (bc *borrowChecker) isValOf(v, owner ssa.Value) bool {
	if v == owner {
		return true
	}
	if un, ok := v.(*ssa.UnOp); ok && un.Op == token.MUL {
		return un.X == owner
	}
	return false
}

// Pass C: Dangling Local Stack Return Check (BORROW004)
func (bc *borrowChecker) checkDanglingReturn() {
	type pendingReturn struct {
		origin  *ssa.Alloc
		isField bool
	}
	reportedOrigins := map[*ssa.Alloc]bool{}
	var unpositioned []pendingReturn

	emit := func(pos token.Pos, origin *ssa.Alloc, isField bool) {
		if isField {
			bc.report(pos, DanglingReturn,
				"cannot return reference to local stack variable")
		} else {
			bc.report(pos, DanglingReturn, fmt.Sprintf(
				"cannot return reference to local stack variable '%s'",
				origin.Comment))
		}
	}

	for _, b := range bc.fn.Blocks {
		for _, ins := range b.Instrs {
			ret, ok := ins.(*ssa.Return)
			if !ok {
				continue
			}
			for _, r := range ret.Results {
				// Only pointer-typed return values can return a reference to stack memory
				if _, ok := r.Type().Underlying().(*types.Pointer); !ok {
					continue
				}
				origin := baseAlloc(r)
				if origin == nil || origin.Block().Parent() != bc.fn || !isStackVarAlloc(origin) {
					continue
				}
				_, isField := r.(*ssa.FieldAddr)
				if ret.Pos().IsValid() {
					reportedOrigins[origin] = true
					emit(ret.Pos(), origin, isField)
				} else {
					unpositioned = append(unpositioned, pendingReturn{origin: origin, isField: isField})
				}
			}
		}
	}

	for _, p := range unpositioned {
		if reportedOrigins[p.origin] {
			continue
		}
		reportedOrigins[p.origin] = true
		emit(bc.fallbackReturnPos(p.origin), p.origin, p.isField)
	}
}

func (bc *borrowChecker) fallbackReturnPos(origin *ssa.Alloc) token.Pos {
	for _, blk := range bc.fn.Blocks {
		for _, ins := range blk.Instrs {
			st, ok := ins.(*ssa.Store)
			if !ok || !st.Pos().IsValid() {
				continue
			}
			if baseAlloc(st.Val) == origin || (st.Addr == origin && st.Pos() != origin.Pos()) {
				return st.Pos()
			}
		}
	}
	if origin != nil && origin.Pos().IsValid() {
		return origin.Pos()
	}
	return bc.fn.Pos()
}

func isStackVarAlloc(a *ssa.Alloc) bool {
	if a == nil {
		return false
	}
	c := a.Comment
	return c != "" && c != "complit" && c != "new" && c != "makeslice" && c != "slicelit"
}

func baseAlloc(v ssa.Value) *ssa.Alloc {
	return baseAllocSeen(v, map[ssa.Value]bool{})
}

func cloneValSet(m map[ssa.Value]bool) map[ssa.Value]bool {
	out := make(map[ssa.Value]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func baseAllocSeen(v ssa.Value, seen map[ssa.Value]bool) *ssa.Alloc {
	for v != nil && !seen[v] && len(seen) < 64 {
		seen[v] = true
		switch u := v.(type) {
		case *ssa.Alloc:
			return u
		case *ssa.Phi:
			var fallback *ssa.Alloc
			for _, e := range u.Edges {
				if a := baseAllocSeen(e, seen); a != nil {
					if isStackVarAlloc(a) {
						return a
					}
					if fallback == nil {
						fallback = a
					}
				}
			}
			return fallback
		case *ssa.FieldAddr:
			v = u.X
		case *ssa.IndexAddr:
			v = u.X
		case *ssa.Slice:
			v = u.X
		case *ssa.ChangeType:
			v = u.X
		case *ssa.Convert:
			v = u.X
		case *ssa.MakeInterface:
			v = u.X
		case *ssa.Call:
			if callee := u.Call.StaticCallee(); callee != nil {
				if len(u.Call.Args) == 1 {
					switch callee.Name() {
					case "BorrowMut", "BorrowMub", "Borrow", "BorrowRef", "BorrowRob", "Move":
						v = u.Call.Args[0]
						continue
					}
				}
				if idx := calleeReturnedParamIndex(callee); idx >= 0 && idx < len(u.Call.Args) {
					v = u.Call.Args[idx]
					continue
				}
			}
			return nil
		case *ssa.UnOp:
			if u.Op == token.MUL {
				if refs := u.X.Referrers(); refs != nil {
					for _, ref := range *refs {
						if st, ok := ref.(*ssa.Store); ok && st.Addr == u.X {
							if a := baseAllocSeen(st.Val, seen); isStackVarAlloc(a) {
								return a
							}
						}
					}
				}
				if subBase := baseAllocSeen(u.X, cloneValSet(seen)); subBase != nil && subBase.Block() != nil && subBase.Block().Parent() != nil {
					fn := subBase.Block().Parent()
					for _, blk := range fn.Blocks {
						for _, ins := range blk.Instrs {
							st, ok := ins.(*ssa.Store)
							if !ok {
								continue
							}
							if sameSubAddr(st.Addr, u.X, subBase, seen) {
								if a := baseAllocSeen(st.Val, seen); isStackVarAlloc(a) {
									return a
								}
							}
						}
					}
				}
				v = u.X
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	return nil
}

func sameSubAddr(a, b ssa.Value, base *ssa.Alloc, seen map[ssa.Value]bool) bool {
	if a == b {
		return true
	}
	switch x := a.(type) {
	case *ssa.FieldAddr:
		if y, ok := b.(*ssa.FieldAddr); ok && x.Field == y.Field {
			return baseAllocSeen(x.X, cloneValSet(seen)) == base
		}
	case *ssa.IndexAddr:
		if _, ok := b.(*ssa.IndexAddr); ok {
			return baseAllocSeen(x.X, cloneValSet(seen)) == base
		}
	}
	return false
}

func calleeReturnedParamIndex(callee *ssa.Function) int {
	if callee == nil || len(callee.Blocks) == 0 || len(callee.Params) == 0 {
		return -1
	}
	var pkg *types.Package
	if callee.Pkg != nil {
		pkg = callee.Pkg.Pkg
	} else if callee.Object() != nil {
		pkg = callee.Object().Pkg()
	}
	if pkg != nil && (pkg.Name() == "cap" || strings.HasSuffix(pkg.Path(), "/cap")) {
		return -1
	}
	if strings.Contains(callee.String(), "/cap.Borrow") || strings.HasPrefix(callee.String(), "cap.Borrow") {
		return -1
	}
	for _, b := range callee.Blocks {
		for _, ins := range b.Instrs {
			ret, ok := ins.(*ssa.Return)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			cur := ret.Results[0]
			for range 16 {
				for i, p := range callee.Params {
					if cur == p {
						return i
					}
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
				case *ssa.Convert:
					cur = x.X
				default:
					cur = nil
				}
				if cur == nil {
					break
				}
			}
		}
	}
	return -1
}

func calleeMutatesParam(callee *ssa.Function, paramIdx int) bool {
	if callee == nil || paramIdx < 0 || paramIdx >= len(callee.Params) || len(callee.Blocks) == 0 {
		return false
	}
	p := callee.Params[paramIdx]
	for _, b := range callee.Blocks {
		for _, ins := range b.Instrs {
			st, ok := ins.(*ssa.Store)
			if !ok {
				continue
			}
			cur := st.Addr
			for range 16 {
				if cur == p {
					return true
				}
				switch x := cur.(type) {
				case *ssa.FieldAddr:
					cur = x.X
				case *ssa.IndexAddr:
					cur = x.X
				case *ssa.ChangeType:
					cur = x.X
				default:
					cur = nil
				}
				if cur == nil {
					break
				}
			}
		}
	}
	return false
}

// Pass D: Loop-Carried Borrow Escape Check (BORROW002)
func (bc *borrowChecker) checkLoopEscapes() {
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return
	}

	ast.Inspect(syntax, func(n ast.Node) bool {
		var loopPos token.Pos
		var loopBody *ast.BlockStmt

		switch loop := n.(type) {
		case *ast.ForStmt:
			loopPos = loop.Pos()
			loopBody = loop.Body
		case *ast.RangeStmt:
			loopPos = loop.Pos()
			loopBody = loop.Body
		default:
			return true
		}

		if loopBody == nil {
			return true
		}

		ast.Inspect(loopBody, func(inner ast.Node) bool {
			assign, ok := inner.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				id := rootIdent(lhs)
				if id == nil {
					continue
				}
				v, _ := bc.pass.TypesInfo.Uses[id].(*types.Var)
				if v == nil {
					v, _ = bc.pass.TypesInfo.Defs[id].(*types.Var)
				}
				if v == nil {
					continue
				}
				// Check if v was defined before the loop statement
				if v.Pos() < loopPos {
					if i >= len(assign.Rhs) {
						continue
					}
					rhs := assign.Rhs[i]
					unwrapped := unparen(rhs)

					// Case 1: RHS is an identifier referencing an active mutable borrow (or alias thereof)
					if rhsId, ok := unwrapped.(*ast.Ident); ok {
						var rhsVar *types.Var
						if v, ok := bc.pass.TypesInfo.Uses[rhsId].(*types.Var); ok {
							rhsVar = v
						} else if v, ok := bc.pass.TypesInfo.Defs[rhsId].(*types.Var); ok {
							rhsVar = v
						}
						for _, brw := range bc.borrows {
							targetVars := bc.borrowTargetVars(brw)
							isBorrowVar := false
							if rhsVar != nil {
								isBorrowVar = targetVars[rhsVar]
							} else if brw.Var != nil && brw.Var.Name() == rhsId.Name {
								isBorrowVar = true
							}
							if brw.Kind == annot.BorrowMut && isBorrowVar {
								bc.report(assign.Pos(), ConflictingBorrow, fmt.Sprintf(
									"cannot borrow '%s' as mutable: loop-carried borrow escapes to outer scope",
									brw.OwnerName))
							}
						}
					}

					// Case 2: RHS is a direct borrow expression (&x /*gosree:mut*/ or cap.BorrowMut(&x))
					candExpr := unwrapped
					if call, ok := candExpr.(*ast.CallExpr); ok && len(call.Args) == 1 && bc.ix.BorrowAt(call.Pos()) != annot.BorrowNone {
						candExpr = unparen(call.Args[0])
					}
					if ue, ok := candExpr.(*ast.UnaryExpr); ok && ue.Op == token.AND {
						isMut := false
						if bc.ix.IsMub(v) {
							isMut = true
						} else if bc.ix.BorrowAt(assign.Pos()) == annot.BorrowMut {
							isMut = true
						} else if bc.ix.BorrowAt(rhs.Pos()) == annot.BorrowMut {
							isMut = true
						} else if bc.ix.BorrowAt(unwrapped.Pos()) == annot.BorrowMut {
							isMut = true
						} else if bc.ix.BorrowAt(ue.OpPos) == annot.BorrowMut {
							isMut = true
						} else {
							line := bc.pass.Fset.Position(assign.Pos()).Line
							for _, brw := range bc.borrows {
								if brw.Line == line && brw.Kind == annot.BorrowMut {
									isMut = true
									break
								}
							}
						}
						if isMut {
							ownerName, _, _ := bc.resolveOwner(rhs)
							bc.report(assign.Pos(), ConflictingBorrow, fmt.Sprintf(
								"cannot borrow '%s' as mutable: loop-carried borrow escapes to outer scope",
								ownerName))
						}
					}
				}
			}
			return true
		})
		return true
	})
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func rootIdent(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.SelectorExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.SliceExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		default:
			return nil
		}
	}
}

func indexIn(ins ssa.Instruction) int {
	if ins == nil || ins.Block() == nil {
		return -1
	}
	for i, x := range ins.Block().Instrs {
		if x == ins {
			return i
		}
	}
	return -1
}
