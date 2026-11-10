package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

const (
	capPath      = "github.com/bharathvbcr/gocap/cap"
	rootPath     = "github.com/bharathvbcr/gocap"
	sreeCapPath  = "github.com/bharathvbcr/gosree/cap"
	sreeRootPath = "github.com/bharathvbcr/gosree"
)

// markerTypes are the generic alias types of package cap: cap.Mut[T] = *T.
var markerTypes = map[string]bool{"Mut": true, "Mub": true, "Ref": true, "Rob": true, "Iso": true, "Imm": true}

// helperFuncs are the identity helpers of package cap.
var helperFuncs = map[string]bool{"BorrowMut": true, "Borrow": true, "BorrowRef": true, "BorrowMub": true, "BorrowRob": true, "Move": true}

// capImport is one import of package cap (or the gocap root path).
type capImport struct {
	spec  *ast.ImportSpec
	path  string
	local string // local package name; empty for dot and blank imports
	dot   bool
	blank bool
}

// eraser holds the state of erasing one parsed file.
//
// Name resolution: go/parser resolves every identifier that is declared in the
// file (locals, parameters, receivers, type parameters, package-level
// declarations) to an *ast.Object. An identifier that names an import, a
// universe name, or a declaration in another file of the package is left with
// a nil Obj. Go forbids a file-scope import name (or dot-imported name) from
// also being declared in the package block, so for an identifier spelled like
// the cap import, "Obj == nil" is exactly "refers to the import". Every
// decision of this tool about "is this the cap package" goes through member.
type eraser struct {
	fset     *token.FileSet
	file     *ast.File
	imports  []capImport
	declared map[string]bool // every name declared or resolved to a declaration in the file
	consts   map[*ast.ValueSpec]effConst
	stack    []ast.Node // ancestors of the node being visited, the node itself last
	problems []string
	depth    int
}

// effConst is the type and values a constant spec has after implicit repetition.
type effConst struct {
	typ    ast.Expr
	values []ast.Expr
}

// Erase transforms gocap-annotated Go source into standard Go source.
//
// A file with no gocap content (no cap import and no directive comment) is
// returned byte-for-byte. A construct that cannot be erased without changing
// the program makes Erase fail with a description instead of guessing.
func Erase(src []byte) ([]byte, error) {
	cleanSrc := stripDirectives(src)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "source.go", cleanSrc, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing source: %w", err)
	}

	e := &eraser{
		fset:     fset,
		file:     f,
		declared: map[string]bool{},
		consts:   map[*ast.ValueSpec]effConst{},
	}
	e.collectImports()
	if len(e.imports) == 0 {
		if bytes.Equal(cleanSrc, src) {
			return src, nil
		}
		return formatFile(fset, f)
	}

	e.index()
	e.refuseEmbeddedMarkers()

	f = astutil.Apply(f,
		func(c *astutil.Cursor) bool {
			e.stack = append(e.stack, c.Node())
			return true
		},
		func(c *astutil.Cursor) bool {
			e.visit(c)
			e.stack = e.stack[:len(e.stack)-1]
			return true
		}).(*ast.File)
	e.file = f

	e.reportResidue()
	if len(e.problems) > 0 {
		return nil, fmt.Errorf("cannot erase this file safely: %s", strings.Join(e.problems, "; "))
	}

	for _, imp := range e.imports {
		switch {
		case imp.dot:
			astutil.DeleteNamedImport(fset, f, ".", imp.path)
		case imp.blank:
			astutil.DeleteNamedImport(fset, f, "_", imp.path)
		case imp.spec.Name != nil:
			astutil.DeleteNamedImport(fset, f, imp.local, imp.path)
		default:
			astutil.DeleteImport(fset, f, imp.path)
		}
	}
	return formatFile(fset, f)
}

func formatFile(fset *token.FileSet, f *ast.File) ([]byte, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, fmt.Errorf("formatting node: %w", err)
	}
	return format.Source(buf.Bytes())
}

func (e *eraser) problem(pos token.Pos, msg string, args ...any) {
	p := e.fset.Position(pos)
	e.problems = append(e.problems, fmt.Sprintf("%d:%d: %s", p.Line, p.Column, fmt.Sprintf(msg, args...)))
}

func (e *eraser) collectImports() {
	for _, spec := range e.file.Imports {
		path := strings.Trim(spec.Path.Value, "`\"")
		if path != capPath && path != rootPath && path != sreeCapPath && path != sreeRootPath {
			continue
		}
		imp := capImport{spec: spec, path: path}
		switch {
		case spec.Name == nil && (path == capPath || path == sreeCapPath):
			imp.local = "cap"
		case spec.Name == nil && path == sreeRootPath:
			imp.local = "gosree"
		case spec.Name == nil:
			imp.local = "gocap"
		case spec.Name.Name == ".":
			imp.dot = true
		case spec.Name.Name == "_":
			imp.blank = true
		default:
			imp.local = spec.Name.Name
		}
		e.imports = append(e.imports, imp)
	}
}

// index records the names declared in the file and the effective type and
// values of every constant spec.
func (e *eraser) index() {
	ast.Inspect(e.file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Obj != nil {
				e.declared[x.Obj.Name] = true
			}
		case *ast.GenDecl:
			if x.Tok != token.CONST {
				return true
			}
			var last effConst
			for _, s := range x.Specs {
				vs := s.(*ast.ValueSpec)
				if vs.Type != nil || len(vs.Values) > 0 {
					last = effConst{vs.Type, vs.Values}
				}
				e.consts[vs] = last
			}
		}
		return true
	})
}

// member reports which member of package cap x names: cap.Move through a
// package import, or Move through a dot import. Shadowing identifiers (Obj !=
// nil) never match.
func (e *eraser) member(x ast.Expr) (string, bool) {
	switch x := x.(type) {
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok || id.Obj != nil || !(markerTypes[x.Sel.Name] || helperFuncs[x.Sel.Name]) {
			return "", false
		}
		for _, imp := range e.imports {
			if imp.local != "" && id.Name == imp.local {
				return x.Sel.Name, true
			}
		}
	case *ast.Ident:
		if x.Obj != nil || !(markerTypes[x.Name] || helperFuncs[x.Name]) {
			return "", false
		}
		for _, imp := range e.imports {
			if imp.dot {
				return x.Name, true
			}
		}
	}
	return "", false
}

// refuseEmbeddedMarkers rejects struct fields that embed a marker type. The
// alias makes the field's name the marker's name (Mut); erasing it to *T
// would rename the field to T, and erasing it to a named field would drop the
// promoted methods and fields. No plain-Go spelling is equivalent.
func (e *eraser) refuseEmbeddedMarkers() {
	ast.Inspect(e.file, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fld := range st.Fields.List {
			if len(fld.Names) != 0 {
				continue
			}
			t := fld.Type
			if s, ok := t.(*ast.StarExpr); ok {
				t = s.X
			}
			if ix, ok := t.(*ast.IndexExpr); ok {
				if name, ok := e.member(ix.X); ok && markerTypes[name] {
					e.problem(fld.Pos(), "embedded field %s: erasure would rename the field from %s and change which fields and methods are promoted", types.ExprString(fld.Type), name)
				}
			}
		}
		return true
	})
}

// visit rewrites one node; it runs after the node's children (post-order).
func (e *eraser) visit(c *astutil.Cursor) {
	switch n := c.Node().(type) {
	case *ast.IndexExpr:
		name, ok := e.member(n.X)
		if !ok {
			return
		}
		switch {
		case markerTypes[name]:
			c.Replace(&ast.StarExpr{Star: n.Pos(), X: n.Index})
		case helperFuncs[name]:
			if _, isCall := c.Parent().(*ast.CallExpr); isCall && c.Name() == "Fun" {
				return // instantiated and called: the call is rewritten instead
			}
			c.Replace(identityFunc(n.Pos(), name, n.Index))
		}

	case *ast.CallExpr:
		if e.inStatementPosition() {
			return
		}
		h, ok := e.helperCall(n)
		if !ok {
			return
		}
		repl, err := e.replacement(h, e.sensitive(n))
		if err != nil {
			e.problem(n.Pos(), "%s: %v", types.ExprString(n.Fun), err)
			return
		}
		c.Replace(e.guardHeader(repl))

	case *ast.ExprStmt:
		e.eraseStatement(c, n.Pos(), unparen(n.X))
	case *ast.DeferStmt:
		e.eraseStatement(c, n.Pos(), n.Call)
	case *ast.GoStmt:
		e.eraseStatement(c, n.Pos(), n.Call)
	}
}

func unparen(x ast.Expr) ast.Expr {
	for {
		p, ok := x.(*ast.ParenExpr)
		if !ok {
			return x
		}
		x = p.X
	}
}

// eraseStatement replaces `cap.Move(x)`, `defer cap.Move(x)` and `go
// cap.Move(x)` by `_ = x`. The argument is evaluated at the same point with
// the same effects (a deferred or spawned call evaluates its arguments when
// the statement runs), and the identity call has no effect of its own, so
// nothing is lost by dropping the defer or go.
func (e *eraser) eraseStatement(c *astutil.Cursor, pos token.Pos, x ast.Expr) {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		return
	}
	h, ok := e.helperCall(call)
	if !ok {
		return
	}
	repl, err := e.replacement(h, false)
	if err != nil {
		e.problem(call.Pos(), "%s: %v", types.ExprString(call.Fun), err)
		return
	}
	c.Replace(&ast.AssignStmt{
		Lhs:    []ast.Expr{&ast.Ident{NamePos: pos, Name: "_"}},
		TokPos: pos,
		Tok:    token.ASSIGN,
		Rhs:    []ast.Expr{repl},
	})
}

// inStatementPosition reports whether the call being visited is the whole of
// an expression, defer or go statement; those are rewritten as statements.
func (e *eraser) inStatementPosition() bool {
	for i := len(e.stack) - 2; i >= 0; i-- {
		switch e.stack[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.ExprStmt, *ast.DeferStmt, *ast.GoStmt:
			return true
		default:
			return false
		}
	}
	return false
}

// helper is a recognised call of a cap helper function.
type helper struct {
	name  string
	targs []ast.Expr
	arg   ast.Expr
	pos   token.Pos
}

func (e *eraser) helperCall(call *ast.CallExpr) (helper, bool) {
	fun := call.Fun
	var targs []ast.Expr
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun, targs = f.X, []ast.Expr{f.Index}
	case *ast.IndexListExpr:
		fun, targs = f.X, f.Indices
	}
	name, ok := e.member(fun)
	if !ok || !helperFuncs[name] || len(targs) > 1 || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return helper{}, false
	}
	return helper{name: name, targs: targs, arg: call.Args[0], pos: call.Pos()}, true
}

// replacement builds the expression that stands for a helper call. sensitive
// reports whether the call's value is an operand whose constant-ness is
// observable (operators, indices, conversions, case values).
//
// A helper call is never a constant, but its argument may be: an untyped
// constant argument takes its type from the generic call, and a constant
// operand folds (and can overflow or divide by zero) where a call's result
// does not. The cases:
//
//   - explicit type argument T: the value is T(arg) (a conversion, which for a
//     value assignable to T is that value, with the type T); when sensitive and
//     arg may be a constant, func() T { return arg }(), which is not constant.
//   - no type argument, arg not a constant: arg itself.
//   - no type argument, untyped constant arg: arg itself where an untyped
//     constant ends up with its default type anyway (assignments, call
//     arguments, returns); in a sensitive position
//     func() <default type> { return arg }().
//   - no type argument, typed constant arg: arg itself, or in a sensitive
//     position func() <its type> { return arg }() when its type can be named
//     here (a typed constant folds, and can overflow, where a call does not).
//   - no type argument, arg possibly a constant of unknown kind (an identifier
//     or pkg.Name this file does not declare): arg itself, except in a
//     sensitive position, where it is refused.
func (e *eraser) replacement(h helper, sensitive bool) (ast.Expr, error) {
	if h.name != "Move" {
		if len(h.targs) == 0 {
			return h.arg, nil
		}
		if u, ok := unparen(h.arg).(*ast.UnaryExpr); ok && u.Op == token.AND {
			return h.arg, nil // &x has exactly the type *typeof(x), which the call requires to be *T
		}
		return conversion(&ast.StarExpr{X: h.targs[0]}, h.arg), nil
	}

	info := e.classify(h.arg)
	if len(h.targs) == 1 {
		if sensitive && info.class != classNot {
			return funcCall(h.pos, h.targs[0], h.arg), nil
		}
		return conversion(h.targs[0], h.arg), nil
	}
	switch info.class {
	case classUntyped:
		if !sensitive {
			return h.arg, nil
		}
		t, err := e.defaultType(info.kind)
		if err != nil {
			return nil, err
		}
		return funcCall(h.pos, t, h.arg), nil
	case classTyped:
		if sensitive && info.typ != nil {
			return funcCall(h.pos, info.typ, h.arg), nil
		}
	case classUnknown:
		if sensitive {
			return nil, fmt.Errorf("the argument %s may be a constant of this package or another file whose type is unknown here, and its value is an operand where constants fold differently from a call result; assign it to a variable first", types.ExprString(h.arg))
		}
	}
	return h.arg, nil
}

func (e *eraser) defaultType(k untypedKind) (ast.Expr, error) {
	name := map[untypedKind]string{
		kindInt: "int", kindRune: "rune", kindFloat: "float64",
		kindComplex: "complex128", kindString: "string", kindBool: "bool",
	}[k]
	if e.declared[name] {
		return nil, fmt.Errorf("%q is redeclared in this file, so it cannot name the type of a constant", name)
	}
	return ast.NewIdent(name), nil
}

// conversion builds T(arg), parenthesising T where the syntax needs it.
func conversion(t, arg ast.Expr) ast.Expr {
	t = cloneType(t)
	switch t.(type) {
	case *ast.Ident, *ast.SelectorExpr, *ast.ParenExpr, *ast.IndexExpr, *ast.IndexListExpr,
		*ast.ArrayType, *ast.MapType, *ast.StructType, *ast.InterfaceType:
	default:
		t = &ast.ParenExpr{X: t}
	}
	return &ast.CallExpr{Fun: t, Args: []ast.Expr{arg}}
}

// funcCall builds func() T { return arg }(), an identity that is not a constant.
func funcCall(pos token.Pos, t, arg ast.Expr) ast.Expr {
	return &ast.CallExpr{Fun: &ast.FuncLit{
		Type: &ast.FuncType{
			Func:    pos,
			Params:  &ast.FieldList{},
			Results: &ast.FieldList{List: []*ast.Field{{Type: cloneType(t)}}},
		},
		Body: &ast.BlockStmt{Lbrace: pos, List: []ast.Stmt{&ast.ReturnStmt{Return: pos, Results: []ast.Expr{arg}}}, Rbrace: pos},
	}}
}

// identityFunc builds func(x T) T { return x } for Move[T] used as a value
// (func(x *T) *T for the Borrow family).
func identityFunc(pos token.Pos, name string, t ast.Expr) ast.Expr {
	typ := func() ast.Expr {
		if name == "Move" {
			return cloneType(t)
		}
		return &ast.StarExpr{X: cloneType(t)}
	}
	return &ast.FuncLit{
		Type: &ast.FuncType{
			Func:    pos,
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("x")}, Type: typ()}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: typ()}}},
		},
		Body: &ast.BlockStmt{Lbrace: pos, List: []ast.Stmt{&ast.ReturnStmt{Return: pos, Results: []ast.Expr{ast.NewIdent("x")}}}, Rbrace: pos},
	}
}

// cloneType copies the common type-expression shapes so one type argument can
// appear twice in the output; other shapes are shared.
func cloneType(t ast.Expr) ast.Expr {
	switch x := t.(type) {
	case *ast.Ident:
		return ast.NewIdent(x.Name)
	case *ast.SelectorExpr:
		return &ast.SelectorExpr{X: cloneType(x.X), Sel: ast.NewIdent(x.Sel.Name)}
	case *ast.StarExpr:
		return &ast.StarExpr{X: cloneType(x.X)}
	case *ast.ParenExpr:
		return &ast.ParenExpr{X: cloneType(x.X)}
	case *ast.ArrayType:
		return &ast.ArrayType{Len: x.Len, Elt: cloneType(x.Elt)}
	case *ast.MapType:
		return &ast.MapType{Key: cloneType(x.Key), Value: cloneType(x.Value)}
	case *ast.ChanType:
		return &ast.ChanType{Dir: x.Dir, Value: cloneType(x.Value)}
	case *ast.IndexExpr:
		return &ast.IndexExpr{X: cloneType(x.X), Index: cloneType(x.Index)}
	case *ast.IndexListExpr:
		idx := make([]ast.Expr, len(x.Indices))
		for i, ix := range x.Indices {
			idx[i] = cloneType(ix)
		}
		return &ast.IndexListExpr{X: cloneType(x.X), Indices: idx}
	}
	return t
}

// sensitive reports whether the value of the call being visited is an operand
// whose constant-ness is observable. Helper calls and parentheses are
// transparent: they pass the question on to their own context.
func (e *eraser) sensitive(call *ast.CallExpr) bool {
	var child ast.Node = call
	for i := len(e.stack) - 2; i >= 0; i-- {
		switch p := e.stack[i].(type) {
		case *ast.ParenExpr:
			child = p
		case *ast.CallExpr:
			if child == ast.Node(p.Fun) {
				return false
			}
			if _, ok := e.helperCall(p); ok {
				child = p
				continue
			}
			return e.conversionOrBuiltin(p.Fun)
		case *ast.BinaryExpr, *ast.IndexExpr, *ast.SliceExpr:
			return true
		case *ast.UnaryExpr:
			switch p.Op {
			case token.ADD, token.SUB, token.XOR, token.NOT:
				return true
			}
			return false
		case *ast.SwitchStmt:
			return child == ast.Node(p.Tag)
		case *ast.CaseClause:
			for _, x := range p.List {
				if ast.Node(x) == child {
					return true
				}
			}
			return false
		case *ast.KeyValueExpr:
			return child == ast.Node(p.Key)
		default:
			return false
		}
	}
	return false
}

var basicTypeNames = map[string]bool{
	"bool": true, "string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"byte": true, "rune": true, "float32": true, "float64": true, "complex64": true, "complex128": true,
}

var sensitiveBuiltins = map[string]bool{
	"len": true, "cap": true, "make": true, "new": true, "min": true, "max": true,
	"complex": true, "real": true, "imag": true,
}

// conversionOrBuiltin reports whether a call of fun is a type conversion or a
// builtin that constant-folds its operands. A call of a qualified or otherwise
// unresolved name is assumed to be a function call.
func (e *eraser) conversionOrBuiltin(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		if f.Obj == nil {
			return basicTypeNames[f.Name] || sensitiveBuiltins[f.Name]
		}
		return f.Obj.Kind == ast.Typ
	case *ast.IndexExpr:
		return e.conversionOrBuiltin(f.X)
	case *ast.ParenExpr, *ast.StarExpr, *ast.ArrayType, *ast.MapType, *ast.ChanType,
		*ast.FuncType, *ast.StructType, *ast.InterfaceType:
		return true
	}
	return false
}

// guardHeader parenthesises a replacement that would otherwise expose a
// composite literal in the header of an if, for or switch statement, where
// `T{` would be parsed as the start of the statement body.
func (e *eraser) guardHeader(x ast.Expr) ast.Expr {
	if exposesCompositeLit(x) && e.inHeader() {
		return &ast.ParenExpr{X: x}
	}
	return x
}

func exposesCompositeLit(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.CompositeLit:
		switch x.Type.(type) {
		case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr:
			return true
		}
	case *ast.BinaryExpr:
		return exposesCompositeLit(x.X) || exposesCompositeLit(x.Y)
	case *ast.UnaryExpr:
		return exposesCompositeLit(x.X)
	case *ast.StarExpr:
		return exposesCompositeLit(x.X)
	case *ast.SelectorExpr:
		return exposesCompositeLit(x.X)
	case *ast.IndexExpr:
		return exposesCompositeLit(x.X)
	case *ast.SliceExpr:
		return exposesCompositeLit(x.X)
	case *ast.TypeAssertExpr:
		return exposesCompositeLit(x.X)
	case *ast.CallExpr:
		return exposesCompositeLit(x.Fun)
	}
	return false
}

// inHeader reports whether the node being visited sits in the header of an
// if, for, range or switch statement outside any enclosing brackets.
func (e *eraser) inHeader() bool {
	var child ast.Node = e.stack[len(e.stack)-1]
	for i := len(e.stack) - 2; i >= 0; i-- {
		switch p := e.stack[i].(type) {
		case *ast.IfStmt:
			return child != ast.Node(p.Body) && child != ast.Node(p.Else)
		case *ast.ForStmt:
			return child != ast.Node(p.Body)
		case *ast.RangeStmt:
			return child != ast.Node(p.Body)
		case *ast.SwitchStmt:
			return child != ast.Node(p.Body)
		case *ast.TypeSwitchStmt:
			return child != ast.Node(p.Body)
		case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause, *ast.FuncDecl, *ast.ParenExpr, *ast.CompositeLit:
			return false
		case *ast.CallExpr:
			if child != ast.Node(p.Fun) {
				return false
			}
		case *ast.IndexExpr:
			if child != ast.Node(p.X) {
				return false
			}
		}
		child = e.stack[i]
	}
	return false
}

// reportResidue records every reference to the cap package that survived the
// rewrite. Deleting the import under such a reference would not compile; the
// reference is a form this tool does not know how to erase.
func (e *eraser) reportResidue() {
	skip := map[*ast.Ident]bool{}
	ast.Inspect(e.file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			skip[x.Sel] = true
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok {
				skip[id] = true
			}
		case *ast.FuncDecl:
			skip[x.Name] = true
		case *ast.Field:
			for _, id := range x.Names {
				skip[id] = true
			}
		case *ast.LabeledStmt:
			skip[x.Label] = true
		case *ast.BranchStmt:
			if x.Label != nil {
				skip[x.Label] = true
			}
		}
		return true
	})
	ast.Inspect(e.file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil {
				for _, imp := range e.imports {
					if imp.local != "" && id.Name == imp.local {
						e.problem(x.Pos(), "%s is not a form this tool can erase (for a helper used as a value, give it explicit type arguments)", types.ExprString(x))
					}
				}
			}
		case *ast.Ident:
			if x.Obj == nil && !skip[x] && (markerTypes[x.Name] || helperFuncs[x.Name]) {
				for _, imp := range e.imports {
					if imp.dot {
						e.problem(x.Pos(), "%s is not a form this tool can erase (for a helper used as a value, give it explicit type arguments)", x.Name)
						break
					}
				}
			}
		}
		return true
	})
}
