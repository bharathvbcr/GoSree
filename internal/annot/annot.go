// Package annot parses gocap's //safe: directives.
//
// Directives are ordinary Go line comments with no space after the slashes, so gofmt, gopls and the
// compiler leave them alone:
//
//	//safe:unsafe <reason>   exempts the statement or declaration it is attached to from every gocap rule
//	//safe:iso p, q          on a func: parameters p and q are owned; passing an argument to them moves it
//	//safe:imm               on a type: values of the type are deeply immutable
//
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

// Prefix starts every gocap directive.
const Prefix = "//safe:"

// CategoryMalformed is the diagnostic category for directives gocap cannot apply.
const CategoryMalformed = "SAFE007"

type span struct{ start, end token.Pos }

// Index holds the directives of one package.
type Index struct {
	unsafe []span
	// IsoVars holds the parameters named by //safe:iso.
	IsoVars map[*types.Var]bool
	// ImmTypes holds the types annotated //safe:imm. They are candidates: the ownership pass decides
	// whether each one actually satisfies the imm rules.
	ImmTypes map[*types.TypeName]token.Pos
}

// Suppressed reports whether pos lies inside a //safe:unsafe scope.
func (ix *Index) Suppressed(pos token.Pos) bool {
	for _, s := range ix.unsafe {
		if pos >= s.start && pos < s.end {
			return true
		}
	}
	return false
}

// UnsafeScopes reports how many //safe:unsafe scopes the package declares.
func (ix *Index) UnsafeScopes() int { return len(ix.unsafe) }

// Analyzer parses the directives and reports the malformed ones.
var Analyzer = &analysis.Analyzer{
	Name:       "safeannot",
	Doc:        "parse //safe: directives (reports malformed ones as " + CategoryMalformed + ")",
	Run:        run,
	ResultType: reflect.TypeFor[*Index](),
}

func run(pass *analysis.Pass) (any, error) {
	ix := &Index{IsoVars: map[*types.Var]bool{}, ImmTypes: map[*types.TypeName]token.Pos{}}
	for _, file := range pass.Files {
		starts := lineStarts(pass.Fset, file)
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if !strings.HasPrefix(c.Text, Prefix) {
					continue
				}
				text := strings.TrimPrefix(c.Text, Prefix)
				// A trailing "// note" is commentary, not arguments.
				if i := strings.Index(text, "//"); i >= 0 {
					text = text[:i]
				}
				verb, args, _ := strings.Cut(strings.TrimSpace(text), " ")
				args = strings.TrimSpace(args)
				target := attach(pass.Fset, cg, c, starts)
				apply(pass, ix, c, verb, args, target)
			}
		}
	}
	return ix, nil
}

func apply(pass *analysis.Pass, ix *Index, c *ast.Comment, verb, args string, target ast.Node) {
	malformed := func(format string, a ...any) {
		pass.Report(analysis.Diagnostic{Pos: c.Pos(), End: c.End(), Category: CategoryMalformed,
			Message: CategoryMalformed + ": " + fmt.Sprintf(format, a...)})
	}
	switch verb {
	case "unsafe":
		if target == nil {
			malformed("//safe:unsafe is not attached to a statement or declaration")
			return
		}
		if args == "" {
			malformed("//safe:unsafe needs a reason after the directive")
		}
		ix.unsafe = append(ix.unsafe, span{target.Pos(), target.End()})
	case "iso":
		fd, ok := target.(*ast.FuncDecl)
		if !ok {
			malformed("//safe:iso must be in a function's doc comment")
			return
		}
		names := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' })
		if len(names) == 0 {
			malformed("//safe:iso needs at least one parameter name")
			return
		}
		for _, name := range names {
			v := paramVar(pass, fd, name)
			if v == nil {
				malformed("//safe:iso names %q, which is not a parameter of %s", name, fd.Name.Name)
				continue
			}
			ix.IsoVars[v] = true
		}
	case "imm":
		ts := typeSpec(target)
		if ts == nil {
			malformed("//safe:imm must be in a type declaration's doc comment")
			return
		}
		if args != "" {
			malformed("//safe:imm takes no arguments")
		}
		if tn, ok := pass.TypesInfo.Defs[ts.Name].(*types.TypeName); ok {
			ix.ImmTypes[tn] = ts.Pos()
		}
	default:
		malformed("unknown directive //safe:%s (known: unsafe, iso, imm)", verb)
	}
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

// attach finds the node a directive applies to: a node it trails on the same line, otherwise the node
// that starts on the line after its comment group.
func attach(fset *token.FileSet, cg *ast.CommentGroup, c *ast.Comment, starts map[int]ast.Node) ast.Node {
	line := fset.Position(c.Slash).Line
	if n, ok := starts[line]; ok && n.Pos() < c.Slash {
		return n
	}
	return starts[fset.Position(cg.End()).Line+1]
}
