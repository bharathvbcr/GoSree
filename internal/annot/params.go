package annot

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// A fieldList is a receiver, type-parameter, parameter or result list of a function.
type fieldList struct {
	fl   *ast.FieldList
	role string // "receiver", "type parameter", "parameter" or "result"
	// fn is the *ast.FuncDecl or *ast.FuncLit the list belongs to, or nil for an interface method or a
	// function type, whose parameters no body uses.
	fn ast.Node
}

// fieldListsOf maps each directive comment that lies inside a function's field list to the innermost
// such list. dirs must be in source order.
func fieldListsOf(file *ast.File, dirs []*ast.Comment) map[*ast.Comment]*fieldList {
	if len(dirs) == 0 {
		return nil
	}
	var lists []*fieldList
	owned := map[*ast.FuncType]ast.Node{}
	add := func(fl *ast.FieldList, role string, fn ast.Node) {
		if fl != nil && fl.Opening.IsValid() {
			lists = append(lists, &fieldList{fl, role, fn})
		}
	}
	addType := func(ft *ast.FuncType, fn ast.Node) {
		add(ft.TypeParams, "type parameter", fn)
		add(ft.Params, "parameter", fn)
		add(ft.Results, "result", fn)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			owned[n.Type] = n
			add(n.Recv, "receiver", n)
			addType(n.Type, n)
		case *ast.FuncLit:
			owned[n.Type] = n
			addType(n.Type, n)
		case *ast.FuncType:
			if _, ok := owned[n]; !ok {
				addType(n, nil)
			}
		}
		return true
	})
	// Lists are in pre-order, so a list nested in another (a func-typed parameter) comes later and
	// overwrites: each comment ends with its innermost list.
	in := map[*ast.Comment]*fieldList{}
	for _, l := range lists {
		i := sort.Search(len(dirs), func(i int) bool { return dirs[i].Pos() > l.fl.Opening })
		for ; i < len(dirs) && dirs[i].Pos() < l.fl.Closing; i++ {
			in[dirs[i]] = l
		}
	}
	return in
}

// applyInField applies a directive written inside a function's field list. The one accepted place is
// directly after a parameter's or receiver's type, before the comma or closing parenthesis:
// func f(p *T /*gosree:mub*/). It then applies to every name of that field, as the type does; args,
// if any, choose among them. Anywhere else in a field list the directive is reported.
func (a *applier) applyInField(c *ast.Comment, lead, verb, args string, l *fieldList) {
	switch verb {
	case "mub", "mut", "rob", "ref", "iso":
	case "unsafe", "imm", "guarded_by", "holds":
		a.malformed("%s%s cannot be written inside a %s list", lead, verb, l.role)
		return
	default:
		a.unknown(lead, verb)
		return
	}
	switch {
	case l.role == "result":
		a.malformed("%s%s on a result: only parameters and receivers can be %s", lead, verb, capNoun(verb))
		return
	case l.role == "type parameter":
		a.malformed("%s%s on a type parameter: annotate a parameter instead", lead, verb)
		return
	case l.fn == nil:
		a.malformed("%s%s on a parameter of an interface method or function type, which has no body to check; annotate the function that implements it", lead, verb)
		return
	}
	f, why := a.fieldBefore(c, l.fl)
	if f == nil {
		a.malformed("%s%s %s; write it directly after the parameter's type, as in (p *T /*%s%s*/)", lead, verb, why, strings.TrimPrefix(lead, "//"), verb)
		return
	}
	if len(f.Names) == 0 {
		a.malformed("%s%s on an unnamed %s, which no code can use; name it", lead, verb, l.role)
		return
	}
	ids := f.Names
	if args != "" {
		ids = nil
		for _, name := range splitNames(args) {
			var id *ast.Ident
			for _, n := range f.Names {
				if n.Name == name {
					id = n
				}
			}
			if id == nil {
				a.malformed("%s%s names %q, which is not declared in the %s it follows", lead, verb, name, l.role)
				return
			}
			ids = append(ids, id)
		}
	}
	var vars []*types.Var
	for _, id := range ids {
		v, ok := a.pass.TypesInfo.Defs[id].(*types.Var)
		if !ok || v == nil {
			a.malformed("%s%s names %q, which is not a variable", lead, verb, id.Name)
			return
		}
		vars = append(vars, v)
	}
	a.applyParamCaps(lead, verb, vars)
}

// fieldBefore returns the field whose type c directly follows, or nil and where c is instead. A
// comment after the comma that ends a parameter's line, with the next parameter on a later line, also
// follows that parameter: it is how a parameter list written one per line is annotated.
func (a *applier) fieldBefore(c *ast.Comment, fl *ast.FieldList) (*ast.Field, string) {
	var prev, next *ast.Field
	for _, f := range fl.List {
		if f.Pos() <= c.Pos() && c.Pos() < f.End() {
			return nil, "is inside the declaration of " + fieldNames(f)
		}
		if f.End() <= c.Pos() {
			prev = f
		} else if next == nil {
			next = f
		}
	}
	if prev == nil {
		return nil, "precedes every parameter in the list"
	}
	src, ok := a.source(c.Pos())
	if !ok {
		return nil, "cannot be placed: the source of this file cannot be read"
	}
	tf := a.pass.Fset.File(c.Pos())
	if !strings.ContainsRune(string(src[tf.Offset(prev.End()):tf.Offset(c.Pos())]), ',') {
		return prev, ""
	}
	line := physLine(a.pass.Fset, c.Pos())
	if physLine(a.pass.Fset, prev.End()) == line && (next == nil || physLine(a.pass.Fset, next.Pos()) > line) {
		return prev, ""
	}
	return nil, "follows a comma, so it belongs to no parameter"
}

// source returns the bytes of the file holding pos, read at most once per file.
func (a *applier) source(pos token.Pos) ([]byte, bool) {
	tf := a.pass.Fset.File(pos)
	if tf == nil || a.pass.ReadFile == nil {
		return nil, false
	}
	if b, ok := a.src[tf]; ok {
		return b, b != nil
	}
	b, err := a.pass.ReadFile(tf.Name())
	if err != nil || len(b) != tf.Size() {
		b = nil
	}
	a.src[tf] = b
	return b, b != nil
}

func fieldNames(f *ast.Field) string {
	if len(f.Names) == 0 {
		return "an unnamed parameter"
	}
	names := make([]string, len(f.Names))
	for i, n := range f.Names {
		names[i] = n.Name
	}
	return strings.Join(names, ", ")
}

func capNoun(verb string) string {
	switch verb {
	case "iso":
		return "owned"
	case "rob", "ref":
		return "read-only borrows"
	}
	return "mutable borrows"
}

// splitNames splits a directive's argument list at commas and white space.
func splitNames(args string) []string {
	return strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

// applier applies the directives of one package.
type applier struct {
	pass *analysis.Pass
	ix   *Index
	// c is the directive being applied; malformed reports at it.
	c   *ast.Comment
	src map[*token.File][]byte
	// bound records the capability each directive gave a variable, so that a second directive on the
	// same variable is checked against the first whatever its spelling.
	bound map[*types.Var]string
	// onNode records the borrow kind directives gave each binding statement.
	onNode map[ast.Node]BorrowKind
	// scopes indexes the hatches by extent, to find one that repeats another.
	scopes map[span][]*hatch
}

func (a *applier) malformed(format string, args ...any) {
	a.pass.Report(analysis.Diagnostic{Pos: a.c.Pos(), End: a.c.End(), Category: CategoryMalformed,
		Message: CategoryMalformed + ": " + fmt.Sprintf(format, args...)})
}

func (a *applier) unknown(lead, verb string) {
	if s := suggestVerb(verb); s != "" {
		a.malformed("unknown directive %s%s (did you mean %s%s? known: %s)", lead, verb, lead, s, strings.Join(verbs, ", "))
		return
	}
	a.malformed("unknown directive %s%s (known: %s)", lead, verb, strings.Join(verbs, ", "))
}
