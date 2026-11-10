package annot

import (
	"go/ast"
	"go/token"
)

// A hatch is one //gosree:unsafe scope. It covers the positions in [start, end) that are in none of
// its holes. Which code that is follows one rule a reader can apply by eye:
//
// A hatch covers the code written under it: on its own line above a statement, or in a declaration's
// doc comment, it covers that whole statement or declaration (doc comment included); written on the
// same line as code, it covers that line's statement except the bodies of blocks in it that span
// several lines.
type hatch struct {
	start, end token.Pos
	// holes are disjoint and in source order.
	holes []span
	// at and atEnd delimit the directive comment, where a stale hatch is reported.
	at, atEnd token.Pos
}

// Hatch is a //gosree:unsafe directive: Pos and End delimit its comment.
type Hatch struct{ Pos, End token.Pos }

func (h *hatch) covers(pos token.Pos) bool {
	if pos < h.start || pos >= h.end {
		return false
	}
	for _, s := range h.holes {
		if pos >= s.start && pos < s.end {
			return false
		}
	}
	return true
}

func (h *hatch) size() token.Pos { return h.end - h.start }

// sameScope reports whether h and o cover exactly the same code.
func (h *hatch) sameScope(o *hatch) bool {
	if h.start != o.start || h.end != o.end || len(h.holes) != len(o.holes) {
		return false
	}
	for i := range h.holes {
		if h.holes[i] != o.holes[i] {
			return false
		}
	}
	return true
}

// newHatch builds the hatch the directive c (in group cg) installs on target. inline is true when the
// directive shares a line with the code it is attached to. It returns ok=false when the hatch would
// cover nothing at all: a hatch on the line of an opening brace, which no reader can tell apart from a
// hatch on the block's first statement.
func newHatch(fset *token.FileSet, target ast.Node, cg *ast.CommentGroup, c *ast.Comment, inline bool) (*hatch, bool) {
	h := &hatch{start: target.Pos(), end: target.End(), at: c.Pos(), atEnd: c.End()}
	if doc := docOf(target); doc != nil && doc == cg {
		// A declaration's doc comment is part of the declaration: it holds //go:linkname and the
		// like, which the hatch on the declaration is about.
		h.start = cg.Pos()
	}
	if !inline {
		return h, true
	}
	switch t := target.(type) {
	case *ast.BlockStmt:
		if multiLine(fset, t) {
			return nil, false
		}
	case *ast.CaseClause:
		// A clause the directive trails covers its case expressions, not the statements under it.
		if multiLine(fset, t) {
			h.holes = append(h.holes, span{t.Colon + 1, t.End()})
		}
		return h, true
	case *ast.CommClause:
		if multiLine(fset, t) {
			h.holes = append(h.holes, span{t.Colon + 1, t.End()})
		}
		return h, true
	}
	// Clauses live in a switch or select body, so the outermost multi-line blocks are all the holes.
	ast.Inspect(target, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStmt); ok && multiLine(fset, b) {
			h.holes = append(h.holes, span{b.Lbrace, b.End()})
			return false
		}
		return true
	})
	return h, true
}

// multiLine reports whether n starts and ends on different physical lines.
func multiLine(fset *token.FileSet, n ast.Node) bool {
	return physLine(fset, n.Pos()) != physLine(fset, n.End())
}

// physLine is the line pos is on in the file as written. Every attachment decision uses it, never the
// //line-adjusted line: a //line directive must not move a directive onto other code.
func physLine(fset *token.FileSet, pos token.Pos) int {
	return fset.PositionFor(pos, false).Line
}

// docOf returns the doc comment of a declaration, spec or field, or nil.
func docOf(n ast.Node) *ast.CommentGroup {
	switch n := n.(type) {
	case *ast.FuncDecl:
		return n.Doc
	case *ast.GenDecl:
		return n.Doc
	case *ast.TypeSpec:
		return n.Doc
	case *ast.ValueSpec:
		return n.Doc
	case *ast.ImportSpec:
		return n.Doc
	case *ast.Field:
		return n.Doc
	}
	return nil
}
