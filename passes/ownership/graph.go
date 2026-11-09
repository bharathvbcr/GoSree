package ownership

import (
	"fmt"
	"go/token"
	"go/types"

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
)

type event struct {
	kind  eventKind
	instr ssa.Instruction // nil for parameters and free variables
	pos   token.Pos
	desc  string
	param int // index of the parameter (or -1); the parameter's own foreign event is not a leak of itself
	free  int // index of the free variable (or -1)
}

type node struct {
	parent  *node
	pointee *node
	events  []event
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
	instr ssa.Instruction
	v     ssa.Value
	pos   token.Pos
	what  string
}

type graph struct {
	st        *state
	fn        *ssa.Function
	nodes     map[ssa.Value]*node
	transfers []transfer
	final     map[*ssa.Alloc]bool
	ownedFree bool
	rev       map[*node][]*node
}

func (st *state) buildGraph(fn *ssa.Function) *graph {
	g := &graph{st: st, fn: fn, nodes: map[ssa.Value]*node{}}
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

func (g *graph) newNode() *node { return &node{} }

// val returns v's class, or nil when values of v's type cannot carry unchecked shared memory.
func (g *graph) val(v ssa.Value) *node {
	if n, ok := g.nodes[v]; ok {
		if n == nil {
			return nil
		}
		return n.find()
	}
	var n *node
	switch v := v.(type) {
	case *ssa.Const, *ssa.Function, *ssa.Builtin:
	case *ssa.Global:
		n = g.newNode()
		d := "package variable " + v.Name()
		n.events = append(n.events,
			event{kind: evForeign, pos: v.Pos(), desc: d, param: -1, free: -1},
			event{kind: evEscape, pos: v.Pos(), desc: "stored in " + d, param: -1, free: -1})
	case *ssa.Alloc:
		if !g.final[v] && !g.st.cls.Shareable(v.Type()) {
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
	b.parent = a
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

func (g *graph) instr(ins ssa.Instruction) {
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
	case *ssa.Phi:
		for _, e := range ins.Edges {
			g.join(ins, e)
		}
	case *ssa.FieldAddr:
		g.join(ins, ins.X)
	case *ssa.Field:
		g.join(ins, ins.X)
	case *ssa.IndexAddr:
		g.join(ins, ins.X)
	case *ssa.Index:
		g.join(ins, ins.X)
	case *ssa.Slice:
		g.join(ins, ins.X)
	case *ssa.ChangeType:
		g.join(ins, ins.X)
	case *ssa.SliceToArrayPointer:
		g.join(ins, ins.X)
	case *ssa.Range:
		g.join(ins, ins.X)
	case *ssa.Extract:
		g.join(ins, ins.Tuple)
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
		g.transfer(ins, ins.X, "sent on a channel")
	case *ssa.Select:
		for _, s := range ins.States {
			if s.Dir == types.SendOnly && s.Send != nil {
				g.transfer(ins, s.Send, "sent on a channel in select")
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

func (g *graph) transfer(ins ssa.Instruction, v ssa.Value, what string) {
	g.transfers = append(g.transfers, transfer{instr: ins, v: v, pos: ins.Pos(), what: what})
	g.add(g.val(v), ev(evTransfer, ins, what))
}

func (g *graph) goStmt(ins *ssa.Go) {
	cc := ins.Common()
	switch v := cc.Value.(type) {
	case *ssa.Function, *ssa.Builtin:
	case *ssa.MakeClosure:
		g.transfer(ins, v, "captured by a goroutine")
	default:
		what := "started as a goroutine"
		if cc.IsInvoke() {
			what = "receiver of a method started as a goroutine"
		}
		g.transfer(ins, v, what)
	}
	for _, a := range cc.Args {
		g.transfer(ins, a, "passed to a goroutine")
	}
}

func (g *graph) call(ins ssa.Instruction, cc *ssa.CallCommon, result ssa.Value) {
	if cc.IsInvoke() {
		desc := "passed to interface method " + cc.Method.Name() + " (dynamic dispatch may retain it)"
		g.add(g.val(cc.Value), ev(evEscape, ins, desc))
		for _, a := range cc.Args {
			g.add(g.val(a), ev(evEscape, ins, desc))
		}
		if result != nil {
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
		for _, a := range cc.Args {
			g.add(g.val(a), ev(evEscape, ins, "passed to a dynamic function call"))
		}
		if result != nil {
			g.add(g.val(result), ev(evForeign, ins, "result of a dynamic function call"))
		}
		return
	}
	sum := g.st.summary(fn)
	name := calleeName(fn)
	for i, a := range cc.Args {
		an := g.val(a)
		if an == nil {
			continue
		}
		switch {
		case sum.isIso(i):
			g.transfer(ins, a, fmt.Sprintf("passed to owned parameter %d of %s", i, name))
		case sum.leaks(i):
			g.add(an, ev(evEscape, ins, "passed to "+name+", which may retain or share it"))
		}
	}
	if mc, ok := cc.Value.(*ssa.MakeClosure); ok {
		for j, b := range mc.Bindings {
			if sum.freeLeaks(j) {
				g.add(g.val(b), ev(evEscape, ins, "captured by a closure that may retain or share it"))
			}
		}
	}
	if result != nil && !sum.fresh {
		g.add(g.val(result), ev(evForeign, ins, "result of "+name+" (not known to return a fresh value)"))
	}
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

// region returns every class reachable from n through pointees, plus every class that can reach one
// of those (a container holding a pointer into the region).
func (g *graph) region(n *node) map[*node]bool {
	g.buildRev()
	fwd := map[*node]bool{}
	for c := n.find(); c != nil && !fwd[c]; {
		fwd[c] = true
		if c.pointee == nil {
			break
		}
		c = c.pointee.find()
	}
	out := map[*node]bool{}
	stack := make([]*node, 0, len(fwd))
	for c := range fwd {
		stack = append(stack, c)
	}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out[c] {
			continue
		}
		out[c] = true
		stack = append(stack, g.rev[c]...)
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
