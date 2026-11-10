package errcheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var (
	errorType  = types.Universe.Lookup("error").Type()
	errorIface = errorType.Underlying().(*types.Interface)
)

func isErrorInterface(t types.Type) bool {
	if t == nil {
		return false
	}
	return types.Identical(types.Unalias(t), errorType)
}

func implementsError(t types.Type) bool {
	if t == nil {
		return false
	}
	return types.Implements(t, errorIface)
}

func isConcreteErrPtr(t types.Type) bool {
	if t == nil || types.IsInterface(t) {
		return false
	}
	if _, ok := t.Underlying().(*types.Pointer); !ok {
		return false
	}
	return types.Implements(t, errorIface)
}

func sigReturnsErrorLast(sig *types.Signature) bool {
	if sig == nil || sig.Results() == nil || sig.Results().Len() == 0 {
		return false
	}
	res := sig.Results()
	return isErrorInterface(res.At(res.Len() - 1).Type())
}

func sigReturnsConcreteErrPtr(sig *types.Signature) bool {
	if sig == nil || sig.Results() == nil || sig.Results().Len() != 1 {
		return false
	}
	return isConcreteErrPtr(sig.Results().At(0).Type())
}

type unhandledCallAST struct {
	pos  token.Pos
	desc string
}

type closeCallAST struct {
	pos      token.Pos
	recvExpr ast.Expr
	recvDesc string
	isDefer  bool
}

type blankedTupleAST struct {
	pos      token.Pos
	callDesc string
}

type shadowErrAST struct {
	pos  token.Pos
	name string
}

type funcASTInfo struct {
	unhandled map[token.Pos]unhandledCallAST // keyed by CallExpr.Lparen
	closes    map[token.Pos]closeCallAST     // keyed by CallExpr.Lparen
	blanked   map[token.Pos]blankedTupleAST  // keyed by CallExpr.Lparen
	shadows   []shadowErrAST
}

func scanFuncAST(pass *analysis.Pass, syntax ast.Node) *funcASTInfo {
	info := &funcASTInfo{
		unhandled: make(map[token.Pos]unhandledCallAST),
		closes:    make(map[token.Pos]closeCallAST),
		blanked:   make(map[token.Pos]blankedTupleAST),
	}
	if syntax == nil {
		return info
	}

	var fnType *ast.FuncType
	var body *ast.BlockStmt
	switch n := syntax.(type) {
	case *ast.FuncDecl:
		fnType, body = n.Type, n.Body
	case *ast.FuncLit:
		fnType, body = n.Type, n.Body
	}
	if body == nil {
		return info
	}

	walkNoNestedClosures(body, func(n ast.Node) {
		switch stmt := n.(type) {
		case *ast.ExprStmt:
			if call, ok := unparenExpr(stmt.X).(*ast.CallExpr); ok {
				classifyDiscardedCall(pass, info, call, stmt.Pos(), false, false)
			}
		case *ast.DeferStmt:
			classifyDiscardedCall(pass, info, stmt.Call, stmt.Pos(), true, false)
		case *ast.GoStmt:
			classifyDiscardedCall(pass, info, stmt.Call, stmt.Pos(), false, true)
		case *ast.AssignStmt:
			checkBlankedTupleAssign(pass, info, stmt)
		}
	})

	scanShadowedErrors(pass, fnType, body, info)
	return info
}

func walkNoNestedClosures(root ast.Node, visit func(ast.Node)) {
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n != root {
			if _, isLit := n.(*ast.FuncLit); isLit {
				return false
			}
		}
		visit(n)
		return true
	})
}

func unparenExpr(e ast.Expr) ast.Expr {
	for e != nil {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
	return nil
}

func isBlankIdent(e ast.Expr) bool {
	id, ok := unparenExpr(e).(*ast.Ident)
	return ok && id.Name == "_"
}

func callReturnsAnyError(pass *analysis.Pass, call *ast.CallExpr) bool {
	tv, ok := pass.TypesInfo.Types[call]
	if !ok || tv.Type == nil {
		return false
	}
	if isErrorInterface(tv.Type) {
		return true
	}
	if tup, ok := tv.Type.(*types.Tuple); ok {
		for i := range tup.Len() {
			if isErrorInterface(tup.At(i).Type()) {
				return true
			}
		}
	}
	return false
}

func isCloseErrMethod(pass *analysis.Pass, call *ast.CallExpr) (*ast.SelectorExpr, bool) {
	if len(call.Args) != 0 {
		return nil, false
	}
	sel, ok := unparenExpr(call.Fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Close" {
		return nil, false
	}
	tv, ok := pass.TypesInfo.Types[call]
	if !ok || !isErrorInterface(tv.Type) {
		return nil, false
	}
	// Ensure sel is a method selection (not a package-qualified function `pkg.Close()`).
	if _, isSel := pass.TypesInfo.Selections[sel]; !isSel {
		return nil, false
	}
	return sel, true
}

func classifyDiscardedCall(pass *analysis.Pass, info *funcASTInfo, call *ast.CallExpr, stmtPos token.Pos, isDefer, isGo bool) {
	if call == nil || !callReturnsAnyError(pass, call) {
		return
	}
	if !isGo {
		if sel, ok := isCloseErrMethod(pass, call); ok {
			info.closes[call.Lparen] = closeCallAST{
				pos:      stmtPos,
				recvExpr: sel.X,
				recvDesc: exprSummary(sel.X),
				isDefer:  isDefer,
			}
			return
		}
	}
	if isBenignErrCall(pass, call) {
		return
	}
	info.unhandled[call.Lparen] = unhandledCallAST{
		pos:  stmtPos,
		desc: describeCallAST(pass, call),
	}
}

func isBenignErrCall(pass *analysis.Pass, call *ast.CallExpr) bool {
	fnObj := calleeFuncObj(pass, call)
	if fnObj == nil || fnObj.Pkg() == nil {
		return false
	}
	pkgPath := fnObj.Pkg().Path()
	name := fnObj.Name()

	// Standard console formatting and infallible in-memory buffer writers.
	if pkgPath == "fmt" {
		switch name {
		case "Print", "Println", "Printf":
			return true
		case "Fprint", "Fprintln", "Fprintf":
			if len(call.Args) > 0 && isInfallibleOrStdStreamWriter(pass, call.Args[0]) {
				return true
			}
		}
	}

	if sel, ok := unparenExpr(call.Fun).(*ast.SelectorExpr); ok {
		if selection, ok := pass.TypesInfo.Selections[sel]; ok {
			recvStr := types.TypeString(selection.Recv(), nil)
			switch recvStr {
			case "*bytes.Buffer", "bytes.Buffer", "*strings.Builder", "strings.Builder":
				switch name {
				case "Write", "WriteString", "WriteByte", "WriteRune", "ReadFrom":
					return true
				}
			}
			// hash.Hash implementations document that Write never returns an error.
			if name == "Write" && isHashWriter(selection.Recv()) {
				return true
			}
		}
	}
	return false
}

func isInfallibleOrStdStreamWriter(pass *analysis.Pass, arg ast.Expr) bool {
	if tv, ok := pass.TypesInfo.Types[arg]; ok && tv.Type != nil {
		ts := types.TypeString(tv.Type, nil)
		if ts == "*bytes.Buffer" || ts == "*strings.Builder" {
			return true
		}
	}
	if sel, ok := unparenExpr(arg).(*ast.SelectorExpr); ok {
		if pkgID, ok := unparenExpr(sel.X).(*ast.Ident); ok {
			if pkgName, ok := pass.TypesInfo.Uses[pkgID].(*types.PkgName); ok {
				p := pkgName.Imported().Path()
				if p == "io" && sel.Sel.Name == "Discard" {
					return true
				}
				if p == "os" && (sel.Sel.Name == "Stdout" || sel.Sel.Name == "Stderr") {
					return true
				}
			}
		}
	}
	return false
}

func isHashWriter(t types.Type) bool {
	// hash.Hash defines Sum([]byte) []byte, Reset(), Size() int, BlockSize() int.
	for _, mName := range []string{"Sum", "Reset", "Size", "BlockSize"} {
		obj, _, _ := types.LookupFieldOrMethod(t, true, nil, mName)
		if _, ok := obj.(*types.Func); !ok {
			return false
		}
	}
	return true
}

func calleeFuncObj(pass *analysis.Pass, call *ast.CallExpr) *types.Func {
	fun := unparenExpr(call.Fun)
	switch x := fun.(type) {
	case *ast.Ident:
		if fn, ok := pass.TypesInfo.Uses[x].(*types.Func); ok {
			return fn
		}
	case *ast.SelectorExpr:
		if sel, ok := pass.TypesInfo.Selections[x]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn
			}
		}
		if fn, ok := pass.TypesInfo.Uses[x.Sel].(*types.Func); ok {
			return fn
		}
	case *ast.IndexExpr:
		if id, ok := unparenExpr(x.X).(*ast.Ident); ok {
			if fn, ok := pass.TypesInfo.Uses[id].(*types.Func); ok {
				return fn
			}
		}
		if sel, ok := unparenExpr(x.X).(*ast.SelectorExpr); ok {
			if fn, ok := pass.TypesInfo.Uses[sel.Sel].(*types.Func); ok {
				return fn
			}
		}
	}
	return nil
}

func describeCallAST(pass *analysis.Pass, call *ast.CallExpr) string {
	if fn := calleeFuncObj(pass, call); fn != nil {
		if fn.Pkg() != nil && fn.Pkg() != pass.Pkg {
			return fn.Pkg().Name() + "." + fn.Name() + "()"
		}
		return fn.Name() + "()"
	}
	return exprSummary(call.Fun) + "()"
}

func exprSummary(e ast.Expr) string {
	e = unparenExpr(e)
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprSummary(x.X) + "." + x.Sel.Name
	case *ast.StarExpr:
		return "*" + exprSummary(x.X)
	case *ast.IndexExpr:
		return exprSummary(x.X) + "[...]"
	default:
		return "expression"
	}
}

func checkBlankedTupleAssign(pass *analysis.Pass, info *funcASTInfo, stmt *ast.AssignStmt) {
	if len(stmt.Rhs) != 1 || len(stmt.Lhs) < 2 {
		return
	}
	call, ok := unparenExpr(stmt.Rhs[0]).(*ast.CallExpr)
	if !ok {
		return
	}
	tv, ok := pass.TypesInfo.Types[call]
	if !ok {
		return
	}
	tup, ok := tv.Type.(*types.Tuple)
	if !ok || tup.Len() != len(stmt.Lhs) || tup.Len() < 2 {
		return
	}
	lastIdx := tup.Len() - 1
	if !isErrorInterface(tup.At(lastIdx).Type()) || !isBlankIdent(stmt.Lhs[lastIdx]) {
		return
	}
	hasBoundCompanion := false
	for i := range lastIdx {
		if !isBlankIdent(stmt.Lhs[i]) {
			hasBoundCompanion = true
			break
		}
	}
	if !hasBoundCompanion {
		return
	}
	info.blanked[call.Lparen] = blankedTupleAST{
		pos:      stmt.Pos(),
		callDesc: describeCallAST(pass, call),
	}
}

type outerErrScope struct {
	v             *types.Var
	declPos       token.Pos
	scopeEnd      token.Pos
	isNamedResult bool
}

func scanShadowedErrors(pass *analysis.Pass, fnType *ast.FuncType, body *ast.BlockStmt, info *funcASTInfo) {
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
				checkStmtShadow(pass, s, x.List[idx+1:], x.End(), nextNested, &outers, body, info)
				walkStmtChildren(s, x.End(), nextNested, walkScope, pass, &outers, body, info)
			}
		}
	}
	walkScope(body, body.End(), false)
}

func walkStmtChildren(
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
			checkIfInitShadow(pass, s, outers, fnBody, info)
		}
		walkScope(s.Body, s.End(), true)
		if s.Else != nil {
			switch el := s.Else.(type) {
			case *ast.BlockStmt:
				walkScope(el, el.End(), true)
			case *ast.IfStmt:
				walkStmtChildren(el, s.End(), true, walkScope, pass, outers, fnBody, info)
			}
		}
	case *ast.ForStmt:
		if s.Init != nil {
			checkStmtShadow(pass, s.Init, nil, s.End(), true, outers, fnBody, info)
		}
		walkScope(s.Body, s.End(), true)
	case *ast.RangeStmt:
		walkScope(s.Body, s.End(), true)
	case *ast.SwitchStmt:
		if s.Init != nil {
			checkStmtShadow(pass, s.Init, nil, s.End(), true, outers, fnBody, info)
		}
		if s.Body != nil {
			for _, cc := range s.Body.List {
				if clause, ok := cc.(*ast.CaseClause); ok {
					clauseEnd := s.Body.End()
					for idx, cs := range clause.Body {
						checkStmtShadow(pass, cs, clause.Body[idx+1:], clauseEnd, true, outers, fnBody, info)
						walkStmtChildren(cs, clauseEnd, true, walkScope, pass, outers, fnBody, info)
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
						checkStmtShadow(pass, cs, clause.Body[idx+1:], clauseEnd, true, outers, fnBody, info)
						walkStmtChildren(cs, clauseEnd, true, walkScope, pass, outers, fnBody, info)
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
						checkStmtShadow(pass, cs, clause.Body[idx+1:], clauseEnd, true, outers, fnBody, info)
						walkStmtChildren(cs, clauseEnd, true, walkScope, pass, outers, fnBody, info)
					}
				}
			}
		}
	case *ast.LabeledStmt:
		walkStmtChildren(s.Stmt, parentEnd, inNested, walkScope, pass, outers, fnBody, info)
	}
}

func checkIfInitShadow(
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
		if matchesShadowedOuter(pass, v, id.Pos(), ifStmt.End(), *outers, fnBody) {
			info.shadows = append(info.shadows, shadowErrAST{pos: assign.Pos(), name: v.Name()})
		}
		*outers = append(*outers, outerErrScope{v: v, declPos: id.Pos(), scopeEnd: ifStmt.End()})
	}
}

func checkStmtShadow(
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
						if matchesShadowedOuter(pass, v, id.Pos(), blockEnd, *outers, fnBody) {
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
				if matchesShadowedOuter(pass, v, id.Pos(), blockEnd, *outers, fnBody) {
					info.shadows = append(info.shadows, shadowErrAST{pos: s.Pos(), name: v.Name()})
				}
			}
			*outers = append(*outers, outerErrScope{v: v, declPos: id.Pos(), scopeEnd: blockEnd})
		}
	}
}

func matchesShadowedOuter(
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
		if outerReadAfterWithoutReassign(pass, out, innerScopeEnd, fnBody) {
			return true
		}
	}
	return false
}

func outerReadAfterWithoutReassign(
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
							if exprReadsVar(pass, rhs, out.v) {
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
			if pass.TypesInfo.Uses[x] == out.v && !isLHSOfPlainAssign(fnBody, x) {
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

func exprReadsVar(pass *analysis.Pass, e ast.Expr, target *types.Var) bool {
	found := false
	walkNoNestedClosures(e, func(n ast.Node) {
		if id, ok := n.(*ast.Ident); ok && pass.TypesInfo.Uses[id] == target {
			found = true
		}
	})
	return found
}

func isLHSOfPlainAssign(root ast.Node, target *ast.Ident) bool {
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

func restGuardsNonNilExit(pass *analysis.Pass, stmts []ast.Stmt, errVar *types.Var) bool {
	for _, s := range stmts {
		if ifS, ok := s.(*ast.IfStmt); ok && isGuardedTerminatingIf(pass, ifS, errVar) {
			return true
		}
	}
	return false
}

func isGuardedTerminatingIf(pass *analysis.Pass, ifS *ast.IfStmt, errVar *types.Var) bool {
	if ifS == nil || ifS.Body == nil {
		return false
	}
	bin, ok := unparenExpr(ifS.Cond).(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	var varExpr, nilExpr ast.Expr
	if isNilIdent(pass, bin.Y) {
		varExpr, nilExpr = bin.X, bin.Y
	} else if isNilIdent(pass, bin.X) {
		varExpr, nilExpr = bin.Y, bin.X
	}
	_ = nilExpr
	id, ok := unparenExpr(varExpr).(*ast.Ident)
	if !ok || pass.TypesInfo.Uses[id] != errVar {
		return false
	}
	return blockAlwaysExitsScope(ifS.Body)
}

func isNilIdent(pass *analysis.Pass, e ast.Expr) bool {
	id, ok := unparenExpr(e).(*ast.Ident)
	if !ok || id.Name != "nil" {
		return false
	}
	_, isNil := pass.TypesInfo.Uses[id].(*types.Nil)
	return isNil
}

func blockAlwaysExitsScope(b *ast.BlockStmt) bool {
	if b == nil || len(b.List) == 0 {
		return false
	}
	last := b.List[len(b.List)-1]
	switch s := last.(type) {
	case *ast.ReturnStmt, *ast.BranchStmt:
		return true
	case *ast.ExprStmt:
		if call, ok := unparenExpr(s.X).(*ast.CallExpr); ok {
			if id, ok := unparenExpr(call.Fun).(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
			// Check for Fatal/FailNow/os.Exit style calls syntactically.
			txt := fmt.Sprintf("%s", exprSummary(call.Fun))
			if strings.HasSuffix(txt, ".Fatal") || strings.HasSuffix(txt, ".Fatalf") ||
				strings.HasSuffix(txt, ".Fatalln") || strings.HasSuffix(txt, ".FailNow") ||
				txt == "os.Exit" || txt == "runtime.Goexit" {
				return true
			}
		}
	}
	return false
}
