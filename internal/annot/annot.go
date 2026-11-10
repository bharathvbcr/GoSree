// Package annot parses gocap's //gocap: and //safe: directives.
//
// Directives are ordinary Go line comments with no space after the slashes, so gofmt, gopls and the
// compiler leave them alone:
//
//	//gocap:unsafe <reason>   exempts the statement or declaration it is attached to from every gocap rule
//	//gocap:iso p, q          on a func: parameters p and q are owned; passing an argument to them moves it
//	//gocap:imm               on a type: values of the type are deeply immutable
//	//safe:guarded_by mu      on a struct field, package variable or local: every access must hold mu, a
//	                          sibling field, package variable or local in scope of type sync.Mutex or sync.RWMutex
//	//safe:holds mu           on a func: callers must hold mu (a receiver field or package variable)
//	//gocap:mub p, q          on a func: parameters p and q are mutable second-class borrows (\mub / &mut T)
//	//gocap:rob p, q          on a func: parameters p and q are read-only second-class borrows (\rob / &T)
//	//gocap:mut               on a local assignment b := &x: marks b as an active mutable borrow
//	//gocap:rob               on a local assignment b := &x: marks b as an active read-only borrow
//
// Legacy //safe:* directives and inline comment forms (/*gocap:mut*/, /*gocap:rob*/) are supported.
// A directive attaches to the declaration whose doc comment it is in, to the statement, spec or
// struct field on the line that follows the comment group, or to the one on the same line. Lines are
// physical lines: a //line directive does not move a directive. Inside a function's parameter list a
// borrow or iso directive goes directly after a parameter's type, func f(p *T /*gocap:mub*/), and
// applies to every name declared with that type.
//
// A directive is applied, or reported as SAFE007, never dropped: a malformed directive has no effect
// at all, and a comment that reads as a directive but is not one (// gocap:iso p, //Safe:holds mu,
// /* gocap:mut */) is reported too; see nearMiss for the rule that tells it from prose.
//
// A //gocap:unsafe hatch covers the code written under it: on its own line above a statement, or in
// a declaration's doc comment, the whole statement or declaration (doc comment included); on the same
// line as code, that line's statement except the bodies of blocks in it that span several lines.
package annot

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

// SreePrefix starts GoSree directives.
const SreePrefix = "//gosree:"

// ShortSreePrefix starts short GoSree directives.
const ShortSreePrefix = "//sree:"

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
	// hatches are the //gocap:unsafe scopes, in source order.
	hatches []*hatch
	// used marks the //safe:unsafe scopes that muted at least one finding. Every pass that requires
	// this analyzer shares the Index and may run concurrently, so mu guards it.
	mu   sync.Mutex
	used map[int]bool
	// IsoVars holds the parameters named by //gocap:iso (or //safe:iso).
	IsoVars map[*types.Var]bool
	// ImmTypes holds the types annotated //gocap:imm (or //safe:imm). They are candidates: the ownership pass decides
	// whether each one actually satisfies the imm rules.
	ImmTypes map[*types.TypeName]token.Pos
	// ImmPrefix records whether the annotation was //gocap: or //safe: for each candidate type.
	ImmPrefix map[*types.TypeName]string
	// Guarded maps a struct field, package variable or local variable annotated //safe:guarded_by to
	// the mutex that guards it.
	Guarded map[*types.Var]*types.Var
	// Holds maps a function annotated //safe:holds to the mutexes its callers must hold.
	Holds map[*types.Func][]*types.Var
	// MubVars holds parameters and local variables marked as mutable borrows.
	MubVars map[*types.Var]bool
	// RobVars holds parameters and local variables marked as read-only borrows.
	RobVars map[*types.Var]bool
	// Borrows maps AST positions of borrow statements/expressions to their BorrowKind.
	Borrows map[token.Pos]BorrowKind
}

// HoldsDirectives counts the //safe:holds directives.
func (ix *Index) HoldsDirectives() int {
	n := 0
	for _, mus := range ix.Holds {
		n += len(mus)
	}
	return n
}

// ImmPrefixOf returns the directive prefix ("//gocap:" or "//safe:") used for tn, defaulting to "//gocap:".
func (ix *Index) ImmPrefixOf(tn *types.TypeName) string {
	if ix.ImmPrefix != nil {
		if p, ok := ix.ImmPrefix[tn]; ok && p != "" {
			return p
		}
	}
	return "//gocap:"
}

// IsMub reports whether v is marked as a mutable borrow variable.
func (ix *Index) IsMub(v *types.Var) bool {
	return v != nil && ix.MubVars != nil && (ix.MubVars[v] || ix.MubVars[v.Origin()])
}

// IsRob reports whether v is marked as a read-only borrow variable.
func (ix *Index) IsRob(v *types.Var) bool {
	return v != nil && ix.RobVars != nil && (ix.RobVars[v] || ix.RobVars[v.Origin()])
}

// IsIso reports whether v is marked as an isolated (owned) parameter.
func (ix *Index) IsIso(v *types.Var) bool {
	return v != nil && ix.IsoVars != nil && (ix.IsoVars[v] || ix.IsoVars[v.Origin()])
}

// BorrowAt returns the BorrowKind at pos, or BorrowNone if none.
func (ix *Index) BorrowAt(pos token.Pos) BorrowKind {
	if ix.Borrows == nil {
		return BorrowNone
	}
	return ix.Borrows[pos]
}

// Suppressed reports whether pos lies inside a //safe:unsafe or //gocap:unsafe scope. It only asks;
// a pass that mutes a finding because of the scope calls Mute, so the scope is not counted as stale.
func (ix *Index) Suppressed(pos token.Pos) bool { return ix.scope(pos) >= 0 }

// scope returns the innermost //safe:unsafe scope covering pos, or -1.
func (ix *Index) scope(pos token.Pos) int {
	best := -1
	for i, h := range ix.hatches {
		if h.covers(pos) && (best < 0 || h.size() < ix.hatches[best].size()) {
			best = i
		}
	}
	return best
}

// Mute reports whether pos lies inside a //safe:unsafe scope and, if it does, records that the
// innermost such scope muted a finding.
func (ix *Index) Mute(pos token.Pos) bool {
	i := ix.scope(pos)
	if i < 0 {
		return false
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.used == nil {
		ix.used = map[int]bool{}
	}
	ix.used[i] = true
	return true
}

// StaleScopes counts the //safe:unsafe scopes that have muted nothing so far. Read it after every pass
// over the package has run: a scope one pass leaves unused may mute another pass's finding.
func (ix *Index) StaleScopes() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return len(ix.hatches) - len(ix.used)
}

// StaleHatches returns, in source order, the //gocap:unsafe directives that have muted nothing so far.
// Like StaleScopes, it is the final answer only once every pass that can mute has run on the package.
func (ix *Index) StaleHatches() []Hatch {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var stale []Hatch
	for i, h := range ix.hatches {
		if !ix.used[i] {
			stale = append(stale, Hatch{h.at, h.atEnd})
		}
	}
	return stale
}

// ReportStale reports each stale //gocap:unsafe directive as SAFE007, at the directive. Call it from
// an analyzer that requires every analyzer that mutes findings through this Index, so that it runs
// after all of them.
func (ix *Index) ReportStale(pass *analysis.Pass) {
	for _, h := range ix.StaleHatches() {
		pass.Report(analysis.Diagnostic{Pos: h.Pos, End: h.End, Category: CategoryMalformed,
			Message: CategoryMalformed + ": stale escape hatch: this //gocap:unsafe mutes no finding of any rule; remove it"})
	}
}

// UnsafeScopes reports how many //safe:unsafe scopes the package declares.
func (ix *Index) UnsafeScopes() int { return len(ix.hatches) }

// Analyzer parses the directives and reports the malformed ones.
var Analyzer = &analysis.Analyzer{
	Name:       "safeannot",
	Doc:        "parse //gocap: and //safe: directives (reports malformed ones as " + CategoryMalformed + ")",
	Run:        run,
	ResultType: reflect.TypeFor[*Index](),
}

func run(pass *analysis.Pass) (any, error) {
	ix := &Index{
		IsoVars:   map[*types.Var]bool{},
		ImmTypes:  map[*types.TypeName]token.Pos{},
		ImmPrefix: map[*types.TypeName]string{},
		Guarded:   map[*types.Var]*types.Var{},
		Holds:     map[*types.Func][]*types.Var{},
		MubVars:   map[*types.Var]bool{},
		RobVars:   map[*types.Var]bool{},
		Borrows:   map[token.Pos]BorrowKind{},
	}
	a := &applier{
		pass:   pass,
		ix:     ix,
		src:    map[*token.File][]byte{},
		bound:  map[*types.Var]string{},
		onNode: map[ast.Node]BorrowKind{},
		scopes: map[span][]*hatch{},
	}
	for _, file := range pass.Files {
		var dirs []*ast.Comment
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if _, _, _, ok := parseDirective(c.Text); ok {
					dirs = append(dirs, c)
				} else if written, meant, near := nearMiss(c.Text); near {
					// A comment a reader takes for a directive but gocap does not is reported, never
					// applied: whatever check it asks for is not running.
					a.c = c
					a.malformed("%q is not a gocap directive, so it has no effect: write it as %s (no space after the slashes, lower case)", written, meant)
				}
			}
		}
		if len(dirs) == 0 {
			scanCapMarkers(pass, ix, file)
			continue
		}
		starts, ends, structOf := lineStarts(pass.Fset, file)
		inField := fieldListsOf(file, dirs)
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				lead, verb, args, ok := parseDirective(c.Text)
				if !ok {
					continue
				}
				a.c = c
				if l := inField[c]; l != nil {
					a.applyInField(c, lead, verb, args, l)
					continue
				}
				target, inline := attach(pass.Fset, cg, c, starts, ends)
				a.apply(cg, lead, verb, args, target, inline, structOf)
			}
		}
		scanCapMarkers(pass, ix, file)
	}
	return ix, nil
}

// quoted returns the part of a comment a message quotes: its first line, without a trailing "// note".
func quoted(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s[min(2, len(s)):], "//"); i >= 0 {
		s = strings.TrimRightFunc(s[:i+2], unicode.IsSpace)
	}
	return s
}

func parseDirective(raw string) (lead, verb, args string, ok bool) {
	text := strings.TrimSpace(raw)
	var body string
	switch {
	case strings.HasPrefix(text, "//gosree:"):
		lead = "//gosree:"
		body = text[len("//gosree:"):]
	case strings.HasPrefix(text, "//sree:"):
		lead = "//sree:"
		body = text[len("//sree:"):]
	case strings.HasPrefix(text, "//gocap:"):
		lead = "//gocap:"
		body = text[len("//gocap:"):]
	case strings.HasPrefix(text, "//safe:"):
		lead = "//safe:"
		body = text[len("//safe:"):]
	case strings.HasPrefix(text, "/*gosree:"):
		lead = "//gosree:"
		body = strings.TrimSuffix(text[len("/*gosree:"):], "*/")
	case strings.HasPrefix(text, "/*sree:"):
		lead = "//sree:"
		body = strings.TrimSuffix(text[len("/*sree:"):], "*/")
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
	// The verb ends at any white space: a tab between verb and arguments is a separator.
	if i := strings.IndexFunc(body, unicode.IsSpace); i >= 0 {
		verb, args = body[:i], strings.TrimSpace(body[i:])
	} else {
		verb = body
	}
	return lead, verb, args, true
}

// visibleReason reports whether a hatch's reason has at least one letter or digit: a reason made of
// zero-width or other invisible characters only is no reason a reviewer can read.
func visibleReason(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// apply applies one directive outside a field list. A directive that is reported as malformed has no
// effect at all: every check below runs before the Index changes.
func (a *applier) apply(cg *ast.CommentGroup, lead, verb, args string, target ast.Node, inline bool, structOf map[*ast.Field]*ast.StructType) {
	pass, ix := a.pass, a.ix
	switch verb {
	case "unsafe":
		a.applyHatch(cg, lead, verb, args, target, inline)
	case "iso":
		fd, ok := target.(*ast.FuncDecl)
		if !ok {
			a.malformed("%s%s must be in a function's doc comment", lead, verb)
			return
		}
		if vars, ok := a.paramVars(lead, verb, args, fd); ok {
			a.applyParamCaps(lead, verb, vars)
		}
	case "imm":
		ts := typeSpec(target)
		if ts == nil {
			a.malformed("%s%s must be in a type declaration's doc comment", lead, verb)
			return
		}
		if args != "" {
			a.malformed("%s%s takes no arguments", lead, verb)
			return
		}
		tn, ok := pass.TypesInfo.Defs[ts.Name].(*types.TypeName)
		if !ok {
			return
		}
		if _, dup := ix.ImmTypes[tn]; dup {
			a.malformed("%s%s repeats the %s already on %s", lead, verb, verb, tn.Name())
			return
		}
		ix.ImmTypes[tn] = ts.Pos()
		ix.ImmPrefix[tn] = lead
	case "guarded_by":
		applyGuardedBy(pass, ix, target, args, structOf, a.malformed)
	case "holds":
		applyHolds(pass, ix, target, args, a.malformed)
	case "mub", "mut":
		a.applyBorrow(lead, verb, args, target, BorrowMut)
	case "rob", "ref":
		a.applyBorrow(lead, verb, args, target, BorrowRob)
	default:
		a.unknown(lead, verb)
	}
}

// applyHatch installs a //gocap:unsafe scope.
func (a *applier) applyHatch(cg *ast.CommentGroup, lead, verb, args string, target ast.Node, inline bool) {
	if target == nil {
		a.malformed("%s%s is not attached to a statement or declaration", lead, verb)
		return
	}
	if args == "" {
		a.malformed("%s%s needs a reason after the directive", lead, verb)
		return
	}
	if !visibleReason(args) {
		a.malformed("%s%s needs a reason after the directive: %q has no visible letter or digit", lead, verb, args)
		return
	}
	h, ok := newHatch(a.pass.Fset, target, cg, a.c, inline)
	if !ok {
		a.malformed("%s%s on the line of an opening brace covers nothing: write it above the statement it is for", lead, verb)
		return
	}
	key := span{h.start, h.end}
	for _, o := range a.scopes[key] {
		if o.sameScope(h) {
			a.malformed("%s%s repeats a //gocap:unsafe that already covers exactly this code", lead, verb)
			return
		}
	}
	a.scopes[key] = append(a.scopes[key], h)
	a.ix.hatches = append(a.ix.hatches, h)
}

// paramVars resolves a doc-comment directive's parameter names on fd.
func (a *applier) paramVars(lead, verb, args string, fd *ast.FuncDecl) ([]*types.Var, bool) {
	names := splitNames(args)
	if len(names) == 0 {
		a.malformed("%s%s needs at least one parameter name", lead, verb)
		return nil, false
	}
	vars := make([]*types.Var, 0, len(names))
	for _, name := range names {
		v := paramVar(a.pass, fd, name)
		if v == nil {
			a.malformed("%s%s names %q, which is not a parameter of %s", lead, verb, name, fd.Name.Name)
			return nil, false
		}
		vars = append(vars, v)
	}
	return vars, true
}

// capability names what an iso, borrow or no directive gives a variable.
func capability(verb string) string {
	switch verb {
	case "iso":
		return "iso"
	case "mub", "mut":
		return "mub"
	case "rob", "ref":
		return "rob"
	}
	return ""
}

// capOf returns the capability v holds so far from directives or cap markers, or "".
func (a *applier) capOf(v *types.Var) string {
	switch {
	case a.ix.IsoVars[v]:
		return "iso"
	case a.ix.MubVars[v]:
		return "mub"
	case a.ix.RobVars[v]:
		return "rob"
	}
	return ""
}

// conflict says why v cannot also take capability want, or "". It is the one check for parameters
// and bindings: a variable is owned, a mutable borrow or a read-only borrow, never two of them, and
// two directives that say the same thing about it are a duplicate.
func (a *applier) conflict(v *types.Var, want string) string {
	have := a.capOf(v)
	switch {
	case have == "":
		return ""
	case have == want:
		if a.bound[v] == want {
			return "which an earlier directive already annotates the same way"
		}
		return ""
	case want == "iso":
		return "which already has a conflicting capability/borrow annotation"
	case have == "iso":
		return "which already has an iso capability annotation"
	case have == "rob":
		return "which is already annotated as read-only borrow"
	}
	return "which is already annotated as mutable borrow"
}

// applyParamCaps gives each of vars the capability verb names, or reports why not and gives none.
func (a *applier) applyParamCaps(lead, verb string, vars []*types.Var) {
	want := capability(verb)
	seen := map[*types.Var]bool{}
	for _, v := range vars {
		if seen[v] {
			a.malformed("%s%s names %q twice", lead, verb, v.Name())
			return
		}
		seen[v] = true
		if why := a.conflict(v, want); why != "" {
			a.malformed("%s%s names %q, %s", lead, verb, v.Name(), why)
			return
		}
	}
	for _, v := range vars {
		a.bound[v] = want
		switch want {
		case "iso":
			a.ix.IsoVars[v] = true
		case "mub":
			a.ix.MubVars[v] = true
			a.ix.Borrows[v.Pos()] = BorrowMut
		case "rob":
			a.ix.RobVars[v] = true
			a.ix.Borrows[v.Pos()] = BorrowRob
		}
	}
}

func (a *applier) applyBorrow(lead, verb, args string, target ast.Node, kind BorrowKind) {
	if target == nil {
		a.malformed("%s%s is not attached to a function or assignment", lead, verb)
		return
	}
	if fd, ok := target.(*ast.FuncDecl); ok {
		if vars, ok := a.paramVars(lead, verb, args, fd); ok {
			a.applyParamCaps(lead, verb, vars)
		}
		return
	}

	// A local binding: an assignment, or a var declaration in a function body. Everything is checked
	// before anything is recorded.
	var lhs, rhs []ast.Expr
	switch n := target.(type) {
	case *ast.AssignStmt:
		lhs, rhs = n.Lhs, n.Rhs
	case *ast.DeclStmt:
		gd, ok := n.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			a.malformed("%s%s must be attached to a function declaration or an assignment statement", lead, verb)
			return
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, id := range vs.Names {
					lhs = append(lhs, id)
				}
				rhs = append(rhs, vs.Values...)
			}
		}
	default:
		a.malformed("%s%s must be attached to a function declaration or an assignment statement", lead, verb)
		return
	}
	if args != "" {
		matched := false
		for _, e := range lhs {
			if id, ok := e.(*ast.Ident); ok && id.Name == args {
				matched = true
			}
		}
		if !matched {
			a.malformed("%s%s on assignment names %q, which does not match assigned variable", lead, verb, args)
			return
		}
	}
	if prev, ok := a.onNode[target]; ok {
		if prev == kind {
			a.malformed("%s%s repeats the borrow directive already on this statement", lead, verb)
		} else {
			a.malformed("%s%s conflicts with the //gocap:%s directive already on this statement: a binding is a mutable or a read-only borrow, not both", lead, verb, prev)
		}
		return
	}
	want := capability(verb)
	var vars []*types.Var
	for _, e := range lhs {
		id, ok := e.(*ast.Ident)
		if !ok {
			continue
		}
		v, ok := a.pass.TypesInfo.Defs[id].(*types.Var)
		if !ok || v == nil {
			v, _ = a.pass.TypesInfo.Uses[id].(*types.Var)
		}
		if v == nil {
			continue
		}
		if have := a.capOf(v); have != "" && have != want {
			a.malformed("%s%s names %q, %s", lead, verb, v.Name(), a.conflict(v, want))
			return
		}
		vars = append(vars, v)
	}
	a.onNode[target] = kind
	ix := a.ix
	ix.Borrows[target.Pos()] = kind
	ix.Borrows[a.c.Pos()] = kind
	for _, e := range rhs {
		ix.Borrows[e.Pos()] = kind
		if ue, ok := e.(*ast.UnaryExpr); ok {
			ix.Borrows[ue.OpPos] = kind
		}
	}
	for _, e := range lhs {
		ix.Borrows[e.Pos()] = kind
	}
	for _, v := range vars {
		if kind == BorrowMut {
			ix.MubVars[v] = true
		} else {
			ix.RobVars[v] = true
		}
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

// lineStarts maps each line to the outermost statement, declaration, spec or struct field that starts
// on it (and ends on it, for multi-line statements), and records the struct each field belongs to.
func lineStarts(fset *token.FileSet, file *ast.File) (map[int]ast.Node, map[int]ast.Node, map[*ast.Field]*ast.StructType) {
	starts := map[int]ast.Node{}
	ends := map[int]ast.Node{}
	structOf := map[*ast.Field]*ast.StructType{}
	mark := func(n ast.Node) {
		line := physLine(fset, n.Pos())
		if _, seen := starts[line]; !seen {
			starts[line] = n
		}
	}
	markEnd := func(n ast.Node) {
		switch n.(type) {
		case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
			*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.FuncDecl:
			return
		}
		endLine := physLine(fset, n.End())
		if _, seen := ends[endLine]; !seen {
			ends[endLine] = n
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Stmt, ast.Decl, ast.Spec:
			mark(n)
			markEnd(n)
		case *ast.StructType:
			for _, f := range n.Fields.List {
				structOf[f] = n
				mark(f)
				markEnd(f)
			}
		}
		return true
	})
	return starts, ends, structOf
}

// attach finds the node a directive applies to: a node starting or ending on the same line (inline is
// then true), otherwise the node that starts on the line after its comment group. Lines are physical
// lines: a //line directive renumbers what is printed, not what a directive is written next to.
func attach(fset *token.FileSet, cg *ast.CommentGroup, c *ast.Comment, starts, ends map[int]ast.Node) (ast.Node, bool) {
	line := physLine(fset, c.Slash)
	if n, ok := starts[line]; ok {
		return n, true
	}
	if n, ok := ends[line]; ok {
		return n, true
	}
	n := starts[physLine(fset, cg.End())+1]
	return n, false
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

func isCapPkg(pkg *types.Package) bool {
	if pkg == nil {
		return false
	}
	path := pkg.Path()
	return path == "github.com/bharathvbcr/gosree/cap" ||
		path == "github.com/bharathvbcr/gocap/cap" ||
		strings.HasSuffix(path, "/cap") ||
		pkg.Name() == "cap"
}

func capSymbol(pass *analysis.Pass, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	expr = unparen(expr)
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return capSymbol(pass, e.X)
	case *ast.IndexListExpr:
		return capSymbol(pass, e.X)
	case *ast.Ident:
		if pass != nil && pass.TypesInfo != nil {
			if obj, ok := pass.TypesInfo.Uses[e]; ok && obj != nil && isCapPkg(obj.Pkg()) {
				return obj.Name()
			}
			if obj, ok := pass.TypesInfo.Defs[e]; ok && obj != nil && isCapPkg(obj.Pkg()) {
				return obj.Name()
			}
		}
		switch e.Name {
		case "Mut", "Mub", "Ref", "Rob", "Iso", "Imm",
			"BorrowMut", "BorrowMub", "Borrow", "BorrowRef", "BorrowRob", "Move":
			return e.Name
		}
	case *ast.SelectorExpr:
		if pass != nil && pass.TypesInfo != nil {
			if obj, ok := pass.TypesInfo.Uses[e.Sel]; ok && obj != nil && isCapPkg(obj.Pkg()) {
				return obj.Name()
			}
			if xId, ok := unparen(e.X).(*ast.Ident); ok {
				if obj, ok := pass.TypesInfo.Uses[xId]; ok && obj != nil {
					if pkgName, ok := obj.(*types.PkgName); ok && isCapPkg(pkgName.Imported()) {
						return e.Sel.Name
					}
				}
			}
		}
		if xId, ok := unparen(e.X).(*ast.Ident); ok && (xId.Name == "cap" || strings.HasSuffix(xId.Name, "cap")) {
			return e.Sel.Name
		}
	}
	return ""
}

func scanParamFieldLists(pass *analysis.Pass, ix *Index, lists []*ast.FieldList) {
	for _, fl := range lists {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			sym := capSymbol(pass, f.Type)
			switch sym {
			case "Mut", "Mub":
				for _, id := range f.Names {
					if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
						ix.MubVars[v] = true
						ix.Borrows[v.Pos()] = BorrowMut
						ix.Borrows[id.Pos()] = BorrowMut
					}
				}
			case "Ref", "Rob":
				for _, id := range f.Names {
					if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
						ix.RobVars[v] = true
						ix.Borrows[v.Pos()] = BorrowRob
						ix.Borrows[id.Pos()] = BorrowRob
					}
				}
			case "Iso":
				for _, id := range f.Names {
					if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
						ix.IsoVars[v] = true
					}
				}
			}
		}
	}
}

func scanCapMarkers(pass *analysis.Pass, ix *Index, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			scanParamFieldLists(pass, ix, []*ast.FieldList{node.Recv, node.Type.Params})
		case *ast.FuncLit:
			if node.Type != nil {
				scanParamFieldLists(pass, ix, []*ast.FieldList{node.Type.Params})
			}

		case *ast.CallExpr:
			sym := capSymbol(pass, node.Fun)
			switch sym {
			case "BorrowMut", "BorrowMub":
				ix.Borrows[node.Pos()] = BorrowMut
			case "Borrow", "BorrowRef", "BorrowRob":
				ix.Borrows[node.Pos()] = BorrowRob
			}

		case *ast.AssignStmt:
			for i, rhs := range node.Rhs {
				rhsExpr := unparen(rhs)
				var kind BorrowKind = BorrowNone
				if call, ok := rhsExpr.(*ast.CallExpr); ok {
					sym := capSymbol(pass, call.Fun)
					switch sym {
					case "BorrowMut", "BorrowMub":
						kind = BorrowMut
					case "Borrow", "BorrowRef", "BorrowRob":
						kind = BorrowRob
					}
				}
				if kind != BorrowNone {
					ix.Borrows[rhsExpr.Pos()] = kind
					ix.Borrows[node.Pos()] = kind
					if i < len(node.Lhs) {
						lhsExpr := unparen(node.Lhs[i])
						ix.Borrows[lhsExpr.Pos()] = kind
						if id, ok := lhsExpr.(*ast.Ident); ok {
							var v *types.Var
							if tv, ok := pass.TypesInfo.Defs[id].(*types.Var); ok {
								v = tv
							} else if tv, ok := pass.TypesInfo.Uses[id].(*types.Var); ok {
								v = tv
							}
							if v != nil {
								if kind == BorrowMut {
									ix.MubVars[v] = true
								} else {
									ix.RobVars[v] = true
								}
								ix.Borrows[v.Pos()] = kind
							}
						}
					}
				}
			}

		case *ast.GenDecl:
			if node.Tok == token.VAR {
				for _, spec := range node.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if vs.Type != nil {
						sym := capSymbol(pass, vs.Type)
						var kind BorrowKind = BorrowNone
						switch sym {
						case "Mut", "Mub":
							kind = BorrowMut
						case "Ref", "Rob":
							kind = BorrowRob
						case "Iso":
							for _, id := range vs.Names {
								if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
									ix.IsoVars[v] = true
								}
							}
						}
						if kind != BorrowNone {
							ix.Borrows[vs.Pos()] = kind
							ix.Borrows[node.Pos()] = kind
							for _, id := range vs.Names {
								ix.Borrows[id.Pos()] = kind
								if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
									if kind == BorrowMut {
										ix.MubVars[v] = true
									} else {
										ix.RobVars[v] = true
									}
									ix.Borrows[v.Pos()] = kind
								}
							}
						}
					}
					for i, val := range vs.Values {
						valExpr := unparen(val)
						var kind BorrowKind = BorrowNone
						if call, ok := valExpr.(*ast.CallExpr); ok {
							sym := capSymbol(pass, call.Fun)
							switch sym {
							case "BorrowMut", "BorrowMub":
								kind = BorrowMut
							case "Borrow", "BorrowRef", "BorrowRob":
								kind = BorrowRob
							}
						}
						if kind != BorrowNone {
							ix.Borrows[valExpr.Pos()] = kind
							ix.Borrows[vs.Pos()] = kind
							ix.Borrows[node.Pos()] = kind
							if i < len(vs.Names) {
								id := vs.Names[i]
								ix.Borrows[id.Pos()] = kind
								if v, ok := pass.TypesInfo.Defs[id].(*types.Var); ok && v != nil {
									if kind == BorrowMut {
										ix.MubVars[v] = true
									} else {
										ix.RobVars[v] = true
									}
									ix.Borrows[v.Pos()] = kind
								}
							}
						}
					}
				}
			} else if node.Tok == token.TYPE {
				for _, spec := range node.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if ts.Type != nil {
						sym := capSymbol(pass, ts.Type)
						if sym == "Imm" {
							if tn, ok := pass.TypesInfo.Defs[ts.Name].(*types.TypeName); ok && tn != nil {
								ix.ImmTypes[tn] = ts.Pos()
								ix.ImmPrefix[tn] = "//gocap:"
							}
						}
					}
				}
			}
		}
		return true
	})
}
