// Package annot parses gocap's //gocap: and legacy //safe: directives.
//
// Directives are ordinary Go line comments with no space after the slashes, so gofmt, gopls and the
// compiler leave them alone:
//
//	//gocap:unsafe <reason>   exempts the statement or declaration from every gocap rule
//	//gocap:iso p, q          on a func: parameters p and q are owned; passing an argument moves it
//	//gocap:imm               on a type: values of the type are deeply immutable
//	//gocap:mub p, q          on a func: parameters p and q are mutable second-class borrows (\mub / &mut T)
//	//gocap:rob p, q          on a func: parameters p and q are read-only second-class borrows (\rob / &T)
//	//gocap:mut               on a local assignment b := &x: marks b as an active mutable borrow
//	//gocap:rob               on a local assignment b := &x: marks b as an active read-only borrow
//
// Legacy //safe:* directives and inline comment forms (/*gocap:mut*/, /*gocap:rob*/) are supported.
// A directive attaches to the declaration whose doc comment it is in, to the statement on the line
// that follows the comment group, or to the statement it trails on the same line.
package annot

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// CanonicalPrefix starts modern gocap directives.
const CanonicalPrefix = "//gocap:"

// LegacyPrefix starts legacy directives for backward compatibility.
const LegacyPrefix = "//safe:"

// Prefix starts modern gocap directives (alias for CanonicalPrefix).
const Prefix = CanonicalPrefix

// BorrowKind classifies second-class borrows.
type BorrowKind int

const (
	BorrowNone BorrowKind = iota
	BorrowMut             // mutable borrow (mub / mut)
	BorrowRob             // read-only borrow (rob / ref)
)

// BorrowRef is an alias for BorrowRob.
const BorrowRef = BorrowRob

func (k BorrowKind) String() string {
	switch k {
	case BorrowMut:
		return "mut"
	case BorrowRob:
		return "rob"
	default:
		return "none"
	}
}

// CategoryMalformed is the diagnostic category for directives gocap cannot apply.
const CategoryMalformed = "SAFE007"

type span struct{ start, end token.Pos }

// Index holds the directives of one package.
type Index struct {
	unsafe []span
	// IsoVars holds the parameters named by //gocap:iso (or //safe:iso).
	IsoVars map[*types.Var]bool
	// ImmTypes holds the types annotated //gocap:imm (or //safe:imm). They are candidates: the ownership pass decides
	// whether each one actually satisfies the imm rules.
	ImmTypes map[*types.TypeName]token.Pos
	// MubVars holds parameters and local variables marked as mutable borrows.
	MubVars map[*types.Var]bool
	// RobVars holds parameters and local variables marked as read-only borrows.
	RobVars map[*types.Var]bool
	// Borrows maps AST positions of borrow statements/expressions to their BorrowKind.
	Borrows map[token.Pos]BorrowKind
}

// IsMub reports whether v is marked as a mutable borrow variable.
func (ix *Index) IsMub(v *types.Var) bool {
	return ix.MubVars != nil && ix.MubVars[v]
}

// IsRob reports whether v is marked as a read-only borrow variable.
func (ix *Index) IsRob(v *types.Var) bool {
	return ix.RobVars != nil && ix.RobVars[v]
}

// BorrowAt returns the BorrowKind at pos, or BorrowNone if none.
func (ix *Index) BorrowAt(pos token.Pos) BorrowKind {
	if ix.Borrows == nil {
		return BorrowNone
	}
	return ix.Borrows[pos]
}

// Suppressed reports whether pos lies inside a //safe:unsafe or //gocap:unsafe scope.
func (ix *Index) Suppressed(pos token.Pos) bool {
	for _, s := range ix.unsafe {
		if pos >= s.start && pos < s.end {
			return true
		}
	}
	return false
}

// UnsafeScopes reports how many unsafe scopes the package declares.
func (ix *Index) UnsafeScopes() int { return len(ix.unsafe) }

// Analyzer parses the directives and reports the malformed ones.
var Analyzer = &analysis.Analyzer{
	Name:       "safeannot",
	Doc:        "parse //gocap: and //safe: directives (reports malformed ones as " + CategoryMalformed + ")",
	Run:        run,
	ResultType: reflect.TypeFor[*Index](),
}

func run(pass *analysis.Pass) (any, error) {
	ix := &Index{
		IsoVars:  map[*types.Var]bool{},
		ImmTypes: map[*types.TypeName]token.Pos{},
		MubVars:  map[*types.Var]bool{},
		RobVars:  map[*types.Var]bool{},
		Borrows:  map[token.Pos]BorrowKind{},
	}
	for _, file := range pass.Files {
		starts := lineStarts(pass.Fset, file)
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				lead, verb, args, ok := parseDirective(c.Text)
				if !ok {
					continue
				}
				target := attach(pass.Fset, cg, c, starts)
				apply(pass, ix, c, lead, verb, args, target)
			}
		}
	}
	return ix, nil
}

func parseDirective(raw string) (lead, verb, args string, ok bool) {
	text := strings.TrimSpace(raw)
	var body string
	switch {
	case strings.HasPrefix(text, "//gocap:"):
		lead = "//gocap:"
		body = text[len("//gocap:"):]
	case strings.HasPrefix(text, "//safe:"):
		lead = "//safe:"
		body = text[len("//safe:"):]
	case strings.HasPrefix(text, "/*gocap:"):
		lead = "//gocap:"
		body = strings.TrimSuffix(text[len("/*gocap:"):], "*/")
	case strings.HasPrefix(text, "/*safe:"):
		lead = "//safe:"
		body = strings.TrimSuffix(text[len("/*safe:"):], "*/")
	default:
		return "", "", "", false
	}

	// A trailing "// note" is commentary, not arguments.
	if i := strings.Index(body, "//"); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	verb, args, _ = strings.Cut(body, " ")
	verb = strings.TrimSpace(verb)
	args = strings.TrimSpace(args)
	return lead, verb, args, true
}

func apply(pass *analysis.Pass, ix *Index, c *ast.Comment, lead, verb, args string, target ast.Node) {
	malformed := func(format string, a ...any) {
		pass.Report(analysis.Diagnostic{Pos: c.Pos(), End: c.End(), Category: CategoryMalformed,
			Message: CategoryMalformed + ": " + fmt.Sprintf(format, a...)})
	}
	switch verb {
	case "unsafe":
		if target == nil {
			malformed("%s%s is not attached to a statement or declaration", lead, verb)
			return
		}
		if args == "" {
			malformed("%s%s needs a reason after the directive", lead, verb)
		}
		ix.unsafe = append(ix.unsafe, span{target.Pos(), target.End()})
	case "iso":
		fd, ok := target.(*ast.FuncDecl)
		if !ok {
			malformed("%s%s must be in a function's doc comment", lead, verb)
			return
		}
		names := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' })
		if len(names) == 0 {
			malformed("%s%s needs at least one parameter name", lead, verb)
			return
		}
		for _, name := range names {
			v := paramVar(pass, fd, name)
			if v == nil {
				malformed("%s%s names %q, which is not a parameter of %s", lead, verb, name, fd.Name.Name)
				continue
			}
			if ix.MubVars[v] || ix.RobVars[v] {
				malformed("%s%s names %q, which already has a conflicting capability/borrow annotation", lead, verb, name)
				continue
			}
			ix.IsoVars[v] = true
		}
	case "imm":
		ts := typeSpec(target)
		if ts == nil {
			malformed("%s%s must be in a type declaration's doc comment", lead, verb)
			return
		}
		if args != "" {
			malformed("%s%s takes no arguments", lead, verb)
		}
		if tn, ok := pass.TypesInfo.Defs[ts.Name].(*types.TypeName); ok {
			ix.ImmTypes[tn] = ts.Pos()
		}
	case "mub", "mut":
		applyBorrow(pass, ix, c, lead, verb, args, target, BorrowMut, malformed)
	case "rob", "ref":
		applyBorrow(pass, ix, c, lead, verb, args, target, BorrowRob, malformed)
	default:
		malformed("unknown directive %s%s (known: unsafe, iso, imm, mub, rob, mut, ref)", lead, verb)
	}
}

func applyBorrow(pass *analysis.Pass, ix *Index, c *ast.Comment, lead, verb, args string, target ast.Node, kind BorrowKind, malformed func(string, ...any)) {
	if target == nil {
		malformed("%s%s is not attached to a function or assignment", lead, verb)
		return
	}

	// Case 1: Function or method parameter directive
	if fd, ok := target.(*ast.FuncDecl); ok {
		if args == "" {
			malformed("%s%s needs at least one parameter name", lead, verb)
			return
		}
		names := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' })
		if len(names) == 0 {
			malformed("%s%s needs at least one parameter name", lead, verb)
			return
		}
		for _, name := range names {
			v := paramVar(pass, fd, name)
			if v == nil {
				malformed("%s%s names %q, which is not a parameter of %s", lead, verb, name, fd.Name.Name)
				continue
			}
			if ix.IsoVars[v] {
				malformed("%s%s names %q, which already has an iso capability annotation", lead, verb, name)
				continue
			}
			if kind == BorrowMut && ix.RobVars[v] {
				malformed("%s%s names %q, which is already annotated as read-only borrow", lead, verb, name)
				continue
			}
			if kind == BorrowRob && ix.MubVars[v] {
				malformed("%s%s names %q, which is already annotated as mutable borrow", lead, verb, name)
				continue
			}
			if kind == BorrowMut {
				ix.MubVars[v] = true
			} else {
				ix.RobVars[v] = true
			}
			ix.Borrows[v.Pos()] = kind
		}
		return
	}

	// Case 2: Local assignment statement
	switch n := target.(type) {
	case *ast.AssignStmt:
		if args != "" {
			matched := false
			for _, lhs := range n.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == args {
					matched = true
					break
				}
			}
			if !matched {
				malformed("%s%s on assignment names %q, which does not match assigned variable", lead, verb, args)
				return
			}
		}
		ix.Borrows[n.Pos()] = kind
		ix.Borrows[c.Pos()] = kind
		for _, rhs := range n.Rhs {
			ix.Borrows[rhs.Pos()] = kind
			if ue, ok := rhs.(*ast.UnaryExpr); ok {
				ix.Borrows[ue.OpPos] = kind
			}
		}
		for _, lhs := range n.Lhs {
			ix.Borrows[lhs.Pos()] = kind
			if id, ok := lhs.(*ast.Ident); ok {
				if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
					if kind == BorrowMut {
						ix.MubVars[v] = true
					} else {
						ix.RobVars[v] = true
					}
				} else if v, ok := pass.TypesInfo.Uses[id].(*types.Var); ok && v != nil {
					if kind == BorrowMut {
						ix.MubVars[v] = true
					} else {
						ix.RobVars[v] = true
					}
				}
			}
		}
		return

	case *ast.DeclStmt:
		gd, ok := n.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			malformed("%s%s must be attached to a function declaration or an assignment statement", lead, verb)
			return
		}
		ix.Borrows[n.Pos()] = kind
		ix.Borrows[c.Pos()] = kind
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if args != "" {
				matched := false
				for _, name := range vs.Names {
					if name.Name == args {
						matched = true
						break
					}
				}
				if !matched {
					malformed("%s%s on assignment names %q, which does not match assigned variable", lead, verb, args)
					return
				}
			}
			for _, val := range vs.Values {
				ix.Borrows[val.Pos()] = kind
				if ue, ok := val.(*ast.UnaryExpr); ok {
					ix.Borrows[ue.OpPos] = kind
				}
			}
			for _, name := range vs.Names {
				ix.Borrows[name.Pos()] = kind
				if v, ok := pass.TypesInfo.Defs[name].(*types.Var); ok && v != nil {
					if kind == BorrowMut {
						ix.MubVars[v] = true
					} else {
						ix.RobVars[v] = true
					}
				}
			}
		}
		return
	}

	malformed("%s%s must be attached to a function declaration or an assignment statement", lead, verb)
}

func paramVar(pass *analysis.Pass, fd *ast.FuncDecl, name string) *types.Var {
	lists := []*ast.FieldList{fd.Recv, fd.Type.Params}
	for _, fl := range lists {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, id := range f.Names {
				if id.Name == name {
					v, _ := pass.TypesInfo.Defs[id].(*types.Var)
					return v
				}
			}
		}
	}
	return nil
}

func typeSpec(n ast.Node) *ast.TypeSpec {
	switch n := n.(type) {
	case *ast.TypeSpec:
		return n
	case *ast.GenDecl:
		if n.Tok == token.TYPE && len(n.Specs) == 1 {
			ts, _ := n.Specs[0].(*ast.TypeSpec)
			return ts
		}
	}
	return nil
}

// lineStarts maps each line to the outermost statement, declaration or spec that starts on it.
func lineStarts(fset *token.FileSet, file *ast.File) map[int]ast.Node {
	starts := map[int]ast.Node{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n.(type) {
		case ast.Stmt, ast.Decl, ast.Spec:
			line := fset.Position(n.Pos()).Line
			if _, seen := starts[line]; !seen {
				starts[line] = n
			}
		}
		return true
	})
	return starts
}

// attach finds the node a directive applies to: a node on the same line, otherwise the node
// that starts on the line after its comment group.
func attach(fset *token.FileSet, cg *ast.CommentGroup, c *ast.Comment, starts map[int]ast.Node) ast.Node {
	line := fset.Position(c.Slash).Line
	if n, ok := starts[line]; ok {
		return n
	}
	return starts[fset.Position(cg.End()).Line+1]
}
