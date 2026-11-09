package annot

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// applyGuardedBy records `//safe:guarded_by mu` on a struct field or a package variable. mu names a
// field of the same struct (an embedded sync.Mutex is "Mutex") or, for a package variable, another
// package variable. It must be a sync.Mutex or sync.RWMutex held by value, so "the same mutex" is a
// question about paths, not about pointers. The guarded field or variable must be unexported: its
// accesses are checked in this package only.
func applyGuardedBy(pass *analysis.Pass, ix *Index, target ast.Node, args string, structOf map[*ast.Field]*ast.StructType, malformed func(string, ...any)) {
	if !isIdent(args) {
		malformed("//safe:guarded_by needs one mutex name")
		return
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
			if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok {
				guarded = append(guarded, v)
			}
		}
		if len(guarded) == 0 {
			malformed("//safe:guarded_by cannot guard an embedded field")
			return
		}
		mu = structField(pass, st, args)
		if mu == nil {
			malformed("//safe:guarded_by names %q, which is not a field of this struct", args)
			return
		}
	default:
		vs := valueSpec(target)
		if vs == nil {
			malformed("//safe:guarded_by must be on a struct field or a package variable")
			return
		}
		for _, id := range vs.Names {
			v, ok := pass.TypesInfo.Defs[id].(*types.Var)
			if !ok {
				continue
			}
			if v.Parent() != pass.Pkg.Scope() {
				malformed("//safe:guarded_by on a local variable is not supported yet; guard a struct field or package variable")
				return
			}
			guarded = append(guarded, v)
		}
		if len(guarded) == 0 {
			return
		}
		obj, ok := pass.Pkg.Scope().Lookup(args).(*types.Var)
		if !ok {
			malformed("//safe:guarded_by names %q, which is not a package variable", args)
			return
		}
		mu = obj
	}
	if why := notValueMutex(mu.Type()); why != "" {
		malformed("//safe:guarded_by names %s, which %s", args, why)
		return
	}
	for _, v := range guarded {
		switch {
		case v == mu:
			malformed("//safe:guarded_by: %s cannot guard itself", v.Name())
		case v.Exported():
			malformed("//safe:guarded_by on exported %s: accesses are checked in this package only, so guard an unexported field or variable", v.Name())
		default:
			ix.Guarded[v] = mu
		}
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
	ix.Holds[fn] = append(ix.Holds[fn], mu)
}

func isIdent(s string) bool { return s != "" && token.IsIdentifier(s) }

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
