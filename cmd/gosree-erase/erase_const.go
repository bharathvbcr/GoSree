package main

import (
	"go/ast"
	"go/token"
)

// constClass says what is known, from this file alone, about whether an
// expression is a constant expression.
type constClass int

const (
	classNot     constClass = iota // not a constant (a variable, a call, ...)
	classUntyped                   // an untyped constant of a known kind
	classTyped                     // a typed constant
	classUnknown                   // may be a constant: a name declared elsewhere (another file, an imported package)
)

// untypedKind is the kind of an untyped constant; its default type follows.
type untypedKind int

const (
	kindInt untypedKind = iota
	kindRune
	kindFloat
	kindComplex
	kindString
	kindBool
)

func (k untypedKind) numeric() bool { return k <= kindComplex }

type constInfo struct {
	class constClass
	kind  untypedKind // for classUntyped
	typ   ast.Expr    // for classTyped: the constant's type when it can be named safely here, else nil
}

var (
	infoNot     = constInfo{class: classNot}
	infoUnknown = constInfo{class: classUnknown}
)

func untyped(k untypedKind) constInfo { return constInfo{class: classUntyped, kind: k} }

func typed(t ast.Expr) constInfo { return constInfo{class: classTyped, typ: t} }

// basicType returns the identifier for a predeclared type, or nil when this
// file redeclares the name (the identifier would then name something else).
func (e *eraser) basicType(name string) ast.Expr {
	if e.declared[name] {
		return nil
	}
	return ast.NewIdent(name)
}

// nameableType returns t if it can be repeated elsewhere in this file with the
// same meaning: a predeclared type, a type declared at file level, or a
// qualified name.
func (e *eraser) nameableType(t ast.Expr) ast.Expr {
	switch t := t.(type) {
	case *ast.Ident:
		if t.Obj == nil && basicTypeNames[t.Name] {
			return e.basicType(t.Name)
		}
		if t.Obj != nil && t.Obj.Kind == ast.Typ && e.file.Scope != nil && e.file.Scope.Lookup(t.Name) == t.Obj {
			return ast.NewIdent(t.Name)
		}
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Obj == nil {
			return cloneType(t)
		}
	}
	return nil
}

// classify decides, syntactically and conservatively, whether x is a constant
// expression. It consults only this file: identifiers it cannot resolve are
// classUnknown, never assumed to be variables.
func (e *eraser) classify(x ast.Expr) constInfo {
	e.depth++
	defer func() { e.depth-- }()
	if e.depth > 64 {
		return infoUnknown
	}
	switch x := x.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT:
			return untyped(kindInt)
		case token.CHAR:
			return untyped(kindRune)
		case token.FLOAT:
			return untyped(kindFloat)
		case token.IMAG:
			return untyped(kindComplex)
		case token.STRING:
			return untyped(kindString)
		}
		return infoNot
	case *ast.ParenExpr:
		return e.classify(x.X)
	case *ast.Ident:
		return e.classifyIdent(x)
	case *ast.UnaryExpr:
		switch x.Op {
		case token.ADD, token.SUB, token.XOR, token.NOT:
			return e.classify(x.X)
		}
		return infoNot
	case *ast.BinaryExpr:
		return e.classifyBinary(x)
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil {
			return infoUnknown // pkg.Name, or a field of a variable declared in another file
		}
		return infoNot
	case *ast.CallExpr:
		return e.classifyCall(x)
	}
	return infoNot
}

func (e *eraser) classifyIdent(id *ast.Ident) constInfo {
	if id.Obj == nil {
		switch id.Name {
		case "true", "false":
			return untyped(kindBool)
		case "iota":
			return untyped(kindInt)
		case "nil":
			return infoNot
		}
		return infoUnknown
	}
	if id.Obj.Kind != ast.Con {
		return infoNot
	}
	spec, ok := id.Obj.Decl.(*ast.ValueSpec)
	if !ok {
		return infoUnknown
	}
	eff := e.consts[spec]
	if eff.typ != nil {
		return typed(e.nameableType(eff.typ))
	}
	for i, n := range spec.Names {
		if n.Name == id.Obj.Name && i < len(eff.values) {
			return e.classify(eff.values[i])
		}
	}
	return infoUnknown
}

func (e *eraser) classifyBinary(b *ast.BinaryExpr) constInfo {
	l, r := e.classify(b.X), e.classify(b.Y)
	switch b.Op {
	case token.SHL, token.SHR:
		// A shift by a non-constant is not a constant; a constant shift of an
		// untyped constant is an integer constant.
		switch {
		case l.class == classNot || r.class == classNot:
			return infoNot
		case l.class == classUnknown || r.class == classUnknown:
			return infoUnknown
		case l.class == classTyped:
			return typed(l.typ)
		}
		k := l.kind
		if k != kindInt && k != kindRune {
			k = kindInt
		}
		return untyped(k)
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		// Comparing constants gives an untyped boolean constant.
		switch {
		case l.class == classNot || r.class == classNot:
			return infoNot
		case l.class == classUnknown || r.class == classUnknown:
			return infoUnknown
		}
		return untyped(kindBool)
	}
	return combine(l, r)
}

// combine is the class of an arithmetic or logical operation on l and r.
func combine(l, r constInfo) constInfo {
	switch {
	case l.class == classNot || r.class == classNot:
		return infoNot
	case l.class == classUnknown || r.class == classUnknown:
		return infoUnknown
	case l.class == classTyped:
		return typed(l.typ)
	case r.class == classTyped:
		return typed(r.typ)
	}
	k := l.kind
	if r.kind > k && r.kind.numeric() && k.numeric() {
		k = r.kind
	}
	return untyped(k)
}

var unsafeConsts = map[string]bool{"Sizeof": true, "Alignof": true, "Offsetof": true}

func (e *eraser) classifyCall(c *ast.CallExpr) constInfo {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		if f.Obj != nil {
			return infoNot
		}
		switch {
		case basicTypeNames[f.Name] && len(c.Args) == 1:
			// T(c) of a constant is a typed constant.
			switch a := e.classify(c.Args[0]); a.class {
			case classNot:
				return infoNot
			case classUnknown:
				return infoUnknown
			}
			return typed(e.basicType(f.Name))
		case f.Name == "len" || f.Name == "cap":
			// Constant for a constant string argument. (Also constant for an
			// array-typed operand, which cannot be told apart here.)
			if len(c.Args) == 1 && e.classify(c.Args[0]).class == classUntyped {
				return typed(e.basicType("int"))
			}
			return infoNot
		case f.Name == "min" || f.Name == "max" || f.Name == "real" || f.Name == "imag" || f.Name == "complex":
			out := untyped(kindInt)
			for i, a := range c.Args {
				if i == 0 {
					out = e.classify(a)
					continue
				}
				out = combine(out, e.classify(a))
			}
			return out
		}
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok && id.Obj == nil && id.Name == "unsafe" && unsafeConsts[f.Sel.Name] {
			return typed(e.basicType("uintptr"))
		}
	}
	return infoNot
}
