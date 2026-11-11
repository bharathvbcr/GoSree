package ownership

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// checkGlobals reports SAFE006 at the declaration of every package variable of this package that is
// written outside initialisation and is not //gosree:guarded_by. A package variable is accessed in
// place by whatever goroutine touches it, so a write after initialisation is shared mutable state
// whatever its type; sync and atomic types are written through method calls, which are not writes
// here. The writes are attached as related positions, so a reader (or the race-corpus scorer) can
// see which code makes the variable mutable. A //gosree:unsafe on the declaration accepts it.
//
// Initialisation is init, package variable initialisers, and init-only helpers: functions every
// caller of which is initialisation and whose value is never taken.
func (st *state) collectGlobalMutations(fns []*ssa.Function) {
	initOnly := st.initFuncs(fns)
	writes := map[*ssa.Global][]token.Pos{}
	mutated := map[*ssa.Global]bool{}
	for _, fn := range fns {
		if initOnly[fn] {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			if w, ok := writeOf(ins); ok {
				addr := w.addr
				g, ok := globalRoot(addr)
				if !ok || g.Pkg != st.ssa.Pkg {
					return
				}
				mutated[g] = true
				if w.grows {
					return // append changes a package variable only through the store of its result
				}
				if _, guarded := st.slotOf(addr); guarded {
					return // SAFE009 checks guarded variables and fields at every access
				}
				writes[g] = append(writes[g], posOf(ins, ins))
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			if _, isBuiltin := cc.Value.(*ssa.Builtin); isBuiltin {
				return
			}
			if cc.IsInvoke() {
				for _, v := range append([]ssa.Value{cc.Value}, cc.Args...) {
					if g, ok := st.globalMutArg(v); ok {
						mutated[g] = true
						writes[g] = append(writes[g], posOf(ins, ins))
					}
				}
				return
			}
			callee := cc.StaticCallee()
			if callee == nil {
				for _, v := range cc.Args {
					if g, ok := st.globalMutArg(v); ok {
						mutated[g] = true
						writes[g] = append(writes[g], posOf(ins, ins))
					}
				}
				return
			}
			if callee.Pkg == st.ssa.Pkg && initOnly[callee] {
				return
			}
			if callee.Pkg != nil && callee.Pkg.Pkg != nil && callee.Pkg.Pkg.Path() == "testing" && callee.Name() == "MainStart" {
				return
			}
			sum := st.summary(callee)
			for i, a := range cc.Args {
				if !sum.writes(i) {
					continue
				}
				if g, ok := st.globalMutArg(a); ok {
					mutated[g] = true
					writes[g] = append(writes[g], posOf(ins, ins))
				}
			}
		})
	}
	// In non-main packages, an unexported non-shareable package variable whose backing
	// state escapes across an exported boundary (returned from an exported function/method,
	// stored into an exported package variable or exported function parameter, or sent on a
	// channel) can be mutated concurrently by external callers and is not init-only immutable.
	if st.ssa.Pkg != nil && (st.ssa.Pkg.Pkg == nil || st.ssa.Pkg.Pkg.Name() != "main") {
		scanFns := fns
		if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil {
			scanFns = append(append([]*ssa.Function(nil), fns...), pkgInit)
		}
		for _, fn := range scanFns {
			exportedFn := fn.Parent() == nil && token.IsExported(fn.Name())
			forEachInstr(fn, func(ins ssa.Instruction) {
				switch x := ins.(type) {
				case *ssa.Return:
					if !exportedFn {
						return
					}
					for _, r := range x.Results {
						if g, ok := st.globalMutArg(r); ok {
							mutated[g] = true
						}
					}
				case *ssa.Store:
					g, ok := st.globalMutArg(x.Val)
					if !ok {
						return
					}
					if dstG, ok := globalRoot(x.Addr); ok && dstG.Pkg == st.ssa.Pkg && token.IsExported(dstG.Name()) {
						mutated[g] = true
						return
					}
					if exportedFn && rootsAtParam(x.Addr, fn) {
						mutated[g] = true
					}
				case *ssa.MapUpdate:
					g, ok := st.globalMutArg(x.Value)
					if !ok {
						return
					}
					if dstG, ok := globalRoot(x.Map); ok && dstG.Pkg == st.ssa.Pkg && token.IsExported(dstG.Name()) {
						mutated[g] = true
						return
					}
					if exportedFn && rootsAtParam(x.Map, fn) {
						mutated[g] = true
					}
				case *ssa.Send:
					if g, ok := st.globalMutArg(x.X); ok {
						mutated[g] = true
					}
				}
			})
		}
	}
	st.markEscapedGlobals(fns, mutated)
	st.globalWrites = writes
	st.mutatedGlobals = mutated
}

// markEscapedGlobals marks as mutated every package variable whose backing memory leaves the
// function that reads it by a route that is not a freeze: returned (from any function, since a
// caller can hand the result on), retained by a callee, captured by a closure that retains it,
// or stored where other code can reach it. The alias graph already records these as escape and
// return events on the class of the variable's value and on every class that holds or is held by
// it, so a table copied into a heap struct or a returned closure is found as well as the table
// itself. Publishing into an atomic container is a transfer event and does not count: it is the
// freeze the exemption exists for. A variable whose memory escapes this way can be written by
// code gosree does not see, so it is not init-only and fails closed.
func (st *state) markEscapedGlobals(fns []*ssa.Function, mutated map[*ssa.Global]bool) {
	// The init-only exemption is consulted only where a call publishes through a container that
	// freezes to imm (graph.call makes those transfers and check.go reads the exemption for them
	// alone), so a package with no such call never reads what this finds, and building every
	// function's alias graph here would be wasted.
	if !st.hasFreezeSite(fns) {
		return
	}
	scan := fns
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil {
		scan = append(append([]*ssa.Function(nil), fns...), pkgInit)
	}
	for _, fn := range scan {
		if len(fn.Blocks) == 0 {
			continue
		}
		g := st.graphOf(fn)
		for v, n := range g.nodes {
			glob, ok := v.(*ssa.Global)
			if !ok || n == nil || glob.Pkg != st.ssa.Pkg || mutated[glob] {
				continue
			}
			region := g.region(n)
		scan:
			for c := range region {
				for _, e := range c.events {
					// An event tied to a variable is the variable's own seed (it is "stored in"
					// itself), not something done with its memory.
					if e.global == nil && (e.kind == evEscape || e.kind == evReturn) && !g.formatsOnly(e, region) {
						mutated[glob] = true
						break scan
					}
				}
			}
		}
	}
}

// formatCallees are the calls that render their operands and keep none of them: the fmt and log
// printing families. The one thing they run on an operand is a method fmt looks for.
var formatCallees = func() map[string]bool {
	m := map[string]bool{}
	for _, base := range []string{"Print", "Println", "Printf"} {
		for _, pre := range []string{"fmt.", "log.", "(*log.Logger)."} {
			m[pre+base] = true
		}
	}
	for _, base := range []string{"Fatal", "Fatalf", "Fatalln", "Panic", "Panicf", "Panicln"} {
		m["log."+base] = true
		m["(*log.Logger)."+base] = true
	}
	for _, base := range []string{"Sprint", "Sprintf", "Sprintln", "Fprint", "Fprintf", "Fprintln", "Errorf", "Append", "Appendf", "Appendln"} {
		m["fmt."+base] = true
	}
	return m
}()

// calledByFormat names the methods fmt calls on an operand (Stringer, error, Formatter, GoStringer)
// or on a writer (Write).
var calledByFormat = map[string]bool{"String": true, "Error": true, "Format": true, "GoString": true, "Write": true}

// formatsOnly reports whether e is the escape of a call to a printing function that cannot leak
// anything in region: every operand whose class is in region has a type with no method the call
// would run. An operand the call's arguments do not spell out (a spread slice, an interface value)
// fails closed. fmt.Errorf keeps a %w operand, but only an error value, which has an Error method.
func (g *graph) formatsOnly(e event, region map[*node]bool) bool {
	call, ok := e.instr.(ssa.CallInstruction)
	if !ok {
		return false
	}
	callee := call.Common().StaticCallee()
	if callee == nil || !formatCallees[calleeFullName(callee)] {
		return false
	}
	relevant := func(v ssa.Value) bool {
		n, ok := g.nodes[v]
		return ok && n != nil && region[n.find()]
	}
	for _, a := range call.Common().Args {
		for _, op := range formatOperands(a) {
			if relevant(op) && typeCallsFormatMethod(op.Type(), map[types.Type]bool{}) {
				return false
			}
		}
	}
	return true
}

func calleeFullName(fn *ssa.Function) string {
	if o, ok := fn.Object().(*types.Func); ok {
		return o.FullName()
	}
	return ""
}

// formatOperands lists the values a printing call receives for argument a: the elements of a
// ...any slice built in place, a boxed value's own value, or a itself.
func formatOperands(a ssa.Value) []ssa.Value {
	if mi, ok := a.(*ssa.MakeInterface); ok {
		return []ssa.Value{mi.X}
	}
	sl, ok := a.(*ssa.Slice)
	if !ok {
		return []ssa.Value{a}
	}
	alloc, ok := sl.X.(*ssa.Alloc)
	if !ok || alloc.Referrers() == nil {
		return []ssa.Value{a}
	}
	var ops []ssa.Value
	for _, r := range *alloc.Referrers() {
		switch r := r.(type) {
		case *ssa.Slice:
		case *ssa.IndexAddr:
			if r.Referrers() == nil {
				continue
			}
			for _, u := range *r.Referrers() {
				st, ok := u.(*ssa.Store)
				if !ok || st.Addr != ssa.Value(r) {
					return []ssa.Value{a, ssa.Value(alloc)}
				}
				ops = append(ops, formatOperands(st.Val)...)
			}
		default:
			return []ssa.Value{a, ssa.Value(alloc)}
		}
	}
	return ops
}

// typeCallsFormatMethod reports whether a value of type t, or anything it holds, has a method a
// printing call runs. An interface, or a type parameter, may hold any type, so it does.
func typeCallsFormatMethod(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if _, isTP := t.(*types.TypeParam); isTP {
		return true
	}
	for _, base := range []types.Type{t, types.NewPointer(t)} {
		ms := types.NewMethodSet(base)
		for i := range ms.Len() {
			if calledByFormat[ms.At(i).Obj().Name()] {
				return true
			}
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		return true
	case *types.Pointer:
		return typeCallsFormatMethod(u.Elem(), seen)
	case *types.Slice:
		return typeCallsFormatMethod(u.Elem(), seen)
	case *types.Array:
		return typeCallsFormatMethod(u.Elem(), seen)
	case *types.Chan:
		return typeCallsFormatMethod(u.Elem(), seen)
	case *types.Map:
		return typeCallsFormatMethod(u.Key(), seen) || typeCallsFormatMethod(u.Elem(), seen)
	case *types.Struct:
		for f := range u.Fields() {
			if typeCallsFormatMethod(f.Type(), seen) {
				return true
			}
		}
	}
	return false
}

// hasFreezeSite reports whether any function calls, statically, a function whose cap contract
// publishes into a container that freezes to imm.
func (st *state) hasFreezeSite(fns []*ssa.Function) bool {
	for _, fn := range fns {
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				call, ok := ins.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if c, ok := capContractFor(call.Common().StaticCallee()); ok && c.freezesToImm && len(c.publishArgs) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func rootsAtParam(v ssa.Value, fn *ssa.Function) bool {
	for range 16 {
		for _, p := range fn.Params {
			if v == p {
				return true
			}
		}
		switch x := v.(type) {
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return false
			}
			v = x.X
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		default:
			return false
		}
	}
	return false
}

func (st *state) checkGlobals() {
	writes := st.globalWrites
	globals := make([]*ssa.Global, 0, len(writes))
	for g := range writes {
		globals = append(globals, g)
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i].Pos() < globals[j].Pos() })
	for _, g := range globals {
		ws := writes[g]
		sort.Slice(ws, func(i, j int) bool { return ws[i] < ws[j] })
		related := make([]analysis.RelatedInformation, 0, len(ws))
		for _, w := range ws {
			related = append(related, analysis.RelatedInformation{Pos: w, Message: "written here"})
		}
		st.reportRelated(g.Pos(), GlobalUse, fmt.Sprintf(
			"package variable %s is written outside init%s; guard it with //gosree:guarded_by or write it only during initialisation",
			g.Name(), st.at(ws[0])), related)
	}
}

// isInitOnlyImmutableGlobal reports whether g is a package variable of this package proven to be
// written only during package initialisation and unreachable for external mutation.
func (st *state) isInitOnlyImmutableGlobal(g *ssa.Global) bool {
	if g == nil || st.ssa == nil || g.Pkg != st.ssa.Pkg {
		return false
	}
	if st.mutatedGlobals[g] || len(st.globalWrites[g]) > 0 {
		return false
	}
	if _, guarded := st.slotOf(g); guarded {
		return false
	}
	if token.IsExported(g.Name()) && (g.Pkg.Pkg == nil || g.Pkg.Pkg.Name() != "main") {
		return false
	}
	return true
}

// directInitOnlyGlobal reports whether v is a direct read/projection of an init-only immutable
// package variable (never passing through a local Alloc cell).
func (st *state) directInitOnlyGlobal(v ssa.Value) bool {
	for range 32 {
		if v == nil {
			return false
		}
		switch x := v.(type) {
		case *ssa.Global:
			return st.isInitOnlyImmutableGlobal(x)
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return false
			}
			v = x.X
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Field:
			v = x.X
		case *ssa.Index:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.TypeAssert:
			v = x.X
		default:
			return false
		}
	}
	return false
}

// initFuncs returns the functions that run only synchronously during initialisation: top-level init
// functions, the package initialiser, and functions/closures whose every caller is a synchronous
// call or defer from one of those and whose value is never spawned or stored.
func (st *state) initFuncs(fns []*ssa.Function) map[*ssa.Function]bool {
	out := map[*ssa.Function]bool{}
	for _, fn := range fns {
		if isInit(fn) {
			out[fn] = true
		}
	}
	scanFns := fns
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil {
		out[pkgInit] = true
		scanFns = append(append([]*ssa.Function(nil), fns...), pkgInit)
	}
	callers := map[*ssa.Function][]*ssa.Function{}
	taken := map[*ssa.Function]bool{}
	var opBuf [8]*ssa.Value
	for _, fn := range scanFns {
		forEachInstr(fn, func(ins ssa.Instruction) {
			var static *ssa.Function
			switch c := ins.(type) {
			case *ssa.Call, *ssa.Defer:
				cc := c.(ssa.CallInstruction).Common()
				if !cc.IsInvoke() {
					if f, ok := cc.Value.(*ssa.Function); ok {
						static = f
						callers[f] = append(callers[f], fn)
					}
				}
			case *ssa.MakeClosure:
				if mcFn, ok := c.Fn.(*ssa.Function); ok {
					refs := c.Referrers()
					syncOnly := refs != nil && len(*refs) > 0
					if syncOnly {
						for _, ref := range *refs {
							switch rc := ref.(type) {
							case *ssa.Call, *ssa.Defer:
								cc := rc.(ssa.CallInstruction).Common()
								if cc.IsInvoke() || cc.Value != c {
									syncOnly = false
								}
								for _, arg := range cc.Args {
									if arg == c {
										syncOnly = false
									}
								}
							default:
								syncOnly = false
							}
						}
					}
					if syncOnly {
						static = mcFn
						callers[mcFn] = append(callers[mcFn], fn)
					} else {
						taken[mcFn] = true
					}
				}
			}
			for _, op := range ins.Operands(opBuf[:0]) {
				if op == nil {
					continue
				}
				if f, ok := (*op).(*ssa.Function); ok && f != static {
					taken[f] = true
				}
			}
		})
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			if out[fn] || taken[fn] || len(callers[fn]) == 0 || fn.Pkg != st.ssa.Pkg {
				continue
			}
			all := true
			for _, c := range callers[fn] {
				if !out[c] {
					all = false
					break
				}
			}
			if all {
				out[fn] = true
				changed = true
			}
		}
	}
	return out
}

func isInit(fn *ssa.Function) bool {
	if fn == nil || fn.Parent() != nil {
		return false
	}
	if fn.Name() == "init" && fn.Signature.Recv() == nil {
		return true
	}
	if len(fn.Name()) > 5 && fn.Name()[:5] == "init#" {
		return true
	}
	return false
}

func forEachInstr(fn *ssa.Function, f func(ssa.Instruction)) {
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			f(ins)
		}
	}
}

func (st *state) globalMutArg(v ssa.Value) (*ssa.Global, bool) {
	if v == nil || st.cls.Shareable(v.Type()) {
		return nil, false
	}
	if _, guarded := st.slotOf(v); guarded {
		return nil, false
	}
	g, ok := globalRoot(v)
	if !ok || g.Pkg != st.ssa.Pkg {
		return nil, false
	}
	return g, true
}

// globalRoot follows field, index, slice, phi, local cell store, and getter call steps back to a package variable.
func globalRoot(v ssa.Value) (*ssa.Global, bool) {
	return globalRootSeen(v, map[ssa.Value]bool{})
}

func globalRootSeen(v ssa.Value, seen map[ssa.Value]bool) (*ssa.Global, bool) {
	for range 64 {
		if v == nil || seen[v] || len(seen) >= 64 {
			return nil, false
		}
		seen[v] = true
		switch x := v.(type) {
		case *ssa.Global:
			return x, true
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Field:
			v = x.X
		case *ssa.Index:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.TypeAssert:
			v = x.X
		case *ssa.Phi:
			for _, e := range x.Edges {
				if g, ok := globalRootSeen(e, seen); ok {
					return g, true
				}
			}
			return nil, false
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			var alloc *ssa.Alloc
			if a, ok := x.X.(*ssa.Alloc); ok {
				alloc = a
			} else if fa, ok := x.X.(*ssa.FieldAddr); ok {
				alloc, _ = fa.X.(*ssa.Alloc)
			}
			if alloc != nil && alloc.Referrers() != nil {
				for _, ref := range *alloc.Referrers() {
					if st, ok := ref.(*ssa.Store); ok && st.Addr == alloc {
						if g, ok := globalRootSeen(st.Val, seen); ok {
							return g, true
						}
					}
					if fa, ok := ref.(*ssa.FieldAddr); ok && fa.Referrers() != nil {
						for _, r2 := range *fa.Referrers() {
							if st, ok := r2.(*ssa.Store); ok && st.Addr == fa {
								if g, ok := globalRootSeen(st.Val, seen); ok {
									return g, true
								}
							}
						}
					}
					if ia, ok := ref.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
						for _, r2 := range *ia.Referrers() {
							if st, ok := r2.(*ssa.Store); ok && st.Addr == ia {
								if g, ok := globalRootSeen(st.Val, seen); ok {
									return g, true
								}
							}
						}
					}
				}
			}
			v = x.X
		case *ssa.Call:
			callee := x.Call.StaticCallee()
			if callee == nil || len(callee.Blocks) == 0 {
				return nil, false
			}
			for _, b := range callee.Blocks {
				for _, ins := range b.Instrs {
					if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) == 1 {
						if g, ok := globalRootSeen(ret.Results[0], seen); ok {
							return g, true
						}
						if pi := paramRootIndex(ret.Results[0], callee); pi >= 0 && pi < len(x.Call.Args) {
							if g, ok := globalRootSeen(x.Call.Args[pi], seen); ok {
								return g, true
							}
						}
					}
				}
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}

func paramRootIndex(v ssa.Value, callee *ssa.Function) int {
	for range 16 {
		for i, p := range callee.Params {
			if v == p {
				return i
			}
		}
		switch x := v.(type) {
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.IndexAddr:
			v = x.X
		case *ssa.Slice:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.Convert:
			v = x.X
		default:
			return -1
		}
	}
	return -1
}
