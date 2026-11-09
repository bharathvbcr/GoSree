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

	"github.com/bharathvbcr/gocap/internal/annot"
)

// StorageViolation is the diagnostic code for second-class borrow storage violations.
const StorageViolation = "BORROW001"

type storageChecker struct {
	pass    *analysis.Pass
	ix      *annot.Index
	ssa     *buildssa.SSA
	reports map[reportKey]bool
}

type reportKey struct {
	pos token.Pos
	cat string
}

// CheckStorage performs both AST declaration checking and SSA escape analysis for second-class borrows.
func CheckStorage(pass *analysis.Pass, ix *annot.Index, ssaProg *buildssa.SSA) {
	sc := &storageChecker{
		pass:    pass,
		ix:      ix,
		ssa:     ssaProg,
		reports: make(map[reportKey]bool),
	}

	// Phase 1: Static AST declaration checks
	sc.checkStructDeclarations()
	sc.checkGlobalDeclarations()

	// Phase 2: Function-level AST and SSA flow/escape checks
	for _, fn := range sc.ssa.SrcFuncs {
		if len(fn.Blocks) == 0 {
			continue
		}
		sc.checkFunctionStorage(fn)
	}
}

func (sc *storageChecker) report(pos token.Pos, cat, msg string) {
	if sc.ix.Suppressed(pos) {
		return
	}
	k := reportKey{pos, cat}
	if sc.reports[k] {
		return
	}
	sc.reports[k] = true
	sc.pass.Report(analysis.Diagnostic{
		Pos:      pos,
		Category: cat,
		Message:  cat + ": " + msg,
	})
}

// Phase 1: AST Checkers

func isBorrowDirective(text string) (string, bool) {
	text = strings.TrimSpace(text)
	// Block comments: /*gocap:mut*/ or /*safe:mut*/
	if strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") {
		inner := strings.TrimSpace(text[2 : len(text)-2])
		if strings.HasPrefix(inner, "gocap:") {
			return checkVerb(strings.TrimPrefix(inner, "gocap:"))
		}
		if strings.HasPrefix(inner, "safe:") {
			return checkVerb(strings.TrimPrefix(inner, "safe:"))
		}
	}
	// Line comments: //gocap:... or //safe:...
	if strings.HasPrefix(text, "//gocap:") {
		verb, _, _ := strings.Cut(strings.TrimPrefix(text, "//gocap:"), " ")
		return checkVerb(verb)
	}
	if strings.HasPrefix(text, "//safe:") {
		verb, _, _ := strings.Cut(strings.TrimPrefix(text, "//safe:"), " ")
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

// Phase 2: Function Storage & Escape Checking

func (sc *storageChecker) checkFunctionStorage(fn *ssa.Function) {
	syntax := fn.Syntax()
	if syntax == nil {
		return
	}

	// 1. AST-level tracking of borrow variables and structs holding borrows
	isBorrowVar := make(map[*types.Var]bool)
	structVarHoldsBorrow := make(map[*types.Var]string) // var -> field name

	// Seed with parameters
	for _, p := range fn.Params {
		if v, ok := p.Object().(*types.Var); ok {
			if sc.ix.IsMub(v) || sc.ix.IsRob(v) {
				isBorrowVar[v] = true
			}
		}
	}

	// Seed with annotated local variables
	for v := range sc.ix.MubVars {
		isBorrowVar[v] = true
	}
	for v := range sc.ix.RobVars {
		isBorrowVar[v] = true
	}

	// AST walk to find borrows and struct composite literals
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				if star, ok := lhs.(*ast.StarExpr); ok {
					_ = star
					if i < len(stmt.Rhs) {
						if rhsId, ok := stmt.Rhs[i].(*ast.Ident); ok {
							if rv, ok := sc.pass.TypesInfo.Uses[rhsId].(*types.Var); ok {
								if fld, ok := structVarHoldsBorrow[rv]; ok {
									sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
										"borrow stored in struct field '%s' escapes stack frame", fld))
								}
							}
						}
					}
					continue
				}

				lhsId, ok := lhs.(*ast.Ident)
				if !ok {
					// Check if LHS is a package global
					if id, ok := lhs.(*ast.Ident); ok {
						if v, ok := sc.pass.TypesInfo.Uses[id].(*types.Var); ok && sc.isPackageGlobal(v) {
							if i < len(stmt.Rhs) {
								if rhsId, ok := stmt.Rhs[i].(*ast.Ident); ok {
									if rv, ok := sc.pass.TypesInfo.Uses[rhsId].(*types.Var); ok {
										if isBorrowVar[rv] || structVarHoldsBorrow[rv] != "" {
											sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
												"package variable '%s' cannot store second-class borrow", v.Name()))
										}
									}
								}
							}
						}
					}
					continue
				}

				v, _ := sc.pass.TypesInfo.Defs[lhsId].(*types.Var)
				if v == nil {
					v, _ = sc.pass.TypesInfo.Uses[lhsId].(*types.Var)
				}
				if v == nil {
					continue
				}

				// Check if LHS is a package global
				if sc.isPackageGlobal(v) {
					if i < len(stmt.Rhs) {
						if rhsId, ok := stmt.Rhs[i].(*ast.Ident); ok {
							if rv, ok := sc.pass.TypesInfo.Uses[rhsId].(*types.Var); ok {
								if isBorrowVar[rv] || structVarHoldsBorrow[rv] != "" {
									sc.report(stmt.Pos(), StorageViolation, fmt.Sprintf(
										"package variable '%s' cannot store second-class borrow", v.Name()))
								}
							}
						}
					}
					continue
				}

				// Check if RHS is a composite literal holding a borrow
				if i < len(stmt.Rhs) {
					rhs := stmt.Rhs[i]
					// Address of composite literal: &StackTuple{p: b}
					if ue, ok := rhs.(*ast.UnaryExpr); ok && ue.Op == token.AND {
						rhs = ue.X
					}
					if cl, ok := rhs.(*ast.CompositeLit); ok {
						for _, elt := range cl.Elts {
							if kve, ok := elt.(*ast.KeyValueExpr); ok {
								fldName := ""
								if fldId, ok := kve.Key.(*ast.Ident); ok {
									fldName = fldId.Name
								}
								if valId, ok := kve.Value.(*ast.Ident); ok {
									if rv, ok := sc.pass.TypesInfo.Uses[valId].(*types.Var); ok && isBorrowVar[rv] {
										structVarHoldsBorrow[v] = fldName
									}
								}
							}
						}
					}
				}
			}

		case *ast.ReturnStmt:
			for _, r := range stmt.Results {
				// Address of struct: return &t
				resExpr := r
				if ue, ok := resExpr.(*ast.UnaryExpr); ok && ue.Op == token.AND {
					resExpr = ue.X
				}
				if id, ok := resExpr.(*ast.Ident); ok {
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
	for _, l := range fn.Locals {
		for v := range isBorrowVar {
			if v.Name() == l.Comment {
				isBorrowVal[l] = true
			}
		}
		for v, fld := range structVarHoldsBorrow {
			if v.Name() == l.Comment {
				structHoldsBorrow[l] = fld
			}
		}
	}

	forEachInstr(fn, func(ins ssa.Instruction) {
		switch ins := ins.(type) {
		case *ssa.Store:
			valIsBorrow := isBorrowVal[ins.Val]

			// A. Store into package-level global
			if g, ok := globalRoot(ins.Addr); ok && g.Pkg == sc.ssa.Pkg {
				if valIsBorrow {
					sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
						"package variable '%s' cannot store second-class borrow", g.Name()))
					return
				}
				if _, ok := structHoldsBorrow[ins.Val]; ok {
					sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
						"package variable '%s' cannot store second-class borrow", g.Name()))
					return
				}
			}

			// B. Store into struct field
			if fa, ok := ins.Addr.(*ssa.FieldAddr); ok {
				if valIsBorrow {
					fldName := structFieldName(fa)
					structHoldsBorrow[fa.X] = fldName
				}
			}

			// C. Store of struct holding borrow to heap
			if fld, ok := structHoldsBorrow[ins.Val]; ok {
				if !isStackLocalAddr(ins.Addr) {
					sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
				}
			}

		case *ssa.UnOp:
			if ins.Op == token.MUL {
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

		case *ssa.Convert:
			if isBorrowVal[ins.X] {
				isBorrowVal[ins] = true
			}
			if fld, ok := structHoldsBorrow[ins.X]; ok {
				structHoldsBorrow[ins] = fld
			}

		case *ssa.MakeInterface:
			if isBorrowVal[ins.X] || structHoldsBorrow[ins.X] != "" {
				sc.report(ins.Pos(), StorageViolation,
					"struct field or interface cannot box second-class borrow")
			}

		case *ssa.Return:
			for _, r := range ins.Results {
				if fld, ok := structHoldsBorrow[r]; ok {
					sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
						"borrow stored in struct field '%s' escapes stack frame", fld))
					continue
				}
				for sVal, fld := range structHoldsBorrow {
					if isDerivedFrom(r, sVal) {
						sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
							"borrow stored in struct field '%s' escapes stack frame", fld))
						break
					}
				}
			}

		case *ssa.Send:
			if fld, ok := structHoldsBorrow[ins.X]; ok {
				sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
					"borrow stored in struct field '%s' escapes stack frame", fld))
			}

		case *ssa.Go:
			cc := ins.Common()
			for _, arg := range cc.Args {
				if fld, ok := structHoldsBorrow[arg]; ok {
					sc.report(ins.Pos(), StorageViolation, fmt.Sprintf(
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
		return !a.Heap
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
