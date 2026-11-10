package errcheck

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"

	"github.com/bharathvbcr/gocap/internal/gusseterr"
)

type packageBuilder struct {
	pass       *analysis.Pass
	funcs      []*ssa.Function
	funcIdxMap map[*ssa.Function]uint32
	sites      []siteMeta
	// blankedCallSites maps (funcIdx, tupleSiteID) or slot use to the blanked assignment site.
	blankedBySite map[uint32]siteMeta
}

func newPackageBuilder(pass *analysis.Pass, funcs []*ssa.Function) *packageBuilder {
	idxMap := make(map[*ssa.Function]uint32, len(funcs))
	for i, fn := range funcs {
		idxMap[fn] = uint32(i)
	}
	b := &packageBuilder{
		pass:          pass,
		funcs:         funcs,
		funcIdxMap:    idxMap,
		sites:         []siteMeta{{pos: token.NoPos}}, // site 0 reserved
		blankedBySite: make(map[uint32]siteMeta),
	}
	return b
}

func (b *packageBuilder) addSite(pos token.Pos, detail, aux string) uint32 {
	id := uint32(len(b.sites))
	b.sites = append(b.sites, siteMeta{pos: pos, detail: detail, aux: aux})
	return id
}

func (b *packageBuilder) site(id uint32) siteMeta {
	if int(id) < len(b.sites) {
		return b.sites[id]
	}
	return siteMeta{}
}

func (b *packageBuilder) blankedCallSite(_ uint32, siteID uint32) (token.Pos, string, bool) {
	if sm, ok := b.blankedBySite[siteID]; ok {
		return sm.pos, sm.detail, true
	}
	return token.NoPos, "", false
}

func (b *packageBuilder) lowerAll() []gusseterr.FuncIR {
	out := make([]gusseterr.FuncIR, len(b.funcs))
	for i, fn := range b.funcs {
		out[i] = b.lowerFunc(uint32(i), fn)
	}
	return out
}

type funcLowerer struct {
	pkg             *packageBuilder
	funcIdx         uint32
	fn              *ssa.Function
	astInfo         *funcASTInfo
	usedBlankedVars map[*types.Var]bool

	valSlots        map[ssa.Value]uint32
	slotFlags       []uint8
	localCells      map[*ssa.Alloc]bool
	loadedCell      map[ssa.Value]*ssa.Alloc
	constInits      []gusseterr.OpIR
	mem2regErrSlots map[*types.Var]uint32
	mem2regErrOrder []*types.Var
	callErrVar      map[*ssa.Call]*types.Var
	valErrVars      map[ssa.Value][]*types.Var

	tuples           []gusseterr.TupleMeta
	callTupleID      map[*ssa.Call]uint32
	blankedCompSite  map[uint32]siteMeta // comp slot -> blanked call siteMeta
	boxedConcreteErr map[ssa.Value]types.Type
	ioReadErrVals    map[ssa.Value]bool
	companionVals    map[ssa.Value]bool
}

func (b *packageBuilder) lowerFunc(funcIdx uint32, fn *ssa.Function) gusseterr.FuncIR {
	var flags uint8
	if sigReturnsErrorLast(fn.Signature) {
		flags |= gusseterr.FuncReturnsError
	}
	if sigReturnsConcreteErrPtr(fn.Signature) {
		flags |= gusseterr.FuncReturnsConcreteErrPtr
	}

	if len(fn.Blocks) == 0 {
		return gusseterr.FuncIR{Flags: flags}
	}

	astInfo := scanFuncAST(b.pass, fn.Syntax())
	_, usedVars := scanBlankDiscardedAndUsedVars(b.pass, fn.Syntax())

	fl := &funcLowerer{
		pkg:              b,
		funcIdx:          funcIdx,
		fn:               fn,
		astInfo:          astInfo,
		usedBlankedVars:  usedVars,
		valSlots:         make(map[ssa.Value]uint32),
		localCells:       make(map[*ssa.Alloc]bool),
		loadedCell:       make(map[ssa.Value]*ssa.Alloc),
		mem2regErrSlots:  make(map[*types.Var]uint32),
		callErrVar:       make(map[*ssa.Call]*types.Var),
		valErrVars:       make(map[ssa.Value][]*types.Var),
		callTupleID:      make(map[*ssa.Call]uint32),
		blankedCompSite:  make(map[uint32]siteMeta),
		boxedConcreteErr: make(map[ssa.Value]types.Type),
		ioReadErrVals:    make(map[ssa.Value]bool),
		companionVals:    make(map[ssa.Value]bool),
	}

	return fl.build(flags)
}

func scanBlankDiscardedAndUsedVars(pass *analysis.Pass, syntax ast.Node) (map[*types.Var]bool, map[*types.Var]bool) {
	discarded := make(map[*types.Var]bool)
	nonBlankUses := make(map[*types.Var]int)
	if syntax == nil {
		return discarded, nil
	}
	blankRHSIdents := make(map[*ast.Ident]bool)
	lhsAssignIdents := make(map[*ast.Ident]bool)
	walkNoNestedClosures(syntax, func(n ast.Node) {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return
		}
		if as.Tok == token.ASSIGN || as.Tok == token.DEFINE {
			for _, lhs := range as.Lhs {
				if id, ok := unparenExpr(lhs).(*ast.Ident); ok {
					lhsAssignIdents[id] = true
				}
			}
		}
		if as.Tok == token.ASSIGN {
			for i, lhs := range as.Lhs {
				if isBlankIdent(lhs) && i < len(as.Rhs) {
					if id, ok := unparenExpr(as.Rhs[i]).(*ast.Ident); ok {
						blankRHSIdents[id] = true
						if v, ok := pass.TypesInfo.Uses[id].(*types.Var); ok {
							discarded[v] = true
						}
					}
				}
			}
		}
	})
	walkNoNestedClosures(syntax, func(n ast.Node) {
		if id, ok := n.(*ast.Ident); ok && !blankRHSIdents[id] && !lhsAssignIdents[id] {
			if v, ok := pass.TypesInfo.Uses[id].(*types.Var); ok {
				nonBlankUses[v]++
			}
		}
	})
	used := make(map[*types.Var]bool, len(nonBlankUses))
	for v, count := range nonBlankUses {
		if count > 0 {
			used[v] = true
		}
	}
	return discarded, used
}

func (fl *funcLowerer) allocSlot(sf uint8) uint32 {
	id := uint32(len(fl.slotFlags))
	fl.slotFlags = append(fl.slotFlags, sf)
	return id
}

func slotFlagsForType(t types.Type) uint8 {
	if t == nil {
		return 0
	}
	if isErrorInterface(t) || implementsError(t) {
		return gusseterr.SlotIsError
	}
	return 0
}

func (fl *funcLowerer) slot(v ssa.Value) uint32 {
	if v == nil {
		return gusseterr.NoneU32
	}
	if id, ok := fl.valSlots[v]; ok {
		return id
	}
	var sf uint8
	if alloc, isAlloc := v.(*ssa.Alloc); isAlloc && fl.localCells[alloc] {
		if ptr, ok := alloc.Type().Underlying().(*types.Pointer); ok {
			sf = slotFlagsForType(ptr.Elem())
		}
	} else {
		sf = slotFlagsForType(v.Type())
	}
	if _, isParam := v.(*ssa.Parameter); isParam {
		sf |= gusseterr.SlotInitMayNilParam
	}
	id := fl.allocSlot(sf)
	fl.valSlots[v] = id

	// Materialize nil/non-nil constant state in entry block 0 so Phi operands and
	// MakeInterface(Const) see exact nilness.
	if c, isConst := v.(*ssa.Const); isConst {
		nh := gusseterr.AssignNilNonNil
		if c.IsNil() {
			nh = gusseterr.AssignNilMustNil
		}
		fl.constInits = append(fl.constInits, gusseterr.OpIR{
			Kind:    gusseterr.OpAssignSlot,
			SiteID:  0,
			SlotA:   id,
			SlotB:   gusseterr.NoneU32,
			NilHint: nh,
		})
	}
	return id
}

func (fl *funcLowerer) rootSlot(v ssa.Value) uint32 {
	if v == nil {
		return gusseterr.NoneU32
	}
	if cell, ok := fl.loadedCell[v]; ok {
		return fl.slot(cell)
	}
	return fl.slot(v)
}

func (fl *funcLowerer) mem2regErrSlot(v *types.Var) uint32 {
	if s, ok := fl.mem2regErrSlots[v]; ok {
		return s
	}
	s := fl.allocSlot(gusseterr.SlotIsError)
	fl.mem2regErrSlots[v] = s
	fl.mem2regErrOrder = append(fl.mem2regErrOrder, v)
	return s
}

func (fl *funcLowerer) addValErrVar(val ssa.Value, v *types.Var) bool {
	if val == nil || v == nil {
		return false
	}
	existing := fl.valErrVars[val]
	for _, cur := range existing {
		if cur == v {
			return false
		}
	}
	fl.valErrVars[val] = append(existing, v)
	return true
}

func (fl *funcLowerer) emitObserveErr(ops *[]gusseterr.OpIR, val ssa.Value) {
	if val == nil {
		return
	}
	if isErrorInterface(val.Type()) {
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpObserveErr,
			SiteID: 0,
			SlotA:  fl.rootSlot(val),
		})
	}
	fl.emitObserveErrVarsOnly(ops, val)
}

func (fl *funcLowerer) emitObserveErrVarsOnly(ops *[]gusseterr.OpIR, val ssa.Value) {
	for _, v := range fl.valErrVars[val] {
		if vs, ok := fl.mem2regErrSlots[v]; ok {
			*ops = append(*ops, gusseterr.OpIR{
				Kind:   gusseterr.OpObserveErr,
				SiteID: 0,
				SlotA:  vs,
			})
		}
	}
}

func (fl *funcLowerer) propagateBlankedMeta(dst, src ssa.Value) {
	if srcSlot, ok := fl.valSlots[src]; ok {
		if sm, ok := fl.blankedCompSite[srcSlot]; ok {
			fl.blankedCompSite[fl.slot(dst)] = sm
		}
	}
}

func (fl *funcLowerer) seedErrVarOrigins() {
	if fl.fn.Syntax() == nil {
		return
	}
	for _, b := range fl.fn.Blocks {
		if b == fl.fn.Recover {
			continue
		}
		for _, ins := range b.Instrs {
			call, ok := ins.(*ssa.Call)
			if !ok {
				continue
			}
			sig := call.Call.Signature()
			if !sigReturnsErrorLast(sig) {
				continue
			}
			pos := call.Call.Pos()
			v := fl.assignedErrVarNotDiscarded(pos)
			if v == nil {
				continue
			}
			var errVal ssa.Value
			if sig.Results().Len() == 1 {
				errVal = call
			} else if call.Referrers() != nil {
				lastIdx := sig.Results().Len() - 1
				for _, ref := range *call.Referrers() {
					if ext, ok := ref.(*ssa.Extract); ok && ext.Index == lastIdx {
						errVal = ext
						break
					}
				}
			}
			if fl.valStoredInLocalCell(errVal) {
				continue
			}
			_ = fl.mem2regErrSlot(v)
			fl.callErrVar[call] = v
			if errVal != nil {
				fl.addValErrVar(errVal, v)
			}
		}
	}

	// Propagate mem2reg error variable provenance through SSA Phi and wrapper nodes.
	for {
		changed := false
		for _, b := range fl.fn.Blocks {
			if b == fl.fn.Recover {
				continue
			}
			for _, ins := range b.Instrs {
				switch x := ins.(type) {
				case *ssa.Phi:
					for _, edge := range x.Edges {
						for _, v := range fl.valErrVars[edge] {
							if fl.addValErrVar(x, v) {
								changed = true
							}
						}
					}
				case *ssa.ChangeInterface:
					for _, v := range fl.valErrVars[x.X] {
						if fl.addValErrVar(x, v) {
							changed = true
						}
					}
				case *ssa.MakeInterface:
					for _, v := range fl.valErrVars[x.X] {
						if fl.addValErrVar(x, v) {
							changed = true
						}
					}
				case *ssa.ChangeType:
					for _, v := range fl.valErrVars[x.X] {
						if fl.addValErrVar(x, v) {
							changed = true
						}
					}
				case *ssa.Convert:
					for _, v := range fl.valErrVars[x.X] {
						if fl.addValErrVar(x, v) {
							changed = true
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
}

func (fl *funcLowerer) seedBoxedConcreteErr() {
	for _, b := range fl.fn.Blocks {
		if b == fl.fn.Recover {
			continue
		}
		for _, ins := range b.Instrs {
			if mi, ok := ins.(*ssa.MakeInterface); ok {
				if isConcreteErrPtr(mi.X.Type()) && !(types.IsInterface(mi.Type()) && implementsError(mi.Type())) {
					fl.boxedConcreteErr[mi] = mi.X.Type()
				}
			}
		}
	}
	if len(fl.boxedConcreteErr) == 0 {
		return
	}
	for {
		changed := false
		for _, b := range fl.fn.Blocks {
			if b == fl.fn.Recover {
				continue
			}
			for _, ins := range b.Instrs {
				switch x := ins.(type) {
				case *ssa.Phi:
					if fl.boxedConcreteErr[x] == nil {
						for _, edge := range x.Edges {
							if ct := fl.boxedConcreteErr[edge]; ct != nil {
								fl.boxedConcreteErr[x] = ct
								changed = true
								break
							}
						}
					}
				case *ssa.ChangeInterface:
					if fl.boxedConcreteErr[x] == nil {
						if ct := fl.boxedConcreteErr[x.X]; ct != nil {
							fl.boxedConcreteErr[x] = ct
							changed = true
						}
					}
				case *ssa.ChangeType:
					if fl.boxedConcreteErr[x] == nil {
						if ct := fl.boxedConcreteErr[x.X]; ct != nil {
							fl.boxedConcreteErr[x] = ct
							changed = true
						}
					}
				case *ssa.Store:
					if alloc, ok := x.Addr.(*ssa.Alloc); ok && fl.localCells[alloc] && fl.boxedConcreteErr[alloc] == nil {
						if ct := fl.boxedConcreteErr[x.Val]; ct != nil {
							fl.boxedConcreteErr[alloc] = ct
							changed = true
						}
					}
				case *ssa.UnOp:
					if x.Op == token.MUL && fl.boxedConcreteErr[x] == nil {
						if alloc, ok := x.X.(*ssa.Alloc); ok && fl.localCells[alloc] {
							if ct := fl.boxedConcreteErr[alloc]; ct != nil {
								fl.boxedConcreteErr[x] = ct
								changed = true
							}
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
}

func (fl *funcLowerer) seedIOReadErrVals() {
	for _, b := range fl.fn.Blocks {
		if b == fl.fn.Recover {
			continue
		}
		for _, ins := range b.Instrs {
			call, ok := ins.(*ssa.Call)
			if !ok || !isIOReaderRead(&call.Call) || call.Referrers() == nil {
				continue
			}
			for _, ref := range *call.Referrers() {
				if ext, ok := ref.(*ssa.Extract); ok && ext.Index == 1 {
					fl.ioReadErrVals[ext] = true
				}
			}
		}
	}
	if len(fl.ioReadErrVals) == 0 {
		return
	}
	for {
		changed := false
		for _, b := range fl.fn.Blocks {
			if b == fl.fn.Recover {
				continue
			}
			for _, ins := range b.Instrs {
				switch x := ins.(type) {
				case *ssa.Phi:
					if !fl.ioReadErrVals[x] {
						for _, edge := range x.Edges {
							if fl.ioReadErrVals[edge] {
								fl.ioReadErrVals[x] = true
								changed = true
								break
							}
						}
					}
				case *ssa.Store:
					if alloc, ok := x.Addr.(*ssa.Alloc); ok && fl.localCells[alloc] && !fl.ioReadErrVals[alloc] {
						if fl.ioReadErrVals[x.Val] {
							fl.ioReadErrVals[alloc] = true
							changed = true
						}
					}
				case *ssa.UnOp:
					if x.Op == token.MUL && !fl.ioReadErrVals[x] {
						if alloc, ok := x.X.(*ssa.Alloc); ok && fl.localCells[alloc] && fl.ioReadErrVals[alloc] {
							fl.ioReadErrVals[x] = true
							changed = true
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
}

func (fl *funcLowerer) seedCompanionVals() {
	for _, b := range fl.fn.Blocks {
		if b == fl.fn.Recover {
			continue
		}
		for _, ins := range b.Instrs {
			call, ok := ins.(*ssa.Call)
			if !ok || call.Referrers() == nil {
				continue
			}
			sig := call.Call.Signature()
			if !sigReturnsErrorLast(sig) || sig.Results().Len() < 2 {
				continue
			}
			lastIdx := sig.Results().Len() - 1
			for _, ref := range *call.Referrers() {
				if ext, ok := ref.(*ssa.Extract); ok && ext.Index < lastIdx {
					fl.companionVals[ext] = true
				}
			}
		}
	}
	if len(fl.companionVals) == 0 {
		return
	}
	for {
		changed := false
		for _, b := range fl.fn.Blocks {
			if b == fl.fn.Recover {
				continue
			}
			for _, ins := range b.Instrs {
				switch x := ins.(type) {
				case *ssa.Phi:
					if !fl.companionVals[x] {
						for _, edge := range x.Edges {
							if fl.companionVals[edge] {
								fl.companionVals[x] = true
								changed = true
								break
							}
						}
					}
				case *ssa.Store:
					if alloc, ok := x.Addr.(*ssa.Alloc); ok && fl.localCells[alloc] && !fl.companionVals[alloc] {
						if fl.companionVals[x.Val] {
							fl.companionVals[alloc] = true
							changed = true
						}
					}
				case *ssa.UnOp:
					if x.Op == token.MUL && !fl.companionVals[x] {
						if alloc, ok := x.X.(*ssa.Alloc); ok && fl.localCells[alloc] && fl.companionVals[alloc] {
							fl.companionVals[x] = true
							changed = true
						}
					}
				case *ssa.ChangeType:
					if !fl.companionVals[x] && fl.companionVals[x.X] {
						fl.companionVals[x] = true
						changed = true
					}
				case *ssa.Convert:
					if !fl.companionVals[x] && fl.companionVals[x.X] {
						fl.companionVals[x] = true
						changed = true
					}
				case *ssa.ChangeInterface:
					if !fl.companionVals[x] && fl.companionVals[x.X] {
						fl.companionVals[x] = true
						changed = true
					}
				case *ssa.MakeInterface:
					if !fl.companionVals[x] && fl.companionVals[x.X] {
						fl.companionVals[x] = true
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}
}

func isIOEOFValue(v ssa.Value) bool {
	if u, ok := v.(*ssa.UnOp); ok && u.Op == token.MUL {
		v = u.X
	}
	g, ok := v.(*ssa.Global)
	return ok && g.Pkg != nil && g.Pkg.Pkg != nil && g.Pkg.Pkg.Path() == "io" && g.Name() == "EOF"
}

func (fl *funcLowerer) valStoredInLocalCell(v ssa.Value) bool {
	if v == nil || v.Referrers() == nil {
		return false
	}
	for _, ref := range *v.Referrers() {
		if st, ok := ref.(*ssa.Store); ok && st.Val == v {
			if alloc, ok := st.Addr.(*ssa.Alloc); ok && fl.localCells[alloc] {
				return true
			}
		}
	}
	return false
}

func (fl *funcLowerer) build(flags uint8) gusseterr.FuncIR {
	// Identify local variable storage cells (*ssa.Alloc written by Store or read by UnOp MUL,
	// as distinct from value allocations like `&MyErr{}` whose Alloc value itself is used as *MyErr).
	for _, b := range fl.fn.Blocks {
		for _, ins := range b.Instrs {
			alloc, ok := ins.(*ssa.Alloc)
			if !ok {
				continue
			}
			if isLocalCellAlloc(alloc) {
				fl.localCells[alloc] = true
			}
		}
	}

	for _, p := range fl.fn.Params {
		_ = fl.slot(p)
	}
	for _, fv := range fl.fn.FreeVars {
		_ = fl.slot(fv)
	}

	// Pre-register local variable cells and initialize zero-initialized *ConcreteErr cells to MustNil.
	var cellZeroInits []gusseterr.OpIR
	for _, b := range fl.fn.Blocks {
		for _, ins := range b.Instrs {
			alloc, ok := ins.(*ssa.Alloc)
			if !ok {
				continue
			}
			if fl.localCells[alloc] {
				if ptr, ok := alloc.Type().Underlying().(*types.Pointer); ok {
					if isConcreteErrPtr(ptr.Elem()) || isErrorInterface(ptr.Elem()) {
						s := fl.slot(alloc)
						cellZeroInits = append(cellZeroInits, gusseterr.OpIR{
							Kind:    gusseterr.OpAssignSlot,
							SiteID:  0,
							SlotA:   s,
							SlotB:   gusseterr.NoneU32,
							NilHint: gusseterr.AssignNilMustNil,
						})
					}
				}
			} else if isConcreteErrPtr(alloc.Type()) {
				// `&MyErr{...}` or `new(MyErr)` is a fresh non-nil concrete error pointer.
				s := fl.slot(alloc)
				cellZeroInits = append(cellZeroInits, gusseterr.OpIR{
					Kind:    gusseterr.OpAssignSlot,
					SiteID:  0,
					SlotA:   s,
					SlotB:   gusseterr.NoneU32,
					NilHint: gusseterr.AssignNilNonNil,
				})
			}
		}
	}

	fl.seedErrVarOrigins()
	fl.seedBoxedConcreteErr()
	fl.seedIOReadErrVals()
	fl.seedCompanionVals()

	nBlocks := len(fl.fn.Blocks)
	blocks := make([]gusseterr.BlockIR, nBlocks)

	// Map AST shadowed error positions to their best matching basic block.
	shadowBlocks := make(map[int][]shadowErrAST)
	for _, sh := range fl.astInfo.shadows {
		bb := fl.findBlockForPos(sh.pos)
		shadowBlocks[bb] = append(shadowBlocks[bb], sh)
	}

	consumedUnhandled := make(map[token.Pos]bool)
	consumedCloses := make(map[token.Pos]bool)

	for bbIdx, b := range fl.fn.Blocks {
		if b == fl.fn.Recover {
			blocks[bbIdx] = gusseterr.BlockIR{Term: gusseterr.Terminator{Kind: gusseterr.TermStop}}
			continue
		}

		var phis []gusseterr.PhiIR
		var ops []gusseterr.OpIR

		for _, sh := range shadowBlocks[bbIdx] {
			dummySlot := fl.allocSlot(gusseterr.SlotIsError)
			sid := fl.pkg.addSite(sh.pos, sh.name, "")
			ops = append(ops, gusseterr.OpIR{
				Kind:   gusseterr.OpShadowErr,
				SiteID: sid,
				SlotA:  dummySlot,
				SlotB:  dummySlot,
			})
		}

		terminatedEarly := false
		for _, ins := range b.Instrs {
			switch x := ins.(type) {
			case *ssa.Phi:
				dst := fl.slot(x)
				inc := make([]gusseterr.PhiEdge, 0, len(x.Edges))
				for ei, edgeVal := range x.Edges {
					if ei < len(b.Preds) {
						predIdx := uint32(b.Preds[ei].Index)
						inc = append(inc, gusseterr.PhiEdge{
							PredBB:  predIdx,
							SrcSlot: fl.slot(edgeVal),
						})
					}
					fl.propagateBlankedMeta(x, edgeVal)
				}
				phis = append(phis, gusseterr.PhiIR{DstSlot: dst, Incoming: inc})

			case *ssa.Call:
				fl.lowerCall(x, &ops, consumedUnhandled, consumedCloses)
				if isNoReturnCall(&x.Call) {
					terminatedEarly = true
				}

			case *ssa.Defer:
				fl.lowerDefer(x, &ops, consumedUnhandled, consumedCloses)

			case *ssa.Go:
				fl.lowerGo(x, &ops, consumedUnhandled)

			case *ssa.Store:
				if alloc, ok := x.Addr.(*ssa.Alloc); ok && fl.localCells[alloc] {
					sid := fl.siteForVal(x.Pos(), describeSSAVal(alloc))
					ops = append(ops, gusseterr.OpIR{
						Kind:    gusseterr.OpAssignSlot,
						SiteID:  sid,
						SlotA:   fl.slot(alloc),
						SlotB:   fl.slot(x.Val),
						NilHint: gusseterr.AssignNilCopy,
					})
					fl.propagateBlankedMeta(alloc, x.Val)
				} else {
					if isErrorInterface(x.Val.Type()) || len(fl.valErrVars[x.Val]) > 0 {
						fl.emitObserveErr(&ops, x.Val)
					} else {
						fl.emitUseCompanion(&ops, x.Pos(), x.Val)
					}
					fl.emitUseCompanion(&ops, x.Pos(), x.Addr)
				}

			case *ssa.MapUpdate:
				fl.emitUseCompanion(&ops, x.Pos(), x.Map)
				fl.emitUseCompanion(&ops, x.Pos(), x.Key)
				if isErrorInterface(x.Value.Type()) || len(fl.valErrVars[x.Value]) > 0 {
					fl.emitObserveErr(&ops, x.Value)
				} else {
					fl.emitUseCompanion(&ops, x.Pos(), x.Value)
				}

			case *ssa.UnOp:
				if x.Op == token.MUL {
					if alloc, ok := x.X.(*ssa.Alloc); ok && fl.localCells[alloc] {
						fl.loadedCell[x] = alloc
						ops = append(ops, gusseterr.OpIR{
							Kind:    gusseterr.OpAssignSlot,
							SiteID:  0,
							SlotA:   fl.slot(x),
							SlotB:   fl.slot(alloc),
							NilHint: gusseterr.AssignNilCopy,
						})
						fl.propagateBlankedMeta(x, alloc)
					} else {
						fl.emitUseCompanion(&ops, x.Pos(), x.X)
					}
				} else if x.Op != token.NOT {
					fl.emitUseCompanion(&ops, x.Pos(), x.X)
				}

			case *ssa.FieldAddr:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
			case *ssa.Field:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
			case *ssa.IndexAddr:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
				fl.emitUseCompanion(&ops, x.Pos(), x.Index)
			case *ssa.Index:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
				fl.emitUseCompanion(&ops, x.Pos(), x.Index)
			case *ssa.Lookup:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
				fl.emitUseCompanion(&ops, x.Pos(), x.Index)
			case *ssa.Range:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
			case *ssa.MakeSlice:
				fl.emitUseCompanion(&ops, x.Pos(), x.Len)
				if x.Cap != nil {
					fl.emitUseCompanion(&ops, x.Pos(), x.Cap)
				}
			case *ssa.MakeChan:
				if x.Size != nil {
					fl.emitUseCompanion(&ops, x.Pos(), x.Size)
				}
			case *ssa.MakeMap:
				if x.Reserve != nil {
					fl.emitUseCompanion(&ops, x.Pos(), x.Reserve)
				}
			case *ssa.SliceToArrayPointer:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
			case *ssa.Slice:
				fl.emitUseCompanion(&ops, x.Pos(), x.X)
				if x.Low != nil {
					fl.emitUseCompanion(&ops, x.Pos(), x.Low)
				}
				if x.High != nil {
					fl.emitUseCompanion(&ops, x.Pos(), x.High)
				}
				if x.Max != nil {
					fl.emitUseCompanion(&ops, x.Pos(), x.Max)
				}
			case *ssa.Select:
				for _, st := range x.States {
					fl.emitUseCompanion(&ops, st.Pos, st.Chan)
					if st.Send != nil {
						if isErrorInterface(st.Send.Type()) || len(fl.valErrVars[st.Send]) > 0 {
							fl.emitObserveErr(&ops, st.Send)
						} else {
							fl.emitUseCompanion(&ops, st.Pos, st.Send)
						}
					}
				}
			case *ssa.Send:
				fl.emitUseCompanion(&ops, x.Pos(), x.Chan)
				if isErrorInterface(x.X.Type()) || len(fl.valErrVars[x.X]) > 0 {
					fl.emitObserveErr(&ops, x.X)
				} else {
					fl.emitUseCompanion(&ops, x.Pos(), x.X)
				}
			case *ssa.TypeAssert:
				if types.IsInterface(x.AssertedType) && implementsError(x.AssertedType) {
					if ct := fl.boxedConcreteErr[x.X]; ct != nil {
						pos := x.Pos()
						if !pos.IsValid() {
							pos = bestUserPosInBlock(b, fl.fn)
						}
						sid := fl.pkg.addSite(pos, types.TypeString(ct, types.RelativeTo(fl.pkg.pass.Pkg)), "")
						ops = append(ops, gusseterr.OpIR{
							Kind:   gusseterr.OpBoxConcreteErr,
							SiteID: sid,
							SlotA:  fl.slot(x.X),
						})
					}
				}
				if isErrorInterface(x.X.Type()) || len(fl.valErrVars[x.X]) > 0 {
					fl.emitObserveErr(&ops, x.X)
				} else {
					fl.emitUseCompanion(&ops, x.Pos(), x.X)
				}
			case *ssa.ChangeType:
				ops = append(ops, gusseterr.OpIR{
					Kind:    gusseterr.OpAssignSlot,
					SiteID:  0,
					SlotA:   fl.slot(x),
					SlotB:   fl.slot(x.X),
					NilHint: gusseterr.AssignNilCopy,
				})
				fl.propagateBlankedMeta(x, x.X)
			case *ssa.Convert:
				if isBasicValueUse(x.X.Type()) {
					fl.emitUseCompanion(&ops, x.Pos(), x.X)
				}
				ops = append(ops, gusseterr.OpIR{
					Kind:    gusseterr.OpAssignSlot,
					SiteID:  0,
					SlotA:   fl.slot(x),
					SlotB:   fl.slot(x.X),
					NilHint: gusseterr.AssignNilCopy,
				})
				fl.propagateBlankedMeta(x, x.X)
			case *ssa.ChangeInterface:
				if isErrorInterface(x.X.Type()) || len(fl.valErrVars[x.X]) > 0 {
					fl.emitObserveErr(&ops, x.X)
				}
				ops = append(ops, gusseterr.OpIR{
					Kind:    gusseterr.OpAssignSlot,
					SiteID:  0,
					SlotA:   fl.slot(x),
					SlotB:   fl.slot(x.X),
					NilHint: gusseterr.AssignNilCopy,
				})
				fl.propagateBlankedMeta(x, x.X)
			case *ssa.MakeInterface:
				if types.IsInterface(x.Type()) && implementsError(x.Type()) && isConcreteErrPtr(x.X.Type()) {
					pos := x.Pos()
					if !pos.IsValid() {
						pos = bestUserPosInBlock(b, fl.fn)
					}
					sid := fl.pkg.addSite(pos, types.TypeString(x.X.Type(), types.RelativeTo(fl.pkg.pass.Pkg)), "")
					ops = append(ops, gusseterr.OpIR{
						Kind:   gusseterr.OpBoxConcreteErr,
						SiteID: sid,
						SlotA:  fl.slot(x.X),
					})
				} else if isErrorInterface(x.X.Type()) || len(fl.valErrVars[x.X]) > 0 {
					fl.emitObserveErr(&ops, x.X)
				} else {
					fl.emitUseCompanion(&ops, x.Pos(), x.X)
				}
				ops = append(ops, gusseterr.OpIR{
					Kind:    gusseterr.OpAssignSlot,
					SiteID:  0,
					SlotA:   fl.slot(x),
					SlotB:   fl.slot(x.X),
					NilHint: gusseterr.AssignNilCopy,
				})
				fl.propagateBlankedMeta(x, x.X)
			case *ssa.BinOp:
				if !isGuardComparison(x) {
					fl.emitUseCompanion(&ops, x.Pos(), x.X)
					fl.emitUseCompanion(&ops, x.Pos(), x.Y)
				} else if isNonNilErrorSentinelCompare(x) {
					// `err == io.EOF` or `err != sql.ErrNoRows` inspects err!
					if isErrorInterface(x.X.Type()) || len(fl.valErrVars[x.X]) > 0 {
						fl.emitObserveErr(&ops, x.X)
					}
					if isErrorInterface(x.Y.Type()) || len(fl.valErrVars[x.Y]) > 0 {
						fl.emitObserveErr(&ops, x.Y)
					}
				}
			}
			if terminatedEarly {
				break
			}
		}

		var term gusseterr.Terminator
		if terminatedEarly || len(b.Instrs) == 0 {
			term = gusseterr.Terminator{Kind: gusseterr.TermStop}
		} else {
			term = fl.lowerTerminator(b, &ops)
		}

		blocks[bbIdx] = gusseterr.BlockIR{
			Phis: phis,
			Ops:  ops,
			Term: term,
		}
	}

	// Prepend entry-block constant and zero-cell initializations AFTER all instructions
	// have registered any referenced *ssa.Const slots.
	if len(blocks) > 0 && (len(fl.constInits) > 0 || len(cellZeroInits) > 0) {
		prefix := make([]gusseterr.OpIR, 0, len(cellZeroInits)+len(fl.constInits)+len(blocks[0].Ops))
		prefix = append(prefix, cellZeroInits...)
		prefix = append(prefix, fl.constInits...)
		prefix = append(prefix, blocks[0].Ops...)
		blocks[0].Ops = prefix
	}

	return gusseterr.FuncIR{
		Flags:     flags,
		SlotFlags: fl.slotFlags,
		Tuples:    fl.tuples,
		Blocks:    blocks,
	}
}

func (fl *funcLowerer) lowerCall(
	ins *ssa.Call,
	ops *[]gusseterr.OpIR,
	consumedUnhandled map[token.Pos]bool,
	consumedCloses map[token.Pos]bool,
) {
	cc := &ins.Call
	pos := cc.Pos()

	// Observe error arguments and mark companion uses on non-error arguments / receivers.
	fl.lowerCallOperands(cc, pos, ops)

	// Mark receiver Writable if this call mutates/flushes a closable stream/file.
	if recv := methodReceiverVal(cc); recv != nil && isWriteMutatingMethod(cc) {
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpMarkWritable,
			SiteID: 0,
			SlotA:  fl.rootSlot(recv),
		})
	}

	// 1. Check Close() error statement discard (ERR005).
	if cl, key, ok := fl.matchCloseSite(pos, consumedCloses); ok {
		consumedCloses[key] = true
		if recv := methodReceiverVal(cc); recv != nil {
			sid := fl.pkg.addSite(cl.pos, cl.recvDesc, "")
			*ops = append(*ops, gusseterr.OpIR{
				Kind:    gusseterr.OpUnhandledClose,
				SiteID:  sid,
				SlotA:   fl.rootSlot(recv),
				IsDefer: false,
			})
		}
		return
	}

	// 2. Check bare unhandled error-returning call statement (ERR001).
	if uh, key, ok := fl.matchUnhandledSite(pos, consumedUnhandled); ok {
		consumedUnhandled[key] = true
		sid := fl.pkg.addSite(uh.pos, uh.desc, "")
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpUnhandledErr,
			SiteID: sid,
		})
		return
	}

	sig := cc.Signature()
	if sig == nil || sig.Results() == nil || sig.Results().Len() == 0 {
		return
	}
	res := sig.Results()

	// 3. Single concrete error pointer return (`func() *MyErr`).
	if sigReturnsConcreteErrPtr(sig) {
		dst := fl.slot(ins)
		if callee := cc.StaticCallee(); callee != nil {
			if cIdx, inPkg := fl.pkg.funcIdxMap[callee]; inPkg {
				*ops = append(*ops, gusseterr.OpIR{
					Kind:   gusseterr.OpCallConcretePtr,
					SiteID: 0,
					SlotB:  cIdx,
					SlotA:  dst,
				})
				return
			}
			// Cross-package static callee: check exported FuncFact.
			if obj := callee.Object(); obj != nil {
				var fact FuncFact
				mayNil := true
				if fl.pkg.pass.ImportObjectFact(obj, &fact) {
					mayNil = fact.MayReturnNilConcrete
				} else if strings.HasPrefix(callee.Name(), "New") {
					mayNil = false
				} else if obj.Pkg() != nil && isStdlibInfallibleConstructor(obj.Pkg().Path(), obj.Name()) {
					mayNil = false
				}
				nh := gusseterr.AssignNilNonNil
				if mayNil {
					nh = gusseterr.AssignNilMayNil
				}
				*ops = append(*ops, gusseterr.OpIR{
					Kind:    gusseterr.OpAssignSlot,
					SiteID:  0,
					SlotA:   dst,
					SlotB:   gusseterr.NoneU32,
					NilHint: nh,
				})
				return
			}
		}
		// Dynamic closure/interface call returning *ConcreteErr: conservative MayNil.
		*ops = append(*ops, gusseterr.OpIR{
			Kind:    gusseterr.OpAssignSlot,
			SiteID:  0,
			SlotA:   dst,
			SlotB:   gusseterr.NoneU32,
			NilHint: gusseterr.AssignNilMayNil,
		})
		return
	}

	if !sigReturnsErrorLast(sig) {
		// Still check if single non-error return is a writable resource wrapper (e.g. bufio.NewWriter).
		if isWritableResourceProducer(cc) {
			s := fl.slot(ins)
			*ops = append(*ops, gusseterr.OpIR{
				Kind:   gusseterr.OpMarkWritable,
				SiteID: 0,
				SlotA:  s,
			})
		}
		return
	}

	// 4. Single `error` return (`func() error`).
	if res.Len() == 1 {
		if isExplicitBlankAssignAt(fl.pkg.pass, fl.fn.Syntax(), pos) {
			return
		}
		if v, ok := fl.callErrVar[ins]; ok {
			varSid := fl.pkg.addSite(pos, v.Name(), "")
			*ops = append(*ops, gusseterr.OpIR{
				Kind:   gusseterr.OpBindErr,
				SiteID: varSid,
				SlotA:  fl.mem2regErrSlot(v),
			})
		}
		if countLiveReferrers(ins) == 0 {
			return
		}
		s := fl.slot(ins)
		sid := fl.pkg.addSite(pos, describeCallCommon(cc), "")
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpBindErr,
			SiteID: sid,
			SlotA:  s,
		})
		return
	}

	// 5. Product tuple `(T_0, ..., T_{k-1}, error)` (`k >= 1`).
	lastIdx := res.Len() - 1
	var compSlots []uint32
	var errExtract *ssa.Extract
	for _, ref := range *ins.Referrers() {
		ext, ok := ref.(*ssa.Extract)
		if !ok {
			continue
		}
		if ext.Index == lastIdx {
			errExtract = ext
		} else if countLiveReferrers(ext) > 0 {
			compSlots = append(compSlots, fl.slot(ext))
		}
	}

	blankInfo, isBlankedAST := fl.matchBlankedTuple(pos)
	var tflags uint8
	errSlot := gusseterr.NoneU32
	callSitePos := pos
	callDesc := describeCallCommon(cc)
	if isBlankedAST {
		tflags |= gusseterr.TupleErrBlanked
		callSitePos = blankInfo.pos
		callDesc = blankInfo.callDesc
	} else {
		if v, ok := fl.callErrVar[ins]; ok {
			varSid := fl.pkg.addSite(pos, v.Name(), "")
			*ops = append(*ops, gusseterr.OpIR{
				Kind:   gusseterr.OpBindErr,
				SiteID: varSid,
				SlotA:  fl.mem2regErrSlot(v),
			})
		}
		if errExtract != nil {
			errSlot = fl.slot(errExtract)
		}
	}
	if isIOReaderRead(cc) {
		tflags |= gusseterr.TupleIORead
	}

	callSiteID := fl.pkg.addSite(callSitePos, callDesc, "")
	tid := uint32(len(fl.tuples))
	fl.tuples = append(fl.tuples, gusseterr.TupleMeta{
		ErrSlot:    errSlot,
		CallSiteID: callSiteID,
		Flags:      tflags,
	})
	fl.callTupleID[ins] = tid

	*ops = append(*ops, gusseterr.OpIR{
		Kind:       gusseterr.OpBindTuple,
		SiteID:     callSiteID,
		TupleID:    tid,
		SlotA:      errSlot,
		Companions: compSlots,
	})

	if isBlankedAST {
		sm := siteMeta{pos: blankInfo.pos, detail: blankInfo.callDesc}
		for _, cs := range compSlots {
			fl.blankedCompSite[cs] = sm
		}
		// If companion was written directly to a field/global/index (not a local ident) or a used local,
		// ensure blanked tuple use is recorded with exact blanked call-site attribution.
		if len(compSlots) == 0 && fl.blankedAssignWritesNonLocalOrUsed(pos) {
			tmpSlot := fl.allocSlot(0)
			fl.blankedCompSite[tmpSlot] = sm
			*ops = append(*ops, gusseterr.OpIR{
				Kind:       gusseterr.OpBindTuple,
				SiteID:     callSiteID,
				TupleID:    tid,
				SlotA:      gusseterr.NoneU32,
				Companions: []uint32{tmpSlot},
			})
			fl.emitUseCompanionSlot(ops, blankInfo.pos, tmpSlot, blankInfo.callDesc)
		}
	}

	if isWritableResourceProducer(cc) && len(compSlots) > 0 {
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpMarkWritable,
			SiteID: 0,
			SlotA:  compSlots[0],
		})
	}
}

func (fl *funcLowerer) lowerDefer(
	ins *ssa.Defer,
	ops *[]gusseterr.OpIR,
	consumedUnhandled map[token.Pos]bool,
	consumedCloses map[token.Pos]bool,
) {
	cc := &ins.Call
	pos := cc.Pos()
	fl.lowerCallOperands(cc, pos, ops)

	if cl, key, ok := fl.matchCloseSite(pos, consumedCloses); ok {
		consumedCloses[key] = true
		if recv := methodReceiverVal(cc); recv != nil {
			sid := fl.pkg.addSite(cl.pos, cl.recvDesc, "")
			*ops = append(*ops, gusseterr.OpIR{
				Kind:    gusseterr.OpUnhandledClose,
				SiteID:  sid,
				SlotA:   fl.rootSlot(recv),
				IsDefer: true,
			})
		}
		return
	}
	if uh, key, ok := fl.matchUnhandledSite(pos, consumedUnhandled); ok {
		consumedUnhandled[key] = true
		sid := fl.pkg.addSite(uh.pos, uh.desc, "")
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpUnhandledErr,
			SiteID: sid,
		})
	}
}

func (fl *funcLowerer) lowerGo(
	ins *ssa.Go,
	ops *[]gusseterr.OpIR,
	consumedUnhandled map[token.Pos]bool,
) {
	cc := &ins.Call
	pos := cc.Pos()
	fl.lowerCallOperands(cc, pos, ops)
	if uh, key, ok := fl.matchUnhandledSite(pos, consumedUnhandled); ok {
		consumedUnhandled[key] = true
		sid := fl.pkg.addSite(uh.pos, uh.desc, "")
		*ops = append(*ops, gusseterr.OpIR{
			Kind:   gusseterr.OpUnhandledErr,
			SiteID: sid,
		})
	}
}

func (fl *funcLowerer) lowerCallOperands(cc *ssa.CallCommon, pos token.Pos, ops *[]gusseterr.OpIR) {
	if cc.IsInvoke() {
		if cc.Value != nil {
			if isErrorInterface(cc.Value.Type()) || len(fl.valErrVars[cc.Value]) > 0 {
				fl.emitObserveErr(ops, cc.Value)
			} else {
				fl.emitUseCompanion(ops, pos, cc.Value)
			}
		}
	}
	for _, arg := range cc.Args {
		if isErrorInterface(arg.Type()) || len(fl.valErrVars[arg]) > 0 {
			fl.emitObserveErr(ops, arg)
		} else {
			fl.emitUseCompanion(ops, pos, arg)
		}
	}
}

func (fl *funcLowerer) lowerTerminator(b *ssa.BasicBlock, ops *[]gusseterr.OpIR) gusseterr.Terminator {
	last := b.Instrs[len(b.Instrs)-1]
	switch t := last.(type) {
	case *ssa.Jump:
		if len(b.Succs) == 1 {
			return gusseterr.Terminator{Kind: gusseterr.TermJump, Target: uint32(b.Succs[0].Index)}
		}
		return gusseterr.Terminator{Kind: gusseterr.TermStop}

	case *ssa.If:
		if len(b.Succs) != 2 {
			return gusseterr.Terminator{Kind: gusseterr.TermStop}
		}
		trueBB := uint32(b.Succs[0].Index)
		falseBB := uint32(b.Succs[1].Index)
		ck, cSlot, subj, invert := fl.classifyCond(t.Cond)
		if subj != nil && (ck == gusseterr.CondIsNil || ck == gusseterr.CondNotNil) {
			fl.emitObserveErrVarsOnly(ops, subj)
		}
		if invert {
			trueBB, falseBB = falseBB, trueBB
		}
		return gusseterr.Terminator{
			Kind:     gusseterr.TermCond,
			Target:   trueBB,
			FalseBB:  falseBB,
			CondKind: ck,
			CondSlot: cSlot,
		}

	case *ssa.Return:
		if !t.Pos().IsValid() {
			return gusseterr.Terminator{Kind: gusseterr.TermStop}
		}
		returnsErr := sigReturnsErrorLast(fl.fn.Signature)
		returnsConcretePtr := sigReturnsConcreteErrPtr(fl.fn.Signature)

		var retFlags uint8
		retSlot := gusseterr.NoneU32

		if returnsErr && len(t.Results) > 0 {
			lastRes := t.Results[len(t.Results)-1]
			retSlot = fl.rootSlot(lastRes)
			isConstNil := false
			if c, ok := lastRes.(*ssa.Const); ok && c.IsNil() {
				isConstNil = true
				retFlags |= gusseterr.RetLiteralNilErr
				// If a named-result error variable was assigned by a call and never checked,
				// `return nil` overwrote it directly.
				for _, nv := range fl.mem2regErrOrder {
					if fl.isNamedResultVar(nv) {
						sid := fl.pkg.addSite(t.Pos(), nv.Name(), "")
						*ops = append(*ops, gusseterr.OpIR{
							Kind:   gusseterr.OpAssignSlot,
							SiteID: sid,
							SlotA:  fl.mem2regErrSlots[nv],
							SlotB:  gusseterr.NoneU32,
						})
					}
				}
			} else {
				fl.emitObserveErr(ops, lastRes)
			}
			// Companion values returned with `nil` error (or whose error was blanked) must be validated.
			for _, r := range t.Results[:len(t.Results)-1] {
				if isConstNil {
					fl.emitUseCompanion(ops, t.Pos(), r)
				} else if rSlot, ok := fl.valSlots[r]; ok {
					if _, isBlanked := fl.blankedCompSite[rSlot]; isBlanked {
						fl.emitUseCompanion(ops, t.Pos(), r)
					}
				}
			}
		} else if returnsConcretePtr && len(t.Results) == 1 {
			r0 := t.Results[0]
			if c, ok := r0.(*ssa.Const); ok && c.IsNil() {
				retFlags |= gusseterr.RetConcreteLiteralNil
			} else {
				retSlot = fl.rootSlot(r0)
			}
		} else {
			for _, r := range t.Results {
				if isErrorInterface(r.Type()) || len(fl.valErrVars[r]) > 0 {
					fl.emitObserveErr(ops, r)
				} else {
					fl.emitUseCompanion(ops, t.Pos(), r)
				}
			}
		}

		sid := fl.pkg.addSite(t.Pos(), "return", "")
		return gusseterr.Terminator{
			Kind:     gusseterr.TermReturn,
			SiteID:   sid,
			RetSlot:  retSlot,
			RetFlags: retFlags,
		}

	case *ssa.Panic:
		if isErrorInterface(t.X.Type()) || len(fl.valErrVars[t.X]) > 0 {
			fl.emitObserveErr(ops, t.X)
		}
		return gusseterr.Terminator{Kind: gusseterr.TermStop}

	default:
		return gusseterr.Terminator{Kind: gusseterr.TermStop}
	}
}

func (fl *funcLowerer) classifyCond(cond ssa.Value) (uint8, uint32, ssa.Value, bool) {
	invert := false
	for {
		u, ok := cond.(*ssa.UnOp)
		if !ok || u.Op != token.NOT {
			break
		}
		invert = !invert
		cond = u.X
	}
	bin, ok := cond.(*ssa.BinOp)
	if !ok {
		if call, ok := cond.(*ssa.Call); ok {
			if callee := call.Call.StaticCallee(); callee != nil && callee.Pkg != nil && callee.Pkg.Pkg != nil &&
				callee.Pkg.Pkg.Path() == "errors" && callee.Name() == "Is" && len(call.Call.Args) == 2 {
				arg0, arg1 := call.Call.Args[0], call.Call.Args[1]
				if fl.ioReadErrVals[arg0] && isIOEOFValue(arg1) {
					return gusseterr.CondIsNil, fl.rootSlot(arg0), arg0, invert
				}
			}
		}
		return gusseterr.CondOpaque, gusseterr.NoneU32, nil, false
	}

	// Nil comparison (`x == nil` / `x != nil`) and io.EOF sentinel check on io.Reader.Read error.
	if bin.Op == token.EQL || bin.Op == token.NEQ {
		var subj ssa.Value
		if c, ok := bin.Y.(*ssa.Const); ok && c.IsNil() {
			subj = bin.X
		} else if c, ok := bin.X.(*ssa.Const); ok && c.IsNil() {
			subj = bin.Y
		} else if isIOEOFValue(bin.Y) && fl.ioReadErrVals[bin.X] {
			subj = bin.X
		} else if isIOEOFValue(bin.X) && fl.ioReadErrVals[bin.Y] {
			subj = bin.Y
		}
		if subj != nil {
			kind := gusseterr.CondIsNil
			if bin.Op == token.NEQ {
				kind = gusseterr.CondNotNil
			}
			return kind, fl.rootSlot(subj), subj, invert
		}
	}

	// Positive read count check: `n > 0`, `n >= 1`, `n != 0`, `0 < n`.
	if bin.Op == token.GTR || bin.Op == token.NEQ {
		if isIntConstVal(bin.Y, 0) {
			return gusseterr.CondGtZero, fl.rootSlot(bin.X), bin.X, invert
		}
	}
	if bin.Op == token.GEQ && isIntConstVal(bin.Y, 1) {
		return gusseterr.CondGtZero, fl.rootSlot(bin.X), bin.X, invert
	}
	if bin.Op == token.LSS && isIntConstVal(bin.X, 0) {
		return gusseterr.CondGtZero, fl.rootSlot(bin.Y), bin.Y, invert
	}

	return gusseterr.CondOpaque, gusseterr.NoneU32, nil, false
}

func (fl *funcLowerer) emitUseCompanion(ops *[]gusseterr.OpIR, pos token.Pos, v ssa.Value) {
	if v == nil || !fl.companionVals[v] {
		return
	}
	switch v.(type) {
	case *ssa.Const, *ssa.Global, *ssa.Function, *ssa.Builtin:
		return
	}
	if alloc, ok := v.(*ssa.Alloc); ok && fl.localCells[alloc] {
		return
	}
	s := fl.slot(v)
	fl.emitUseCompanionSlot(ops, pos, s, describeSSAVal(v))
}

func (fl *funcLowerer) emitUseCompanionSlot(ops *[]gusseterr.OpIR, pos token.Pos, s uint32, desc string) {
	if !pos.IsValid() && fl.fn.Syntax() != nil {
		pos = fl.fn.Syntax().Pos()
	}
	sid := fl.pkg.addSite(pos, desc, "")
	if sm, ok := fl.blankedCompSite[s]; ok {
		fl.pkg.blankedBySite[sid] = sm
	}
	*ops = append(*ops, gusseterr.OpIR{
		Kind:   gusseterr.OpUseCompanion,
		SiteID: sid,
		SlotA:  s,
	})
}

func (fl *funcLowerer) siteForVal(pos token.Pos, desc string) uint32 {
	if !pos.IsValid() && fl.fn.Syntax() != nil {
		pos = fl.fn.Syntax().Pos()
	}
	return fl.pkg.addSite(pos, desc, "")
}

func (fl *funcLowerer) matchUnhandledSite(pos token.Pos, consumed map[token.Pos]bool) (unhandledCallAST, token.Pos, bool) {
	if uh, ok := fl.astInfo.unhandled[pos]; ok && !consumed[pos] {
		return uh, pos, true
	}
	posLine := fl.pkg.pass.Fset.Position(pos).Line
	if posLine == 0 {
		return unhandledCallAST{}, token.NoPos, false
	}
	for k, uh := range fl.astInfo.unhandled {
		if !consumed[k] && fl.pkg.pass.Fset.Position(k).Line == posLine {
			return uh, k, true
		}
	}
	return unhandledCallAST{}, token.NoPos, false
}

func (fl *funcLowerer) matchCloseSite(pos token.Pos, consumed map[token.Pos]bool) (closeCallAST, token.Pos, bool) {
	if cl, ok := fl.astInfo.closes[pos]; ok && !consumed[pos] {
		return cl, pos, true
	}
	posLine := fl.pkg.pass.Fset.Position(pos).Line
	if posLine == 0 {
		return closeCallAST{}, token.NoPos, false
	}
	for k, cl := range fl.astInfo.closes {
		if !consumed[k] && fl.pkg.pass.Fset.Position(k).Line == posLine {
			return cl, k, true
		}
	}
	return closeCallAST{}, token.NoPos, false
}

func (fl *funcLowerer) matchBlankedTuple(pos token.Pos) (blankedTupleAST, bool) {
	if bl, ok := fl.astInfo.blanked[pos]; ok {
		return bl, true
	}
	posLine := fl.pkg.pass.Fset.Position(pos).Line
	if posLine == 0 {
		return blankedTupleAST{}, false
	}
	for k, bl := range fl.astInfo.blanked {
		if fl.pkg.pass.Fset.Position(k).Line == posLine {
			return bl, true
		}
	}
	return blankedTupleAST{}, false
}

func (fl *funcLowerer) blankedAssignWritesNonLocalOrUsed(callPos token.Pos) bool {
	if fl.fn.Syntax() == nil {
		return false
	}
	matched := false
	walkNoNestedClosures(fl.fn.Syntax(), func(n ast.Node) {
		if matched {
			return
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) < 2 {
			return
		}
		call, ok := unparenExpr(as.Rhs[0]).(*ast.CallExpr)
		if !ok || call.Lparen != callPos {
			return
		}
		for _, lhs := range as.Lhs[:len(as.Lhs)-1] {
			if isBlankIdent(lhs) {
				continue
			}
			id, isID := unparenExpr(lhs).(*ast.Ident)
			if !isID {
				matched = true
				return
			}
			var v *types.Var
			if defV, ok := fl.pkg.pass.TypesInfo.Defs[id].(*types.Var); ok {
				v = defV
			} else if useV, ok := fl.pkg.pass.TypesInfo.Uses[id].(*types.Var); ok {
				v = useV
			}
			if v != nil && (v.Pkg() != nil && v.Parent() == v.Pkg().Scope() || fl.usedBlankedVars[v]) {
				matched = true
				return
			}
		}
	})
	return matched
}

func (fl *funcLowerer) isNamedResultVar(target *types.Var) bool {
	if target == nil || fl.fn.Signature == nil || fl.fn.Signature.Results() == nil {
		return false
	}
	res := fl.fn.Signature.Results()
	for i := range res.Len() {
		if res.At(i) == target {
			return true
		}
	}
	return false
}

func (fl *funcLowerer) assignedErrVarNotDiscarded(callPos token.Pos) *types.Var {
	if fl.fn.Syntax() == nil {
		return nil
	}
	var found *types.Var
	walkNoNestedClosures(fl.fn.Syntax(), func(n ast.Node) {
		if found != nil {
			return
		}
		switch blk := n.(type) {
		case *ast.BlockStmt:
			found = fl.findErrVarInStmtList(blk.List, callPos)
		case *ast.CaseClause:
			found = fl.findErrVarInStmtList(blk.Body, callPos)
		case *ast.CommClause:
			found = fl.findErrVarInStmtList(blk.Body, callPos)
		case *ast.IfStmt:
			if blk.Init != nil {
				if v := fl.extractLHSAssignedErrVar(blk.Init, callPos); v != nil {
					found = v
				}
			}
		case *ast.SwitchStmt:
			if blk.Init != nil {
				if v := fl.extractLHSAssignedErrVar(blk.Init, callPos); v != nil {
					found = v
				}
			}
		case *ast.ForStmt:
			if blk.Init != nil {
				if v := fl.extractLHSAssignedErrVar(blk.Init, callPos); v != nil {
					found = v
				}
			}
		}
	})
	return found
}

func (fl *funcLowerer) findErrVarInStmtList(stmts []ast.Stmt, callPos token.Pos) *types.Var {
	for i, s := range stmts {
		v := fl.extractLHSAssignedErrVar(s, callPos)
		if v == nil {
			continue
		}
		if fl.stmtListDiscardsWithBlank(stmts[i+1:], v) {
			return nil
		}
		return v
	}
	return nil
}

func (fl *funcLowerer) extractLHSAssignedErrVar(stmt ast.Stmt, callPos token.Pos) *types.Var {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if len(s.Rhs) != 1 || len(s.Lhs) == 0 {
			return nil
		}
		call, ok := unparenExpr(s.Rhs[0]).(*ast.CallExpr)
		if !ok || call.Lparen != callPos {
			return nil
		}
		errLHS := s.Lhs[len(s.Lhs)-1]
		if isBlankIdent(errLHS) {
			return nil
		}
		if id, ok := unparenExpr(errLHS).(*ast.Ident); ok {
			var v *types.Var
			if dv, ok := fl.pkg.pass.TypesInfo.Defs[id].(*types.Var); ok {
				v = dv
			} else if uv, ok := fl.pkg.pass.TypesInfo.Uses[id].(*types.Var); ok {
				v = uv
			}
			if v != nil && isErrorInterface(v.Type()) && (v.Pkg() == nil || v.Parent() != v.Pkg().Scope()) {
				return v
			}
		}
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return nil
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 || len(vs.Names) == 0 {
				continue
			}
			call, ok := unparenExpr(vs.Values[0]).(*ast.CallExpr)
			if !ok || call.Lparen != callPos {
				continue
			}
			errID := vs.Names[len(vs.Names)-1]
			if errID.Name == "_" {
				return nil
			}
			if v, ok := fl.pkg.pass.TypesInfo.Defs[errID].(*types.Var); ok && isErrorInterface(v.Type()) {
				return v
			}
		}
	}
	return nil
}

func (fl *funcLowerer) stmtListDiscardsWithBlank(rest []ast.Stmt, target *types.Var) bool {
	for _, s := range rest {
		as, ok := s.(*ast.AssignStmt)
		if !ok {
			if _, isRet := s.(*ast.ReturnStmt); isRet {
				return false
			}
			continue
		}
		// Check `_ = err` before any reassignment to `err`.
		for i, lhs := range as.Lhs {
			if isBlankIdent(lhs) && i < len(as.Rhs) {
				if id, ok := unparenExpr(as.Rhs[i]).(*ast.Ident); ok && fl.pkg.pass.TypesInfo.Uses[id] == target {
					return true
				}
			}
			if id, ok := unparenExpr(lhs).(*ast.Ident); ok {
				if fl.pkg.pass.TypesInfo.Uses[id] == target || fl.pkg.pass.TypesInfo.Defs[id] == target {
					return false
				}
			}
		}
	}
	return false
}

func (fl *funcLowerer) findBlockForPos(target token.Pos) int {
	targetLine := fl.pkg.pass.Fset.Position(target).Line
	bestBB := 0
	bestDist := 1 << 30
	for bi, b := range fl.fn.Blocks {
		if b == fl.fn.Recover {
			continue
		}
		for _, ins := range b.Instrs {
			p := ins.Pos()
			if !p.IsValid() {
				continue
			}
			ln := fl.pkg.pass.Fset.Position(p).Line
			dist := ln - targetLine
			if dist < 0 {
				dist = -dist
			}
			if dist < bestDist {
				bestDist = dist
				bestBB = bi
				if dist == 0 {
					return bi
				}
			}
		}
	}
	return bestBB
}

func isLocalCellAlloc(alloc *ssa.Alloc) bool {
	if alloc.Referrers() == nil {
		return false
	}
	// A local variable cell has Store(Addr==alloc) or UnOp(MUL, alloc) as its primary uses,
	// whereas a composite literal `&MyErr{Field: "x"}` is passed to FieldAddr, MakeInterface, Return, etc.
	hasCellLoadOrStore := false
	for _, ref := range *alloc.Referrers() {
		switch r := ref.(type) {
		case *ssa.DebugRef:
			continue
		case *ssa.Store:
			if r.Addr == alloc {
				hasCellLoadOrStore = true
			} else {
				return false
			}
		case *ssa.UnOp:
			if r.Op == token.MUL && r.X == alloc {
				hasCellLoadOrStore = true
			}
		case *ssa.MakeClosure:
			// Closure-captured local variable cell!
			hasCellLoadOrStore = true
		default:
			return false
		}
	}
	return hasCellLoadOrStore
}

func countLiveReferrers(v ssa.Value) int {
	if v == nil || v.Referrers() == nil {
		return 0
	}
	n := 0
	for _, r := range *v.Referrers() {
		if _, dbg := r.(*ssa.DebugRef); dbg {
			continue
		}
		n++
	}
	return n
}

func isExplicitBlankAssignAt(pass *analysis.Pass, syntax ast.Node, callPos token.Pos) bool {
	if syntax == nil {
		return false
	}
	found := false
	callLine := pass.Fset.Position(callPos).Line
	walkNoNestedClosures(syntax, func(n ast.Node) {
		if found {
			return
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) != 1 || !isBlankIdent(as.Lhs[0]) {
			return
		}
		if call, ok := unparenExpr(as.Rhs[0]).(*ast.CallExpr); ok {
			if call.Lparen == callPos || pass.Fset.Position(call.Lparen).Line == callLine {
				found = true
			}
		}
	})
	return found
}

func methodReceiverVal(cc *ssa.CallCommon) ssa.Value {
	if cc.IsInvoke() {
		return cc.Value
	}
	if callee := cc.StaticCallee(); callee != nil && callee.Signature.Recv() != nil && len(cc.Args) > 0 {
		return cc.Args[0]
	}
	return nil
}

func isWriteMutatingMethod(cc *ssa.CallCommon) bool {
	var name string
	var recvType types.Type
	if cc.IsInvoke() && cc.Method != nil {
		name = cc.Method.Name()
		if cc.Value != nil {
			recvType = cc.Value.Type()
		}
	} else if callee := cc.StaticCallee(); callee != nil && callee.Signature.Recv() != nil {
		name = callee.Name()
		recvType = callee.Signature.Recv().Type()
	}
	if recvType == nil {
		return false
	}
	switch name {
	case "Write", "WriteString", "WriteByte", "WriteRune", "WriteAt", "ReadFrom", "Flush", "Sync":
		obj, _, _ := types.LookupFieldOrMethod(recvType, true, nil, "Close")
		_, hasClose := obj.(*types.Func)
		return hasClose
	}
	return false
}

func isWritableResourceProducer(cc *ssa.CallCommon) bool {
	callee := cc.StaticCallee()
	if callee == nil || callee.Pkg == nil || callee.Pkg.Pkg == nil {
		return false
	}
	pkgPath := callee.Pkg.Pkg.Path()
	name := callee.Name()

	switch pkgPath {
	case "os":
		switch name {
		case "Create", "CreateTemp":
			return true
		case "OpenFile":
			// os.OpenFile(name, flag, perm): O_RDONLY is 0; any non-zero write/create/append/trunc flag makes it writable!
			if len(cc.Args) >= 2 {
				if c, ok := cc.Args[1].(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.Int {
					if v, ok := constant.Int64Val(c.Value); ok {
						// O_WRONLY=1, O_RDWR=2, O_APPEND, O_CREATE, O_TRUNC: any non-zero flag except O_CLOEXEC-only
						return v != 0
					}
				}
				return true
			}
		}
	case "bufio":
		return name == "NewWriter" || name == "NewWriterSize"
	case "compress/gzip", "compress/zlib", "compress/flate":
		return strings.HasPrefix(name, "NewWriter")
	case "archive/zip", "archive/tar", "encoding/csv", "mime/multipart":
		return name == "NewWriter"
	}
	return false
}

func isIOReaderRead(cc *ssa.CallCommon) bool {
	var name string
	var sig *types.Signature
	if cc.IsInvoke() && cc.Method != nil {
		name = cc.Method.Name()
		sig, _ = cc.Method.Type().(*types.Signature)
	} else if callee := cc.StaticCallee(); callee != nil && callee.Signature.Recv() != nil {
		name = callee.Name()
		sig = callee.Signature
	}
	if name != "Read" || sig == nil || sig.Params().Len() != 1 || sig.Results().Len() != 2 {
		return false
	}
	sl, ok := sig.Params().At(0).Type().Underlying().(*types.Slice)
	if !ok {
		return false
	}
	basic, ok := sl.Elem().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Byte && isErrorInterface(sig.Results().At(1).Type())
}

func isNoReturnCall(cc *ssa.CallCommon) bool {
	var pkgPath, typeName, fnName string
	if cc.IsInvoke() && cc.Method != nil {
		fnName = cc.Method.Name()
		if cc.Value != nil {
			typeName = types.TypeString(cc.Value.Type(), nil)
		}
	} else if callee := cc.StaticCallee(); callee != nil {
		fnName = callee.Name()
		if callee.Pkg != nil && callee.Pkg.Pkg != nil {
			pkgPath = callee.Pkg.Pkg.Path()
		}
		if callee.Signature.Recv() != nil {
			typeName = types.TypeString(callee.Signature.Recv().Type(), nil)
		}
	}
	if pkgPath == "os" && fnName == "Exit" {
		return true
	}
	if pkgPath == "runtime" && fnName == "Goexit" {
		return true
	}
	if pkgPath == "log" && (strings.HasPrefix(fnName, "Fatal") || strings.HasPrefix(fnName, "Panic")) {
		return true
	}
	if strings.Contains(typeName, "testing.") && (fnName == "Fatal" || fnName == "Fatalf" || fnName == "FailNow") {
		return true
	}
	return false
}

func isStdlibInfallibleConstructor(pkgPath, name string) bool {
	return pkgPath == "errors" && name == "New"
}

func isGuardComparison(bin *ssa.BinOp) bool {
	switch bin.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		if c, ok := bin.X.(*ssa.Const); ok && (c.IsNil() || isIntConstVal(c, 0) || isIntConstVal(c, 1)) {
			return true
		}
		if c, ok := bin.Y.(*ssa.Const); ok && (c.IsNil() || isIntConstVal(c, 0) || isIntConstVal(c, 1)) {
			return true
		}
		if isErrorInterface(bin.X.Type()) || isErrorInterface(bin.Y.Type()) {
			return true
		}
	}
	return false
}

func isNonNilErrorSentinelCompare(bin *ssa.BinOp) bool {
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return false
	}
	if c, ok := bin.X.(*ssa.Const); ok && c.IsNil() {
		return false
	}
	if c, ok := bin.Y.(*ssa.Const); ok && c.IsNil() {
		return false
	}
	return isErrorInterface(bin.X.Type()) || isErrorInterface(bin.Y.Type())
}

func isIntConstVal(v ssa.Value, want int64) bool {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.Int {
		return false
	}
	got, exact := constant.Int64Val(c.Value)
	return exact && got == want
}

func isBasicValueUse(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Basic)
	return ok
}

func bestUserPosInBlock(b *ssa.BasicBlock, fn *ssa.Function) token.Pos {
	for i := len(b.Instrs) - 1; i >= 0; i-- {
		if p := b.Instrs[i].Pos(); p.IsValid() {
			return p
		}
	}
	if fn.Syntax() != nil {
		return fn.Syntax().Pos()
	}
	return fn.Pos()
}

func describeCallCommon(cc *ssa.CallCommon) string {
	if cc.IsInvoke() && cc.Method != nil {
		return cc.Method.Name() + "()"
	}
	if callee := cc.StaticCallee(); callee != nil {
		if callee.Pkg != nil && callee.Pkg.Pkg != nil {
			return callee.Pkg.Pkg.Name() + "." + callee.Name() + "()"
		}
		return callee.Name() + "()"
	}
	return "call()"
}

func describeSSAVal(v ssa.Value) string {
	switch x := v.(type) {
	case *ssa.Extract:
		if call, ok := x.Tuple.(*ssa.Call); ok {
			return "result of " + describeCallCommon(&call.Call)
		}
	case *ssa.Alloc:
		if x.Comment != "" {
			return "'" + x.Comment + "'"
		}
	case *ssa.Parameter:
		if x.Name() != "" {
			return "'" + x.Name() + "'"
		}
	}
	return "companion value"
}
