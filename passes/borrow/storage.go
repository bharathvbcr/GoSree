package borrow

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
)

// StorageViolation is the diagnostic code for second-class borrow storage violations.
const StorageViolation = "BORROW001"

type storageChecker struct {
	pass        *analysis.Pass
	ix          *annot.Index
	ssa         *buildssa.SSA
	reports     map[reportKey]bool
	lineReports map[reportLineKey]bool
	// cur is the function being checked. Its checks run on the syntax and again on the SSA, and an
	// instruction the source does not spell out (the boxing in `var i any = b`) has no position:
	// the SSA check's finding for it is the syntax check's finding again, with nowhere to report
	// it. Those wait in pending until every function is checked. One whose function has a finding
	// of the same rule with a position (located) is that finding again and is left out; any other
	// is reported at its function (flushPending). It used to be dropped either way.
	cur     *ssa.Function
	located []locatedFinding
	pending []pendingFinding
}

type locatedFinding struct {
	pos token.Pos
	cat string
}

type pendingFinding struct {
	fn       *ssa.Function
	cat, msg string
}

// reportKey and reportLineKey tell findings apart. msg is set only for a finding that had no position
// of its own (locate): where it is reported does not tell two of those apart, so their messages do.
type reportKey struct {
	pos token.Pos
	cat string
	msg string
}

type reportLineKey struct {
	filename string
	line     int
	cat      string
	msg      string
}

// locate returns where a finding is reported and what it says. A finding with no position used to be
// dropped, which is how a defect comes to read as clean. It is reported at the function it is in
// (fn, where the caller has one), else at the package clause, and its message says so; located is
// false then.
func locate(pass *analysis.Pass, pos token.Pos, fn *ssa.Function, msg string) (at token.Pos, text string, located bool) {
	if pos.IsValid() {
		return pos, msg, true
	}
	switch {
	case fn != nil && fn.Pos().IsValid():
		return fn.Pos(), msg + "; the code this is about has no source position, so it is reported at the function it is in", false
	case len(pass.Files) > 0:
		return pass.Files[0].Name.Pos(), msg + "; the code this is about has no source position, so it is reported at the package clause", false
	}
	return pos, msg, false
}

// CheckStorage performs both AST declaration checking and SSA escape analysis for second-class borrows.
func CheckStorage(pass *analysis.Pass, ix *annot.Index, ssaProg *buildssa.SSA) {
	sc := &storageChecker{
		pass:        pass,
		ix:          ix,
		ssa:         ssaProg,
		reports:     make(map[reportKey]bool),
		lineReports: make(map[reportLineKey]bool),
	}

	// Phase 1: Static AST declaration checks
	sc.checkStructDeclarations()
	sc.checkGlobalDeclarations()

	// Phase 2: Function-level AST and SSA flow/escape checks
	for _, fn := range allPackageFuncs(sc.ssa) {
		if len(fn.Blocks) == 0 {
			continue
		}
		sc.cur = fn
		sc.checkFunctionStorage(fn)
		sc.cur = nil
	}
	sc.flushPending()
}

// flushPending reports the findings that had no position and that no finding with a position
// accounts for: none of the same rule lies inside the function they were found in.
func (sc *storageChecker) flushPending() {
	pending := sc.pending
	sc.pending = nil
	for _, p := range pending {
		if syn := p.fn.Syntax(); syn != nil && slices.ContainsFunc(sc.located, func(l locatedFinding) bool {
			return l.cat == p.cat && syn.Pos() <= l.pos && l.pos <= syn.End()
		}) {
			continue
		}
		pos, msg, _ := locate(sc.pass, token.NoPos, p.fn, p.msg)
		sc.reportAt(pos, p.cat, msg, false)
	}
}

func allPackageFuncs(ssaProg *buildssa.SSA) []*ssa.Function {
	out := make([]*ssa.Function, 0, len(ssaProg.SrcFuncs)+4)
	seen := make(map[*ssa.Function]bool, len(ssaProg.SrcFuncs)+4)
	var add func(fn *ssa.Function)
	add = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		out = append(out, fn)
		for _, anon := range fn.AnonFuncs {
			add(anon)
		}
	}
	for _, fn := range ssaProg.SrcFuncs {
		add(fn)
	}
	if ssaProg.Pkg != nil {
		if pkgInit := ssaProg.Pkg.Func("init"); pkgInit != nil {
			for _, anon := range pkgInit.AnonFuncs {
				add(anon)
			}
		}
	}
	return out
}

func (sc *storageChecker) report(pos token.Pos, cat, msg string) {
	if !pos.IsValid() && sc.cur != nil {
		sc.pending = append(sc.pending, pendingFinding{fn: sc.cur, cat: cat, msg: msg})
		return
	}
	pos, msg, located := locate(sc.pass, pos, nil, msg)
	sc.reportAt(pos, cat, msg, located)
}

// reportAt reports a finding at pos; located says pos is the finding's own position (see locate). A
// finding a //gosree:unsafe mutes still counts as one with a position: what repeats it is not
// reported in its place.
func (sc *storageChecker) reportAt(pos token.Pos, cat, msg string, located bool) {
	if located {
		sc.located = append(sc.located, locatedFinding{pos: pos, cat: cat})
	}
	if sc.ix.Mute(pos) {
		return
	}
	p := sc.pass.Fset.Position(pos)
	lk := reportLineKey{filename: p.Filename, line: p.Line, cat: cat}
	if !located {
		lk.msg = msg
	}
	if sc.lineReports[lk] {
		return
	}
	sc.lineReports[lk] = true
	sc.pass.Report(analysis.Diagnostic{
		Pos:      pos,
		Category: cat,
		Message:  cat + ": " + msg,
	})
}

// Phase 1: AST Checkers

func isBorrowDirective(text string) (string, bool) {
	text = strings.TrimSpace(text)
	// Block comments: /*gosree:mut*/
	if strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") {
		inner := strings.TrimSpace(text[2 : len(text)-2])
		if strings.HasPrefix(inner, "gosree:") {
			return checkVerb(strings.TrimPrefix(inner, "gosree:"))
		}
	}
	// Line comments: //gosree:...
	if strings.HasPrefix(text, "//gosree:") {
		verb, _, _ := strings.Cut(strings.TrimPrefix(text, "//gosree:"), " ")
		return checkVerb(verb)
	}
	return "", false
}

func checkVerb(verb string) (string, bool) {
	switch verb {
	case "mut", "mub":
		return "mut", true
	case "rob", "ref":
		return "rob", true
	}
	return "", false
}

func commentGroupHasBorrow(cg *ast.CommentGroup) (string, bool) {
	if cg == nil {
		return "", false
	}
	for _, c := range cg.List {
		if k, ok := isBorrowDirective(c.Text); ok {
			return k, true
		}
	}
	return "", false
}

func isBorrowTypeExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.IndexExpr: // cap.Mut[T] or Mut[T]
		return isBorrowTypeName(e.X)
	case *ast.IndexListExpr:
		return isBorrowTypeName(e.X)
	case *ast.SelectorExpr:
		return isBorrowIdent(e.Sel.Name)
	case *ast.Ident:
		return isBorrowIdent(e.Name)
	}
	return false
}

func isBorrowTypeName(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return isBorrowIdent(e.Name)
	case *ast.SelectorExpr:
		return isBorrowIdent(e.Sel.Name)
	}
	return false
}

func isBorrowIdent(name string) bool {
	switch name {
	case "Mut", "Mub", "Ref", "Rob":
		return true
	}
	return false
}

func (sc *storageChecker) checkStructDeclarations() {
	for _, file := range sc.pass.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, f := range st.Fields.List {
					_, hasDocBorrow := commentGroupHasBorrow(f.Doc)
					_, hasLineBorrow := commentGroupHasBorrow(f.Comment)
					isMarker := isBorrowTypeExpr(f.Type)

					if hasDocBorrow || hasLineBorrow || isMarker {
						for _, id := range f.Names {
							sc.report(id.Pos(), StorageViolation, fmt.Sprintf(
								"struct field '%s' cannot have second-class borrow type: borrows are second-class and cannot be stored in struct fields",
								id.Name))
						}
						if len(f.Names) == 0 {
							sc.report(f.Pos(), StorageViolation,
								"embedded struct field cannot have second-class borrow type: borrows are second-class and cannot be stored in struct fields")
						}
					}
				}
			}
		}
	}
}

func (sc *storageChecker) checkGlobalDeclarations() {
	for _, file := range sc.pass.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			_, gdHasBorrow := commentGroupHasBorrow(gd.Doc)
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				_, hasDocBorrow := commentGroupHasBorrow(vs.Doc)
				_, hasLineBorrow := commentGroupHasBorrow(vs.Comment)
				isMarker := isBorrowTypeExpr(vs.Type)

				if hasDocBorrow || hasLineBorrow || gdHasBorrow || isMarker {
					for _, id := range vs.Names {
						sc.report(id.Pos(), StorageViolation, fmt.Sprintf(
							"package variable '%s' cannot store second-class borrow",
							id.Name))
					}
				}
			}
		}
	}
}

// canHoldBorrow returns true if a type can be or can hold a second-class borrow.
// Scalar primitives (int, bool, float, string) return false.
func canHoldBorrow(t types.Type) bool {
	return canHoldBorrowSeen(t, map[types.Type]bool{})
}

func canHoldBorrowSeen(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	t = types.Unalias(t)
	if seen[t] {
		return false
	}
	seen[t] = true
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return false
	case *types.Pointer:
		return true
	case *types.Interface:
		return true
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if canHoldBorrowSeen(u.Field(i).Type(), seen) {
				return true
			}
		}
		return false
	case *types.Array:
		return canHoldBorrowSeen(u.Elem(), seen)
	case *types.Slice:
		return canHoldBorrowSeen(u.Elem(), seen)
	case *types.Map:
		return canHoldBorrowSeen(u.Key(), seen) || canHoldBorrowSeen(u.Elem(), seen)
	case *types.Chan:
		return canHoldBorrowSeen(u.Elem(), seen)
	case *types.Signature:
		return true
	}
	return false
}

func (sc *storageChecker) isBorrowExpr(expr ast.Expr, isBorrowVar map[*types.Var]bool) bool {
	if expr == nil {
		return false
	}
	expr = unparen(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		if v, ok := sc.pass.TypesInfo.Uses[e].(*types.Var); ok && isBorrowVar[v] {
			return true
		}
		if v, ok := sc.pass.TypesInfo.Defs[e].(*types.Var); ok && isBorrowVar[v] {
			return true
		}
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			if sc.ix.BorrowAt(e.Pos()) != annot.BorrowNone || sc.ix.BorrowAt(e.OpPos) != annot.BorrowNone {
				return true
			}
			if id, ok := unparen(e.X).(*ast.Ident); ok {
				if v, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok && isBorrowVar[v] {
					return true
				}
				if v, ok := sc.pass.TypesInfo.Defs[id].(*types.Var); ok && isBorrowVar[v] {
					return true
				}
			}
		}
	case *ast.CallExpr:
		if sc.ix.BorrowAt(e.Pos()) != annot.BorrowNone {
			return true
		}
		if funId, ok := unparen(e.Fun).(*ast.Ident); ok {
			if fnObj, ok := sc.pass.TypesInfo.Uses[funId].(*types.Func); ok && sc.ssa != nil && sc.ssa.Pkg != nil && sc.ssa.Pkg.Prog != nil {
				if callee := sc.ssa.Pkg.Prog.FuncValue(fnObj); callee != nil && len(callee.Blocks) > 0 {
					for i, arg := range e.Args {
						if calleeReturnsParam(callee, i) && sc.isBorrowExpr(arg, isBorrowVar) {
							return true
						}
					}
				}
			}
		}
	case *ast.FuncLit:
		capturesBorrow := false
		if e.Body != nil {
			ast.Inspect(e.Body, func(n ast.Node) bool {
				if capturesBorrow {
					return false
				}
				if id, ok := n.(*ast.Ident); ok {
					if v, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok && isBorrowVar[v] {
						capturesBorrow = true
						return false
					}
				}
				return true
			})
		}
		return capturesBorrow
	case *ast.TypeAssertExpr:
		return sc.isBorrowExpr(e.X, isBorrowVar)
	}
	return false
}

// findBorrowInExpr recursively inspects an AST expression for borrows or structs holding borrows.
// Returns the name of the struct field holding the borrow, or the borrow variable name.
func (sc *storageChecker) findBorrowInExpr(
	expr ast.Expr,
	isBorrowVar map[*types.Var]bool,
	structVarHoldsBorrow map[*types.Var]string,
) string {
	if expr == nil {
		return ""
	}
	expr = unparen(expr)
	if ue, ok := expr.(*ast.UnaryExpr); ok && ue.Op == token.AND {
		expr = unparen(ue.X)
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if v, ok := sc.pass.TypesInfo.Uses[e].(*types.Var); ok {
			if fld, ok := structVarHoldsBorrow[v]; ok {
				return fld
			}
			if isBorrowVar[v] {
				return e.Name
			}
		}
		if v, ok := sc.pass.TypesInfo.Defs[e].(*types.Var); ok {
			if fld, ok := structVarHoldsBorrow[v]; ok {
				return fld
			}
			if isBorrowVar[v] {
				return e.Name
			}
		}
	case *ast.SelectorExpr:
		if id, ok := unparen(e.X).(*ast.Ident); ok {
			var v *types.Var
			if tv, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok {
				v = tv
			} else if tv, ok := sc.pass.TypesInfo.Defs[id].(*types.Var); ok {
				v = tv
			}
			if v != nil {
				if fld, ok := structVarHoldsBorrow[v]; ok && fld == e.Sel.Name {
					return fld
				}
			}
		}
	case *ast.TypeAssertExpr:
		return sc.findBorrowInExpr(e.X, isBorrowVar, structVarHoldsBorrow)
	case *ast.FuncLit:
		var foundFld string
		if e.Body != nil {
			ast.Inspect(e.Body, func(n ast.Node) bool {
				if foundFld != "" {
					return false
				}
				if id, ok := n.(*ast.Ident); ok {
					if v, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok {
						if fld, ok := structVarHoldsBorrow[v]; ok {
							foundFld = fld
							return false
						}
					}
				}
				return true
			})
		}
		return foundFld
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			valExpr := elt
			fldName := ""
			if kve, ok := elt.(*ast.KeyValueExpr); ok {
				valExpr = kve.Value
				if fldId, ok := unparen(kve.Key).(*ast.Ident); ok {
					fldName = fldId.Name
				}
			}
			if f := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow); f != "" {
				if fldName != "" {
					if sc.isBorrowExpr(valExpr, isBorrowVar) {
						return fldName
					}
					return f
				}
				return f
			}
		}
	}
	return ""
}

// resolveInstructionPos returns a valid token.Pos for an SSA instruction,
// falling back to call referrers (e.g. function arguments) or instruction pos.
func resolveInstructionPos(ins ssa.Instruction, fn *ssa.Function) token.Pos {
	pos := ins.Pos()
	if pos.IsValid() {
		return pos
	}
	if val, ok := ins.(ssa.Value); ok {
		if refs := val.Referrers(); refs != nil {
			for _, ref := range *refs {
				if call, ok := ref.(*ssa.Call); ok && call.Pos().IsValid() {
					return call.Pos()
				}
				if store, ok := ref.(*ssa.Store); ok && store.Pos().IsValid() {
					return store.Pos()
				}
			}
		}
	}
	return token.NoPos
}

func (sc *storageChecker) varEscapes(v *types.Var, syntax ast.Node) bool {
	if v == nil || syntax == nil {
		return false
	}
	escapes := false
	ast.Inspect(syntax, func(n ast.Node) bool {
		if escapes {
			return false
		}
		switch stmt := n.(type) {
		case *ast.ReturnStmt:
			for _, r := range stmt.Results {
				if sc.exprUsesVar(r, v) {
					escapes = true
					return false
				}
			}
		case *ast.SendStmt:
			if sc.exprUsesVar(stmt.Value, v) {
				escapes = true
				return false
			}
		case *ast.GoStmt:
			for _, arg := range stmt.Call.Args {
				if sc.exprUsesVar(arg, v) {
					escapes = true
					return false
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				if id, ok := unparen(lhs).(*ast.Ident); ok {
					if gv, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok && sc.isPackageGlobal(gv) {
						if i < len(stmt.Rhs) && sc.exprUsesVar(stmt.Rhs[i], v) {
							escapes = true
							return false
						}
					}
				}
			}
		}
		return true
	})
	return escapes
}

func (sc *storageChecker) exprUsesVar(expr ast.Expr, target *types.Var) bool {
	if expr == nil || target == nil {
		return false
	}
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok {
			if uv, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok && uv == target {
				found = true
				return false
			}
			if dv, ok := sc.pass.TypesInfo.Defs[id].(*types.Var); ok && dv == target {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// Phase 2: Function Storage & Escape Checking

func (sc *storageChecker) checkFunctionStorage(fn *ssa.Function) {
	syntax := fn.Syntax()
	if syntax == nil {
		return
	}

	// Track which variable each composite literal is assigned to (if any)
	litAssignedVar := make(map[*ast.CompositeLit]*types.Var)
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range s.Rhs {
				if cl, ok := unparen(rhs).(*ast.CompositeLit); ok && i < len(s.Lhs) {
					if id, ok := unparen(s.Lhs[i]).(*ast.Ident); ok {
						if tv, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok {
							litAssignedVar[cl] = tv
						} else if tv, ok := sc.pass.TypesInfo.Defs[id].(*types.Var); ok {
							litAssignedVar[cl] = tv
						}
					}
				}
			}
		case *ast.ValueSpec:
			for i, val := range s.Values {
				if cl, ok := unparen(val).(*ast.CompositeLit); ok && i < len(s.Names) {
					if tv, ok := sc.pass.TypesInfo.Defs[s.Names[i]].(*types.Var); ok {
						litAssignedVar[cl] = tv
					}
				}
			}
		}
		return true
	})

	// Track lines of composite literals whose assigned variable escapes later
	escapingLitLines := make(map[int]bool)
	for cl, v := range litAssignedVar {
		if !sc.isPackageGlobal(v) && sc.varEscapes(v, syntax) {
			escapingLitLines[sc.pass.Fset.Position(cl.Pos()).Line] = true
		}
	}
	escapingAssignLines := make(map[int]bool)

	// 1. AST-level tracking of borrow variables and structs holding borrows
	isBorrowVar := make(map[*types.Var]bool)
	structVarHoldsBorrow := make(map[*types.Var]string) // var -> field name
	ptrToGlobal := make(map[*types.Var]string)          // var -> global variable name
	heapPtrs := make(map[*types.Var]bool)               // var -> true if allocated via new()

	// Seed with parameters
	for _, p := range fn.Params {
		if v, ok := p.Object().(*types.Var); ok {
			if sc.ix.IsMub(v) || sc.ix.IsRob(v) {
				isBorrowVar[v] = true
			}
		}
	}

	// Seed with annotated local variables belonging to this function
	for v := range sc.ix.MubVars {
		if v.Pos().IsValid() && v.Pos() >= syntax.Pos() && v.Pos() <= syntax.End() {
			isBorrowVar[v] = true
		}
	}
	for v := range sc.ix.RobVars {
		if v.Pos().IsValid() && v.Pos() >= syntax.Pos() && v.Pos() <= syntax.End() {
			isBorrowVar[v] = true
		}
	}

	// AST walk to find borrows and struct composite literals and check local escapes
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				// A. LHS is pointer indirection (*p = b, *h = b)
				if star, ok := unparen(lhs).(*ast.StarExpr); ok {
					if i < len(stmt.Rhs) {
						valExpr := stmt.Rhs[i]
						isBorrow := sc.isBorrowExpr(valExpr, isBorrowVar)
						fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow)

						if isBorrow || fld != "" {
							baseId, _ := unparen(star.X).(*ast.Ident)
							var baseVar *types.Var
							if baseId != nil {
								baseVar, _ = sc.pass.TypesInfo.Uses[baseId].(*types.Var)
								if baseVar == nil {
									baseVar, _ = sc.pass.TypesInfo.Defs[baseId].(*types.Var)
								}
							}

							if baseVar != nil && ptrToGlobal[baseVar] != "" {
								sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
									"package variable '%s' cannot store second-class borrow", ptrToGlobal[baseVar]))
							} else if baseVar != nil && heapPtrs[baseVar] {
								sc.report(stmt.Pos(), StorageViolation,
									"cannot store second-class borrow to heap or non-stack memory")
							} else if isBorrow {
								sc.report(stmt.Pos(), StorageViolation,
									"cannot store second-class borrow to heap or non-stack memory")
							} else if fld != "" {
								sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", fld))
							}
						}
					}
					continue
				}

				// B. LHS is field selector (h.p = b, t.p = b)
				if sel, ok := unparen(lhs).(*ast.SelectorExpr); ok {
					if i < len(stmt.Rhs) {
						valExpr := stmt.Rhs[i]
						isBorrow := sc.isBorrowExpr(valExpr, isBorrowVar)
						fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow)
						if isBorrow || fld != "" {
							isLocalStackStruct := false
							var localStructVar *types.Var
							if id, ok := unparen(sel.X).(*ast.Ident); ok {
								var v *types.Var
								if tv, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok {
									v = tv
								} else if tv, ok := sc.pass.TypesInfo.Defs[id].(*types.Var); ok {
									v = tv
								}
								if v != nil && !sc.isPackageGlobal(v) && !heapPtrs[v] {
									if _, isStruct := v.Type().Underlying().(*types.Struct); isStruct {
										isLocalStackStruct = true
										localStructVar = v
									}
								}
							}
							if fldType := sc.pass.TypesInfo.TypeOf(sel); fldType != nil && types.IsInterface(fldType.Underlying()) {
								if localStructVar != nil {
									structVarHoldsBorrow[localStructVar] = sel.Sel.Name
									if sc.varEscapes(localStructVar, syntax) {
										escapingAssignLines[sc.pass.Fset.Position(stmt.Pos()).Line] = true
									} else {
										sc.report(stmt.Pos(), StorageViolation,
											"struct field or interface cannot box second-class borrow")
									}
								} else {
									sc.report(stmt.Pos(), StorageViolation,
										"struct field or interface cannot box second-class borrow")
								}
								continue
							}
							if !isLocalStackStruct {
								fldName := sel.Sel.Name
								sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", fldName))
								continue
							} else if localStructVar != nil {
								// Transient local stack struct field update: record in structVarHoldsBorrow
								structVarHoldsBorrow[localStructVar] = sel.Sel.Name
							}
						}
					}
					continue
				}

				// C. LHS is Map or Slice index: m[0] = b, s[0] = b, m[0] = t, s[0] = t
				if idxExpr, ok := unparen(lhs).(*ast.IndexExpr); ok {
					if t := sc.pass.TypesInfo.TypeOf(idxExpr.X); t != nil && i < len(stmt.Rhs) {
						valExpr := stmt.Rhs[i]
						isBorrow := sc.isBorrowExpr(valExpr, isBorrowVar)
						fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow)

						switch u := t.Underlying().(type) {
						case *types.Map:
							// Check map key
							keyIsBorrow := sc.isBorrowExpr(idxExpr.Index, isBorrowVar)
							keyFld := sc.findBorrowInExpr(idxExpr.Index, isBorrowVar, structVarHoldsBorrow)
							if keyIsBorrow {
								sc.report(idxExpr.Index.Pos(), StorageViolation, "map cannot store second-class borrow")
							} else if keyFld != "" {
								sc.report(idxExpr.Index.Pos(), StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", keyFld))
							}
							// Check map value
							if isBorrow {
								sc.report(valExpr.Pos(), StorageViolation, "map cannot store second-class borrow")
							} else if fld != "" {
								sc.report(valExpr.Pos(), StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", fld))
							}
						case *types.Slice:
							if isBorrow {
								sc.report(valExpr.Pos(), StorageViolation, "slice cannot store second-class borrow")
							} else if fld != "" {
								sc.report(valExpr.Pos(), StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", fld))
							}
						case *types.Array:
							if isBorrow || fld != "" {
								if types.IsInterface(u.Elem().Underlying()) {
									sc.report(valExpr.Pos(), StorageViolation,
										"struct field or interface cannot box second-class borrow")
								} else {
									sc.report(valExpr.Pos(), StorageViolation,
										"array cannot store second-class borrow")
								}
							}
						}
					}
					continue
				}

				// D. LHS is Interface Boxing: var i any = ... / i = b / i = t
				if i < len(stmt.Rhs) {
					valExpr := stmt.Rhs[i]
					if lhsType := sc.pass.TypesInfo.TypeOf(lhs); lhsType != nil && types.IsInterface(lhsType.Underlying()) {
						if sc.isBorrowExpr(valExpr, isBorrowVar) || sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow) != "" {
							sc.report(stmt.Pos(), StorageViolation,
								"struct field or interface cannot box second-class borrow")
							continue
						}
					}
				}

				lhsId, ok := unparen(lhs).(*ast.Ident)
				if !ok {
					continue
				}

				v, _ := sc.pass.TypesInfo.Defs[lhsId].(*types.Var)
				if v == nil {
					v, _ = sc.pass.TypesInfo.Uses[lhsId].(*types.Var)
				}
				if v == nil {
					continue
				}

				// E. Check if LHS is a package global or captured outer variable
				if sc.isPackageGlobal(v) {
					if i < len(stmt.Rhs) {
						valExpr := stmt.Rhs[i]
						if sc.isBorrowExpr(valExpr, isBorrowVar) || sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow) != "" {
							sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
								"package variable '%s' cannot store second-class borrow", v.Name()))
						}
					}
					continue
				}
				if v.Pos().IsValid() && (v.Pos() < syntax.Pos() || v.Pos() > syntax.End()) {
					if i < len(stmt.Rhs) {
						valExpr := stmt.Rhs[i]
						if sc.isBorrowExpr(valExpr, isBorrowVar) || sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow) != "" {
							sc.report(stmt.Pos(), StorageViolation,
								"cannot store second-class borrow to heap or non-stack memory")
						}
					}
					continue
				}

				// Track pointer-to-global: p := &globalTarget
				if i < len(stmt.Rhs) {
					rhs := stmt.Rhs[i]
					if ue, ok := unparen(rhs).(*ast.UnaryExpr); ok && ue.Op == token.AND {
						if baseId, ok := unparen(ue.X).(*ast.Ident); ok {
							if gv, ok := sc.pass.TypesInfo.Uses[baseId].(*types.Var); ok && sc.isPackageGlobal(gv) {
								ptrToGlobal[v] = gv.Name()
							}
						}
					}
					// Track heap pointer: h := new(*int) or h := new(StackTuple)
					if call, ok := unparen(rhs).(*ast.CallExpr); ok {
						if funId, ok := unparen(call.Fun).(*ast.Ident); ok && funId.Name == "new" {
							heapPtrs[v] = true
						}
					}
					// Check if RHS is a borrow or composite literal holding a borrow
					if fld := sc.findBorrowInExpr(rhs, isBorrowVar, structVarHoldsBorrow); fld != "" {
						if _, isStruct := v.Type().Underlying().(*types.Struct); isStruct {
							structVarHoldsBorrow[v] = fld
						}
					}
					// Also propagate direct borrow assignments: b2 := b
					if sc.isBorrowExpr(rhs, isBorrowVar) {
						isBorrowVar[v] = true
					}
				}
			}

		case *ast.DeclStmt:
			if gd, ok := stmt.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						isInterface := false
						if vs.Type != nil {
							if t := sc.pass.TypesInfo.TypeOf(vs.Type); t != nil && types.IsInterface(t.Underlying()) {
								isInterface = true
							}
						}
						for idx, valExpr := range vs.Values {
							if !isInterface {
								if t := sc.pass.TypesInfo.TypeOf(valExpr); t != nil && types.IsInterface(t.Underlying()) {
									isInterface = true
								}
							}
							if isInterface {
								if sc.isBorrowExpr(valExpr, isBorrowVar) || sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow) != "" {
									sc.report(vs.Pos(), StorageViolation,
										"struct field or interface cannot box second-class borrow")
								}
							} else {
								// Also propagate to declared variable
								if idx < len(vs.Names) {
									declId := vs.Names[idx]
									if dv, ok := sc.pass.TypesInfo.Defs[declId].(*types.Var); ok {
										if ue, ok := unparen(valExpr).(*ast.UnaryExpr); ok && ue.Op == token.AND {
											if baseId, ok := unparen(ue.X).(*ast.Ident); ok {
												if gv, ok := sc.pass.TypesInfo.Uses[baseId].(*types.Var); ok && sc.isPackageGlobal(gv) {
													ptrToGlobal[dv] = gv.Name()
												}
											}
										}
										if call, ok := unparen(valExpr).(*ast.CallExpr); ok {
											if funId, ok := unparen(call.Fun).(*ast.Ident); ok && funId.Name == "new" {
												heapPtrs[dv] = true
											}
										}
										if fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow); fld != "" {
											if _, isStruct := dv.Type().Underlying().(*types.Struct); isStruct {
												structVarHoldsBorrow[dv] = fld
											}
										}
										if sc.isBorrowExpr(valExpr, isBorrowVar) {
											isBorrowVar[dv] = true
										}
									}
								}
							}
						}
					}
				}
			}

		case *ast.CompositeLit:
			if t := sc.pass.TypesInfo.TypeOf(stmt); t != nil {
				switch t.Underlying().(type) {
				case *types.Map:
					for _, elt := range stmt.Elts {
						valExpr := elt
						var keyExpr ast.Expr
						if kve, ok := elt.(*ast.KeyValueExpr); ok {
							keyExpr = kve.Key
							valExpr = kve.Value
						}
						if keyExpr != nil {
							if sc.isBorrowExpr(keyExpr, isBorrowVar) {
								sc.report(keyExpr.Pos(), StorageViolation, "map cannot store second-class borrow")
							} else if fld := sc.findBorrowInExpr(keyExpr, isBorrowVar, structVarHoldsBorrow); fld != "" {
								sc.report(keyExpr.Pos(), StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", fld))
							}
						}
						if sc.isBorrowExpr(valExpr, isBorrowVar) {
							sc.report(valExpr.Pos(), StorageViolation, "map cannot store second-class borrow")
						} else if fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow); fld != "" {
							sc.report(valExpr.Pos(), StorageViolation, fmt.Sprintf(
								"borrow stored in struct field '%s' escapes stack frame", fld))
						}
					}
				case *types.Slice:
					for _, elt := range stmt.Elts {
						valExpr := elt
						if kve, ok := elt.(*ast.KeyValueExpr); ok {
							valExpr = kve.Value
						}
						if sc.isBorrowExpr(valExpr, isBorrowVar) {
							sc.report(valExpr.Pos(), StorageViolation, "slice cannot store second-class borrow")
						} else if fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow); fld != "" {
							sc.report(valExpr.Pos(), StorageViolation, fmt.Sprintf(
								"borrow stored in struct field '%s' escapes stack frame", fld))
						}
					}
				case *types.Struct:
					st := t.Underlying().(*types.Struct)
					assignedVar := litAssignedVar[stmt]
					escapesLater := assignedVar != nil && !sc.isPackageGlobal(assignedVar) && sc.varEscapes(assignedVar, syntax)
					if !escapesLater {
						for i, elt := range stmt.Elts {
							valExpr := elt
							var fieldType types.Type
							if kve, ok := elt.(*ast.KeyValueExpr); ok {
								valExpr = kve.Value
								if id, ok := unparen(kve.Key).(*ast.Ident); ok {
									for fi := 0; fi < st.NumFields(); fi++ {
										if st.Field(fi).Name() == id.Name {
											fieldType = st.Field(fi).Type()
											break
										}
									}
								}
							} else {
								if i < st.NumFields() {
									fieldType = st.Field(i).Type()
								}
							}
							if fieldType != nil && types.IsInterface(fieldType.Underlying()) {
								if sc.isBorrowExpr(valExpr, isBorrowVar) || sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow) != "" {
									sc.report(valExpr.Pos(), StorageViolation,
										"struct field or interface cannot box second-class borrow")
								}
							}
						}
					}
				case *types.Array:
					arr := t.Underlying().(*types.Array)
					elemType := arr.Elem()
					for _, elt := range stmt.Elts {
						valExpr := elt
						if kve, ok := elt.(*ast.KeyValueExpr); ok {
							valExpr = kve.Value
						}
						isBorrow := sc.isBorrowExpr(valExpr, isBorrowVar)
						fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow)
						if isBorrow || fld != "" {
							if types.IsInterface(elemType.Underlying()) {
								sc.report(valExpr.Pos(), StorageViolation,
									"struct field or interface cannot box second-class borrow")
							} else {
								sc.report(valExpr.Pos(), StorageViolation,
									"array cannot store second-class borrow")
							}
						}
					}
				}
			}

		case *ast.CallExpr:
			// Check if arguments are boxed into interface parameters
			if funType := sc.pass.TypesInfo.TypeOf(stmt.Fun); funType != nil {
				if sig, ok := funType.Underlying().(*types.Signature); ok && sig.Params() != nil {
					numParams := sig.Params().Len()
					for argIdx, arg := range stmt.Args {
						var paramType types.Type
						if sig.Variadic() && argIdx >= numParams-1 {
							if stmt.Ellipsis.IsValid() && argIdx == numParams-1 {
								paramType = sig.Params().At(numParams - 1).Type()
							} else if sliceType, ok := sig.Params().At(numParams - 1).Type().Underlying().(*types.Slice); ok {
								paramType = sliceType.Elem()
							}
						} else if argIdx < numParams {
							paramType = sig.Params().At(argIdx).Type()
						}
						if paramType != nil && types.IsInterface(paramType.Underlying()) {
							if sc.isBorrowExpr(arg, isBorrowVar) || sc.findBorrowInExpr(arg, isBorrowVar, structVarHoldsBorrow) != "" {
								sc.report(arg.Pos(), StorageViolation,
									"struct field or interface cannot box second-class borrow")
							}
						}
					}
				}
			}

			// Built-in append(s, t)
			if id, ok := unparen(stmt.Fun).(*ast.Ident); ok && id.Name == "append" {
				for _, arg := range stmt.Args[1:] {
					if sc.isBorrowExpr(arg, isBorrowVar) {
						sc.report(arg.Pos(), StorageViolation, "slice append cannot store second-class borrow")
					} else if fld := sc.findBorrowInExpr(arg, isBorrowVar, structVarHoldsBorrow); fld != "" {
						sc.report(arg.Pos(), StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fld))
					}
				}
			}
			// Multi-hop helper: assignHelper(&globalTarget2, b)
			var hasGlobalAddr string
			var hasBorrowPos token.Pos
			for _, arg := range stmt.Args {
				if ue, ok := unparen(arg).(*ast.UnaryExpr); ok && ue.Op == token.AND {
					if id, ok := unparen(ue.X).(*ast.Ident); ok {
						if v, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok && sc.isPackageGlobal(v) {
							hasGlobalAddr = v.Name()
						}
					}
				}
				if sc.isBorrowExpr(arg, isBorrowVar) || sc.findBorrowInExpr(arg, isBorrowVar, structVarHoldsBorrow) != "" {
					hasBorrowPos = arg.Pos()
				}
			}
			if hasGlobalAddr != "" && hasBorrowPos.IsValid() {
				sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
					"package variable '%s' cannot store second-class borrow", hasGlobalAddr))
			}

		case *ast.SendStmt:
			valExpr := stmt.Value
			if sc.isBorrowExpr(valExpr, isBorrowVar) {
				sc.report(valExpr.Pos(), StorageViolation, "channel send cannot store second-class borrow")
			} else if fld := sc.findBorrowInExpr(valExpr, isBorrowVar, structVarHoldsBorrow); fld != "" {
				sc.report(valExpr.Pos(), StorageViolation, fmt.Sprintf(
					"borrow stored in struct field '%s' escapes stack frame", fld))
			}

		case *ast.ReturnStmt:
			for idx, r := range stmt.Results {
				// Scalar primitive returns (e.g. *t.p yielding int) cannot escape borrows!
				if t := sc.pass.TypesInfo.TypeOf(r); t != nil && !canHoldBorrow(t) {
					continue
				}
				// Check if the function signature return type at this index is an interface
				var isInterfaceRet bool
				if fn.Signature != nil && fn.Signature.Results() != nil && idx < fn.Signature.Results().Len() {
					retType := fn.Signature.Results().At(idx).Type()
					if retType != nil && types.IsInterface(retType.Underlying()) {
						isInterfaceRet = true
					}
				}
				isBorrow := sc.isBorrowExpr(r, isBorrowVar)
				fld := sc.findBorrowInExpr(r, isBorrowVar, structVarHoldsBorrow)
				if isInterfaceRet {
					if isBorrow || fld != "" {
						sc.report(r.Pos(), StorageViolation,
							"struct field or interface cannot box second-class borrow")
						continue
					}
				}
				if fld != "" {
					sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					continue
				}
				resExpr := r
				if ue, ok := unparen(resExpr).(*ast.UnaryExpr); ok && ue.Op == token.AND {
					resExpr = unparen(ue.X)
				}
				if id, ok := unparen(resExpr).(*ast.Ident); ok {
					if v, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok {
						if fld, ok := structVarHoldsBorrow[v]; ok {
							sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
								"borrow stored in struct field '%s' escapes stack frame", fld))
						}
					}
				}
			}
		}
		return true
	})

	// 2. SSA-level flow checks
	isBorrowVal := make(map[ssa.Value]bool)
	structHoldsBorrow := make(map[ssa.Value]string)

	// Map AST borrow variables to SSA values
	for _, p := range fn.Params {
		if v, ok := p.Object().(*types.Var); ok && isBorrowVar[v] {
			isBorrowVal[p] = true
		}
	}
	for _, fv := range fn.FreeVars {
		for v := range isBorrowVar {
			if (fv.Pos().IsValid() && v.Pos().IsValid() && fv.Pos() == v.Pos()) || fv.Name() == v.Name() {
				isBorrowVal[fv] = true
			}
		}
		for v, fld := range structVarHoldsBorrow {
			if (fv.Pos().IsValid() && v.Pos().IsValid() && fv.Pos() == v.Pos()) || fv.Name() == v.Name() {
				structHoldsBorrow[fv] = fld
			}
		}
	}

	// Map local borrow variables using ValueForExpr and AST identifiers
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			if v, ok := sc.pass.TypesInfo.Uses[node].(*types.Var); ok {
				if isBorrowVar[v] {
					if val, _ := fn.ValueForExpr(node); val != nil {
						isBorrowVal[val] = true
					}
				}
				if fld, ok := structVarHoldsBorrow[v]; ok {
					if val, _ := fn.ValueForExpr(node); val != nil {
						structHoldsBorrow[val] = fld
					}
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				if lhsId, ok := lhs.(*ast.Ident); ok {
					if v, ok := sc.pass.TypesInfo.Defs[lhsId].(*types.Var); ok {
						if isBorrowVar[v] && i < len(node.Rhs) {
							if val, _ := fn.ValueForExpr(node.Rhs[i]); val != nil {
								isBorrowVal[val] = true
							}
							if val, _ := fn.ValueForExpr(lhsId); val != nil {
								isBorrowVal[val] = true
							}
							// Owner allocation in fn.Locals and fn.Blocks
							ownerName := ""
							rhs := unparen(node.Rhs[i])
							if call, ok := rhs.(*ast.CallExpr); ok && len(call.Args) == 1 && sc.ix.BorrowAt(call.Pos()) != annot.BorrowNone {
								rhs = unparen(call.Args[0])
							}
							if ue, ok := rhs.(*ast.UnaryExpr); ok && ue.Op == token.AND {
								if baseId, ok := unparen(ue.X).(*ast.Ident); ok {
									ownerName = baseId.Name
								}
							}
							if ownerName != "" {
								for _, l := range fn.Locals {
									if l.Comment == ownerName {
										isBorrowVal[l] = true
									}
								}
								for _, b := range fn.Blocks {
									for _, ins := range b.Instrs {
										if alloc, ok := ins.(*ssa.Alloc); ok && alloc.Comment == ownerName {
											isBorrowVal[alloc] = true
										}
									}
								}
							}
						}
						if fld, ok := structVarHoldsBorrow[v]; ok {
							if i < len(node.Rhs) {
								if val, _ := fn.ValueForExpr(node.Rhs[i]); val != nil {
									structHoldsBorrow[val] = fld
								}
							}
							if val, _ := fn.ValueForExpr(lhsId); val != nil {
								structHoldsBorrow[val] = fld
							}
							for _, l := range fn.Locals {
								if l.Comment == v.Name() {
									structHoldsBorrow[l] = fld
								}
							}
							for _, b := range fn.Blocks {
								for _, ins := range b.Instrs {
									if alloc, ok := ins.(*ssa.Alloc); ok && alloc.Comment == v.Name() {
										structHoldsBorrow[alloc] = fld
									}
								}
							}
						}
					}
				}
			}
		}
		return true
	})

	// Also map fn.Locals matching variable names directly
	for _, l := range fn.Locals {
		for v := range isBorrowVar {
			if (l.Pos().IsValid() && v.Pos().IsValid() && l.Pos() == v.Pos()) || v.Name() == l.Comment {
				isBorrowVal[l] = true
			}
		}
		for v, fld := range structVarHoldsBorrow {
			if (l.Pos().IsValid() && v.Pos().IsValid() && l.Pos() == v.Pos()) || v.Name() == l.Comment {
				structHoldsBorrow[l] = fld
			}
		}
	}

	// Scan fn.Blocks for *ssa.Alloc instructions matching variable names or positions directly
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if alloc, ok := ins.(*ssa.Alloc); ok {
				for v := range isBorrowVar {
					if (alloc.Pos().IsValid() && v.Pos().IsValid() && alloc.Pos() == v.Pos()) || (alloc.Comment != "" && alloc.Comment == v.Name()) {
						isBorrowVal[alloc] = true
					}
				}
				for v, fld := range structVarHoldsBorrow {
					if (alloc.Pos().IsValid() && v.Pos().IsValid() && alloc.Pos() == v.Pos()) || (alloc.Comment != "" && alloc.Comment == v.Name()) {
						structHoldsBorrow[alloc] = fld
					}
				}
			}
		}
	}

	forEachInstr(fn, func(ins ssa.Instruction) {
		switch ins := ins.(type) {
		case *ssa.Store:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			valIsBorrow := isBorrowVal[ins.Val]
			valHoldsBorrow := structHoldsBorrow[ins.Val] != ""

			// Propagate borrow into local alloc
			if alloc, ok := ins.Addr.(*ssa.Alloc); ok {
				if valIsBorrow {
					isBorrowVal[alloc] = true
				}
				if valHoldsBorrow {
					structHoldsBorrow[alloc] = structHoldsBorrow[ins.Val]
				}
			}

			// A. Store into package-level global
			if g, ok := globalRoot(ins.Addr); ok && g.Pkg == sc.ssa.Pkg {
				if valIsBorrow || valHoldsBorrow {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"package variable '%s' cannot store second-class borrow", g.Name()))
					return
				}
			}

			// B. Store into struct field (including nested fields)
			if fa, ok := ins.Addr.(*ssa.FieldAddr); ok {
				fldName := structFieldName(fa)
				if !isStackLocalAddr(fa.X) {
					if valIsBorrow {
						sc.report(pos, StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fldName))
						return
					}
					if fld, ok := structHoldsBorrow[ins.Val]; ok {
						sc.report(pos, StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fld))
						return
					}
					for sVal, fld := range structHoldsBorrow {
						if isDerivedFrom(ins.Val, sVal) {
							sc.report(pos, StorageViolation, fmt.Sprintf(
								"borrow stored in struct field '%s' escapes stack frame", fld))
							return
						}
					}
				}
				if valIsBorrow {
					structHoldsBorrow[fa.X] = fldName
					// Follow up to root struct
					root := fa.X
					for {
						if innerFa, ok := root.(*ssa.FieldAddr); ok {
							root = innerFa.X
							structHoldsBorrow[root] = fldName
						} else {
							break
						}
					}
				} else if fld, ok := structHoldsBorrow[ins.Val]; ok {
					structHoldsBorrow[fa.X] = fld
				}
			}

			// C. Store into slice element (*ssa.IndexAddr)
			if _, ok := ins.Addr.(*ssa.IndexAddr); ok {
				if valIsBorrow {
					sc.report(pos, StorageViolation,
						"slice cannot store second-class borrow")
					return
				}
				if fld, ok := structHoldsBorrow[ins.Val]; ok {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					return
				}
			}

			// D. Store to heap or non-stack memory
			if !isStackLocalAddr(ins.Addr) {
				if valIsBorrow {
					sc.report(pos, StorageViolation,
						"cannot store second-class borrow to heap or non-stack memory")
					return
				}
				if fld, ok := structHoldsBorrow[ins.Val]; ok {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					return
				}
			}

		case *ssa.MapUpdate:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			if isBorrowVal[ins.Value] || isBorrowVal[ins.Key] {
				sc.report(pos, StorageViolation,
					"map cannot store second-class borrow")
				return
			}
			if fld, ok := structHoldsBorrow[ins.Value]; ok {
				sc.report(pos, StorageViolation, fmt.Sprintf(
					"borrow stored in struct field '%s' escapes stack frame", fld))
				return
			}
			if fld, ok := structHoldsBorrow[ins.Key]; ok {
				sc.report(pos, StorageViolation, fmt.Sprintf(
					"borrow stored in struct field '%s' escapes stack frame", fld))
				return
			}
			for sVal, fld := range structHoldsBorrow {
				if isDerivedFrom(ins.Value, sVal) || isDerivedFrom(ins.Key, sVal) {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					break
				}
			}

		case *ssa.Call:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			if callee := ins.Common().StaticCallee(); callee != nil {
				cName := callee.Name()
				if idx := strings.IndexByte(cName, '['); idx >= 0 {
					cName = cName[:idx]
				}
				switch cName {
				case "BorrowMut", "BorrowMub", "Borrow", "BorrowRef", "BorrowRob":
					var pkg *types.Package
					if callee.Pkg != nil {
						pkg = callee.Pkg.Pkg
					} else if callee.Object() != nil {
						pkg = callee.Object().Pkg()
					}
					if (pkg != nil && (pkg.Name() == "cap" || strings.HasSuffix(pkg.Path(), "/cap"))) ||
						strings.Contains(callee.String(), "/cap.Borrow") || strings.HasPrefix(callee.String(), "cap.Borrow") {
						isBorrowVal[ins] = true
						for _, arg := range ins.Common().Args {
							isBorrowVal[arg] = true
						}
					}
				}
			}
			// Check built-in append(s, t)
			if b, ok := ins.Common().Value.(*ssa.Builtin); ok && b.Name() == "append" {
				for _, arg := range ins.Common().Args[1:] {
					if isBorrowVal[arg] {
						sc.report(pos, StorageViolation,
							"slice append cannot store second-class borrow")
					}
					if fld, ok := structHoldsBorrow[arg]; ok {
						sc.report(pos, StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fld))
					}
					for sVal, fld := range structHoldsBorrow {
						if isDerivedFrom(arg, sVal) {
							sc.report(pos, StorageViolation, fmt.Sprintf(
								"borrow stored in struct field '%s' escapes stack frame", fld))
							break
						}
					}
				}
			}
			// Multi-hop helper escape: passing borrow alongside global variable pointer
			hasBorrowArg := false
			var globalTargetName string
			for _, arg := range ins.Common().Args {
				if isBorrowVal[arg] {
					hasBorrowArg = true
				}
				if g, ok := globalRoot(arg); ok && g.Pkg == sc.ssa.Pkg {
					globalTargetName = g.Name()
				}
			}
			if hasBorrowArg && globalTargetName != "" {
				sc.report(pos, StorageViolation, fmt.Sprintf(
					"package variable '%s' cannot store second-class borrow", globalTargetName))
			} else if callee := ins.Common().StaticCallee(); callee != nil && callee != fn {
				bodyFn := callee
				if o := bodyFn.Origin(); o != nil && len(o.Blocks) > 0 {
					bodyFn = o
				}
				if bodyFn != fn && len(bodyFn.Blocks) > 0 {
					for i, arg := range ins.Common().Args {
						if i >= len(bodyFn.Params) {
							break
						}
						if i < len(callee.Params) {
							if pVar, ok := callee.Params[i].Object().(*types.Var); ok && (sc.ix.IsMub(pVar) || sc.ix.IsRob(pVar)) {
								continue
							}
						}
						if pVar, ok := bodyFn.Params[i].Object().(*types.Var); ok && (sc.ix.IsMub(pVar) || sc.ix.IsRob(pVar)) {
							continue
						}
						valIsBorrow := isBorrowVal[arg]
						fld := structHoldsBorrow[arg]
						if fld == "" {
							for sVal, fName := range structHoldsBorrow {
								if isDerivedFrom(arg, sVal) {
									fld = fName
									break
								}
							}
						}
						if !valIsBorrow && fld == "" {
							continue
						}
						if gName := calleeStoresParamInGlobal(bodyFn, i); gName != "" {
							if fld != "" {
								sc.report(pos, StorageViolation, fmt.Sprintf(
									"borrow stored in struct field '%s' escapes stack frame", fld))
							} else {
								sc.report(pos, StorageViolation, fmt.Sprintf(
									"package variable '%s' cannot store second-class borrow", gName))
							}
						}
						if calleeReturnsParam(bodyFn, i) && canHoldBorrow(ins.Type()) {
							if valIsBorrow {
								isBorrowVal[ins] = true
							}
							if fld != "" {
								structHoldsBorrow[ins] = fld
							}
						}
					}
				}
			}

		case *ssa.UnOp:
			if ins.Op == token.MUL {
				// If the loaded type is a scalar primitive (int, bool, string),
				// it cannot be or hold a borrow: do NOT propagate!
				if !canHoldBorrow(ins.Type()) {
					break
				}
				if isBorrowVal[ins.X] {
					isBorrowVal[ins] = true
				}
				if fld, ok := structHoldsBorrow[ins.X]; ok {
					structHoldsBorrow[ins] = fld
				}
			}

		case *ssa.ChangeType:
			if isBorrowVal[ins.X] {
				isBorrowVal[ins] = true
			}
			if fld, ok := structHoldsBorrow[ins.X]; ok {
				structHoldsBorrow[ins] = fld
			}

		case *ssa.MakeClosure:
			for _, b := range ins.Bindings {
				if ptr, ok := b.Type().Underlying().(*types.Pointer); ok && !canHoldBorrow(ptr.Elem()) {
					continue
				}
				if isBorrowVal[b] {
					isBorrowVal[ins] = true
				}
				if fld, ok := structHoldsBorrow[b]; ok {
					structHoldsBorrow[ins] = fld
				}
			}

		case *ssa.Phi:
			for _, e := range ins.Edges {
				if isBorrowVal[e] {
					isBorrowVal[ins] = true
				}
				if fld, ok := structHoldsBorrow[e]; ok {
					structHoldsBorrow[ins] = fld
				}
			}

		case *ssa.Convert:
			if isBorrowVal[ins.X] {
				isBorrowVal[ins] = true
			}
			if fld, ok := structHoldsBorrow[ins.X]; ok {
				structHoldsBorrow[ins] = fld
			}

		case *ssa.TypeAssert:
			if isBorrowVal[ins.X] {
				isBorrowVal[ins] = true
			}
			if fld, ok := structHoldsBorrow[ins.X]; ok {
				isBorrowVal[ins] = true
				structHoldsBorrow[ins] = fld
			}
			for sVal, fld := range structHoldsBorrow {
				if isDerivedFrom(ins.X, sVal) {
					isBorrowVal[ins] = true
					structHoldsBorrow[ins] = fld
					break
				}
			}

		case *ssa.MakeInterface:
			isBorrow := isBorrowVal[ins.X]
			holdsBorrow := structHoldsBorrow[ins.X] != ""
			if !isBorrow && !holdsBorrow {
				for sVal := range structHoldsBorrow {
					if isDerivedFrom(ins.X, sVal) {
						holdsBorrow = true
						break
					}
				}
			}
			if isBorrow || holdsBorrow {
				pos := ins.Pos()
				if !pos.IsValid() {
					pos = resolveInstructionPos(ins, fn)
				}
				// A line the syntax check has reported is not reported again. A boxing with no
				// position (an implicit conversion) cannot be matched to a line: it waits, and is
				// reported at its function unless the function has a finding of this rule (report).
				line := sc.pass.Fset.Position(pos).Line
				if !pos.IsValid() || (!escapingLitLines[line] && !escapingAssignLines[line]) {
					sc.report(pos, StorageViolation,
						"struct field or interface cannot box second-class borrow")
				}
			}

		case *ssa.Return:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			for _, r := range ins.Results {
				// Scalar primitives (e.g. *t.p returning int) cannot escape borrows!
				if !canHoldBorrow(r.Type()) {
					continue
				}
				// Interface return boxing is checked at boxing site (MakeInterface or AST ReturnStmt)
				if types.IsInterface(r.Type()) {
					continue
				}
				if isBorrowVal[r] {
					sc.report(pos, StorageViolation,
						"second-class borrow cannot escape stack frame via return")
					continue
				}
				if fld, ok := structHoldsBorrow[r]; ok {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					continue
				}
				for sVal, fld := range structHoldsBorrow {
					if isDerivedFrom(r, sVal) {
						sc.report(pos, StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fld))
						break
					}
				}
			}

		case *ssa.Send:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			if isBorrowVal[ins.X] {
				sc.report(pos, StorageViolation,
					"channel send cannot store second-class borrow")
			}
			if fld, ok := structHoldsBorrow[ins.X]; ok {
				sc.report(pos, StorageViolation, fmt.Sprintf(
					"borrow stored in struct field '%s' escapes stack frame", fld))
			}
			for sVal, fld := range structHoldsBorrow {
				if isDerivedFrom(ins.X, sVal) {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					break
				}
			}

		case *ssa.Select:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			for _, st := range ins.States {
				if st.Dir != types.SendOnly || st.Send == nil {
					continue
				}
				sPos := st.Pos
				if !sPos.IsValid() {
					sPos = pos
				}
				if isBorrowVal[st.Send] {
					sc.report(sPos, StorageViolation,
						"channel send cannot store second-class borrow")
				}
				if fld, ok := structHoldsBorrow[st.Send]; ok {
					sc.report(sPos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
				}
				for sVal, fld := range structHoldsBorrow {
					if isDerivedFrom(st.Send, sVal) {
						sc.report(sPos, StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fld))
						break
					}
				}
			}

		case *ssa.Go:
			pos := ins.Pos()
			if !pos.IsValid() {
				pos = resolveInstructionPos(ins, fn)
			}
			cc := ins.Common()
			for _, arg := range cc.Args {
				if isBorrowVal[arg] {
					sc.report(pos, StorageViolation,
						"goroutine cannot capture second-class borrow")
				}
				if fld, ok := structHoldsBorrow[arg]; ok {
					sc.report(pos, StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
				}
			}
		}
	})
}

func (sc *storageChecker) isPackageGlobal(v *types.Var) bool {
	if v == nil || v.Pkg() == nil {
		return false
	}
	return v.Pkg() == sc.pass.Pkg && v.Parent() == sc.pass.Pkg.Scope()
}

func calleeStoresParamInGlobal(callee *ssa.Function, paramIdx int) string {
	if callee == nil || paramIdx < 0 || paramIdx >= len(callee.Params) {
		return ""
	}
	p := callee.Params[paramIdx]
	for _, b := range callee.Blocks {
		for _, ins := range b.Instrs {
			st, ok := ins.(*ssa.Store)
			if !ok {
				continue
			}
			if !canHoldBorrow(st.Val.Type()) {
				continue
			}
			if st.Val == p || isDerivedFrom(st.Val, p) {
				if g, ok := globalRoot(st.Addr); ok && g != nil {
					return g.Name()
				}
			}
		}
	}
	return ""
}

func calleeReturnsParam(callee *ssa.Function, paramIdx int) bool {
	if callee == nil || paramIdx < 0 || paramIdx >= len(callee.Params) || len(callee.Blocks) == 0 {
		return false
	}
	p := callee.Params[paramIdx]
	for _, b := range callee.Blocks {
		for _, ins := range b.Instrs {
			ret, ok := ins.(*ssa.Return)
			if !ok {
				continue
			}
			for _, r := range ret.Results {
				if !canHoldBorrow(r.Type()) {
					if _, isTP := r.Type().(*types.TypeParam); !isTP {
						continue
					}
				}
				if r == p || isDerivedFrom(r, p) {
					return true
				}
			}
		}
	}
	return false
}

func structFieldName(fa *ssa.FieldAddr) string {
	t := fa.X.Type()
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if str, ok := t.Underlying().(*types.Struct); ok {
		if fa.Field >= 0 && fa.Field < str.NumFields() {
			return str.Field(fa.Field).Name()
		}
	}
	return fmt.Sprintf("#%d", fa.Field)
}

func isStackLocalAddr(addr ssa.Value) bool {
	switch a := addr.(type) {
	case *ssa.Alloc:
		if !a.Heap {
			return true
		}
		// A composite literal &Struct{...} is a transient stack-scoped tuple until escaped
		if strings.Contains(a.Comment, "complit") || strings.Contains(a.Comment, "literal") {
			return true
		}
		if a.Comment != "new" && a.Comment != "" && !strings.HasPrefix(a.Comment, "new") {
			return true
		}
		return false
	case *ssa.FieldAddr:
		return isStackLocalAddr(a.X)
	case *ssa.IndexAddr:
		return isStackLocalAddr(a.X)
	}
	return false
}

func isDerivedFrom(v, root ssa.Value) bool {
	if v == root {
		return true
	}
	for range 16 {
		switch x := v.(type) {
		case *ssa.UnOp:
			if x.Op == token.MUL {
				if !canHoldBorrow(x.Type()) {
					return false
				}
				v = x.X
			} else {
				return false
			}
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.Field:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Index:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.TypeAssert:
			v = x.X
		default:
			return v == root
		}
		if v == root {
			return true
		}
	}
	return false
}

func forEachInstr(fn *ssa.Function, f func(ssa.Instruction)) {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			f(ins)
		}
	}
}

func globalRoot(v ssa.Value) (*ssa.Global, bool) {
	for range 64 {
		switch x := v.(type) {
		case *ssa.Global:
			return x, true
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Field:
			v = x.X
		case *ssa.Index:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			v = x.X
		default:
			return nil, false
		}
	}
	return nil, false
}
