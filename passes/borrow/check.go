package borrow

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gocap/internal/annot"
)

// Diagnostic Categories for Borrow Checking
const (
	ConflictingBorrow = "BORROW002"
	OwnerInvalidation = "BORROW003"
	DanglingReturn    = "BORROW004"
)

// node represents an equivalence class in Steensgaard alias analysis.
type node struct {
	parent  *node
	pointee *node
}

func (n *node) find() *node {
	for n.parent != nil {
		if n.parent.parent != nil {
			n.parent = n.parent.parent
		}
		n = n.parent
	}
	return n
}

type graph struct {
	fn    *ssa.Function
	nodes map[ssa.Value]*node
}

func newGraph(fn *ssa.Function) *graph {
	g := &graph{fn: fn, nodes: map[ssa.Value]*node{}}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			g.instr(ins)
		}
	}
	return g
}

func (g *graph) val(v ssa.Value) *node {
	if v == nil {
		return nil
	}
	if n, ok := g.nodes[v]; ok {
		if n == nil {
			return nil
		}
		return n.find()
	}
	n := &node{}
	g.nodes[v] = n
	return n
}

func (g *graph) pointee(n *node) *node {
	n = n.find()
	if n.pointee == nil {
		n.pointee = &node{}
	}
	return n.pointee.find()
}

func (g *graph) union(a, b *node) {
	if a == nil || b == nil {
		return
	}
	a, b = a.find(), b.find()
	if a == b {
		return
	}
	b.parent = a
	pa, pb := a.pointee, b.pointee
	b.pointee = nil
	switch {
	case pa == nil:
		a.pointee = pb
	case pb != nil:
		g.union(pa, pb)
		a.pointee = pa.find()
	}
}

func (g *graph) join(v, x ssa.Value) {
	vn, xn := g.val(v), g.val(x)
	if vn != nil && xn != nil {
		g.union(vn, xn)
	}
}

func (g *graph) instr(ins ssa.Instruction) {
	switch ins := ins.(type) {
	case *ssa.Alloc:
		g.val(ins)
	case *ssa.Phi:
		for _, e := range ins.Edges {
			g.join(ins, e)
		}
	case *ssa.FieldAddr:
		g.join(ins, ins.X)
	case *ssa.IndexAddr:
		g.join(ins, ins.X)
	case *ssa.Slice:
		g.join(ins, ins.X)
	case *ssa.ChangeType:
		g.join(ins, ins.X)
	case *ssa.Convert:
		g.join(ins, ins.X)
	case *ssa.UnOp:
		if ins.Op == token.MUL {
			if xn := g.val(ins.X); xn != nil {
				g.union(g.val(ins), g.pointee(xn))
			}
		}
	case *ssa.Store:
		vn := g.val(ins.Val)
		an := g.val(ins.Addr)
		if an != nil && vn != nil {
			g.union(g.pointee(an), vn)
		}
	}
}

// Borrow represents an active or tracked second-class borrow.
type Borrow struct {
	ID        int
	Kind      annot.BorrowKind
	OwnerNode *node
	OwnerName string
	OwnerVar  *types.Var
	OwnerVal  ssa.Value
	Pos       token.Pos
	Line      int
	Site      ssa.Instruction
	Var       *types.Var
	Val       ssa.Value
	Uses      map[ssa.Instruction]bool
}

type borrowChecker struct {
	pass     *analysis.Pass
	ix       *annot.Index
	ssa      *buildssa.SSA
	fn       *ssa.Function
	graph    *graph
	borrows  []*Borrow
	reports  map[reportKey]bool
	deferred map[*types.Var]bool
}

// CheckBorrows performs local SSA borrow checking and NLL conflict analysis for all functions in the package.
func CheckBorrows(pass *analysis.Pass, ix *annot.Index, ssaProg *buildssa.SSA) {
	for _, fn := range ssaProg.SrcFuncs {
		if len(fn.Blocks) == 0 {
			continue
		}
		bc := &borrowChecker{
			pass:     pass,
			ix:       ix,
			ssa:      ssaProg,
			fn:       fn,
			reports:  make(map[reportKey]bool),
			deferred: make(map[*types.Var]bool),
		}
		bc.run()
	}
}

func (bc *borrowChecker) report(pos token.Pos, cat, msg string) {
	if bc.ix.Suppressed(pos) {
		return
	}
	k := reportKey{pos, cat}
	if bc.reports[k] {
		return
	}
	bc.reports[k] = true
	bc.pass.Report(analysis.Diagnostic{
		Pos:      pos,
		Category: cat,
		Message:  cat + ": " + msg,
	})
}

func (bc *borrowChecker) run() {
	bc.graph = newGraph(bc.fn)
	bc.findBorrows()
	bc.checkConflictingBorrows()
	bc.checkOwnerMutation()
	bc.checkDanglingReturn()
	bc.checkLoopEscapes()
}

func (bc *borrowChecker) findBorrows() {
	// 1. Parameter Borrows
	for _, p := range bc.fn.Params {
		if vr, ok := p.Object().(*types.Var); ok {
			var kind annot.BorrowKind
			if bc.ix.IsMub(vr) {
				kind = annot.BorrowMut
			} else if bc.ix.IsRob(vr) {
				kind = annot.BorrowRob
			}
			if kind != annot.BorrowNone {
				var site ssa.Instruction
				if len(bc.fn.Blocks) > 0 && len(bc.fn.Blocks[0].Instrs) > 0 {
					site = bc.fn.Blocks[0].Instrs[0]
				}
				b := &Borrow{
					ID:        len(bc.borrows) + 1,
					Kind:      kind,
					OwnerNode: bc.graph.val(p),
					OwnerName: vr.Name(),
					OwnerVar:  vr,
					OwnerVal:  p,
					Pos:       p.Pos(),
					Line:      bc.pass.Fset.Position(p.Pos()).Line,
					Site:      site,
					Var:       vr,
					Val:       p,
					Uses:      make(map[ssa.Instruction]bool),
				}
				bc.collectUses(b)
				bc.borrows = append(bc.borrows, b)
			}
		}
	}

	// 2. AST Local Assignments & Directives
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return
	}

	// Track deferred variables
	ast.Inspect(syntax, func(n ast.Node) bool {
		if def, ok := n.(*ast.DeferStmt); ok {
			ast.Inspect(def.Call, func(sub ast.Node) bool {
				if id, ok := sub.(*ast.Ident); ok {
					if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
						bc.deferred[v] = true
					}
				}
				return true
			})
		}
		return true
	})

	ast.Inspect(syntax, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for i, lhs := range assign.Lhs {
			lhsIdent, ok := unparen(lhs).(*ast.Ident)
			if !ok {
				continue
			}
			v, _ := bc.pass.TypesInfo.Defs[lhsIdent].(*types.Var)
			if v == nil {
				v, _ = bc.pass.TypesInfo.Uses[lhsIdent].(*types.Var)
			}
			if v == nil {
				continue
			}

			// Determine borrow kind strictly from annot.Index
			var kind annot.BorrowKind
			if bc.ix.IsMub(v) {
				kind = annot.BorrowMut
			} else if bc.ix.IsRob(v) {
				kind = annot.BorrowRob
			} else if k := bc.ix.BorrowAt(assign.Pos()); k != annot.BorrowNone {
				kind = k
			} else if i < len(assign.Rhs) && bc.ix.BorrowAt(assign.Rhs[i].Pos()) != annot.BorrowNone {
				kind = bc.ix.BorrowAt(assign.Rhs[i].Pos())
			} else if i < len(assign.Rhs) {
				unwrapped := unparen(assign.Rhs[i])
				if ue, ok := unwrapped.(*ast.UnaryExpr); ok && bc.ix.BorrowAt(ue.OpPos) != annot.BorrowNone {
					kind = bc.ix.BorrowAt(ue.OpPos)
				}
				if ue, ok := assign.Rhs[i].(*ast.UnaryExpr); ok && bc.ix.BorrowAt(ue.OpPos) != annot.BorrowNone {
					kind = bc.ix.BorrowAt(ue.OpPos)
				}
			}

			if kind == annot.BorrowNone {
				continue
			}

			// Determine owner
			ownerName := "x"
			var ownerVar *types.Var
			var ownerVal ssa.Value
			if i < len(assign.Rhs) {
				ownerName, ownerVar, ownerVal = bc.resolveOwner(assign.Rhs[i])
			}

			var ownerNode *node
			if ownerVal != nil {
				ownerNode = bc.graph.val(ownerVal)
			}
			if ownerNode == nil {
				for _, l := range bc.fn.Locals {
					if l.Comment == ownerName {
						ownerVal = l
						ownerNode = bc.graph.val(l)
						break
					}
				}
			}

			pos := lhsIdent.Pos()
			line := bc.pass.Fset.Position(pos).Line
			site := bc.findInstructionAtOrAfter(pos)

			b := &Borrow{
				ID:        len(bc.borrows) + 1,
				Kind:      kind,
				OwnerNode: ownerNode,
				OwnerName: ownerName,
				OwnerVar:  ownerVar,
				OwnerVal:  ownerVal,
				Pos:       pos,
				Line:      line,
				Site:      site,
				Var:       v,
				Uses:      make(map[ssa.Instruction]bool),
			}
			bc.collectUses(b)
			bc.borrows = append(bc.borrows, b)
		}
		return true
	})
}

func (bc *borrowChecker) resolveOwner(rhs ast.Expr) (string, *types.Var, ssa.Value) {
	switch e := rhs.(type) {
	case *ast.ParenExpr:
		return bc.resolveOwner(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return bc.resolveOwner(e.X)
		}
	case *ast.Ident:
		name := e.Name
		var vr *types.Var
		if v, ok := bc.pass.TypesInfo.Uses[e].(*types.Var); ok {
			vr = v
		} else if v, ok := bc.pass.TypesInfo.Defs[e].(*types.Var); ok {
			vr = v
		}

		for _, prev := range bc.borrows {
			if prev.Var != nil {
				if vr != nil {
					if prev.Var == vr {
						return prev.OwnerName, prev.OwnerVar, prev.OwnerVal
					}
				} else if prev.Var.Name() == name {
					return prev.OwnerName, prev.OwnerVar, prev.OwnerVal
				}
			}
		}
		// Also trace earlier pointer alias assignments in AST: e.g. p := &x; b1 := p
		if syntax := bc.fn.Syntax(); syntax != nil {
			var origName string
			var origVar *types.Var
			ast.Inspect(syntax, func(n ast.Node) bool {
				if asgn, ok := n.(*ast.AssignStmt); ok {
					for idx, lhs := range asgn.Lhs {
						if id, ok := unparen(lhs).(*ast.Ident); ok {
							var idVar *types.Var
							if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
								idVar = v
							} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
								idVar = v
							}
							isTarget := false
							if vr != nil && idVar != nil {
								isTarget = (vr == idVar)
							} else if id.Name == name {
								isTarget = true
							}
							if isTarget && idx < len(asgn.Rhs) {
								if ue, ok := unparen(asgn.Rhs[idx]).(*ast.UnaryExpr); ok && ue.Op == token.AND {
									if baseId, ok := unparen(ue.X).(*ast.Ident); ok {
										origName = baseId.Name
										if v, ok := bc.pass.TypesInfo.Uses[baseId].(*types.Var); ok {
											origVar = v
										} else if v, ok := bc.pass.TypesInfo.Defs[baseId].(*types.Var); ok {
											origVar = v
										}
										return false
									}
								}
							}
						}
					}
				}
				return true
			})
			if origName != "" {
				name = origName
				if origVar != nil {
					vr = origVar
				}
			}
		}
		// 1. If variable identity is known, prefer address-taken allocation in basic blocks
		if vr != nil {
			for _, b := range bc.fn.Blocks {
				for _, ins := range b.Instrs {
					if alloc, ok := ins.(*ssa.Alloc); ok && alloc.Pos() == vr.Pos() {
						return name, vr, alloc
					}
				}
			}
			for _, l := range bc.fn.Locals {
				if l.Pos() == vr.Pos() {
					return name, vr, l
				}
			}
			for _, p := range bc.fn.Params {
				if p.Pos() == vr.Pos() || (p.Object() != nil && p.Object() == vr) {
					return name, vr, p
				}
			}
		}

		// 2. Fallback to lexical name matching only when vr is nil
		for _, b := range bc.fn.Blocks {
			for _, ins := range b.Instrs {
				if alloc, ok := ins.(*ssa.Alloc); ok && alloc.Comment == name {
					return name, vr, alloc
				}
			}
		}
		for _, l := range bc.fn.Locals {
			if l.Comment == name {
				return name, vr, l
			}
		}
		for _, p := range bc.fn.Params {
			if p.Name() == name {
				return name, vr, p
			}
		}
		return name, vr, nil
	case *ast.SelectorExpr:
		return bc.resolveOwner(e.X)
	case *ast.IndexExpr:
		return bc.resolveOwner(e.X)
	}
	return "x", nil, nil
}

func (bc *borrowChecker) findInstructionAtOrAfter(pos token.Pos) ssa.Instruction {
	line := bc.pass.Fset.Position(pos).Line
	var best ssa.Instruction

	for _, b := range bc.fn.Blocks {
		for _, ins := range b.Instrs {
			insLine := bc.pass.Fset.Position(ins.Pos()).Line
			if insLine == line {
				return ins
			}
			if insLine > line && (best == nil || insLine < bc.pass.Fset.Position(best.Pos()).Line) {
				best = ins
			}
		}
	}
	if best != nil {
		return best
	}
	if len(bc.fn.Blocks) > 0 && len(bc.fn.Blocks[0].Instrs) > 0 {
		return bc.fn.Blocks[0].Instrs[0]
	}
	return nil
}

func (bc *borrowChecker) collectUses(b *Borrow) {
	syntax := bc.fn.Syntax()
	if syntax == nil || b.Var == nil {
		return
	}

	// 1. Collect all AST lines where b.Var is used
	useLines := make(map[int]bool)
	isDeferred := bc.deferred[b.Var]

	ast.Inspect(syntax, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && v == b.Var {
			line := bc.pass.Fset.Position(id.Pos()).Line
			if line >= b.Line {
				useLines[line] = true
			}
		}
		return true
	})

	// 2. Map SSA instructions on those lines to b.Uses
	for _, blk := range bc.fn.Blocks {
		for _, ins := range blk.Instrs {
			insLine := bc.pass.Fset.Position(ins.Pos()).Line
			if useLines[insLine] {
				b.Uses[ins] = true
			}
			if isDeferred {
				if _, ok := ins.(*ssa.Return); ok {
					b.Uses[ins] = true
				}
			}
		}
	}
}

// isBorrowActiveAt reports whether borrow b is active at instruction t.
func (bc *borrowChecker) isBorrowActiveAt(b *Borrow, t ssa.Instruction) bool {
	if b == nil || t == nil || b.Site == nil {
		return false
	}
	if !bc.isReachable(b.Site, t) {
		return false
	}
	if b.Site.Block() == t.Block() && indexIn(t) < indexIn(b.Site) {
		return false
	}

	startBlock := t.Block()
	tIdx := indexIn(t)

	// Check remaining instructions in startBlock
	for i := tIdx; i < len(startBlock.Instrs); i++ {
		ins := startBlock.Instrs[i]
		if b.Uses[ins] {
			return true
		}
	}

	// BFS across successor basic blocks
	visited := map[*ssa.BasicBlock]bool{startBlock: true}
	queue := append([]*ssa.BasicBlock(nil), startBlock.Succs...)
	for len(queue) > 0 {
		blk := queue[0]
		queue = queue[1:]
		if visited[blk] {
			continue
		}
		visited[blk] = true

		for _, ins := range blk.Instrs {
			if b.Uses[ins] {
				return true
			}
		}
		queue = append(queue, blk.Succs...)
	}

	return false
}

func (bc *borrowChecker) isReachable(from, to ssa.Instruction) bool {
	if from == nil || to == nil || from.Block() == nil || to.Block() == nil {
		return false
	}
	if from.Block() == to.Block() {
		return indexIn(to) >= indexIn(from)
	}
	visited := map[*ssa.BasicBlock]bool{from.Block(): true}
	queue := append([]*ssa.BasicBlock(nil), from.Block().Succs...)
	for len(queue) > 0 {
		blk := queue[0]
		queue = queue[1:]
		if blk == to.Block() {
			return true
		}
		if visited[blk] {
			continue
		}
		visited[blk] = true
		queue = append(queue, blk.Succs...)
	}
	return false
}

// Pass A: Conflicting Borrow Check (BORROW002)
func (bc *borrowChecker) checkConflictingBorrows() {
	for i, b2 := range bc.borrows {
		for j := 0; j < i; j++ {
			b1 := bc.borrows[j]

			// Escape hatch check on either borrow site
			if bc.ix.Suppressed(b1.Pos) || bc.ix.Suppressed(b2.Pos) {
				continue
			}

			// Check if both borrows target the same owner
			sameOwner := false
			if b1.OwnerVar != nil && b2.OwnerVar != nil {
				sameOwner = (b1.OwnerVar == b2.OwnerVar)
			} else if b1.OwnerNode != nil && b2.OwnerNode != nil && b1.OwnerNode.find() == b2.OwnerNode.find() {
				sameOwner = true
			} else if b1.OwnerVal != nil && b2.OwnerVal != nil && b1.OwnerVal == b2.OwnerVal {
				sameOwner = true
			} else if b1.OwnerName != "" && b1.OwnerName == b2.OwnerName {
				sameOwner = true
			}

			if !sameOwner {
				continue
			}

			// Check if b1 is active at b2's creation site
			if !bc.isBorrowActiveAt(b1, b2.Site) {
				continue
			}

			// Conflict rules:
			if b1.Kind == annot.BorrowMut && b2.Kind == annot.BorrowMut {
				bc.report(b2.Pos, ConflictingBorrow, fmt.Sprintf(
					"cannot borrow '%s' as mutable more than once at a time: already borrowed as mutable at line %d",
					b2.OwnerName, b1.Line))
			} else if b1.Kind == annot.BorrowRob && b2.Kind == annot.BorrowMut {
				bc.report(b2.Pos, ConflictingBorrow, fmt.Sprintf(
					"cannot borrow '%s' as mutable because it is also borrowed as read-only at line %d",
					b2.OwnerName, b1.Line))
			} else if b1.Kind == annot.BorrowMut && b2.Kind == annot.BorrowRob {
				bc.report(b2.Pos, ConflictingBorrow, fmt.Sprintf(
					"cannot borrow '%s' as read-only because it is also borrowed as mutable at line %d",
					b2.OwnerName, b1.Line))
			}
		}
	}
}

// Pass B: Owner Invalidation Check (BORROW003)
func (bc *borrowChecker) checkOwnerMutation() {
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return
	}

	// 1. Direct Owner Assignment in AST: x = 20, x++, x += 1, pt.X = 20, arr[0] = 20
	ast.Inspect(syntax, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range stmt.Lhs {
				id := rootIdent(lhs)
				if id == nil {
					continue
				}
				var assignedVar *types.Var
				if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
					assignedVar = v
				} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
					assignedVar = v
				}
				name := id.Name
				line := bc.pass.Fset.Position(id.Pos()).Line
				ins := bc.findInstructionAtOrAfter(id.Pos())
				for _, brw := range bc.borrows {
					if assignedVar != nil && brw.Var == assignedVar && brw.Kind == annot.BorrowMut {
						continue
					}
					isOwner := false
					if brw.OwnerVar != nil && assignedVar != nil {
						isOwner = (brw.OwnerVar == assignedVar)
					} else if brw.OwnerName == name {
						isOwner = true
					}
					if isOwner && (line > brw.Line || (line == brw.Line && id.Pos() > brw.Pos)) && bc.isBorrowActiveAt(brw, ins) {
						bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
							"cannot assign to '%s' because it is borrowed at line %d",
							brw.OwnerName, brw.Line))
					}
				}
			}
		case *ast.IncDecStmt:
			id := rootIdent(stmt.X)
			if id == nil {
				break
			}
			var assignedVar *types.Var
			if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok {
				assignedVar = v
			} else if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok {
				assignedVar = v
			}
			name := id.Name
			line := bc.pass.Fset.Position(id.Pos()).Line
			ins := bc.findInstructionAtOrAfter(id.Pos())
			for _, brw := range bc.borrows {
				if assignedVar != nil && brw.Var == assignedVar && brw.Kind == annot.BorrowMut {
					continue
				}
				isOwner := false
				if brw.OwnerVar != nil && assignedVar != nil {
					isOwner = (brw.OwnerVar == assignedVar)
				} else if brw.OwnerName == name {
					isOwner = true
				}
				if isOwner && (line > brw.Line || (line == brw.Line && id.Pos() > brw.Pos)) && bc.isBorrowActiveAt(brw, ins) {
					bc.report(id.Pos(), OwnerInvalidation, fmt.Sprintf(
						"cannot assign to '%s' because it is borrowed at line %d",
						brw.OwnerName, brw.Line))
				}
			}
		}
		return true
	})

	// 2. Channel Send of Owner Allocation: ch <- x, ch <- pt.X, ch <- arr[0]
	for _, b := range bc.fn.Blocks {
		for _, ins := range b.Instrs {
			send, ok := ins.(*ssa.Send)
			if !ok {
				continue
			}
			// If what is being sent is a borrow variable, skip (handled as BORROW001 storage violation)
			line := bc.pass.Fset.Position(send.Pos()).Line
			isBorrowSend := false
			for _, brw := range bc.borrows {
				if brw.Val != nil && send.X == brw.Val {
					isBorrowSend = true
					break
				}
				if brw.Var != nil && syntax != nil {
					ast.Inspect(syntax, func(n ast.Node) bool {
						if snd, ok := n.(*ast.SendStmt); ok && bc.pass.Fset.Position(snd.Pos()).Line == line {
							if id := rootIdent(snd.Value); id != nil {
								if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && v == brw.Var {
									isBorrowSend = true
									return false
								}
								if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok && v == brw.Var {
									isBorrowSend = true
									return false
								}
							}
						}
						return true
					})
				}
				if isBorrowSend {
					break
				}
			}
			if isBorrowSend {
				continue
			}
			for _, brw := range bc.borrows {
				isOwnerSend := false
				if brw.OwnerVal != nil && (send.X == brw.OwnerVal || bc.isValOf(send.X, brw.OwnerVal)) {
					isOwnerSend = true
				}
				if !isOwnerSend {
					if alloc := baseAlloc(send.X); alloc != nil {
						if brw.OwnerVal != nil {
							if alloc == brw.OwnerVal {
								isOwnerSend = true
							}
						} else if brw.OwnerVar == nil && alloc.Comment == brw.OwnerName {
							isOwnerSend = true
						}
					}
				}
				if !isOwnerSend && syntax != nil {
					ast.Inspect(syntax, func(n ast.Node) bool {
						if snd, ok := n.(*ast.SendStmt); ok && bc.pass.Fset.Position(snd.Pos()).Line == line {
							if id := rootIdent(snd.Value); id != nil {
								if brw.OwnerVar != nil {
									if v, ok := bc.pass.TypesInfo.Uses[id].(*types.Var); ok && v == brw.OwnerVar {
										isOwnerSend = true
										return false
									}
									if v, ok := bc.pass.TypesInfo.Defs[id].(*types.Var); ok && v == brw.OwnerVar {
										isOwnerSend = true
										return false
									}
								}
							}
						}
						return true
					})
				}
				if isOwnerSend && (line > brw.Line || (line == brw.Line && send.Pos() > brw.Pos)) && bc.isBorrowActiveAt(brw, ins) {
					bc.report(send.Pos(), OwnerInvalidation, fmt.Sprintf(
						"cannot move '%s' because it is borrowed at line %d",
						brw.OwnerName, brw.Line))
				}
			}
		}
	}
}

func (bc *borrowChecker) isValOf(v, owner ssa.Value) bool {
	if v == owner {
		return true
	}
	if un, ok := v.(*ssa.UnOp); ok && un.Op == token.MUL {
		return un.X == owner
	}
	return false
}

// Pass C: Dangling Local Stack Return Check (BORROW004)
func (bc *borrowChecker) checkDanglingReturn() {
	for _, b := range bc.fn.Blocks {
		for _, ins := range b.Instrs {
			ret, ok := ins.(*ssa.Return)
			if !ok {
				continue
			}
			for _, r := range ret.Results {
				// Only pointer-typed return values can return a reference to stack memory
				if _, ok := r.Type().Underlying().(*types.Pointer); !ok {
					continue
				}
				origin := baseAlloc(r)
				if origin == nil {
					continue
				}
				// Verify allocated in current function activation
				if origin.Block().Parent() == bc.fn {
					comment := origin.Comment
					if comment != "" && comment != "complit" && comment != "new" && comment != "makeslice" && comment != "slicelit" {
						if _, isField := r.(*ssa.FieldAddr); isField {
							bc.report(ret.Pos(), DanglingReturn,
								"cannot return reference to local stack variable")
						} else {
							bc.report(ret.Pos(), DanglingReturn, fmt.Sprintf(
								"cannot return reference to local stack variable '%s'",
								comment))
						}
					}
				}
			}
		}
	}
}

func baseAlloc(v ssa.Value) *ssa.Alloc {
	seen := map[ssa.Value]bool{}
	for v != nil && !seen[v] {
		seen[v] = true
		switch u := v.(type) {
		case *ssa.Alloc:
			return u
		case *ssa.FieldAddr:
			v = u.X
		case *ssa.IndexAddr:
			v = u.X
		case *ssa.Slice:
			v = u.X
		case *ssa.ChangeType:
			v = u.X
		case *ssa.Convert:
			v = u.X
		case *ssa.MakeInterface:
			v = u.X
		case *ssa.UnOp:
			if u.Op == token.MUL {
				v = u.X
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	return nil
}

// Pass D: Loop-Carried Borrow Escape Check (BORROW002)
func (bc *borrowChecker) checkLoopEscapes() {
	syntax := bc.fn.Syntax()
	if syntax == nil {
		return
	}

	ast.Inspect(syntax, func(n ast.Node) bool {
		var loopPos token.Pos
		var loopBody *ast.BlockStmt

		switch loop := n.(type) {
		case *ast.ForStmt:
			loopPos = loop.Pos()
			loopBody = loop.Body
		case *ast.RangeStmt:
			loopPos = loop.Pos()
			loopBody = loop.Body
		default:
			return true
		}

		if loopBody == nil {
			return true
		}

		ast.Inspect(loopBody, func(inner ast.Node) bool {
			assign, ok := inner.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				id := rootIdent(lhs)
				if id == nil {
					continue
				}
				v, _ := bc.pass.TypesInfo.Uses[id].(*types.Var)
				if v == nil {
					v, _ = bc.pass.TypesInfo.Defs[id].(*types.Var)
				}
				if v == nil {
					continue
				}
				// Check if v was defined before the loop statement
				if v.Pos() < loopPos {
					if i >= len(assign.Rhs) {
						continue
					}
					rhs := assign.Rhs[i]
					unwrapped := unparen(rhs)

					// Case 1: RHS is an identifier referencing an active mutable borrow
					if rhsId, ok := unwrapped.(*ast.Ident); ok {
						var rhsVar *types.Var
						if v, ok := bc.pass.TypesInfo.Uses[rhsId].(*types.Var); ok {
							rhsVar = v
						} else if v, ok := bc.pass.TypesInfo.Defs[rhsId].(*types.Var); ok {
							rhsVar = v
						}
						for _, brw := range bc.borrows {
							isBorrowVar := false
							if brw.Var != nil && rhsVar != nil {
								isBorrowVar = (brw.Var == rhsVar)
							} else if brw.Var != nil && brw.Var.Name() == rhsId.Name {
								isBorrowVar = true
							}
							if brw.Kind == annot.BorrowMut && isBorrowVar {
								bc.report(assign.Pos(), ConflictingBorrow, fmt.Sprintf(
									"cannot borrow '%s' as mutable: loop-carried borrow escapes to outer scope",
									brw.OwnerName))
							}
						}
					}

					// Case 2: RHS is a direct borrow expression (&x /*gocap:mut*/)
					if ue, ok := unwrapped.(*ast.UnaryExpr); ok && ue.Op == token.AND {
						isMut := false
						if bc.ix.IsMub(v) {
							isMut = true
						} else if bc.ix.BorrowAt(assign.Pos()) == annot.BorrowMut {
							isMut = true
						} else if bc.ix.BorrowAt(rhs.Pos()) == annot.BorrowMut {
							isMut = true
						} else if bc.ix.BorrowAt(ue.OpPos) == annot.BorrowMut {
							isMut = true
						} else {
							line := bc.pass.Fset.Position(assign.Pos()).Line
							for _, brw := range bc.borrows {
								if brw.Line == line && brw.Kind == annot.BorrowMut {
									isMut = true
									break
								}
							}
						}
						if isMut {
							ownerName, _, _ := bc.resolveOwner(rhs)
							bc.report(assign.Pos(), ConflictingBorrow, fmt.Sprintf(
								"cannot borrow '%s' as mutable: loop-carried borrow escapes to outer scope",
								ownerName))
						}
					}
				}
			}
			return true
		})
		return true
	})
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

func rootIdent(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.SelectorExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		default:
			return nil
		}
	}
}

func indexIn(ins ssa.Instruction) int {
	if ins == nil || ins.Block() == nil {
		return -1
	}
	for i, x := range ins.Block().Instrs {
		if x == ins {
			return i
		}
	}
	return -1
}
