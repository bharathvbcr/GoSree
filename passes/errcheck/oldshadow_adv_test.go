package errcheck

// Pre-9161d81 shadow scan copied verbatim with renamed identifiers for the differential test.

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

func old_scanShadowedErrors(pass *analysis.Pass, fnType *ast.FuncType, body *ast.BlockStmt, info *funcASTInfo) {
	var outers []outerErrScope

	if fnType != nil && fnType.Results != nil {
		for _, field := range fnType.Results.List {
			for _, name := range field.Names {
				if name.Name == "_" {
					continue
				}
				if v, ok := pass.TypesInfo.Defs[name].(*types.Var); ok && isErrorInterface(v.Type()) {
					outers = append(outers, outerErrScope{
						v:             v,
						declPos:       name.Pos(),
						scopeEnd:      body.End(),
						isNamedResult: true,
					})
				}
			}
		}
	}

	// Collect local error declarations and their enclosing block extents.
	var walkScope func(n ast.Node, scopeEnd token.Pos, inNested bool)
	walkScope = func(n ast.Node, scopeEnd token.Pos, inNested bool) {
		if n == nil {
			return
		}
		switch x := n.(type) {
		case *ast.FuncLit:
			return
		case *ast.BlockStmt:
			nextNested := inNested || x != body
			for idx, s := range x.List {
				old_checkStmtShadow(pass, s, x.List[idx+1:], x.End(), nextNested, &outers, body, info)
				old_walkStmtChildren(s, x.End(), nextNested, walkScope, pass, &outers, body, info)
			}
		}
	}
	walkScope(body, body.End(), false)
}

func old_walkStmtChildren(
	stmt ast.Stmt,
	parentEnd token.Pos,
	inNested bool,
	walkScope func(ast.Node, token.Pos, bool),
	pass *analysis.Pass,
	outers *[]outerErrScope,
	fnBody *ast.BlockStmt,
	info *funcASTInfo,
) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		walkScope(s, s.End(), true)
	case *ast.IfStmt:
		if s.Init != nil {
			old_checkIfInitShadow(pass, s, outers, fnBody, info)
		}
		walkScope(s.Body, s.End(), true)
		if s.Else != nil {
			switch el := s.Else.(type) {
			case *ast.BlockStmt:
				walkScope(el, el.End(), true)
			case *ast.IfStmt:
				old_walkStmtChildren(el, s.End(), true, walkScope, pass, outers, fnBody, info)
			}
		}
	case *ast.ForStmt:
		if s.Init != nil {
			old_checkStmtShadow(pass, s.Init, nil, s.End(), true, outers, fnBody, info)
		}
		walkScope(s.Body, s.End(), true)
	case *ast.RangeStmt:
		walkScope(s.Body, s.End(), true)
	case *ast.SwitchStmt:
		if s.Init != nil {
			old_checkStmtShadow(pass, s.Init, nil, s.End(), true, outers, fnBody, info)
		}
		if s.Body != nil {
			for _, cc := range s.Body.List {
				if clause, ok := cc.(*ast.CaseClause); ok {
					clauseEnd := s.Body.End()
					for idx, cs := range clause.Body {
						old_checkStmtShadow(pass, cs, clause.Body[idx+1:], clauseEnd, true, outers, fnBody, info)
						old_walkStmtChildren(cs, clauseEnd, true, walkScope, pass, outers, fnBody, info)
					}
				}
			}
		}
	case *ast.TypeSwitchStmt:
		if s.Body != nil {
			for _, cc := range s.Body.List {
				if clause, ok := cc.(*ast.CaseClause); ok {
					clauseEnd := s.Body.End()
					for idx, cs := range clause.Body {
						old_checkStmtShadow(pass, cs, clause.Body[idx+1:], clauseEnd, true, outers, fnBody, info)
						old_walkStmtChildren(cs, clauseEnd, true, walkScope, pass, outers, fnBody, info)
					}
				}
			}
		}
	case *ast.SelectStmt:
		if s.Body != nil {
			for _, cc := range s.Body.List {
				if clause, ok := cc.(*ast.CommClause); ok {
					clauseEnd := s.Body.End()
					for idx, cs := range clause.Body {
						old_checkStmtShadow(pass, cs, clause.Body[idx+1:], clauseEnd, true, outers, fnBody, info)
						old_walkStmtChildren(cs, clauseEnd, true, walkScope, pass, outers, fnBody, info)
					}
				}
			}
		}
	case *ast.LabeledStmt:
		old_walkStmtChildren(s.Stmt, parentEnd, inNested, walkScope, pass, outers, fnBody, info)
	}
}

func old_checkIfInitShadow(
	pass *analysis.Pass,
	ifStmt *ast.IfStmt,
	outers *[]outerErrScope,
	fnBody *ast.BlockStmt,
	info *funcASTInfo,
) {
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE {
		return
	}
	for _, lhs := range assign.Lhs {
		id, ok := unparenExpr(lhs).(*ast.Ident)
		if !ok || id.Name == "_" {
			continue
		}
		v, ok := pass.TypesInfo.Defs[id].(*types.Var)
		if !ok || !isErrorInterface(v.Type()) {
			continue
		}
		// If `if ..., err := ...; err != nil { return/panic/... }` with no fallthrough on err != nil,
		// the inner error cannot silently leak past the if statement.
		if isGuardedTerminatingIf(pass, ifStmt, v) {
			*outers = append(*outers, outerErrScope{v: v, declPos: id.Pos(), scopeEnd: ifStmt.End()})
			continue
		}
		if old_matchesShadowedOuter(pass, v, id.Pos(), ifStmt.End(), *outers, fnBody) {
			info.shadows = append(info.shadows, shadowErrAST{pos: assign.Pos(), name: v.Name()})
		}
		*outers = append(*outers, outerErrScope{v: v, declPos: id.Pos(), scopeEnd: ifStmt.End()})
	}
}

func old_checkStmtShadow(
	pass *analysis.Pass,
	stmt ast.Stmt,
	restInBlock []ast.Stmt,
	blockEnd token.Pos,
	inNested bool,
	outers *[]outerErrScope,
	fnBody *ast.BlockStmt,
	info *funcASTInfo,
) {
	switch s := stmt.(type) {
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, id := range vs.Names {
				if id.Name == "_" {
					continue
				}
				if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && isErrorInterface(v.Type()) {
					if inNested && len(vs.Values) > 0 && !restGuardsNonNilExit(pass, restInBlock, v) {
						if old_matchesShadowedOuter(pass, v, id.Pos(), blockEnd, *outers, fnBody) {
							info.shadows = append(info.shadows, shadowErrAST{pos: vs.Pos(), name: v.Name()})
						}
					}
					*outers = append(*outers, outerErrScope{v: v, declPos: id.Pos(), scopeEnd: blockEnd})
				}
			}
		}
	case *ast.AssignStmt:
		if s.Tok != token.DEFINE {
			return
		}
		for _, lhs := range s.Lhs {
			id, ok := unparenExpr(lhs).(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			v, ok := pass.TypesInfo.Defs[id].(*types.Var)
			if !ok || !isErrorInterface(v.Type()) {
				continue
			}
			if inNested && !restGuardsNonNilExit(pass, restInBlock, v) {
				if old_matchesShadowedOuter(pass, v, id.Pos(), blockEnd, *outers, fnBody) {
					info.shadows = append(info.shadows, shadowErrAST{pos: s.Pos(), name: v.Name()})
				}
			}
			*outers = append(*outers, outerErrScope{v: v, declPos: id.Pos(), scopeEnd: blockEnd})
		}
	}
}

func old_matchesShadowedOuter(
	pass *analysis.Pass,
	inner *types.Var,
	innerPos, innerScopeEnd token.Pos,
	outers []outerErrScope,
	fnBody *ast.BlockStmt,
) bool {
	for _, out := range outers {
		if out.v == inner || out.v.Name() != inner.Name() {
			continue
		}
		if innerPos <= out.declPos || out.scopeEnd <= innerScopeEnd {
			continue
		}
		if old_outerReadAfterWithoutReassign(pass, out, innerScopeEnd, fnBody) {
			return true
		}
	}
	return false
}

func old_outerReadAfterWithoutReassign(
	pass *analysis.Pass,
	out outerErrScope,
	afterPos token.Pos,
	fnBody *ast.BlockStmt,
) bool {
	// Find earliest read of out.v after afterPos vs earliest unambiguous top-level overwrite of out.v.
	earliestOverwrite := token.NoPos
	for _, topStmt := range fnBody.List {
		if topStmt.Pos() <= afterPos {
			continue
		}
		if assign, ok := topStmt.(*ast.AssignStmt); ok {
			for _, lhs := range assign.Lhs {
				if id, ok := unparenExpr(lhs).(*ast.Ident); ok {
					if pass.TypesInfo.Uses[id] == out.v || pass.TypesInfo.Defs[id] == out.v {
						// Check if RHS itself reads out.v (e.g., err = wrap(err)).
						rhsReads := false
						for _, rhs := range assign.Rhs {
							if old_exprReadsVar(pass, rhs, out.v) {
								rhsReads = true
								break
							}
						}
						if !rhsReads {
							earliestOverwrite = assign.Pos()
						}
						break
					}
				}
			}
		}
		if earliestOverwrite.IsValid() {
			break
		}
	}

	readAfter := false
	walkNoNestedClosures(fnBody, func(n ast.Node) {
		if readAfter {
			return
		}
		switch x := n.(type) {
		case *ast.Ident:
			if x.Pos() <= afterPos {
				return
			}
			if earliestOverwrite.IsValid() && x.Pos() >= earliestOverwrite {
				return
			}
			if pass.TypesInfo.Uses[x] == out.v && !old_isLHSOfPlainAssign(fnBody, x) {
				readAfter = true
			}
		case *ast.ReturnStmt:
			if out.isNamedResult && len(x.Results) == 0 && x.Pos() > afterPos {
				if !earliestOverwrite.IsValid() || x.Pos() < earliestOverwrite {
					readAfter = true
				}
			}
		}
	})
	return readAfter
}

func old_exprReadsVar(pass *analysis.Pass, e ast.Expr, target *types.Var) bool {
	found := false
	walkNoNestedClosures(e, func(n ast.Node) {
		if id, ok := n.(*ast.Ident); ok && pass.TypesInfo.Uses[id] == target {
			found = true
		}
	})
	return found
}

func old_isLHSOfPlainAssign(root ast.Node, target *ast.Ident) bool {
	isLHS := false
	walkNoNestedClosures(root, func(n ast.Node) {
		if as, ok := n.(*ast.AssignStmt); ok && (as.Tok == token.ASSIGN || as.Tok == token.DEFINE) {
			for _, lhs := range as.Lhs {
				if unparenExpr(lhs) == target {
					isLHS = true
				}
			}
		}
	})
	return isLHS
}
