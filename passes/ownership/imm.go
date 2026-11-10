package ownership

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gosree/internal/annot"
	"github.com/bharathvbcr/gosree/internal/kinds"
)

// validateImm decides which //gosree:imm annotations hold. An imm type must be a struct whose fields
// are all unexported (so only its own package can write them) and all shareable, except fields
// annotated //gosree:guarded_by, which may have any type: SAFE009 checks every access to them under the
// struct's own mutex. Validity is computed to a fixpoint because a field may point to another imm
// type of the same package.
func (st *state) validateImm() {
	st.imm = map[*types.TypeName]bool{}
	for tn := range st.ix.ImmTypes {
		st.imm[tn] = true
	}
	problems := map[*types.TypeName]string{}
	for changed := true; changed; {
		changed = false
		cls := kinds.NewClassifier(func(tn *types.TypeName) bool {
			if tn.Pkg() == st.pass.Pkg {
				return st.imm[tn]
			}
			var f ImmFact
			return st.pass.ImportObjectFact(tn, &f) && f.Imm
		})
		for tn, ok := range st.imm {
			if !ok {
				continue
			}
			if msg := immShapeProblem(tn, cls, st.ix.Guarded); msg != "" {
				st.imm[tn] = false
				problems[tn] = msg
				changed = true
			}
		}
	}
	for tn, msg := range problems {
		prefix := annot.Prefix + "imm"
		st.report(st.ix.ImmTypes[tn], Imm, fmt.Sprintf("%s type %s is not immutable: %s", prefix, tn.Name(), msg))
	}
}

func immNestedProblem(fieldPath string, fieldType, curType, rootNamed types.Type, cls *kinds.Classifier, seen map[types.Type]bool) string {
	cur := types.Unalias(curType)
	if seen[cur] {
		return ""
	}
	if _, isNamed := cur.(*types.Named); isNamed && cur != rootNamed {
		return ""
	}
	seen[cur] = true
	switch u := cur.Underlying().(type) {
	case *types.Chan:
		if !cls.Shareable(u.Elem()) {
			return fmt.Sprintf("field %s has type %s", fieldPath, fieldType)
		}
		if prob := immNestedProblem(fieldPath, fieldType, u.Elem(), rootNamed, cls, seen); prob != "" {
			return prob
		}
	case *types.Array:
		if prob := immNestedProblem(fieldPath, fieldType, u.Elem(), rootNamed, cls, seen); prob != "" {
			return prob
		}
	case *types.Struct:
		for sf := range u.Fields() {
			subPath := fieldPath + "." + sf.Name()
			if sf.Exported() {
				return fmt.Sprintf("field %s is exported, so other packages could write it", subPath)
			}
			if prob := immNestedProblem(subPath, sf.Type(), sf.Type(), rootNamed, cls, seen); prob != "" {
				return prob
			}
		}
	}
	return ""
}

func immShapeProblem(tn *types.TypeName, cls *kinds.Classifier, guarded map[*types.Var]*types.Var) string {
	if _, isNamed := tn.Type().(*types.Named); !isNamed {
		return "it must be a defined struct type"
	}
	s, ok := tn.Type().Underlying().(*types.Struct)
	if !ok {
		return "it must be a struct type"
	}
	for f := range s.Fields() {
		if f.Exported() {
			return fmt.Sprintf("field %s is exported, so other packages could write it", f.Name())
		}
		if guarded[f.Origin()] != nil {
			continue
		}
		if prob := immNestedProblem(f.Name(), f.Type(), f.Type(), tn.Type(), cls, map[types.Type]bool{}); prob != "" {
			return prob
		}
		if !cls.Shareable(f.Type()) {
			return fmt.Sprintf("field %s has type %s, which can hold shared mutable memory (imm fields may be pointer-free values, strings, pointers to imm types, or shared-by-design types)", f.Name(), f.Type())
		}
	}
	return ""
}

func (st *state) isImmNamed(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	tn := n.Origin().Obj()
	if tn.Pkg() == st.pass.Pkg {
		return st.imm[tn]
	}
	var f ImmFact
	return st.pass.ImportObjectFact(tn, &f) && f.Imm
}

func pointee(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return nil
}

// checkImmStores reports SAFE005 for writes into imm memory outside its composite literal, for
// taking the address of an imm variable (which would let it be reassigned under a sharer), and for
// mutable borrows targeting fields of immutable types.
func (st *state) checkImmStores(fn *ssa.Function) {
	forEachInstr(fn, func(ins ssa.Instruction) {
		switch ins := ins.(type) {
		case *ssa.Store:
			st.checkImmStore(ins)
		case *ssa.Alloc:
			st.checkImmAddr(ins)
		case *ssa.FieldAddr:
			st.checkImmFieldCall(ins)
		default:
			if w, ok := writeOf(ins); ok {
				st.checkImmWrite(ins, w.addr)
			}
		}
	})
	st.checkImmBorrows(fn)
}

func (st *state) immInValueType(t types.Type) types.Type {
	return st.immInValueTypeSeen(t, map[types.Type]bool{})
}

func (st *state) immInValueTypeSeen(t types.Type, seen map[types.Type]bool) types.Type {
	if t == nil || seen[t] || len(seen) >= 16 {
		return nil
	}
	seen[t] = true
	if st.isImmNamed(t) {
		return t
	}
	switch u := t.Underlying().(type) {
	case *types.Array:
		return st.immInValueTypeSeen(u.Elem(), seen)
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			if st.ix.Guarded[f.Origin()] != nil {
				continue
			}
			if it := st.immInValueTypeSeen(f.Type(), seen); it != nil {
				return it
			}
		}
	}
	return nil
}

func (st *state) checkImmWrite(ins ssa.Instruction, addr ssa.Value) {
	immType, x, firstPos := st.traceImmAddr(addr, map[ssa.Value]bool{})
	if immType == nil && addr != nil && addr.Type() != nil {
		if sl, ok := addr.Type().Underlying().(*types.Slice); ok {
			immType = st.immInValueType(sl.Elem())
		}
	}
	if immType == nil {
		return
	}
	if a, ok := x.(*ssa.Alloc); ok && a.Comment == "complit" && st.inComplit(a, firstPos) {
		return
	}
	pos := ins.Pos()
	if !pos.IsValid() {
		pos = firstPos
	}
	prefix := annot.Prefix + "imm"
	st.report(pos, Imm, fmt.Sprintf("write to a field of %s, which is %s; imm values are only written by their composite literal", typeName(immType), prefix))
}

func (st *state) traceImmAddr(v ssa.Value, seen map[ssa.Value]bool) (types.Type, ssa.Value, token.Pos) {
	var immType types.Type
	x := v
	firstPos := token.NoPos
	for x != nil && !seen[x] && len(seen) < 64 {
		seen[x] = true
		switch a := x.(type) {
		case *ssa.FieldAddr:
			if _, guarded := st.guardedAt(a); guarded {
				return nil, nil, token.NoPos // a guarded field is written under its mutex; SAFE009 checks it
			}
			if t := pointee(a.X.Type()); t != nil && st.isImmNamed(t) && immType == nil {
				immType = t
			}
			if !firstPos.IsValid() {
				firstPos = a.Pos()
			}
			x = a.X
		case *ssa.IndexAddr:
			if !firstPos.IsValid() {
				firstPos = a.Pos()
			}
			x = a.X
		case *ssa.Slice:
			if !firstPos.IsValid() {
				firstPos = a.Pos()
			}
			x = a.X
		case *ssa.ChangeType:
			if t := pointee(a.X.Type()); t != nil && st.isImmNamed(t) && immType == nil {
				immType = t
			}
			if !firstPos.IsValid() {
				firstPos = a.Pos()
			}
			x = a.X
		case *ssa.Convert:
			x = a.X
		case *ssa.Call:
			if callee := a.Call.StaticCallee(); callee != nil && len(callee.Blocks) > 0 {
				for _, b := range callee.Blocks {
					for _, ins := range b.Instrs {
						ret, ok := ins.(*ssa.Return)
						if !ok || len(ret.Results) != 1 {
							continue
						}
						subSeen := make(map[ssa.Value]bool, len(seen))
						for k, val := range seen {
							subSeen[k] = val
						}
						it, root, _ := st.traceImmAddr(ret.Results[0], subSeen)
						if p, ok := root.(*ssa.Parameter); ok {
							for pi, cp := range callee.Params {
								if cp == p && pi < len(a.Call.Args) {
									if it != nil {
										if !firstPos.IsValid() {
											firstPos = a.Pos()
										}
										return it, a.Call.Args[pi], firstPos
									}
									if argIt, argRoot, _ := st.traceImmAddr(a.Call.Args[pi], seen); argIt != nil {
										if !firstPos.IsValid() {
											firstPos = a.Pos()
										}
										return argIt, argRoot, firstPos
									}
								}
							}
						} else if it != nil {
							if !firstPos.IsValid() {
								firstPos = a.Pos()
							}
							return it, root, firstPos
						}
					}
				}
			}
			return immType, x, firstPos
		case *ssa.Phi:
			for _, e := range a.Edges {
				if it, root, fp := st.traceImmAddr(e, seen); it != nil {
					if !firstPos.IsValid() {
						firstPos = fp
					}
					return it, root, firstPos
				}
			}
			return immType, x, firstPos
		case *ssa.UnOp:
			if a.Op == token.MUL && a.X != nil && a.X.Referrers() != nil {
				for _, ref := range *a.X.Referrers() {
					if store, ok := ref.(*ssa.Store); ok && store.Addr == a.X {
						if it, root, fp := st.traceImmAddr(store.Val, seen); it != nil {
							if !firstPos.IsValid() {
								firstPos = fp
							}
							return it, root, firstPos
						}
					}
				}
			}
			return immType, x, firstPos
		default:
			return immType, x, firstPos
		}
	}
	return immType, x, firstPos
}

func (st *state) checkImmFieldCall(fa *ssa.FieldAddr) {
	if _, guarded := st.guardedAt(fa); guarded {
		return
	}
	if st.cls != nil && st.cls.Shareable(fa.Type()) {
		return
	}
	immType := pointee(fa.X.Type())
	if immType == nil || !st.isImmNamed(immType) {
		return
	}
	if a, ok := fa.X.(*ssa.Alloc); ok && a.Comment == "complit" && st.inComplit(a, fa.Pos()) {
		return
	}
	st.checkImmDerivedCalls(fa, immType, fa, map[ssa.Value]bool{})
}

func (st *state) checkImmDerivedCalls(fa *ssa.FieldAddr, immType types.Type, val ssa.Value, seen map[ssa.Value]bool) {
	if val == nil || seen[val] || len(seen) >= 64 || val.Referrers() == nil {
		return
	}
	seen[val] = true
	for _, ref := range *val.Referrers() {
		switch r := ref.(type) {
		case *ssa.FieldAddr, *ssa.IndexAddr, *ssa.Slice, *ssa.ChangeType, *ssa.Convert, *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.Phi, *ssa.UnOp:
			st.checkImmDerivedCalls(fa, immType, r.(ssa.Value), seen)
		case *ssa.Store:
			if r.Val == val {
				if ia, ok := r.Addr.(*ssa.IndexAddr); ok {
					st.checkImmDerivedCalls(fa, immType, ia.X, seen)
				} else if alloc, ok := r.Addr.(*ssa.Alloc); ok {
					st.checkImmDerivedCalls(fa, immType, alloc, seen)
				}
			}
		case *ssa.Call:
			cc := r.Common()
			if _, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
				continue
			}
			callee := cc.StaticCallee()
			if isCapBorrowHelper(callee) {
				continue
			}
			for i, arg := range cc.Args {
				if arg != val {
					continue
				}
				if callee != nil && i < len(callee.Params) {
					if vr, ok := callee.Params[i].Object().(*types.Var); ok {
						if st.ix.IsRob(vr) {
							continue
						}
						if st.ix.IsMub(vr) {
							continue // checkImmBorrows reports this
						}
					}
					if sum := st.sums[callee]; sum != nil && !sum.pessimistic {
						if i < len(sum.writeParam) && !sum.writeParam[i] && i < len(sum.leakParam) && !sum.leakParam[i] {
							nArgs := len(cc.Args)
							for _, l := range sum.links {
								if (l.A == i && l.B >= nArgs) || (l.B == i && l.A >= nArgs) {
									st.checkImmDerivedCalls(fa, immType, r, seen)
									break
								}
							}
							continue
						}
					}
				}
				pos := r.Pos()
				if !pos.IsValid() {
					pos = fa.Pos()
				}
				prefix := annot.Prefix + "imm"
				st.report(pos, Imm, fmt.Sprintf("write to a field of %s, which is %s; imm values are only written by their composite literal", typeName(immType), prefix))
			}
		}
	}
}

func (st *state) checkImmStore(s *ssa.Store) {
	// Direct store into a variable of imm type (or a struct/array containing an imm value):
	// reassigning a local variable is allowed; storing through any other pointer is a write to shared immutable memory.
	if pt := pointee(s.Addr.Type()); pt != nil {
		if t := st.immInValueType(pt); t != nil {
			if a, ok := s.Addr.(*ssa.Alloc); ok {
				if a.Comment != "complit" || st.inComplit(a, s.Pos()) {
					return
				}
			}
			st.report(s.Pos(), Imm, fmt.Sprintf("store through a *%s overwrites an immutable value", typeName(pt)))
			return
		}
	}
	// Field or element store: walk back to the root, noting whether any step goes through imm memory.
	immType, x, firstPos := st.traceImmAddr(s.Addr, map[ssa.Value]bool{})
	if immType == nil {
		return
	}
	if a, ok := x.(*ssa.Alloc); ok && a.Comment == "complit" && st.inComplit(a, firstPos) {
		return
	}
	pos := s.Pos()
	if !pos.IsValid() {
		pos = firstPos
	}
	prefix := annot.Prefix + "imm"
	st.report(pos, Imm, fmt.Sprintf("write to a field of %s, which is %s; imm values are only written by their composite literal", typeName(immType), prefix))
}

func (st *state) checkImmAddr(a *ssa.Alloc) {
	if a.Comment == "complit" {
		return
	}
	t := pointee(a.Type())
	if t == nil || !st.isImmNamed(t) {
		return
	}
	for _, r := range *a.Referrers() {
		switch r := r.(type) {
		case *ssa.Store:
			if r.Addr == a {
				continue
			}
		case *ssa.UnOp:
			if r.Op == token.MUL {
				continue
			}
		case *ssa.DebugRef, *ssa.MakeClosure:
			continue
		case *ssa.FieldAddr:
			if onlyReads(r) {
				continue
			}
		}
		prefix := annot.Prefix + "imm"
		st.report(posOf(r, r), Imm, fmt.Sprintf("taking the address of %s, a variable of %s type %s, exposes it to reassignment; construct shared imm values with &%s{...}",
			describe(a), prefix, typeName(t), typeName(t)))
		return
	}
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

func (st *state) checkImmBorrows(fn *ssa.Function) {
	syntax := fn.Syntax()
	if syntax == nil {
		return
	}
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				id, ok := unparen(lhs).(*ast.Ident)
				if !ok {
					continue
				}
				vr, _ := st.pass.TypesInfo.Defs[id].(*types.Var)
				if vr == nil {
					vr, _ = st.pass.TypesInfo.Uses[id].(*types.Var)
				}
				isMutBorrow := (vr != nil && st.ix.IsMub(vr)) || st.ix.BorrowAt(stmt.Pos()) == annot.BorrowMut
				if !isMutBorrow {
					continue
				}
				if i < len(stmt.Rhs) {
					st.checkMutBorrowExpr(stmt.Pos(), stmt.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range stmt.Names {
				vr, _ := st.pass.TypesInfo.Defs[name].(*types.Var)
				isMutBorrow := (vr != nil && st.ix.IsMub(vr)) || st.ix.BorrowAt(stmt.Pos()) == annot.BorrowMut
				if !isMutBorrow {
					continue
				}
				if i < len(stmt.Values) {
					st.checkMutBorrowExpr(stmt.Pos(), stmt.Values[i])
				}
			}
		case *ast.CallExpr:
			tv := st.pass.TypesInfo.TypeOf(stmt.Fun)
			if sig, ok := tv.(*types.Signature); ok {
				for i, arg := range stmt.Args {
					if i < sig.Params().Len() {
						param := sig.Params().At(i)
						if st.ix.IsMub(param) {
							st.checkMutBorrowExpr(arg.Pos(), arg)
						}
					}
				}
			}
		}
		return true
	})
}

func (st *state) checkMutBorrowExpr(pos token.Pos, expr ast.Expr) {
	switch e := unparen(expr).(type) {
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			x := unparen(e.X)
			if sel, ok := x.(*ast.SelectorExpr); ok {
				if imm := st.enclosingImmType(sel.X); imm != nil {
					st.report(pos, Imm, fmt.Sprintf("cannot borrow field of immutable type %s as mutable", typeName(imm)))
				}
			} else if idx, ok := x.(*ast.IndexExpr); ok {
				if imm := st.enclosingImmType(idx.X); imm != nil {
					st.report(pos, Imm, fmt.Sprintf("cannot borrow field of immutable type %s as mutable", typeName(imm)))
				}
			} else {
				tv := st.pass.TypesInfo.TypeOf(x)
				if tv != nil {
					elem := pointee(tv)
					if elem == nil {
						elem = tv
					}
					if st.isImmNamed(elem) {
						st.report(pos, Imm, fmt.Sprintf("cannot borrow immutable type %s as mutable", typeName(elem)))
					}
				}
			}
		}
	case *ast.Ident, *ast.SelectorExpr:
		tv := st.pass.TypesInfo.TypeOf(e)
		if tv != nil {
			elem := pointee(tv)
			if elem == nil {
				elem = tv
			}
			if st.isImmNamed(elem) {
				st.report(pos, Imm, fmt.Sprintf("cannot borrow immutable type %s as mutable", typeName(elem)))
			}
		}
	case *ast.CallExpr:
		tv := st.pass.TypesInfo.TypeOf(e)
		if tv != nil {
			elem := pointee(tv)
			if elem == nil {
				elem = tv
			}
			if st.isImmNamed(elem) {
				st.report(pos, Imm, fmt.Sprintf("cannot borrow immutable type %s as mutable", typeName(elem)))
			}
		}
		for _, arg := range e.Args {
			st.checkMutBorrowExpr(pos, arg)
		}
	}
}

func (st *state) enclosingImmType(expr ast.Expr) types.Type {
	curr := expr
	for curr != nil {
		currExpr := unparen(curr)
		tv := st.pass.TypesInfo.TypeOf(currExpr)
		if tv != nil {
			elem := pointee(tv)
			if elem == nil {
				elem = tv
			}
			if st.isImmNamed(elem) {
				return elem
			}
		}
		switch c := currExpr.(type) {
		case *ast.SelectorExpr:
			curr = c.X
		case *ast.StarExpr:
			curr = c.X
		case *ast.IndexExpr:
			curr = c.X
		default:
			curr = nil
		}
	}
	return nil
}

func onlyReads(v ssa.Value) bool {
	for _, r := range *v.Referrers() {
		switch r := r.(type) {
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return false
			}
		case *ssa.FieldAddr:
			if !onlyReads(r) {
				return false
			}
		case *ssa.DebugRef:
		default:
			return false
		}
	}
	return true
}

func typeName(t types.Type) string {
	if n, ok := types.Unalias(t).(*types.Named); ok {
		return n.Obj().Name()
	}
	return t.String()
}

// inComplit reports whether pos lies inside the composite literal that allocated a.
func (st *state) inComplit(a *ssa.Alloc, pos token.Pos) bool {
	if !pos.IsValid() {
		return false
	}
	lit := st.complitAt(a.Pos())
	return lit != nil && pos >= lit.Lbrace && pos <= lit.Rbrace
}

func (st *state) complitAt(lbrace token.Pos) *ast.CompositeLit {
	for _, f := range st.pass.Files {
		if lbrace < f.FileStart || lbrace > f.FileEnd {
			continue
		}
		var found *ast.CompositeLit
		ast.Inspect(f, func(n ast.Node) bool {
			if found != nil {
				return false
			}
			if cl, ok := n.(*ast.CompositeLit); ok && cl.Lbrace == lbrace {
				found = cl
				return false
			}
			return n == nil || (n.Pos() <= lbrace && lbrace <= n.End())
		})
		return found
	}
	return nil
}
