package ownership

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// The alias analysis is per-function Steensgaard unification: every pointer-bearing SSA value has a
// class, derivations (field and index addresses, slicing, conversions, phis) join their operand's
// class, a load yields a member of the base class's pointee, and a store joins the stored value into
// the address's pointee. It is flow-insensitive and field-insensitive on purpose: coarse aliasing
// over-reports, it never under-reports. Only the move kill in check.go is flow-sensitive.

type eventKind uint8

const (
	// evForeign: a value of unknown provenance (parameter, package variable, call result) joins the class.
	evForeign eventKind = iota
	// evEscape: the class flows somewhere that outlives the call or is visible to other code.
	evEscape
	// evReturn: the class is returned.
	evReturn
	// evTransfer: the class is moved to another goroutine or to an owned parameter.
	evTransfer
	// evWrite: memory is written through a value of the class (writeOf), here or in a callee. It is
	// not a leak; summaries read it along the forward pointee chain only (pointsTo).
	evWrite
)

type event struct {
	kind   eventKind
	instr  ssa.Instruction // nil for parameters and free variables
	pos    token.Pos
	desc   string
	param  int         // index of the parameter (or -1); the parameter's own foreign event is not a leak of itself
	free   int         // index of the free variable (or -1)
	global *ssa.Global // set when the event originates from a package variable
	// byContract is 1 + the index of the parameter whose contract at a call of this function
	// accounts for this event, or 0. Two kinds of event are accounted for: a publish of the
	// parameter that the index records for the function (directPublish, or a callForwards rule
	// that resolves to a publisher), and the call of a callable or receiver parameter that is
	// handed the parameter, which is what a callForwards rule stands for. A caller that finds the
	// argument published treats these as the publish and every other event as something else the
	// function does with the argument (summary.leaksBesidesContract).
	byContract int
}

func (e event) accountedTo(param int) bool { return e.byContract == param+1 }

type node struct {
	parent  *node
	pointee *node
	events  []event
	size    uint32
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

type transfer struct {
	instr        ssa.Instruction
	caseBlock    *ssa.BasicBlock
	v            ssa.Value
	pos          token.Pos
	what         string
	opPos        token.Pos
	index        int
	freezesToImm bool
	// held: v is the address of a variable, and what is transferred is the variable's value: a
	// closure that captured the variable publishes it (transferHeld). The variable and its
	// value are one region, so the checks read v's as for any transfer.
	held bool
}

type graph struct {
	st         *state
	fn         *ssa.Function
	nodes      map[ssa.Value]*node
	transfers  []transfer
	final      map[ssa.Value]bool
	ownedFree  bool
	rev        map[*node][]*node
	defers     []*ssa.Defer
	idx        map[ssa.Instruction]int
	regions    map[*node]map[*node]bool
	regionVals map[*node][]ssa.Value
	// cutCalls are the calls whose callees could not all be found while they pass an argument one
	// of them could publish (applyCallContract). checkTransfers reports them (TransferCut).
	cutCalls []ssa.Instruction
}

func (st *state) buildGraph(fn *ssa.Function) *graph {
	g := &graph{
		st:         st,
		fn:         fn,
		nodes:      map[ssa.Value]*node{},
		idx:        map[ssa.Instruction]int{},
		regions:    map[*node]map[*node]bool{},
		regionVals: map[*node][]ssa.Value{},
	}
	for _, b := range fn.Blocks {
		for i, ins := range b.Instrs {
			g.idx[ins] = i
			if d, ok := ins.(*ssa.Defer); ok {
				g.defers = append(g.defers, d)
			}
		}
	}
	g.final = finalCells(st, fn)
	g.ownedFree = closureIsGoroutineBody(fn)
	for i, p := range fn.Params {
		n := g.val(p)
		if n == nil || st.isIsoParam(p) {
			continue
		}
		n.events = append(n.events, event{kind: evForeign, pos: p.Pos(), desc: "parameter " + p.Name(), param: i, free: -1})
	}
	for i, fv := range fn.FreeVars {
		n := g.val(fv)
		if n == nil || g.ownedFree {
			continue
		}
		n.events = append(n.events, event{kind: evForeign, pos: fv.Pos(), desc: "captured variable " + fv.Name(), param: -1, free: i})
	}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			g.instr(ins)
		}
	}
	return g
}

func (g *graph) idxOf(ins ssa.Instruction) int {
	if g != nil {
		if i, ok := g.idx[ins]; ok {
			return i
		}
	}
	return indexIn(ins)
}

func (g *graph) dominates(a, b ssa.Instruction) bool {
	if a.Block() == b.Block() {
		return g.idxOf(a) < g.idxOf(b)
	}
	return a.Block().Dominates(b.Block())
}

func (g *graph) newNode() *node { return &node{size: 1} }

// val returns v's class, or nil when values of v's type cannot carry unchecked shared memory.
func (g *graph) val(v ssa.Value) *node {
	if n, ok := g.nodes[v]; ok {
		if n == nil {
			return nil
		}
		return n.find()
	}
	var n *node
	if _, guarded := g.st.localGuards[v]; guarded {
		// A guarded local's cell is shared by design; SAFE009 checks its accesses.
		g.nodes[v] = nil
		return nil
	}
	if g.final[v] {
		g.nodes[v] = nil
		return nil
	}
	switch v := v.(type) {
	case *ssa.Const, *ssa.Function, *ssa.Builtin:
	case *ssa.Global:
		n = g.newNode()
		d := "package variable " + v.Name()
		n.events = append(n.events,
			event{kind: evForeign, pos: v.Pos(), desc: d, param: -1, free: -1, global: v},
			event{kind: evEscape, pos: v.Pos(), desc: "stored in " + d, param: -1, free: -1, global: v})
	case *ssa.Alloc:
		if !g.st.cls.Shareable(v.Type()) {
			n = g.newNode()
		}
	case *ssa.MakeInterface:
		// An interface value is tracked when the value boxed in it is, whatever the interface: a
		// pointer boxed as an error is still that pointer, though error itself counts as shareable.
		if !g.st.cls.Shareable(v.Type()) || !g.st.cls.Shareable(v.X.Type()) {
			n = g.newNode()
		}
	default:
		if !g.st.cls.Shareable(v.Type()) {
			n = g.newNode()
		}
	}
	g.nodes[v] = n
	return n
}

func (g *graph) union(a, b *node) {
	a, b = a.find(), b.find()
	if a == b {
		return
	}
	if a.size < b.size {
		a, b = b, a
	}
	b.parent = a
	a.size += b.size
	a.events = append(a.events, b.events...)
	b.events = nil
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

func (g *graph) pointee(n *node) *node {
	n = n.find()
	if n.pointee == nil {
		n.pointee = g.newNode()
	}
	n.pointee = n.pointee.find()
	return n.pointee
}

func (g *graph) join(v, x ssa.Value) {
	vn := g.val(v)
	if vn == nil {
		return
	}
	if xn := g.val(x); xn != nil {
		g.union(vn, xn)
	}
}

func (g *graph) add(n *node, e event) {
	if n == nil {
		return
	}
	n = n.find()
	n.events = append(n.events, e)
}

func ev(kind eventKind, ins ssa.Instruction, desc string) event {
	var pos token.Pos
	if ins != nil {
		pos = ins.Pos()
	}
	return event{kind: kind, instr: ins, pos: pos, desc: desc, param: -1, free: -1}
}

// written records that ins writes through v. Guarded memory is left out: SAFE009 checks every access
// to it, and a write under its mutex is not a race.
func (g *graph) written(ins ssa.Instruction, v ssa.Value, desc string) {
	if _, guarded := g.st.slotOf(v); guarded {
		return
	}
	g.add(g.val(v), ev(evWrite, ins, desc))
}

// writtenCaptures records a write through every value closure v captured: a function value whose
// body runs where gosree cannot see it may write all of them.
func (g *graph) writtenCaptures(ins ssa.Instruction, v ssa.Value, desc string) {
	if mc, ok := v.(*ssa.MakeClosure); ok {
		for _, b := range mc.Bindings {
			g.written(ins, b, desc)
		}
	}
}

func (g *graph) instr(ins ssa.Instruction) {
	if w, ok := writeOf(ins); ok {
		g.written(ins, w.addr, "written here")
	}
	switch ins := ins.(type) {
	case *ssa.Alloc, *ssa.MakeSlice, *ssa.MakeMap:
		g.val(ins.(ssa.Value))
	case *ssa.MakeClosure:
		c := g.val(ins)
		for _, b := range ins.Bindings {
			if bn := g.val(b); bn != nil && c != nil {
				g.union(g.pointee(c), bn)
			}
		}
		g.closureRetains(ins)
	case *ssa.Phi:
		for _, e := range ins.Edges {
			g.join(ins, e)
		}
	// A field or element of a shared value (one with no class: an imm or shared-by-design type) is
	// shared memory too, not a fresh value; a load through a shared pointer is treated the same way.
	case *ssa.FieldAddr:
		g.deriveOrForeign(ins, ins.X, "a field of shared value "+ins.X.Type().String())
	case *ssa.Field:
		g.deriveOrForeign(ins, ins.X, "a field of shared value "+ins.X.Type().String())
	case *ssa.IndexAddr:
		g.deriveOrForeign(ins, ins.X, "an element of shared value "+ins.X.Type().String())
	case *ssa.Index:
		g.deriveOrForeign(ins, ins.X, "an element of shared value "+ins.X.Type().String())
	case *ssa.Slice:
		g.join(ins, ins.X)
	case *ssa.ChangeType:
		g.join(ins, ins.X)
	case *ssa.SliceToArrayPointer:
		g.join(ins, ins.X)
	case *ssa.Range:
		g.join(ins, ins.X)
	case *ssa.Extract:
		if c, ok := ins.Tuple.(*ssa.Call); ok && !c.Common().IsInvoke() && c.Common().StaticCallee() != nil {
			g.val(ins)
		} else {
			g.join(ins, ins.Tuple)
		}
	case *ssa.MakeInterface:
		// Boxing a shareable value copies it, so the interface is fresh.
		g.join(ins, ins.X)
	case *ssa.Convert:
		// string <-> []byte/[]rune conversions copy; pointer conversions keep the class.
		g.join(ins, ins.X)
	case *ssa.MultiConvert:
		g.join(ins, ins.X)
	case *ssa.ChangeInterface:
		g.deriveOrForeign(ins, ins.X, "conversion of a shared interface value")
	case *ssa.TypeAssert:
		g.deriveOrForeign(ins, ins.X, "type assertion on a value of shared type "+ins.X.Type().String())
	case *ssa.UnOp:
		switch ins.Op {
		case token.MUL:
			vn := g.val(ins)
			if vn == nil {
				return
			}
			if xn := g.val(ins.X); xn != nil {
				g.union(vn, g.pointee(xn))
			} else {
				g.add(vn, ev(evForeign, ins, "load through a shared pointer of type "+ins.X.Type().String()))
			}
		case token.ARROW:
			// A received value was moved to this goroutine by its sender: it is owned here.
			g.val(ins)
		}
	case *ssa.Lookup:
		if vn := g.val(ins); vn != nil {
			if xn := g.val(ins.X); xn != nil {
				g.union(vn, g.pointee(xn))
			}
		}
	case *ssa.Next:
		if vn := g.val(ins); vn != nil {
			if in := g.val(ins.Iter); in != nil {
				g.union(vn, g.pointee(in))
			}
		}
	case *ssa.Store:
		vn := g.val(ins.Val)
		if vn == nil {
			return
		}
		if an := g.val(ins.Addr); an != nil {
			g.union(g.pointee(an), vn)
		} else {
			g.add(vn, ev(evEscape, ins, "stored through a shared pointer"))
		}
	case *ssa.MapUpdate:
		mn := g.val(ins.Map)
		for _, x := range []ssa.Value{ins.Key, ins.Value} {
			if xn := g.val(x); xn != nil {
				if mn != nil {
					g.union(g.pointee(mn), xn)
				} else {
					g.add(xn, ev(evEscape, ins, "stored in a shared map"))
				}
			}
		}
	case *ssa.Send:
		g.transferWithOp(ins, ins.X, ins.Pos(), 0, "sent on a channel")
	case *ssa.Select:
		for i, s := range ins.States {
			if s.Dir == types.SendOnly && s.Send != nil {
				cb := selectCaseBlock(ins, i)
				g.transferWithCase(ins, cb, s.Send, s.Pos, i, "sent on a channel in select")
			}
		}
		g.val(ins)
	case *ssa.Return:
		for _, r := range ins.Results {
			g.add(g.val(r), ev(evReturn, ins, "returned"))
		}
	case *ssa.Panic:
		g.add(g.val(ins.X), ev(evEscape, ins, "passed to panic"))
	case *ssa.Go:
		g.goStmt(ins)
	case *ssa.Call:
		g.call(ins, ins.Common(), ins)
	case *ssa.Defer:
		g.call(ins, ins.Common(), nil)
	}
}

func (g *graph) deriveOrForeign(v, x ssa.Value, desc string) {
	vn := g.val(v)
	if vn == nil {
		return
	}
	if xn := g.val(x); xn != nil {
		g.union(vn, xn)
		return
	}
	g.add(vn, ev(evForeign, v.(ssa.Instruction), desc))
}

func (g *graph) transferWithCase(ins ssa.Instruction, cb *ssa.BasicBlock, v ssa.Value, opPos token.Pos, index int, what string) {
	g.transferFull(ins, cb, v, opPos, index, what, false, 0)
}

func (g *graph) transfer(ins ssa.Instruction, v ssa.Value, what string) {
	g.transferFull(ins, nil, v, token.NoPos, -1, what, false, 0)
}

func (g *graph) transferWithOp(ins ssa.Instruction, v ssa.Value, opPos token.Pos, index int, what string) {
	g.transferFull(ins, nil, v, opPos, index, what, false, 0)
}

// transferPublish records that a call contract publishes v. When v is one of this function's
// parameters and the index holds that the function publishes it, the event is accounted to that
// parameter (event.byContract).
func (g *graph) transferPublish(ins ssa.Instruction, v ssa.Value, index int, what string, freezesToImm bool) {
	g.transferFull(ins, nil, v, token.NoPos, index, what, freezesToImm, g.publishedParam(v))
}

// publishedParam is 1 + the index of the parameter v comes from, when the index records that this
// function publishes that parameter, by itself or through a forward; 0 otherwise.
func (g *graph) publishedParam(v ssa.Value) int {
	ci := g.st.contracts
	if ci == nil {
		return 0
	}
	body := canonicalBodyFn(g.fn)
	p := paramFlowIndex(v, body)
	if p < 0 {
		return 0
	}
	if slices.ContainsFunc(ci.directPublish[body], func(s pubArgSpec) bool { return s.argIdx == p }) ||
		slices.ContainsFunc(ci.callForwards[body], func(r callForwardRule) bool { return r.argParam == p }) {
		return p + 1
	}
	return 0
}

// forwardedEscape is the escape of a, the argument in slot slot of the dynamic call cc. When the
// call is a forward, that is, its target and a both come from this function's parameters and the
// index holds the callForwards rule for it, the event is accounted to the parameter a comes from:
// what such a call does with a is decided at each call of this function, by that rule.
func (g *graph) forwardedEscape(ins ssa.Instruction, cc *ssa.CallCommon, slot int, a ssa.Value, desc string) event {
	e := ev(evEscape, ins, desc)
	ci := g.st.contracts
	if ci == nil {
		return e
	}
	body := canonicalBodyFn(g.fn)
	target, ok := retForwardOf(cc, body)
	if !ok {
		return e
	}
	if p := paramFlowIndex(a, body); p >= 0 && slices.Contains(ci.callForwards[body], callForwardRule{targetParam: target.targetParam, methodName: target.methodName, slot: slot, argParam: p}) {
		e.byContract = p + 1
	}
	return e
}

// handedOn is 1 + the index of the parameter a comes from when everything callee does with its
// parameter i is a forward that this call hands on to this function's own parameters: every
// callForwards rule of callee for parameter i has its target supplied by one of this function's
// parameters, and the index holds the rule that results for this function. 0 otherwise.
func (g *graph) handedOn(cc *ssa.CallCommon, callee *ssa.Function, i int, a ssa.Value) int {
	ci := g.st.contracts
	if ci == nil {
		return 0
	}
	body := canonicalBodyFn(g.fn)
	p := paramFlowIndex(a, body)
	if p < 0 {
		return 0
	}
	rules := 0
	for _, cf := range ci.callForwards[canonicalBodyFn(callee)] {
		if cf.argParam != i {
			continue
		}
		rules++
		if cf.targetParam >= len(cc.Args) {
			return 0
		}
		outer := paramFlowIndex(cc.Args[cf.targetParam], body)
		if outer < 0 || !slices.Contains(ci.callForwards[body], callForwardRule{targetParam: outer, methodName: cf.methodName, slot: cf.slot, argParam: p}) {
			return 0
		}
	}
	if rules == 0 {
		return 0
	}
	return p + 1
}

func (g *graph) transferFull(ins ssa.Instruction, cb *ssa.BasicBlock, v ssa.Value, opPos token.Pos, index int, what string, freezesToImm bool, byContract int) {
	g.transfers = append(g.transfers, transfer{
		instr:        ins,
		caseBlock:    cb,
		v:            v,
		pos:          ins.Pos(),
		what:         what,
		opPos:        opPos,
		index:        index,
		freezesToImm: freezesToImm,
	})
	e := ev(evTransfer, ins, what)
	e.byContract = byContract
	g.add(g.val(v), e)
}

// transferHeld records that ins publishes the value of the variable at cell: a closure called at
// ins captured the variable and publishes it. cell is the variable's address as this function
// has it, its own or one it captured itself.
func (g *graph) transferHeld(ins ssa.Instruction, cell ssa.Value, what string, freezesToImm bool) {
	cn := g.val(cell)
	if cn == nil {
		return
	}
	g.transfers = append(g.transfers, transfer{
		instr:        ins,
		v:            cell,
		pos:          ins.Pos(),
		what:         what,
		index:        -1,
		freezesToImm: freezesToImm,
		held:         true,
	})
	e := ev(evTransfer, ins, what)
	e.byContract = g.publishedHeld(cell)
	g.add(g.pointee(cn), e)
}

// publishedHeld is 1 + the index of a parameter that the variable at cell may hold and that the
// index records this function to publish, or 0. What a closure's publish of the variable does to
// the parameter is then accounted to the parameter's contract (event.byContract), as it is where
// the parameter itself is published.
func (g *graph) publishedHeld(cell ssa.Value) int {
	alloc, ok := cell.(*ssa.Alloc)
	if !ok || g.st.contracts == nil {
		return 0
	}
	body := canonicalBodyFn(g.fn)
	for _, p := range parametersHeld(alloc, body) {
		if slices.ContainsFunc(g.st.contracts.directPublish[body], func(s pubArgSpec) bool { return s.argIdx == p }) {
			return p + 1
		}
	}
	return 0
}

func selectCaseBlock(sel *ssa.Select, stateIdx int) *ssa.BasicBlock {
	var extract *ssa.Extract
	for _, r := range *sel.Referrers() {
		if ext, ok := r.(*ssa.Extract); ok && ext.Index == 0 {
			extract = ext
			break
		}
	}
	if extract == nil {
		return nil
	}
	for _, r := range *extract.Referrers() {
		bin, ok := r.(*ssa.BinOp)
		if !ok || bin.Op != token.EQL {
			continue
		}
		c, ok := bin.Y.(*ssa.Const)
		if !ok || c.IsNil() || c.Int64() != int64(stateIdx) {
			c, ok = bin.X.(*ssa.Const)
			if !ok || c.IsNil() || c.Int64() != int64(stateIdx) {
				continue
			}
		}
		for _, ir := range *bin.Referrers() {
			if ifIns, ok := ir.(*ssa.If); ok {
				b := ifIns.Block()
				if len(b.Succs) > 0 {
					return b.Succs[0]
				}
			}
		}
	}
	return nil
}

// callTarget is one function a called value may be. mc is the closure that carries its captures,
// nil for a function that has none.
type callTarget struct {
	fn *ssa.Function
	mc *ssa.MakeClosure
	// entered: for a target read out of a variable, the calls by which the variable's address
	// reached each function it was passed to (cellUses.entered). A target stored there was made
	// there, and runs on that function's values (lockset.go, valuesHere).
	entered map[*ssa.Function][]*ssa.Call
}

// cellUses is how a piece of local memory is used: a local variable that lives in memory, a
// struct made with &T{}, or a field of one. It has several names. A variable is an Alloc in the
// function that declares it and a free variable in each closure that captures it. Where its
// address is passed to a function of this package it is that function's parameter, and what that
// function returns of it is the call's result. Where two pointers meet on a branch it is the phi,
// and where its address is kept in a pointer variable it is each load of that variable. Whoever
// asks what the memory may hold has to read the stores through all of them. Reading the Alloc's
// alone takes it at what its own function stores to it.
type cellUses struct {
	// stores are the stores to the memory, through any of its names.
	stores []*ssa.Store
	// loads are the reads of the whole of it.
	loads []*ssa.UnOp
	// fields, elems and slices are the addresses taken of a part of it: a field of a struct, an
	// element of an array, a slice of an array. What is written through them is written to that
	// part, and what becomes of each is for whoever asks about that part to read (usesOfAddress
	// again, on the part's address).
	fields []*ssa.FieldAddr
	elems  []*ssa.IndexAddr
	slices []*ssa.Slice
	// open says an address of the whole of it goes somewhere that is not read here: into a
	// package variable, a field, a slice, a map, a channel or an interface, out of the function
	// that made it, to a function that has no body in sight or is not known, or it has more
	// names than cellViewLimit. Something that is not in stores may then write the memory.
	open bool
	// entered are the calls by which an address of the memory reached each function it was
	// passed to. A store such a function makes is made in a call that one of them made.
	entered map[*ssa.Function][]*ssa.Call
}

// cellViewLimit is how many names of one piece of memory usesOfAddress reads. Past it the memory
// is taken as open.
const cellViewLimit = 64

// cellWalkBudget is how many names usesOfAddress reads in all, in its own walk and in the walks of
// the pointer variables the address is kept in (keptIn), each of which starts with sets of its own.
// Layers of pointer variables that each hold the addresses of several below made those walks
// exponential in the layers. Past it the memory is taken as open.
const cellWalkBudget = 16 * cellViewLimit

// usesOfCell reads every use of the local variable alloc.
func usesOfCell(alloc *ssa.Alloc) cellUses {
	return usesOfAddress(alloc)
}

// usesOfAddress reads every use of the memory at addr (an Alloc, or the address of a field of
// one) through every name it is given in this package.
//
// A call is followed into the callee when the callee is one function with a body: a function or
// a method called by name, a closure called where it is made, a method value. Any other call
// leaves the memory open.
func usesOfAddress(addr ssa.Value) cellUses {
	budget := cellWalkBudget
	w := &addressWalk{
		seen:     map[ssa.Value]bool{addr: true},
		entered:  map[*ssa.Function][]*ssa.Call{},
		captured: map[*ssa.Function]bool{},
		reads:    map[*ssa.Function]bool{},
		returns:  map[*ssa.Function][]int{},
		kept:     map[*ssa.Alloc]bool{},
		holding:  map[*ssa.Alloc]bool{},
		budget:   &budget,
	}
	if alloc, ok := addr.(*ssa.Alloc); ok {
		w.holding[alloc] = true
	}
	w.readName(addr, addr.Referrers())
	w.u.entered = w.entered
	return w.u
}

type addressWalk struct {
	u    cellUses
	seen map[ssa.Value]bool
	// entered are the calls through which the address reached each function. A function that
	// returns the address returns it to the calls it was entered by, unless it has the address
	// some other way as well: as a free variable (captured), or by reading a pointer variable
	// that holds it (reads). Such a function can hand it to any call of it, and those are not
	// all read.
	entered  map[*ssa.Function][]*ssa.Call
	captured map[*ssa.Function]bool
	reads    map[*ssa.Function]bool
	returns  map[*ssa.Function][]int
	// kept are the pointer variables the address was found stored in, each read once. holding are
	// the variables whose uses are being read, here and in the walks that asked for this one: a
	// variable reached again while it is being read is a cycle, and the memory is open.
	kept    map[*ssa.Alloc]bool
	holding map[*ssa.Alloc]bool
	// budget is the names left to read, shared with the walks this one asks for (cellWalkBudget).
	budget *int
}

// anotherName reads one more name of the same memory, once.
func (w *addressWalk) anotherName(v ssa.Value, refs *[]ssa.Instruction) {
	if w.seen[v] {
		return
	}
	if len(w.seen) >= cellViewLimit || !w.spend() {
		w.u.open = true
		return
	}
	w.seen[v] = true
	w.readName(v, refs)
}

// spend takes one name from the budget, or says that it is used up.
func (w *addressWalk) spend() bool {
	if *w.budget <= 0 {
		return false
	}
	*w.budget--
	return true
}

// handsBack says that fn was taken to return the address only to the calls it was entered by
// (handedBack), which holds only while fn has the address no other way. Learning later that it
// has, by capturing it or by reading a pointer variable that holds it, gives the answer handedBack
// would have given had that been known first: the memory is open. Without this the answer
// depended on the order of the statements.
func (w *addressWalk) handsBack(fn *ssa.Function) bool {
	return len(w.returns[fn]) > 0
}

// readName reads the uses of one name of the memory.
func (w *addressWalk) readName(addr ssa.Value, refs *[]ssa.Instruction) {
	if refs == nil {
		w.u.open = true
		return
	}
	for _, ref := range *refs {
		switch r := ref.(type) {
		case *ssa.Store:
			if r.Addr == addr {
				w.u.stores = append(w.u.stores, r)
				continue
			}
			w.keptIn(r.Addr)
		case *ssa.UnOp:
			w.u.loads = append(w.u.loads, r)
		case *ssa.DebugRef, *ssa.BinOp:
			// A comparison of the address reads nothing through it.
		case *ssa.FieldAddr:
			w.u.fields = append(w.u.fields, r)
		case *ssa.IndexAddr:
			w.u.elems = append(w.u.elems, r)
		case *ssa.Slice:
			w.u.slices = append(w.u.slices, r)
		case *ssa.Phi:
			w.anotherName(r, r.Referrers())
		case *ssa.ChangeType:
			w.anotherName(r, r.Referrers())
		case *ssa.MakeClosure:
			fn, ok := r.Fn.(*ssa.Function)
			if !ok {
				w.u.open = true
				continue
			}
			for j, b := range r.Bindings {
				if b == addr && j < len(fn.FreeVars) {
					w.captured[fn] = true
					if w.handsBack(fn) {
						w.u.open = true
					}
					w.anotherName(fn.FreeVars[j], fn.FreeVars[j].Referrers())
				}
			}
		case ssa.CallInstruction:
			w.passedTo(addr, r)
		case *ssa.Return:
			w.handedBack(addr, r)
		default:
			w.u.open = true // converted, sent, put in an interface: something else may write it
		}
	}
}

// passedTo follows the address into the one function a call runs.
func (w *addressWalk) passedTo(addr ssa.Value, ins ssa.CallInstruction) {
	cc := ins.Common()
	var body *ssa.Function
	if !cc.IsInvoke() {
		body = canonicalBodyFn(cc.StaticCallee())
	}
	if body == nil || len(body.Blocks) == 0 {
		w.u.open = true // handed to something whose body is not in sight
		return
	}
	if !slices.Contains(cc.Args, addr) {
		w.u.open = true // used by the call in some other way than as an argument
		return
	}
	if call, ok := ins.(*ssa.Call); ok && !slices.Contains(w.entered[body], call) {
		w.entered[body] = append(w.entered[body], call)
		for _, idx := range w.returns[body] {
			w.resultOf(call, idx)
		}
	}
	for i, a := range cc.Args {
		if a != addr {
			continue
		}
		if i >= len(body.Params) {
			w.u.open = true
			continue
		}
		w.anotherName(body.Params[i], body.Params[i].Referrers())
	}
}

// handedBack takes an address a function returns back to the calls it was entered by. The function
// that made the memory, a closure that captures it and a function that reads it out of a pointer
// variable return it to callers that are not read.
func (w *addressWalk) handedBack(addr ssa.Value, ret *ssa.Return) {
	fn := canonicalBodyFn(ret.Parent())
	if fn == nil || w.captured[fn] || w.reads[fn] || len(w.entered[fn]) == 0 {
		w.u.open = true
		return
	}
	for idx, res := range ret.Results {
		if res != addr || slices.Contains(w.returns[fn], idx) {
			continue
		}
		w.returns[fn] = append(w.returns[fn], idx)
		for _, call := range w.entered[fn] {
			w.resultOf(call, idx)
		}
	}
}

// resultOf reads result idx of a call as one more name of the memory.
func (w *addressWalk) resultOf(call *ssa.Call, idx int) {
	if call.Call.Signature().Results().Len() == 1 {
		w.anotherName(call, call.Referrers())
		return
	}
	if call.Referrers() == nil {
		w.u.open = true
		return
	}
	for _, ref := range *call.Referrers() {
		if ext, ok := ref.(*ssa.Extract); ok && ext.Index == idx {
			w.anotherName(ext, ext.Referrers())
		}
	}
}

// keptIn reads an address stored in a pointer variable: each load of that variable is one more
// name of the memory, as long as nothing out of sight reads the variable. Stored anywhere else,
// the memory is open.
func (w *addressWalk) keptIn(holder ssa.Value) {
	var cell *ssa.Alloc
	switch h := holder.(type) {
	case *ssa.Alloc:
		cell = h
	case *ssa.FreeVar:
		cell = cellOfFreeVar(h)
	}
	if cell == nil {
		w.u.open = true
		return
	}
	if w.kept[cell] {
		return
	}
	w.kept[cell] = true
	if w.holding[cell] {
		w.u.open = true // a variable that is being read holds, through others, its own address
		return
	}
	if !w.spend() {
		w.u.open = true
		return
	}
	hw := &addressWalk{
		seen:     map[ssa.Value]bool{cell: true},
		entered:  map[*ssa.Function][]*ssa.Call{},
		captured: map[*ssa.Function]bool{},
		reads:    map[*ssa.Function]bool{},
		returns:  map[*ssa.Function][]int{},
		kept:     map[*ssa.Alloc]bool{},
		holding:  w.holding,
		budget:   w.budget,
	}
	w.holding[cell] = true
	hw.readName(cell, cell.Referrers())
	delete(w.holding, cell)
	if hw.u.open || len(hw.u.fields)+len(hw.u.elems)+len(hw.u.slices) > 0 {
		w.u.open = true // the variable is read by something that is not in sight
		return
	}
	for _, load := range hw.u.loads {
		fn := canonicalBodyFn(load.Parent())
		w.reads[fn] = true
		if w.handsBack(fn) {
			w.u.open = true
		}
	}
	for _, load := range hw.u.loads {
		w.anotherName(load, load.Referrers())
	}
}

// cellOfFreeVar is the local variable a closure's free variable stands for: what the closure, or
// the closure around it, was made with.
func cellOfFreeVar(fv *ssa.FreeVar) *ssa.Alloc {
	for {
		fn := fv.Parent()
		if fn == nil || fn.Parent() == nil {
			return nil
		}
		idx := slices.Index(fn.FreeVars, fv)
		var bound ssa.Value
		forEachInstr(fn.Parent(), func(ins ssa.Instruction) {
			if mc, ok := ins.(*ssa.MakeClosure); ok && mc.Fn == ssa.Value(fn) && idx >= 0 && idx < len(mc.Bindings) {
				bound = mc.Bindings[idx]
			}
		})
		switch b := bound.(type) {
		case *ssa.Alloc:
			return b
		case *ssa.FreeVar:
			fv = b
		default:
			return nil
		}
	}
}

// callTargets lists the functions v may be when it is called: v itself, and what reaches it
// through conversions, phi edges and the stores to a local cell, in this function and in the
// closures that capture the cell. rest is what it could not resolve to a function: a value of
// another kind on some path (a parameter, a field, a call's result), or the load of a cell that
// can be written by something other than the stores seen (usesOfCell: its address goes somewhere
// that is not read). The targets are all v may be when rest is empty. The stores to a cell are
// taken together, whatever their order, and they include the stores a closure that captures the
// cell makes and the stores a function makes that is passed its address. A cell that is called
// holds a function, so no address of a part of it is ever taken.
//
// What a caller takes from the list depends on what it asks. A fact that holds if any target does
// it (releases a lock, publishes, writes) can be taken from each target. A fact that must hold
// whichever target runs (a lock is held afterwards) needs every target to have it and rest to be
// empty. What the call hands over is handed to whatever is in rest as well.
func callTargets(v ssa.Value) (targets []callTarget, rest []ssa.Value) {
	seen := map[ssa.Value]bool{}
	inRest := map[ssa.Value]bool{}
	unresolved := func(v ssa.Value) {
		if !inRest[v] {
			inRest[v] = true
			rest = append(rest, v)
		}
	}
	// entered is what the variables on the way to the value being walked say of the calls
	// their addresses went in by.
	var entered map[*ssa.Function][]*ssa.Call
	var walk func(v ssa.Value)
	walk = func(v ssa.Value) {
		if v == nil || seen[v] {
			return
		}
		seen[v] = true
		switch val := v.(type) {
		case *ssa.Function:
			targets = append(targets, callTarget{fn: val, entered: entered})
		case *ssa.MakeClosure:
			fn, ok := val.Fn.(*ssa.Function)
			if !ok {
				unresolved(v)
				return
			}
			targets = append(targets, callTarget{fn: fn, mc: val, entered: entered})
		case *ssa.ChangeType:
			walk(val.X)
		case *ssa.Phi:
			if len(val.Edges) == 0 {
				unresolved(v)
			}
			for _, edge := range val.Edges {
				walk(edge)
			}
		case *ssa.UnOp:
			alloc, ok := val.X.(*ssa.Alloc)
			if !ok || val.Op != token.MUL || alloc.Referrers() == nil {
				unresolved(v)
				return
			}
			uses := usesOfCell(alloc)
			if uses.open || len(uses.stores) == 0 {
				unresolved(v)
			}
			outer := entered
			if len(uses.entered) > 0 {
				entered = maps.Clone(uses.entered)
				for fn, calls := range outer {
					entered[fn] = append(slices.Clone(calls), entered[fn]...)
				}
			}
			for _, st := range uses.stores {
				walk(st.Val)
			}
			entered = outer
		default:
			unresolved(v)
		}
	}
	walk(v)
	return targets, rest
}

// closureCandidates lists every closure v may be: the closures among callTargets. It is for a
// caller that asks what any one of them may do; what else v may be is callTargets' second answer.
func closureCandidates(v ssa.Value) []*ssa.MakeClosure {
	var out []*ssa.MakeClosure
	targets, _ := callTargets(v)
	for _, t := range targets {
		if t.mc != nil {
			out = append(out, t.mc)
		}
	}
	return out
}

// closureRetains records what a closure's body retains of its captures (its summary's freeLeaks) as
// escapes at the closure's creation, unless every use of the closure is one whose call site already
// accounts for the body: a static call or defer of the closure itself (call applies freeLeaks there),
// or a transfer to another goroutine, where the closure owns its captures and may move them on. A
// closure passed to any other function may be called from there, through a function value whose
// summary cannot say what the closure keeps, so the retention is charged where the closure is made.
//
// A bound method value's body is the method: its receiver is retained if the method leaks it, and
// always for an interface method, whose implementation gosree cannot see.
func (g *graph) closureRetains(mc *ssa.MakeClosure) {
	if g.closureUsedOnlyAtCallSites(mc, mc) {
		return
	}
	fn, _ := mc.Fn.(*ssa.Function)
	leaks := func(j int) bool { return true }
	if fn != nil {
		if strings.HasPrefix(fn.Synthetic, "bound method wrapper") {
			if methodBehind(fn) != nil {
				leaks = g.st.summary(fn).freeLeaks
			}
		} else {
			s := g.st.summary(fn)
			leaks = s.freeLeaks
		}
	}
	for j, b := range mc.Bindings {
		if bn := g.val(b); bn != nil && leaks(j) {
			g.add(bn, ev(evEscape, mc, "captured by a closure that may retain or share it"))
		}
	}
}

// closureUsedOnlyAtCallSites reports whether every use of v (a closure, or a conversion of one) is a
// static call or defer of mc itself, a go statement, or an argument a spawner runs on another
// goroutine or an owned parameter takes. Conversions are followed to their uses however many there
// are (a conversion's result is a new value, so none comes round twice); past the fifth the answer
// used to be no, which charged a closure started with go as if it had been stored.
func (g *graph) closureUsedOnlyAtCallSites(mc *ssa.MakeClosure, v ssa.Value) bool {
	refs := v.Referrers()
	if refs == nil {
		return false
	}
	for _, r := range *refs {
		switch r := r.(type) {
		case *ssa.ChangeType:
			if !g.closureUsedOnlyAtCallSites(mc, r) {
				return false
			}
		case *ssa.MakeInterface:
			if !g.closureUsedOnlyAtCallSites(mc, r) {
				return false
			}
		case *ssa.Go:
			// The function a go statement starts, or an argument passed to it: a transfer either way.
		case *ssa.Return:
			// A returned closure reaches the caller through the result, which the summary links to
			// the captures; charging an escape as well would make every closure factory
			// (http.StripPrefix, sync.OnceFunc) leak its arguments and return a value that is not
			// fresh. What the body keeps when the caller later calls it through a function value is
			// not charged anywhere: the hole this function closes for arguments stays open for
			// results.
		case ssa.CallInstruction:
			cc := r.Common()
			if cc.Value == v && v == mc {
				continue
			}
			if sp, ok := spawnOf(r); ok && sp.fn == v {
				continue
			}
			if !g.isIsoArg(cc, v) {
				return false
			}
		case *ssa.DebugRef:
		default:
			return false
		}
	}
	return true
}

// isIsoArg reports whether v is passed to a //gosree:iso parameter of the static callee, which owns it.
func (g *graph) isIsoArg(cc *ssa.CallCommon, v ssa.Value) bool {
	fn := cc.StaticCallee()
	if fn == nil {
		return false
	}
	sum := g.st.summary(fn)
	for i, a := range cc.Args {
		if a == v && sum.isIso(i) {
			return true
		}
	}
	return false
}

func (g *graph) goStmt(ins *ssa.Go) {
	cc := ins.Common()
	switch v := cc.Value.(type) {
	case *ssa.Function, *ssa.Builtin:
	case *ssa.MakeClosure:
		g.transferWithOp(ins, v, token.NoPos, -1, "captured by a goroutine")
	default:
		// One closure stands for all the value may be: a phi or a cell joins what it may hold
		// into one class, and the transfer is of the class (TestGoOfVariableThatMayBeTwoClosures).
		if cands := closureCandidates(v); len(cands) > 0 {
			g.transferWithOp(ins, cands[0], token.NoPos, -1, "captured by a goroutine")
		} else {
			what := "started as a goroutine"
			if cc.IsInvoke() {
				what = "receiver of a method started as a goroutine"
			}
			g.transferWithOp(ins, v, token.NoPos, -1, what)
		}
	}
	for i, a := range cc.Args {
		g.transferWithOp(ins, a, token.NoPos, i, "passed to a goroutine")
		g.written(ins, a, "passed to a goroutine, which may write it")
	}
	g.writtenCaptures(ins, cc.Value, "captured by a goroutine, which may write it")
}

func (g *graph) applyCallContract(ins ssa.Instruction, cc *ssa.CallCommon) (map[int]bool, resolvedCallContract, bool) {
	rc, ok, cut := g.st.resolveCallContractCut(g.fn, cc)
	if cut && g.couldPublish(cc, rc) {
		g.cutCalls = append(g.cutCalls, ins)
	}
	if !ok {
		return nil, resolvedCallContract{}, false
	}
	var published map[int]bool
	if len(rc.publishArgs) > 0 {
		published = make(map[int]bool, len(rc.publishArgs))
		for _, spec := range rc.publishArgs {
			idx := spec.argIdx
			if idx < 0 || idx >= len(cc.Args) || spec.delegatedToCallers {
				continue
			}
			published[idx] = true
			payload := unwrapPublishedArg(cc.Args[idx])
			if payload == nil {
				continue
			}
			if _, isConst := payload.(*ssa.Const); isConst || g.st.cls.Shareable(payload.Type()) {
				continue
			}
			g.transferPublish(ins, payload, idx, spec.what, spec.freezesToImm)
			g.writtenCaptures(ins, payload, "captured by a closure "+spec.what+", which may write it")
		}
	}
	return published, rc, true
}

// couldPublish reports whether cc passes an argument that a callee could publish and that rc, the
// contract of the callees found, does not already publish: a value that is not a constant and not
// of a type that is safe to share. A call of an interface method can publish only if a method that
// could stand behind it does (capMethodFor).
func (g *graph) couldPublish(cc *ssa.CallCommon, rc resolvedCallContract) bool {
	if cc.IsInvoke() && !g.st.capMethodFor(cc.Value.Type(), cc.Method.Name()).publishes {
		return false
	}
	for i, a := range cc.Args {
		if slices.ContainsFunc(rc.publishArgs, func(s pubArgSpec) bool { return s.argIdx == i && !s.delegatedToCallers }) {
			continue
		}
		payload := unwrapPublishedArg(a)
		if payload == nil {
			continue
		}
		if _, isConst := payload.(*ssa.Const); isConst || g.st.cls.Shareable(payload.Type()) {
			continue
		}
		return true
	}
	return false
}

func (g *graph) call(ins ssa.Instruction, cc *ssa.CallCommon, result ssa.Value) {
	if cc.IsInvoke() {
		published, rc, hasContract := g.applyCallContract(ins, cc)
		desc := "passed to interface method " + cc.Method.Name() + " (dynamic dispatch may retain it)"
		wdesc := "passed to interface method " + cc.Method.Name() + ", which may write it"
		g.add(g.val(cc.Value), ev(evEscape, ins, desc))
		g.written(ins, cc.Value, wdesc)
		for i, a := range cc.Args {
			switch {
			case !published[i]:
				g.add(g.val(a), g.forwardedEscape(ins, cc, i, a, desc))
			case rc.alsoEscapes(i):
				// Published, and a callee does something else with it as well.
				g.add(g.val(a), ev(evEscape, ins, desc))
			}
			if !hasContract || !rc.suppressWrite {
				g.written(ins, a, wdesc)
			}
		}
		if result != nil && (!hasContract || !rc.freshResult) {
			g.add(g.val(result), ev(evForeign, ins, "result of interface method "+cc.Method.Name()))
		}
		return
	}
	if b, ok := cc.Value.(*ssa.Builtin); ok {
		g.builtin(ins, b, cc.Args, result)
		return
	}
	fn := cc.StaticCallee()
	if fn == nil {
		if mc := rangeOverFuncClosure(g.fn, ins, cc); mc != nil {
			yieldSum := g.st.summary(mc.Fn.(*ssa.Function))
			for j, b := range mc.Bindings {
				if yieldSum.freeLeaks(j) {
					g.add(g.val(b), ev(evEscape, ins, "captured by a range-over-func loop body that may retain or share it"))
				}
				if yieldSum.writesFree(j) {
					g.written(ins, b, "captured by a range-over-func loop body that may write it")
				}
			}
			g.written(ins, cc.Value, "called dynamically; its captures may be written")
			return
		}
		published, rc, hasContract := g.applyCallContract(ins, cc)
		for i, a := range cc.Args {
			switch {
			case !published[i]:
				g.add(g.val(a), g.forwardedEscape(ins, cc, i, a, "passed to a dynamic function call"))
			case rc.alsoEscapes(i):
				// Published, and a callee does something else with it as well.
				g.add(g.val(a), ev(evEscape, ins, "passed to a dynamic function call"))
			}
			if !hasContract || !rc.suppressWrite {
				g.written(ins, a, "passed to a dynamic function call, which may write it")
			}
		}
		g.written(ins, cc.Value, "called dynamically; its captures may be written")
		if result != nil && (!hasContract || !rc.freshResult) {
			g.add(g.val(result), ev(evForeign, ins, "result of a dynamic function call"))
		}
		return
	}
	if isCapMove(fn) && len(cc.Args) > 0 {
		g.transferWithOp(ins, cc.Args[0], token.NoPos, 0, "moved by cap.Move")
		if result != nil {
			g.val(result)
		}
		return
	}
	if isCapBorrowHelper(fn) && len(cc.Args) > 0 {
		if result != nil {
			g.join(result, cc.Args[0])
		}
		return
	}
	// A callback the callee runs on another goroutine moves there, like a go statement's function.
	// Its argument is then a transfer and not also an escape into the callee.
	spawned := -1
	if sp, ok := spawnOf(ins); ok && sp.via != "" {
		spawned = sp.arg
		g.transfer(ins, sp.fn, sp.what(sp.fn))
		g.writtenCaptures(ins, sp.fn, "captured by a callback started by "+sp.via+", which may write it")
	}
	published, rc, hasContract := g.applyCallContract(ins, cc)
	sum := g.st.summary(fn)
	name := calleeName(fn)
	note := sum.unavailable()
	g.link(cc, sum, result, spawned)
	for i, a := range cc.Args {
		an := g.val(a)
		if an == nil || i == spawned {
			continue
		}
		switch {
		case published[i]:
			// The contract stands for the publish. Whatever else the callee does with the
			// argument is still an escape: something its body does besides, or a callable it
			// hands the argument to that may keep it (resolvedCallContract.escapeArgs).
			if rc.alsoEscapes(i) {
				g.add(an, ev(evEscape, ins, "passed to "+name+", which publishes it and may also retain or share it"+note))
			}
		case sum.isIso(i):
			g.transferWithOp(ins, a, token.NoPos, i, fmt.Sprintf("passed to owned parameter %d of %s", i, name))
			g.writtenCaptures(ins, a, "captured by a closure passed to "+name+", which may write it")
		case sum.spawns(i):
			what := "started on a goroutine by " + name
			if _, closure := a.(*ssa.MakeClosure); closure {
				what = "captured by a goroutine started by " + name
			}
			g.transferWithOp(ins, a, token.NoPos, i, what)
			g.writtenCaptures(ins, a, "captured by a callback started by "+name+", which may write it")
		case sum.leaks(i):
			e := ev(evEscape, ins, "passed to "+name+", which may retain or share it"+note)
			if !sum.leaksBesidesContract(i) {
				e.byContract = g.handedOn(cc, fn, i, a)
			}
			g.add(an, e)
		}
		if sum.writes(i) && (!hasContract || !rc.suppressWrite) {
			g.written(ins, a, "passed to "+name+", which may write it"+note)
		}
	}
	if mc, ok := cc.Value.(*ssa.MakeClosure); ok {
		// What the closure publishes of what it captured is published here, by this call, and the
		// publish stands for what the closure does with that variable (what else it does with
		// it is reported in the closure, where the publish is). A deferred closure runs when
		// the function returns, not here: it stays a closure that may keep its captures, and
		// what it publishes is still accounted to the parameter the variable holds.
		var publishes []pubArgSpec
		if g.st.contracts != nil {
			publishes = g.st.contracts.freePublish[canonicalBodyFn(fn)]
		}
		_, deferred := ins.(*ssa.Defer)
		for j, b := range mc.Bindings {
			published := false
			for _, spec := range publishes {
				if spec.argIdx == j && !deferred {
					g.transferHeld(ins, b, spec.what, spec.freezesToImm)
					published = true
				}
			}
			if sum.freeLeaks(j) && !published {
				e := ev(evEscape, ins, "captured by a closure that may retain or share it")
				if slices.ContainsFunc(publishes, func(s pubArgSpec) bool { return s.argIdx == j }) {
					e.byContract = g.publishedHeld(b)
				}
				g.add(g.val(b), e)
			}
			if sum.writesFree(j) {
				g.written(ins, b, "captured by a closure that may write it")
			}
		}
	}
	if result != nil {
		if cc.Signature().Results().Len() > 1 && result.Referrers() != nil {
			for _, r := range *result.Referrers() {
				if ext, ok := r.(*ssa.Extract); ok && ext.Tuple == result && !sum.isFreshResult(ext.Index) && !(ext.Index == 0 && hasContract && rc.freshResult) {
					g.add(g.val(ext), ev(evForeign, ins, "result of "+name+" (not known to return a fresh value)"+note))
				}
			}
		} else if !sum.fresh && (!hasContract || !rc.freshResult) {
			g.add(g.val(result), ev(evForeign, ins, "result of "+name+" (not known to return a fresh value)"+note))
		}
	}
}

func rangeOverFuncClosure(parent *ssa.Function, ins ssa.Instruction, cc *ssa.CallCommon) *ssa.MakeClosure {
	if len(cc.Args) != 1 {
		return nil
	}
	mc, ok := cc.Args[0].(*ssa.MakeClosure)
	if !ok {
		return nil
	}
	yieldFn, ok := mc.Fn.(*ssa.Function)
	if !ok || yieldFn == nil || yieldFn.Signature == nil {
		return nil
	}
	if yieldFn.Signature.Results().Len() != 1 || yieldFn.Signature.Results().At(0).Type().Underlying() != types.Typ[types.Bool] {
		return nil
	}
	if yieldFn.Signature.Params().Len() > 2 {
		return nil
	}
	if yieldFn.Synthetic == "range-over-func" || strings.Contains(yieldFn.Name(), "$range") {
		return mc
	}
	if _, isRange := yieldFn.Syntax().(*ast.RangeStmt); isRange {
		return mc
	}
	if parent != nil && parent.Syntax() != nil {
		found := false
		ast.Inspect(parent.Syntax(), func(n ast.Node) bool {
			if found || n == nil {
				return false
			}
			if rng, ok := n.(*ast.RangeStmt); ok {
				if ins.Pos() >= rng.Pos() && ins.Pos() <= rng.End() {
					found = true
					return false
				}
			}
			return true
		})
		if found {
			return mc
		}
	}
	return nil
}

func extractOfResult(result ssa.Value, idx int) ssa.Value {
	if result == nil || result.Referrers() == nil {
		return nil
	}
	for _, r := range *result.Referrers() {
		if ext, ok := r.(*ssa.Extract); ok && ext.Tuple == result && ext.Index == idx {
			return ext
		}
	}
	return nil
}

// link joins the memory of the arguments and the result that the callee links (summary.links): the
// callee stored one into another, or returns memory of one. Each link joins the classes at the depths
// where the callee's own graph joined them. A callback the callee runs on another goroutine (argument
// skip) is a transfer, not memory joined to the others: a spawner that keeps its callback (a ServeMux
// storing a handler) owns that link.
func (g *graph) link(cc *ssa.CallCommon, sum *summary, result ssa.Value, skip int) {
	n := len(cc.Args) + cc.Signature().Results().Len()
	at := func(pos, depth int) *node {
		var v ssa.Value
		switch {
		case pos == skip || pos >= n || (pos < len(cc.Args) && sum.spawns(pos)):
			return nil
		case pos < len(cc.Args):
			v = cc.Args[pos]
		default:
			if cc.Signature().Results().Len() > 1 {
				v = extractOfResult(result, pos-len(cc.Args))
			} else {
				v = result
			}
		}
		if v == nil {
			return nil
		}
		c := g.val(v)
		for i := 0; c != nil && i < depth; i++ {
			c = g.pointee(c)
		}
		return c
	}
	for _, l := range sum.links {
		a, b := at(l.A, l.DepthA), at(l.B, l.DepthB)
		if a != nil && b != nil {
			g.union(a, b)
		}
	}
}

// meetDepth returns how many pointers below a and below b their pointee chains first meet, if they
// do. Two classes whose regions hold each other's events always meet: one reaches the other's
// memory, or a container of it.
func (g *graph) meetDepth(a, b *node) (int, int, bool) {
	at := map[*node]int{}
	for i, c := range g.pointsTo(a) {
		at[c] = i
	}
	for j, c := range g.pointsTo(b) {
		if i, ok := at[c]; ok {
			return i, j, true
		}
	}
	return 0, 0, false
}

func (g *graph) builtin(ins ssa.Instruction, b *ssa.Builtin, args []ssa.Value, result ssa.Value) {
	switch b.Name() {
	case "append":
		if result == nil {
			return
		}
		for _, a := range args {
			g.join(result, a)
		}
	case "copy":
		// copy moves elements; it only creates aliasing when the elements themselves hold pointers.
		if len(args) == 2 && !g.st.cls.Shareable(elemType(args[0].Type())) {
			dn, sn := g.val(args[0]), g.val(args[1])
			if dn != nil && sn != nil {
				g.union(g.pointee(dn), g.pointee(sn))
			}
		}
	case "recover":
		if result != nil {
			g.add(g.val(result), ev(evForeign, ins, "recovered panic value"))
		}
	case "ssa:wrapnilchk":
		if result != nil && len(args) > 0 {
			g.join(result, args[0])
		}
	}
}

func elemType(t types.Type) types.Type {
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return u.Elem()
	case *types.Pointer:
		if a, ok := u.Elem().Underlying().(*types.Array); ok {
			return a.Elem()
		}
	case *types.Array:
		return u.Elem()
	}
	return t
}

func calleeName(fn *ssa.Function) string {
	if fn.Parent() != nil {
		return "a closure in " + fn.Parent().Name()
	}
	return fn.String()
}

// pointsTo returns n's class and every class reachable from it through pointees: the memory a value
// of n can reach, and so write. It is region without the containers: because Steensgaard unification
// joins a stored pointer with the cell it is stored into, region would count a struct or variadic
// slice holding a pointer to p as part of p, and writing that container is not writing p.
func (g *graph) pointsTo(n *node) []*node {
	var out []*node
	seen := map[*node]bool{}
	for c := n.find(); c != nil && !seen[c]; {
		seen[c] = true
		out = append(out, c)
		if c.pointee == nil {
			break
		}
		c = c.pointee.find()
	}
	return out
}

// region returns every class reachable from n through pointees, plus every class that can reach one
// of those (a container holding a pointer into the region).
func (g *graph) region(n *node) map[*node]bool {
	n = n.find()
	if g.regions != nil {
		if cached, ok := g.regions[n]; ok {
			return cached
		}
	}
	g.buildRev()
	out := map[*node]bool{}
	stack := g.pointsTo(n)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[c] {
			continue
		}
		out[c] = true
		stack = append(stack, g.rev[c]...)
	}
	if g.regions != nil {
		g.regions[n] = out
	}
	return out
}

func (g *graph) buildRev() {
	if g.rev != nil {
		return
	}
	g.rev = map[*node][]*node{}
	seen := map[*node]bool{}
	for _, n := range g.nodes {
		if n == nil {
			continue
		}
		// Walk the whole pointee chain: intermediate classes need not belong to any SSA value.
		for r := n.find(); !seen[r]; {
			seen[r] = true
			if r.pointee == nil {
				break
			}
			p := r.pointee.find()
			g.rev[p] = append(g.rev[p], r)
			r = p
		}
	}
}

func isCapPkgPath(path string) bool {
	return path == "github.com/bharathvbcr/gosree/cap" || strings.HasSuffix(path, "/cap") || path == "cap"
}

func isCapMove(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		if isCapPkgPath(obj.Pkg().Path()) || obj.Pkg().Name() == "cap" {
			if obj.Name() == "Move" {
				return true
			}
		}
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		p := fn.Pkg.Pkg.Path()
		if isCapPkgPath(p) || fn.Pkg.Pkg.Name() == "cap" {
			name := fn.Name()
			if name == "Move" || strings.HasPrefix(name, "Move[") {
				return true
			}
		}
	}
	s := fn.String()
	return strings.Contains(s, "/cap.Move") || strings.HasPrefix(s, "cap.Move")
}

func isCapBorrowHelper(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		if isCapPkgPath(obj.Pkg().Path()) || obj.Pkg().Name() == "cap" {
			name := obj.Name()
			if name == "BorrowMut" || name == "BorrowMub" || name == "Borrow" || name == "BorrowRef" || name == "BorrowRob" {
				return true
			}
		}
	}
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		p := fn.Pkg.Pkg.Path()
		if isCapPkgPath(p) || fn.Pkg.Pkg.Name() == "cap" {
			name := fn.Name()
			if name == "BorrowMut" || strings.HasPrefix(name, "BorrowMut[") ||
				name == "BorrowMub" || strings.HasPrefix(name, "BorrowMub[") ||
				name == "Borrow" || strings.HasPrefix(name, "Borrow[") ||
				name == "BorrowRef" || strings.HasPrefix(name, "BorrowRef[") ||
				name == "BorrowRob" || strings.HasPrefix(name, "BorrowRob[") {
				return true
			}
		}
	}
	s := fn.String()
	return strings.Contains(s, "/cap.Borrow") || strings.HasPrefix(s, "cap.Borrow")
}
