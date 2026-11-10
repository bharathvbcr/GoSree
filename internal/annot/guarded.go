package annot

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// applyGuardedBy records `//safe:guarded_by mu` on a struct field, a package variable or a local
// variable (a var declaration or a := that declares only new names). mu names a field of the same
// struct (an embedded sync.Mutex is "Mutex"); for a package variable, another package variable; for a
// local, a variable in scope at its declaration (a local of this or an enclosing function, or a
// package variable). It must be a sync.Mutex or sync.RWMutex held by value, so "the same mutex" is a
// question about paths, not about pointers. A guarded field or package variable must be unexported:
// its accesses are checked in this package only.
func applyGuardedBy(pass *analysis.Pass, ix *Index, target ast.Node, args string, structOf map[*ast.Field]*ast.StructType, malformed func(string, ...any)) {
	parts := splitNames(args)
	if len(parts) == 0 || !isIdent(parts[0]) {
		malformed("//safe:guarded_by needs one mutex name")
		return
	}
	muName := parts[0]
	var filter map[string]bool
	if len(parts) > 1 {
		filter = make(map[string]bool, len(parts)-1)
		for _, p := range parts[1:] {
			if !isIdent(p) {
				malformed("//safe:guarded_by needs one mutex name")
				return
			}
			filter[p] = true
		}
	}
	var guarded []*types.Var
	var mu *types.Var
	switch t := target.(type) {
	case *ast.Field:
		st := structOf[t]
		if st == nil {
			malformed("//safe:guarded_by must be on a struct field or a package variable")
			return
		}
		for _, id := range t.Names {
			if filter != nil && !filter[id.Name] {
				continue
			}
			if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok {
				guarded = append(guarded, v)
			}
		}
		if len(guarded) == 0 {
			malformed("//safe:guarded_by cannot guard an embedded field")
			return
		}
		mu = structField(pass, st, muName)
		if mu == nil {
			malformed("//safe:guarded_by names %q, which is not a field of this struct", muName)
			return
		}
	default:
		names, ok := declaredNames(target)
		if !ok {
			malformed("//safe:guarded_by must be on a struct field or a variable declaration")
			return
		}
		for _, id := range names {
			if filter != nil && !filter[id.Name] {
				continue
			}
			v, ok := pass.TypesInfo.Defs[id].(*types.Var)
			if !ok {
				if _, blank := pass.TypesInfo.Defs[id]; !blank && id.Name != "_" {
					malformed("//safe:guarded_by on a := that redeclares %s; declare guarded variables on their own line", id.Name)
					return
				}
				continue
			}
			guarded = append(guarded, v)
		}
		if len(guarded) == 0 {
			return
		}
		if scope := guarded[0].Parent(); scope == pass.Pkg.Scope() {
			obj, ok := scope.Lookup(muName).(*types.Var)
			if !ok {
				malformed("//safe:guarded_by names %q, which is not a package variable", muName)
				return
			}
			mu = obj
		} else {
			// A local is guarded by a mutex in scope where it is declared: a local of this function or
			// an enclosing one, or a package variable.
			_, obj := scope.LookupParent(muName, guarded[0].Pos())
			v, ok := obj.(*types.Var)
			if !ok || v.IsField() {
				malformed("//safe:guarded_by names %q, which is not a variable in scope here", muName)
				return
			}
			mu = v
		}
	}
	if why := notValueMutex(mu.Type()); why != "" {
		malformed("//safe:guarded_by names %s, which %s", muName, why)
		return
	}
	// Every guarded name is checked before any is recorded: a directive that is reported has no effect.
	for _, v := range guarded {
		switch {
		case v == mu:
			malformed("//safe:guarded_by: %s cannot guard itself", v.Name())
			return
		case v.Exported() && (v.IsField() || v.Parent() == pass.Pkg.Scope()):
			malformed("//safe:guarded_by on exported %s: accesses are checked in this package only, so guard an unexported field or variable", v.Name())
			return
		case ix.Guarded[v] != nil:
			malformed("//safe:guarded_by on %s repeats an earlier guard (by %s): one guard per variable", v.Name(), ix.Guarded[v].Name())
			return
		}
	}
	for _, v := range guarded {
		ix.Guarded[v] = mu
	}
}

// applyHolds records `//safe:holds mu` on a function: mu resolves to a field of the receiver's struct
// first, then to a package variable. The ownership pass checks that every call site holds it.
func applyHolds(pass *analysis.Pass, ix *Index, target ast.Node, args string, malformed func(string, ...any)) {
	fd, ok := target.(*ast.FuncDecl)
	if !ok {
		malformed("//safe:holds must be in a function's doc comment")
		return
	}
	if !isIdent(args) {
		malformed("//safe:holds needs one mutex name")
		return
	}
	fn, ok := pass.TypesInfo.Defs[fd.Name].(*types.Func)
	if !ok {
		return
	}
	var mu *types.Var
	if recv := fn.Signature().Recv(); recv != nil {
		mu = fieldOf(recv.Type(), args)
	}
	if mu == nil {
		if v, ok := pass.Pkg.Scope().Lookup(args).(*types.Var); ok {
			mu = v
		}
	}
	if mu == nil {
		malformed("//safe:holds names %q, which is neither a field of the receiver nor a package variable", args)
		return
	}
	if why := notValueMutex(mu.Type()); why != "" {
		malformed("//safe:holds names %s, which %s", args, why)
		return
	}
	for _, held := range ix.Holds[fn] {
		if held == mu {
			malformed("//safe:holds %s repeats an earlier //safe:holds on %s", args, fn.Name())
			return
		}
	}
	ix.Holds[fn] = append(ix.Holds[fn], mu)
}

func isIdent(s string) bool { return s != "" && token.IsIdentifier(s) }

// declaredNames returns the names a variable declaration introduces: a var spec, or a := statement.
func declaredNames(n ast.Node) ([]*ast.Ident, bool) {
	if vs := valueSpec(n); vs != nil {
		return vs.Names, true
	}
	as, ok := n.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE {
		return nil, false
	}
	names := make([]*ast.Ident, 0, len(as.Lhs))
	for _, e := range as.Lhs {
		id, ok := e.(*ast.Ident)
		if !ok {
			return nil, false
		}
		names = append(names, id)
	}
	return names, true
}

func valueSpec(n ast.Node) *ast.ValueSpec {
	switch n := n.(type) {
	case *ast.ValueSpec:
		return n
	case *ast.GenDecl:
		if n.Tok == token.VAR && len(n.Specs) == 1 {
			vs, _ := n.Specs[0].(*ast.ValueSpec)
			return vs
		}
	case *ast.DeclStmt:
		return valueSpec(n.Decl)
	}
	return nil
}

// structField returns the field of st named name; an embedded field is named by its type.
func structField(pass *analysis.Pass, st *ast.StructType, name string) *types.Var {
	tv, ok := pass.TypesInfo.Types[st]
	if !ok {
		return nil
	}
	return fieldOf(tv.Type, name)
}

// fieldOf returns the direct field of t (or of what t points to) named name.
func fieldOf(t types.Type, name string) *types.Var {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	s, ok := t.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	for f := range s.Fields() {
		if f.Name() == name {
			return f
		}
	}
	return nil
}

// notValueMutex says why t cannot be a guard, or returns "".
func notValueMutex(t types.Type) string {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		if isSyncMutex(p.Elem()) {
			return "is a pointer to a mutex; use a mutex held by value"
		}
		return "is not a sync.Mutex or sync.RWMutex"
	}
	if isSyncMutex(t) {
		return ""
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "sync" && n.Obj().Name() == "Locker" {
		return "is a sync.Locker; use a sync.Mutex or sync.RWMutex held by value"
	}
	return "is not a sync.Mutex or sync.RWMutex"
}

func isSyncMutex(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != "sync" {
		return false
	}
	return n.Obj().Name() == "Mutex" || n.Obj().Name() == "RWMutex"
}

// IsRWMutex reports whether mu is a sync.RWMutex, whose RLock allows reads.
func IsRWMutex(mu *types.Var) bool {
	n, ok := types.Unalias(mu.Type()).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "sync" && n.Obj().Name() == "RWMutex"
}
