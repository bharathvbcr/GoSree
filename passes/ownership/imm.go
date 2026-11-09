package ownership

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gocap/internal/kinds"
)

// validateImm decides which //safe:imm annotations hold. An imm type must be a struct whose fields
// are all unexported (so only its own package can write them) and all shareable, except fields
// annotated //safe:guarded_by, which may have any type: SAFE009 checks every access to them under the
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
		st.report(st.ix.ImmTypes[tn], Imm, fmt.Sprintf("//safe:imm type %s is not immutable: %s", tn.Name(), msg))
	}
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

// checkImmStores reports SAFE005 for writes into imm memory outside its composite literal, and for
// taking the address of an imm variable (which would let it be reassigned under a sharer).
func (st *state) checkImmStores(fn *ssa.Function) {
	forEachInstr(fn, func(ins ssa.Instruction) {
		switch ins := ins.(type) {
		case *ssa.Store:
			st.checkImmStore(ins)
		case *ssa.Alloc:
			st.checkImmAddr(ins)
		}
	})
}

func (st *state) checkImmStore(s *ssa.Store) {
	// Direct store into a variable of imm type: reassigning a local variable is allowed; storing
	// through any other pointer to an imm type is a write to shared immutable memory.
	if t := pointee(s.Addr.Type()); t != nil && st.isImmNamed(t) {
		if a, ok := s.Addr.(*ssa.Alloc); ok {
			if a.Comment != "complit" || st.inComplit(a, s.Pos()) {
				return
			}
		}
		st.report(s.Pos(), Imm, fmt.Sprintf("store through a *%s overwrites an immutable value", typeName(t)))
		return
	}
	// Field or element store: walk back to the root, noting whether any step goes through imm memory.
	var immType types.Type
	x := s.Addr
	firstPos := token.NoPos
loop:
	for {
		switch a := x.(type) {
		case *ssa.FieldAddr:
			if _, guarded := st.guardedAt(a); guarded {
				return // a guarded field is written under its mutex; SAFE009 checks it
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
		default:
			break loop
		}
	}
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
	st.report(pos, Imm, fmt.Sprintf("write to a field of %s, which is //safe:imm; imm values are only written by their composite literal", typeName(immType)))
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
		st.report(posOf(r, r), Imm, fmt.Sprintf("taking the address of %s, a variable of //safe:imm type %s, exposes it to reassignment; construct shared imm values with &%s{...}",
			describe(a), typeName(t), typeName(t)))
		return
	}
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
