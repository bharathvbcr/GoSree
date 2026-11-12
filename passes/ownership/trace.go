package ownership

import (
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// trace is one walk backwards from a value to what it may be. There is one walk (follow), with one
// place where it stops at a limit and one visited set, and one step for each construct it goes
// through: the edges of a phi, the arguments a parameter is called with, the bindings of a
// captured variable, the values a call returns, the stores to a local cell or to a package
// variable. What is asked about the value is a parameter of the walk (ask), not a walk of its own:
// these were eleven functions that handed a visited set to one another, and the same step written
// two or three times had come apart.
type trace struct {
	q    *question
	seen map[valVisitKey]bool
	// copied holds the fields of allocations for which the struct values stored whole anywhere
	// in the package have been read on this walk (fieldOfBase), with the depth they were read
	// at. Such a value may be a copy of another struct whose address is out of sight, which is
	// read the same way, and a field asked of a struct value is not marked as visited: without
	// this the walk would read the same stores again at every struct it comes to, as deep as
	// the depth limit lets it.
	copied map[fieldOfAlloc]int
	// byType holds the fields whose stores anywhere in the package have been read on this walk
	// for a pointer that led nowhere (fieldBehind), with the depth they were read at. Each
	// stored value is marked as visited when it is read, so a second reading finds nothing.
	byType map[structFieldKey]int
}

// firstOrShallower reports whether a reading that is made once on a walk is to be made at this
// depth, and records that it is: the first time, and again from a shallower step. A reading
// stops at the depth limit, so one from a shallower step goes further than the one before, and
// one made at the limit reads nothing at all; a reading from the same step or a deeper one
// finds nothing the earlier one did not.
func firstOrShallower[K comparable](read *map[K]int, key K, depth int) bool {
	if at, ok := (*read)[key]; ok && at <= depth {
		return false
	}
	if *read == nil {
		*read = map[K]int{}
	}
	(*read)[key] = depth
	return true
}

type fieldOfAlloc struct {
	base  *ssa.Alloc
	field int
}

// fresh is a walk for a question asked on the way, on a visited set of its own. What it has
// read is its own too: its answer is not the answer of the walk that asks.
func (t *trace) fresh() *trace {
	return &trace{q: t.q, seen: map[valVisitKey]bool{}}
}

// askKind is what a walk is asked about a value.
type askKind uint8

const (
	// askValue: the functions, closures and boxed values the value may be.
	askValue askKind = iota
	// askBase: the allocations and package variables a pointer may point to.
	askBase
	// askField: as askValue, for one field of a struct value.
	askField
	// askStored: as askValue, for what may be stored at an address.
	askStored
	// askElem: as askValue, for the elements of an array value.
	askElem
)

type ask struct {
	kind  askKind
	field int // for askField
}

// origins returns the functions, closures and boxed values v may be in fn, and whether there may be
// others. It is the walk's entry: a check that asks starts here, on a visited set of its own.
func (q *question) origins(fn *ssa.Function, v ssa.Value) ([]leafValue, bool) {
	t := &trace{q: q, seen: map[valVisitKey]bool{}}
	return t.follow(fn, v, ask{kind: askValue}, 0)
}

// resolveInterfaceTypes returns the concrete types an interface value may hold, and whether there
// may be others: a leaf that is not a boxed value, or one boxed from another interface.
func (q *question) resolveInterfaceTypes(fn *ssa.Function, v ssa.Value) ([]types.Type, bool) {
	leaves, incomplete := q.origins(fn, v)
	var out []types.Type
	addType := func(t types.Type) {
		if t == nil || types.IsInterface(t) {
			incomplete = true
			return
		}
		for _, ex := range out {
			if types.Identical(ex, t) {
				return
			}
		}
		out = append(out, t)
	}
	for _, lf := range leaves {
		if mi, ok := lf.v.(*ssa.MakeInterface); ok {
			addType(mi.X.Type())
		} else {
			incomplete = true
		}
	}
	return out, incomplete
}

// follow answers a for v in fn: what it found, and whether that may not be all.
//
// The limits are here and nowhere else. A walk that goes deeper than originDepth, or visits more
// than originBudget values, answers "may not be all" and marks its question as cut. Two things are
// as the separate walks had them and are not what one walk would choose: askField and askStored
// neither mark what they visit nor spend budget, and askStored is not stopped by depth (the walks
// it starts are).
func (t *trace) follow(fn *ssa.Function, v ssa.Value, a ask, depth int) ([]leafValue, bool) {
	if v == nil {
		return nil, true
	}
	if depth > originDepth && a.kind != askStored {
		t.q.cut = true
		return nil, true
	}
	bodyFn := canonicalBodyFn(fn)
	if a.kind == askValue || a.kind == askBase || a.kind == askElem {
		// An array value is asked for its elements and for nothing else, so one set serves.
		key := valVisitKey{fn: bodyFn, v: v}
		if t.seen[key] {
			return nil, false
		}
		if len(t.seen) >= originBudget {
			// Out of budget: what v may be is not known. This used to answer "nothing more, and that
			// is all", which made a callee chosen among more values than the budget look fully known.
			t.q.cut = true
			return nil, true
		}
		t.seen[key] = true
	}

	switch x := v.(type) {
	case *ssa.Function, *ssa.MakeClosure, *ssa.MakeInterface:
		if a.kind == askValue {
			return []leafValue{{fn: bodyFn, v: v}}, false
		}
	case *ssa.Alloc:
		switch a.kind {
		case askBase:
			return []leafValue{{fn: bodyFn, v: v}}, false
		case askStored:
			if x.Referrers() != nil {
				return t.storedInCell(x, bodyFn, ask{kind: askValue}, depth)
			}
		}
	case *ssa.Global:
		switch a.kind {
		case askBase:
			return []leafValue{{fn: bodyFn, v: v}}, false
		case askStored:
			if t.q.contracts != nil {
				return t.storedInGlobal(x, ask{kind: askValue}, depth)
			}
		}
	case *ssa.ChangeType:
		// For a struct value too: a struct converted from another struct type has the same fields
		// in the same order.
		if a.kind != askStored {
			return t.follow(bodyFn, x.X, a, depth+1)
		}
	case *ssa.ChangeInterface:
		if a.kind == askValue {
			return t.follow(bodyFn, x.X, a, depth+1)
		}
	case *ssa.TypeAssert:
		if a.kind == askValue || a.kind == askBase {
			return t.follow(bodyFn, x.X, a, depth+1)
		}
	case *ssa.Phi:
		if a.kind != askStored {
			var out []leafValue
			incomplete := len(x.Edges) == 0
			for _, e := range x.Edges {
				sub, inc := t.follow(bodyFn, e, a, depth+1)
				out = append(out, sub...)
				if inc {
					incomplete = true
				}
			}
			return out, incomplete
		}
	case *ssa.FreeVar:
		// For askStored this is a captured cell: the bindings are the cell's address in the parent.
		if a.kind != askField {
			return t.bindings(bodyFn, x, a, depth)
		}
	case *ssa.Parameter:
		switch a.kind {
		case askValue, askBase:
			return t.arguments(bodyFn, x, a, depth)
		case askField, askElem:
			// With no call site that says anything, a field is looked up by its type below, and
			// the elements of an array are not known.
			if out, incomplete := t.arguments(bodyFn, x, a, depth); len(out) > 0 {
				return out, incomplete
			}
		}
	case *ssa.UnOp:
		switch {
		case x.Op == token.MUL && a.kind == askValue:
			return t.follow(bodyFn, x.X, ask{kind: askStored}, depth+1)
		case x.Op == token.MUL && a.kind == askField:
			return t.fieldBehind(bodyFn, x.X, a.field, depth)
		case x.Op == token.MUL && a.kind == askElem:
			return t.elemsBehind(bodyFn, x.X, depth)
		case x.Op == token.MUL && a.kind == askBase:
			if alloc, ok := x.X.(*ssa.Alloc); ok && alloc.Referrers() != nil {
				return t.storedInCell(alloc, bodyFn, a, depth)
			}
			if g, ok := x.X.(*ssa.Global); ok && t.q.contracts != nil {
				return t.storedInGlobal(g, a, depth)
			}
		case x.Op == token.ARROW && a.kind == askValue:
			return t.received(bodyFn, x.X, depth+1)
		}
	case *ssa.Field:
		if a.kind == askValue {
			return t.follow(bodyFn, x.X, ask{kind: askField, field: x.Field}, depth+1)
		}
	case *ssa.FieldAddr:
		if a.kind == askStored {
			return t.fieldBehind(fn, x.X, x.Field, depth)
		}
	case *ssa.IndexAddr:
		if a.kind == askStored {
			return t.elements(fn, x, depth)
		}
	case *ssa.Index:
		// An element read out of an array value: a range over an array, an array a call returned.
		if a.kind == askValue {
			return t.follow(bodyFn, x.X, ask{kind: askElem}, depth+1)
		}
	case *ssa.Lookup:
		if a.kind == askValue {
			return t.looked(bodyFn, x.X, depth+1)
		}
	case *ssa.Extract:
		if a.kind == askValue {
			if lk, ok := x.Tuple.(*ssa.Lookup); ok && x.Index == 0 {
				return t.looked(bodyFn, lk.X, depth+1)
			}
			if recv, ok := x.Tuple.(*ssa.UnOp); ok && recv.Op == token.ARROW && x.Index == 0 {
				return t.received(bodyFn, recv.X, depth+1)
			}
		}
		if a.kind == askValue || a.kind == askBase || a.kind == askElem {
			if callIns, ok := x.Tuple.(*ssa.Call); ok {
				return t.returned(bodyFn, callIns, x.Index, a, depth+1)
			}
		}
	case *ssa.Call:
		if a.kind == askValue || a.kind == askBase || a.kind == askElem {
			return t.returned(bodyFn, x, 0, a, depth+1)
		}
	}
	if a.kind == askField {
		return t.fieldByType(v, a.field, depth)
	}
	return nil, true
}

// bindings walks what each closure made from bodyFn binds to its free variable fv.
func (t *trace) bindings(bodyFn *ssa.Function, fv *ssa.FreeVar, a ask, depth int) ([]leafValue, bool) {
	if t.q.contracts == nil || bodyFn == nil {
		return nil, true
	}
	fvIdx := slices.Index(bodyFn.FreeVars, fv)
	if fvIdx < 0 {
		return nil, true
	}
	mcs := t.q.contracts.closuresOf[bodyFn]
	if len(mcs) == 0 {
		return nil, true
	}
	var out []leafValue
	incomplete := false
	for _, mc := range mcs {
		if fvIdx < len(mc.Bindings) {
			sub, inc := t.follow(mc.Parent(), mc.Bindings[fvIdx], a, depth+1)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		} else {
			incomplete = true
		}
	}
	return out, incomplete
}

// arguments walks what each call of bodyFn in sight passes for its parameter p. With no call in
// sight it answers nothing, and that this may not be all.
//
// The arguments of a call line up with bodyFn's parameters as they stand, whatever is called. A
// method value is a closure over a wrapper whose parameters are the method's without the receiver,
// and its calls are listed under the wrapper, so there is no place to step over.
func (t *trace) arguments(bodyFn *ssa.Function, p *ssa.Parameter, a ask, depth int) ([]leafValue, bool) {
	if t.q.contracts == nil || bodyFn == nil {
		return nil, true
	}
	pIdx := slices.Index(bodyFn.Params, p)
	if pIdx < 0 {
		return nil, true
	}
	sites := t.q.contracts.callSitesOf[bodyFn]
	if len(sites) == 0 {
		return nil, true
	}
	incomplete := t.q.contracts.openCallers[bodyFn]
	var out []leafValue
	for _, site := range sites {
		cc := site.ins.Common()
		if pIdx >= len(cc.Args) {
			incomplete = true
			continue
		}
		sub, inc := t.follow(site.caller, cc.Args[pIdx], a, depth+1)
		out = append(out, sub...)
		if inc {
			incomplete = true
		}
	}
	return out, incomplete
}

// storedInCell walks every value stored to a local cell: the stores made through the variable,
// through each closure that captures it and through each function of this package its address is
// passed to (usesOfCell). They are all the cell may hold unless its address goes somewhere that is
// not read, and then the answer may not be all: for a variable that holds a function, the
// functions this package stores through a pointer stand in for what is written there
// (writtenThroughPointer). fn stands in for a store with no function.
func (t *trace) storedInCell(alloc *ssa.Alloc, fn *ssa.Function, a ask, depth int) ([]leafValue, bool) {
	uses := usesOfCell(alloc)
	var out []leafValue
	incomplete := uses.open
	if uses.open {
		out = t.writtenThroughPointer(alloc, pointee(alloc.Type()), a, depth)
	}
	for _, stIns := range uses.stores {
		pFn := stIns.Parent()
		if pFn == nil {
			pFn = fn
		}
		sub, inc := t.follow(pFn, stIns.Val, a, depth+1)
		out = append(out, sub...)
		if inc {
			incomplete = true
		}
	}
	if len(out) == 0 {
		incomplete = true
	}
	return out, incomplete
}

// storedInGlobal walks every value this package stores to a package variable. An exported one may be
// stored to from elsewhere.
func (t *trace) storedInGlobal(g *ssa.Global, a ask, depth int) ([]leafValue, bool) {
	vals := t.q.contracts.globalStores[g]
	incomplete := len(vals) == 0 || token.IsExported(g.Name())
	var out []leafValue
	for _, sv := range vals {
		sub, inc := t.follow(parentOf(sv), sv, a, depth+1)
		out = append(out, sub...)
		if inc {
			incomplete = true
		}
	}
	return out, incomplete
}

// parentOf returns the function a stored value is computed in, when it is computed.
func parentOf(v ssa.Value) *ssa.Function {
	if ins, ok := v.(ssa.Instruction); ok {
		return ins.Parent()
	}
	return nil
}

// returned walks what a call returns in its result resIdx: for each return of the one function the
// call can run, the returned value, or the call's own argument when the value is a parameter.
func (t *trace) returned(caller *ssa.Function, callIns *ssa.Call, resIdx int, a ask, depth int) ([]leafValue, bool) {
	if callIns.Call.IsInvoke() {
		return nil, true
	}
	callee, _ := directCallee(callIns.Call.Value)
	if callee == nil {
		return nil, true
	}
	bodyFn := canonicalBodyFn(callee)
	if len(bodyFn.Blocks) == 0 {
		return nil, true
	}
	var out []leafValue
	incomplete := false
	sawRet := false
	forEachInstr(bodyFn, func(ins ssa.Instruction) {
		ret, ok := ins.(*ssa.Return)
		if !ok || resIdx >= len(ret.Results) {
			return
		}
		sawRet = true
		rv := ret.Results[resIdx]
		if pIdx := paramFlowIndex(rv, bodyFn); pIdx >= 0 && pIdx < len(callIns.Call.Args) {
			sub, inc := t.follow(caller, callIns.Call.Args[pIdx], a, depth+1)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
			return
		}
		sub, inc := t.follow(bodyFn, rv, a, depth+1)
		out = append(out, sub...)
		if inc {
			incomplete = true
		}
	})
	if !sawRet {
		incomplete = true
	}
	return out, incomplete
}

// fieldBehind walks what a field of the struct a pointer points to may hold: for each allocation
// or package variable the pointer may point to, the stores to that field. Where the pointer leads
// nowhere in sight, the stores to the field of that struct type anywhere in the package stand in,
// and the answer may not be all.
//
// What the pointer points to is asked on a visited set of its own. Its answer is used here, for
// this field; on the set of the walk that asks, a second read through the same pointer would be
// answered "nothing more".
func (t *trace) fieldBehind(fn *ssa.Function, ptr ssa.Value, field int, depth int) ([]leafValue, bool) {
	fresh := t.fresh()
	bases, incomplete := fresh.follow(fn, ptr, ask{kind: askBase}, depth+1)
	var out []leafValue
	for _, b := range bases {
		sub, inc := t.fieldOfBase(b, field, depth+1)
		out = append(out, sub...)
		if inc {
			incomplete = true
		}
	}
	if len(out) == 0 && t.q.contracts != nil && ptr != nil {
		if keyStr, ok := derefStructTypeKey(ptr.Type()); ok {
			if firstOrShallower(&t.byType, structFieldKey{typ: keyStr, field: field}, depth) {
				for _, iv := range t.q.contracts.fieldStores(keyStr, field) {
					sub, _ := t.follow(iv.fn, iv.v, ask{kind: askValue}, depth+1)
					out = append(out, sub...)
				}
			}
			incomplete = true
		}
	}
	if len(out) == 0 {
		incomplete = true
	}
	return out, incomplete
}

// fieldOfBase walks what is stored to one field of an allocation or a package variable: the stores
// to the field, and that field of each whole value stored.
//
// For an allocation these are the stores made through every address it has (usesOfAddress): the
// variable, each closure that captures it, each function of this package it is passed to. They
// are all the field may hold unless an address of the whole struct or of this field goes somewhere
// that is not read. The address of another field going somewhere (s.mu.Lock()) says nothing about
// this one. Where the whole struct's address goes out of sight, the stores to the field of that
// struct type anywhere in the package stand in for what is written through its other names, as
// they do in fieldBehind where a pointer leads nowhere, and the answer may not be all.
func (t *trace) fieldOfBase(lf leafValue, field int, depth int) ([]leafValue, bool) {
	switch base := lf.v.(type) {
	case *ssa.Alloc:
		if base.Referrers() == nil {
			return nil, true
		}
		uses := usesOfCell(base)
		var out []leafValue
		incomplete := uses.open
		fieldOpen := false
		walk := func(stIns *ssa.Store, a ask) {
			pFn := stIns.Parent()
			if pFn == nil {
				pFn = lf.fn
			}
			sub, inc := t.follow(pFn, stIns.Val, a, depth+1)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		for _, fa := range uses.fields {
			if fa.Field != field {
				continue
			}
			// The field is memory of its own, with its own addresses: this one, and the parameter
			// of a function it is passed to.
			fu := usesOfAddress(fa)
			if fu.open || len(fu.fields)+len(fu.elems)+len(fu.slices) > 0 {
				incomplete = true // handed on, or a part of the field is written on its own
			}
			if fu.open {
				fieldOpen = true
			}
			for _, stIns := range fu.stores {
				walk(stIns, ask{kind: askValue})
			}
		}
		for _, stIns := range uses.stores {
			walk(stIns, ask{kind: askField, field: field})
		}
		if uses.open && t.q.contracts != nil {
			// The struct's address goes somewhere that is not read: it is returned, stored, joined
			// with another pointer. A store through that other name is a store to this struct, and
			// which stores those are is not followed. The stores the package makes to this field
			// of this struct type stand in for them, and this field of each struct value it stores
			// whole. A store made to another allocation is not one of them: that is other memory.
			if keyStr, ok := derefStructTypeKey(base.Type()); ok {
				for _, iv := range t.q.contracts.fieldStores(keyStr, field) {
					if iv.otherMemory(base) {
						continue
					}
					sub, _ := t.follow(iv.fn, iv.v, ask{kind: askValue}, depth+1)
					out = append(out, sub...)
				}
				if firstOrShallower(&t.copied, fieldOfAlloc{base, field}, depth) {
					for _, iv := range t.q.contracts.wholeStores(keyStr) {
						if iv.otherMemory(base) {
							continue
						}
						sub, _ := t.follow(iv.fn, iv.v, ask{kind: askField, field: field}, depth+1)
						out = append(out, sub...)
					}
				}
			}
		}
		if uses.open || fieldOpen {
			// The field's own address is out of sight, or may be taken through another name of the
			// struct. It is never a variable's, so no allocation a store names is this field.
			if st, ok := pointee(base.Type()).Underlying().(*types.Struct); ok && field < st.NumFields() {
				out = append(out, t.writtenThroughPointer(nil, st.Field(field).Type(), ask{kind: askValue}, depth)...)
			}
		}
		if len(out) == 0 {
			incomplete = true
		}
		return out, incomplete
	case *ssa.Global:
		if t.q.contracts == nil {
			return nil, true
		}
		vals := t.q.contracts.globalFieldStores[base][field]
		wholeVals := t.q.contracts.globalStores[base]
		incomplete := (len(vals) == 0 && len(wholeVals) == 0) || token.IsExported(base.Name())
		var out []leafValue
		for _, sv := range vals {
			sub, inc := t.follow(parentOf(sv), sv, ask{kind: askValue}, depth+1)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		for _, wv := range wholeVals {
			sub, inc := t.follow(parentOf(wv), wv, ask{kind: askField, field: field}, depth+1)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		if len(out) == 0 {
			incomplete = true
		}
		return out, incomplete
	}
	return nil, true
}

// writtenThroughPointer walks the functions this package stores through a pointer (*p = f), for a
// function variable or field of type typ whose address has gone where it is not read: any of
// those stores may be to it, and they stand in for what is written there. A store through a
// pointer that is known to be another allocation's is left out; one that is known to be cell's
// own is kept, since a pointer that is cell's or another's is not read where the cell is. The
// caller has answered that what it found may not be all.
func (t *trace) writtenThroughPointer(cell *ssa.Alloc, typ types.Type, a ask, depth int) []leafValue {
	sig, ok := typ.Underlying().(*types.Signature)
	if !ok || t.q.contracts == nil {
		return nil
	}
	var out []leafValue
	for _, iv := range t.q.contracts.funcPointerStores {
		if !types.Identical(iv.v.Type().Underlying(), sig) || iv.otherMemory(cell) {
			continue
		}
		sub, _ := t.follow(iv.fn, iv.v, a, depth+1)
		out = append(out, sub...)
	}
	return out
}

// fieldByType walks the stores to a field of a struct type anywhere in the package. It is what is
// left when the struct value itself leads nowhere, and its answer may never be all.
func (t *trace) fieldByType(structVal ssa.Value, field int, depth int) ([]leafValue, bool) {
	if t.q.contracts != nil {
		if keyStr, ok := structValueTypeKey(structVal.Type()); ok {
			var out []leafValue
			for _, iv := range t.q.contracts.fieldStores(keyStr, field) {
				sub, _ := t.follow(iv.fn, iv.v, ask{kind: askValue}, depth+1)
				out = append(out, sub...)
			}
			if len(out) > 0 {
				return out, true
			}
		}
	}
	return nil, true
}

// elements walks what an element of an array may hold, for an element reached by its address: the
// array's own, or a slice's of it.
func (t *trace) elements(fn *ssa.Function, ia *ssa.IndexAddr, depth int) ([]leafValue, bool) {
	base := ia.X
	if sl, ok := base.(*ssa.Slice); ok {
		base = sl.X
	}
	return t.elemsBehind(fn, base, depth)
}

// elemsBehind walks what the elements of the local array a pointer points to may hold. The array
// is read as any local memory is (usesOfCell), through every name it has: its own, a closure's
// that captures it, a parameter's of a function it is passed to. What may be in an element is
// what is stored through an element's address, taken from any of those names or from a slice of
// the array, and the elements of every array stored to it whole. An array built as a value is
// stored whole: the literal is built in memory of its own first.
//
// Which element is read is not followed, and a slice of the array may be written through where
// that is not read, so the answer may never be all.
func (t *trace) elemsBehind(fn *ssa.Function, ptr ssa.Value, depth int) ([]leafValue, bool) {
	fresh := t.fresh()
	bases, _ := fresh.follow(fn, ptr, ask{kind: askBase}, depth+1)
	var out []leafValue
	for _, b := range bases {
		alloc, ok := b.v.(*ssa.Alloc)
		if !ok || alloc.Referrers() == nil {
			continue
		}
		walk := func(stIns *ssa.Store, a ask) {
			pFn := stIns.Parent()
			if pFn == nil {
				pFn = b.fn
			}
			sub, _ := t.follow(pFn, stIns.Val, a, depth+1)
			out = append(out, sub...)
		}
		element := func(ia *ssa.IndexAddr) {
			for _, stIns := range usesOfAddress(ia).stores {
				walk(stIns, ask{kind: askValue})
			}
		}
		uses := usesOfCell(alloc)
		for _, ia := range uses.elems {
			element(ia)
		}
		for _, sl := range uses.slices {
			if sl.Referrers() == nil {
				continue
			}
			for _, ref := range *sl.Referrers() {
				if ia, ok := ref.(*ssa.IndexAddr); ok {
					element(ia)
				}
			}
		}
		for _, stIns := range uses.stores {
			walk(stIns, ask{kind: askElem})
		}
	}
	return out, true
}

// looked walks what is put into a map made in sight. What a map holds is never all in sight.
func (t *trace) looked(fn *ssa.Function, m ssa.Value, depth int) ([]leafValue, bool) {
	mm, ok := m.(*ssa.MakeMap)
	if !ok || mm.Referrers() == nil {
		return nil, true
	}
	var out []leafValue
	for _, ref := range *mm.Referrers() {
		if mu, ok := ref.(*ssa.MapUpdate); ok && mu.Map == mm {
			sub, _ := t.follow(fn, mu.Value, ask{kind: askValue}, depth+1)
			out = append(out, sub...)
		}
	}
	return out, true
}

// received walks what is sent on a channel made in sight.
func (t *trace) received(fn *ssa.Function, ch ssa.Value, depth int) ([]leafValue, bool) {
	mc, ok := ch.(*ssa.MakeChan)
	if !ok || mc.Referrers() == nil {
		return nil, true
	}
	var out []leafValue
	incomplete := false
	for _, ref := range *mc.Referrers() {
		if snd, ok := ref.(*ssa.Send); ok && snd.Chan == mc {
			sub, inc := t.follow(snd.Parent(), snd.X, ask{kind: askValue}, depth+1)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
	}
	if len(out) == 0 {
		incomplete = true
	}
	return out, incomplete
}
