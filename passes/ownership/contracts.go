package ownership

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// capContract specifies the ownership/capability transfer contract of a standard-library
// concurrent container or context boundary (Featherweight GoSree §3.3 Function Hooks).
// Like lockOps for SAFE009, capContracts define ownership/immutability rules at stdlib
// concurrent container boundaries rather than assuming unchecked callees are safe.
type capContract struct {
	// publishArgs lists SSA argument indices (receiver is 0) whose non-shareable payload is
	// published or moved into a shared concurrent container (`Iso` consumed -> SAFE002/SAFE003/SAFE004/CAP003).
	publishArgs []int
	// what describes the capability transfer in diagnostics.
	what string
	// freshResult says result 0 transfers unique `Iso` ownership out of the container to the
	// caller (e.g. (*sync.Pool).Get), analogous to receiving from a channel (`<-ch`).
	freshResult bool
	// sharedReadOnlyLoad says result 0 yields shared published memory (`\imm` read-only capability,
	// e.g. (*sync/atomic.Pointer[T]).Load, (*sync/atomic.Value).Load): reading is safe, while
	// unguarded writes through non-shareable memory rooted at the loaded value violate SAFE005.
	sharedReadOnlyLoad bool
	// freezesToImm says every read path out of this container enforces read-only `\imm` capability
	// (SAFE005), so referencing a proven init-only immutable package variable inside the published
	// value cannot race with readers or other goroutines.
	freezesToImm bool
	// displayName is the human-readable function/method name in diagnostics.
	displayName string
}

var capContracts = map[string]capContract{
	"(*sync/atomic.Pointer[T]).Store": {
		publishArgs:  []int{1},
		what:         "published via (*sync/atomic.Pointer).Store",
		freezesToImm: true,
		displayName:  "(*sync/atomic.Pointer).Store",
	},
	"(*sync/atomic.Pointer[T]).Swap": {
		publishArgs:        []int{1},
		what:               "published via (*sync/atomic.Pointer).Swap",
		sharedReadOnlyLoad: true,
		freezesToImm:       true,
		displayName:        "(*sync/atomic.Pointer).Swap",
	},
	"(*sync/atomic.Pointer[T]).CompareAndSwap": {
		publishArgs:  []int{2},
		what:         "published via (*sync/atomic.Pointer).CompareAndSwap",
		freezesToImm: true,
		displayName:  "(*sync/atomic.Pointer).CompareAndSwap",
	},
	"(*sync/atomic.Pointer[T]).Load": {
		sharedReadOnlyLoad: true,
		displayName:        "(*sync/atomic.Pointer).Load",
	},
	"(*sync/atomic.Value).Store": {
		publishArgs:  []int{1},
		what:         "published via (*sync/atomic.Value).Store",
		freezesToImm: true,
		displayName:  "(*sync/atomic.Value).Store",
	},
	"(*sync/atomic.Value).Swap": {
		publishArgs:        []int{1},
		what:               "published via (*sync/atomic.Value).Swap",
		sharedReadOnlyLoad: true,
		freezesToImm:       true,
		displayName:        "(*sync/atomic.Value).Swap",
	},
	"(*sync/atomic.Value).CompareAndSwap": {
		publishArgs:  []int{2},
		what:         "published via (*sync/atomic.Value).CompareAndSwap",
		freezesToImm: true,
		displayName:  "(*sync/atomic.Value).CompareAndSwap",
	},
	"(*sync/atomic.Value).Load": {
		sharedReadOnlyLoad: true,
		displayName:        "(*sync/atomic.Value).Load",
	},
	"(*sync.Map).Store": {
		publishArgs: []int{1, 2},
		what:        "published via (*sync.Map).Store",
		displayName: "(*sync.Map).Store",
	},
	"(*sync.Map).LoadOrStore": {
		publishArgs:        []int{1, 2},
		what:               "published via (*sync.Map).LoadOrStore",
		sharedReadOnlyLoad: true,
		displayName:        "(*sync.Map).LoadOrStore",
	},
	"(*sync.Map).Swap": {
		publishArgs:        []int{1, 2},
		what:               "published via (*sync.Map).Swap",
		sharedReadOnlyLoad: true,
		displayName:        "(*sync.Map).Swap",
	},
	"(*sync.Map).CompareAndSwap": {
		publishArgs: []int{3},
		what:        "published via (*sync.Map).CompareAndSwap",
		displayName: "(*sync.Map).CompareAndSwap",
	},
	"(*sync.Map).Load": {
		sharedReadOnlyLoad: true,
		displayName:        "(*sync.Map).Load",
	},
	"(*sync.Map).LoadAndDelete": {
		sharedReadOnlyLoad: true,
		displayName:        "(*sync.Map).LoadAndDelete",
	},
	"(*sync.Pool).Put": {
		publishArgs: []int{1},
		what:        "returned to (*sync.Pool).Put",
		displayName: "(*sync.Pool).Put",
	},
	"(*sync.Pool).Get": {
		freshResult: true,
		displayName: "(*sync.Pool).Get",
	},
	"context.WithValue": {
		publishArgs: []int{1, 2},
		what:        "stored in shared context.Context via context.WithValue",
		displayName: "context.WithValue",
	},
}

func capContractNames() []string {
	out := make([]string, 0, len(capContracts))
	for name := range capContracts {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func capContractFor(fn *ssa.Function) (capContract, bool) {
	if fn == nil {
		return capContract{}, false
	}
	if o := fn.Origin(); o != nil {
		fn = o
	}
	if obj, ok := fn.Object().(*types.Func); ok {
		if c, ok := capContracts[obj.Origin().FullName()]; ok {
			return c, true
		}
	}
	if fn.Synthetic != "" {
		normSynth := normalizeSynthetic(fn.Synthetic)
		for target, c := range capContracts {
			normTarget := normalizeSynthetic(target)
			if strings.Contains(normSynth, normTarget) {
				return c, true
			}
		}
	}
	name := normalizeSynthetic(fn.String())
	for target, c := range capContracts {
		normTarget := normalizeSynthetic(target)
		if strings.Contains(name, normTarget) {
			return c, true
		}
	}
	return capContract{}, false
}

func capContractForBindings(fn *ssa.Function, bindings []ssa.Value) (capContract, bool) {
	if fn == nil || len(bindings) == 0 {
		return capContract{}, false
	}
	concrete := unwrapInterfaceVal(bindings[0])
	if concrete == nil || concrete == bindings[0] {
		return capContract{}, false
	}
	tStr := normalizeSynthetic(concrete.Type().String())
	mName := fn.Name()
	if idx := strings.Index(mName, "$"); idx >= 0 {
		mName = mName[:idx]
	}
	if idx := strings.LastIndex(mName, "."); idx >= 0 {
		mName = mName[idx+1:]
	}
	switch {
	case strings.Contains(tStr, "sync/atomic.Pointer"):
		switch mName {
		case "Store":
			return capContracts["(*sync/atomic.Pointer[T]).Store"], true
		case "Swap":
			return capContracts["(*sync/atomic.Pointer[T]).Swap"], true
		case "CompareAndSwap":
			return capContracts["(*sync/atomic.Pointer[T]).CompareAndSwap"], true
		case "Load":
			return capContracts["(*sync/atomic.Pointer[T]).Load"], true
		}
	case strings.Contains(tStr, "sync/atomic.Value"):
		switch mName {
		case "Store":
			return capContracts["(*sync/atomic.Value).Store"], true
		case "Swap":
			return capContracts["(*sync/atomic.Value).Swap"], true
		case "CompareAndSwap":
			return capContracts["(*sync/atomic.Value).CompareAndSwap"], true
		case "Load":
			return capContracts["(*sync/atomic.Value).Load"], true
		}
	case strings.Contains(tStr, "sync.Map"):
		switch mName {
		case "Store":
			return capContracts["(*sync.Map).Store"], true
		case "LoadOrStore":
			return capContracts["(*sync.Map).LoadOrStore"], true
		case "Swap":
			return capContracts["(*sync.Map).Swap"], true
		case "CompareAndSwap":
			return capContracts["(*sync.Map).CompareAndSwap"], true
		case "Load":
			return capContracts["(*sync.Map).Load"], true
		case "LoadAndDelete":
			return capContracts["(*sync.Map).LoadAndDelete"], true
		}
	case strings.Contains(tStr, "sync.Pool"):
		switch mName {
		case "Put":
			return capContracts["(*sync.Pool).Put"], true
		case "Get":
			return capContracts["(*sync.Pool).Get"], true
		}
	}
	return capContract{}, false
}

func phiAllEdgesHaveFreshResult(v ssa.Value) bool {
	phi, ok := v.(*ssa.Phi)
	if !ok {
		return true
	}
	for _, edge := range phi.Edges {
		fn, _ := resolveFuncVal(edge, 0, map[ssa.Value]bool{})
		if fn == nil {
			return false
		}
		c, ok := capContractFor(fn)
		if !ok || !c.freshResult {
			return false
		}
	}
	return true
}

func normalizeSynthetic(s string) string {
	for {
		start := strings.IndexByte(s, '[')
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start:], ']')
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+1:]
	}
	return s
}

func contractForInvoke(cc *ssa.CallCommon) (capContract, bool) {
	if !cc.IsInvoke() {
		return capContract{}, false
	}
	concrete := unwrapInterfaceVal(cc.Value)
	if concrete != nil && concrete != cc.Value {
		tStr := normalizeSynthetic(concrete.Type().String())
		switch {
		case strings.Contains(tStr, "sync/atomic.Pointer"):
			switch cc.Method.Name() {
			case "Store":
				return capContracts["(*sync/atomic.Pointer[T]).Store"], true
			case "Swap":
				return capContracts["(*sync/atomic.Pointer[T]).Swap"], true
			case "CompareAndSwap":
				return capContracts["(*sync/atomic.Pointer[T]).CompareAndSwap"], true
			case "Load":
				return capContracts["(*sync/atomic.Pointer[T]).Load"], true
			}
		case strings.Contains(tStr, "sync/atomic.Value"):
			switch cc.Method.Name() {
			case "Store":
				return capContracts["(*sync/atomic.Value).Store"], true
			case "Swap":
				return capContracts["(*sync/atomic.Value).Swap"], true
			case "CompareAndSwap":
				return capContracts["(*sync/atomic.Value).CompareAndSwap"], true
			case "Load":
				return capContracts["(*sync/atomic.Value).Load"], true
			}
		case strings.Contains(tStr, "sync.Map"):
			switch cc.Method.Name() {
			case "Store":
				return capContracts["(*sync.Map).Store"], true
			case "LoadOrStore":
				return capContracts["(*sync.Map).LoadOrStore"], true
			case "Swap":
				return capContracts["(*sync.Map).Swap"], true
			case "CompareAndSwap":
				return capContracts["(*sync.Map).CompareAndSwap"], true
			case "Load":
				return capContracts["(*sync.Map).Load"], true
			case "LoadAndDelete":
				return capContracts["(*sync.Map).LoadAndDelete"], true
			}
		case strings.Contains(tStr, "sync.Pool"):
			switch cc.Method.Name() {
			case "Put":
				return capContracts["(*sync.Pool).Put"], true
			case "Get":
				return capContracts["(*sync.Pool).Get"], true
			}
		}
	}

	// For open interfaces: only treat as a capability container if the interface defines
	// atomic/concurrent container methods (e.g. CompareAndSwap, Swap, LoadOrStore, LoadAndDelete).
	// An arbitrary interface like ConfigStorer that only has Store(x) is not an atomic container.
	if iface, ok := cc.Value.Type().Underlying().(*types.Interface); ok {
		hasCAS := false
		hasSwap := false
		hasMapOps := false
		for i := 0; i < iface.NumMethods(); i++ {
			mName := iface.Method(i).Name()
			switch mName {
			case "CompareAndSwap":
				hasCAS = true
			case "Swap":
				hasSwap = true
			case "LoadOrStore", "LoadAndDelete":
				hasMapOps = true
			}
		}
		if hasMapOps {
			switch cc.Method.Name() {
			case "Store":
				return capContracts["(*sync.Map).Store"], true
			case "LoadOrStore":
				return capContracts["(*sync.Map).LoadOrStore"], true
			case "Swap":
				return capContracts["(*sync.Map).Swap"], true
			case "CompareAndSwap":
				return capContracts["(*sync.Map).CompareAndSwap"], true
			case "Load":
				return capContracts["(*sync.Map).Load"], true
			case "LoadAndDelete":
				return capContracts["(*sync.Map).LoadAndDelete"], true
			}
		} else if hasCAS || hasSwap {
			switch cc.Method.Name() {
			case "Store":
				return capContract{
					publishArgs:  []int{1},
					what:         "published via Store",
					freezesToImm: true,
					displayName:  "Store",
				}, true
			case "Swap":
				return capContract{
					publishArgs:        []int{1},
					what:               "published via Swap",
					sharedReadOnlyLoad: true,
					freezesToImm:       true,
					displayName:        "Swap",
				}, true
			case "CompareAndSwap":
				return capContract{
					publishArgs:  []int{2},
					what:         "published via CompareAndSwap",
					freezesToImm: true,
					displayName:  "CompareAndSwap",
				}, true
			case "Load":
				return capContract{
					sharedReadOnlyLoad: true,
					displayName:        "Load",
				}, true
			}
		}
	}

	switch cc.Method.Name() {
	case "Put":
		if len(cc.Args) == 1 {
			return capContracts["(*sync.Pool).Put"], true
		}
	case "Get":
		if len(cc.Args) == 0 {
			return capContracts["(*sync.Pool).Get"], true
		}
	}
	return capContract{}, false
}

func resolveFuncVal(v ssa.Value, depth int, seen map[ssa.Value]bool) (*ssa.Function, []ssa.Value) {
	if depth > 10 || v == nil || seen[v] || len(seen) >= 32 {
		return nil, nil
	}
	seen[v] = true
	switch val := v.(type) {
	case *ssa.Function:
		return val, nil
	case *ssa.MakeClosure:
		if fn, ok := val.Fn.(*ssa.Function); ok {
			return fn, val.Bindings
		}
	case *ssa.ChangeType:
		return resolveFuncVal(val.X, depth+1, seen)
	case *ssa.UnOp:
		if val.Op == token.MUL {
			switch addr := val.X.(type) {
			case *ssa.Alloc:
				var lastFn *ssa.Function
				var lastBindings []ssa.Value
				if addr.Referrers() != nil {
					for _, ref := range *addr.Referrers() {
						if store, ok := ref.(*ssa.Store); ok && store.Addr == addr {
							if fn, b := resolveFuncVal(store.Val, depth+1, seen); fn != nil {
								lastFn, lastBindings = fn, b
							}
						}
					}
				}
				if lastFn != nil {
					return lastFn, lastBindings
				}
			case *ssa.FieldAddr:
				if last := resolveFieldStore(addr); last != nil {
					if fn, b := resolveFuncVal(last, depth+1, seen); fn != nil {
						return fn, b
					}
				}
			case *ssa.IndexAddr:
				if addr.Referrers() != nil {
					for _, ref := range *addr.Referrers() {
						if store, ok := ref.(*ssa.Store); ok && store.Addr == addr {
							if fn, b := resolveFuncVal(store.Val, depth+1, seen); fn != nil {
								return fn, b
							}
						}
					}
				}
				if sl, ok := addr.X.(*ssa.Slice); ok {
					if alloc, ok := sl.X.(*ssa.Alloc); ok && alloc.Referrers() != nil {
						for _, ref := range *alloc.Referrers() {
							if idxAddr, ok := ref.(*ssa.IndexAddr); ok && idxAddr.X == alloc && idxAddr.Referrers() != nil {
								for _, iref := range *idxAddr.Referrers() {
									if store, ok := iref.(*ssa.Store); ok && store.Addr == idxAddr {
										if fn, b := resolveFuncVal(store.Val, depth+1, seen); fn != nil {
											return fn, b
										}
									}
								}
							}
						}
					}
				}
			}
		}
	case *ssa.Phi:
		for _, edge := range val.Edges {
			if fn, b := resolveFuncVal(edge, depth+1, seen); fn != nil {
				return fn, b
			}
		}
	}
	return nil, nil
}

func forwarderCall(callee *ssa.Function, cc *ssa.CallCommon) (*ssa.CallCommon, bool) {
	if callee == nil {
		return nil, false
	}
	if o := callee.Origin(); o != nil && len(o.Blocks) > 0 {
		callee = o
	}
	if len(callee.Blocks) == 0 || len(callee.Blocks) > 2 {
		return nil, false
	}
	var fwdCall ssa.CallInstruction
	for _, b := range callee.Blocks {
		for _, ins := range b.Instrs {
			if c, ok := ins.(ssa.CallInstruction); ok {
				if fwdCall != nil {
					return nil, false
				}
				fwdCall = c
			}
		}
	}
	if fwdCall == nil {
		return nil, false
	}
	fwdCC := fwdCall.Common()
	if fwdCC.Value == callee {
		return nil, false
	}
	paramMap := map[ssa.Value]ssa.Value{}
	for i, p := range callee.Params {
		if i < len(cc.Args) {
			paramMap[p] = cc.Args[i]
		}
	}
	targetVal := paramMap[fwdCC.Value]
	if targetVal == nil {
		targetVal = fwdCC.Value
	}
	if targetVal == callee {
		return nil, false
	}
	args := make([]ssa.Value, len(fwdCC.Args))
	for i, a := range fwdCC.Args {
		if m, ok := paramMap[a]; ok {
			args[i] = m
		} else {
			args[i] = a
		}
	}
	return &ssa.CallCommon{
		Value:  targetVal,
		Method: fwdCC.Method,
		Args:   args,
	}, true
}

func resolveFieldStore(addr *ssa.FieldAddr) ssa.Value {
	if addr == nil {
		return nil
	}
	if addr.Referrers() != nil {
		for _, ref := range *addr.Referrers() {
			if store, ok := ref.(*ssa.Store); ok && store.Addr == addr {
				return store.Val
			}
		}
	}
	base := addr.X
	for range 4 {
		if base == nil || base.Referrers() == nil {
			break
		}
		for _, ref := range *base.Referrers() {
			if fa, ok := ref.(*ssa.FieldAddr); ok && fa.Field == addr.Field && fa.Referrers() != nil {
				for _, faRef := range *fa.Referrers() {
					if store, ok := faRef.(*ssa.Store); ok && store.Addr == fa {
						return store.Val
					}
				}
			}
		}
		var copiedFrom ssa.Value
		for _, ref := range *base.Referrers() {
			if store, ok := ref.(*ssa.Store); ok && store.Addr == base {
				if unop, ok := store.Val.(*ssa.UnOp); ok && unop.Op == token.MUL {
					copiedFrom = unop.X
					break
				}
			}
		}
		base = copiedFrom
	}
	return nil
}

func unwrapInterfaceVal(v ssa.Value) ssa.Value {
	cur := v
	for range 16 {
		if cur == nil {
			break
		}
		switch x := cur.(type) {
		case *ssa.MakeInterface:
			cur = x.X
		case *ssa.ChangeInterface:
			cur = x.X
		case *ssa.TypeAssert:
			cur = x.X
		case *ssa.Phi:
			if len(x.Edges) > 0 {
				cur = x.Edges[0]
			} else {
				return cur
			}
		case *ssa.UnOp:
			if x.Op == token.MUL {
				switch addr := x.X.(type) {
				case *ssa.Alloc:
					var last ssa.Value
					if addr.Referrers() != nil {
						for _, ref := range *addr.Referrers() {
							if store, ok := ref.(*ssa.Store); ok && store.Addr == addr {
								last = store.Val
							}
						}
					}
					if last != nil {
						cur = last
						continue
					}
				case *ssa.FieldAddr:
					if last := resolveFieldStore(addr); last != nil {
						cur = last
						continue
					}
				case *ssa.IndexAddr:
					var last ssa.Value
					if addr.Referrers() != nil {
						for _, ref := range *addr.Referrers() {
							if store, ok := ref.(*ssa.Store); ok && store.Addr == addr {
								last = store.Val
							}
						}
					}
					if last == nil {
						base := addr.X
						if sl, ok := base.(*ssa.Slice); ok {
							base = sl.X
						}
						if base != nil && base.Referrers() != nil {
							for _, ref := range *base.Referrers() {
								if ia, ok := ref.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
									for _, iref := range *ia.Referrers() {
										if store, ok := iref.(*ssa.Store); ok && store.Addr == ia {
											last = store.Val
										}
									}
								}
							}
						}
					}
					if last != nil {
						cur = last
						continue
					}
				}
			}
			return cur
		case *ssa.Lookup:
			var found ssa.Value
			if x.X != nil && x.X.Referrers() != nil {
				for _, ref := range *x.X.Referrers() {
					if mu, ok := ref.(*ssa.MapUpdate); ok && mu.Map == x.X {
						found = mu.Value
						break
					}
				}
			}
			if found != nil {
				cur = found
				continue
			}
			return cur
		case *ssa.Field:
			cur = x.X
			continue
		default:
			return cur
		}
	}
	return cur
}

func isCapContractTransferWhat(what string) bool {
	return strings.HasPrefix(what, "published via ") ||
		strings.HasPrefix(what, "returned to ") ||
		strings.HasPrefix(what, "stored in shared ")
}

// unwrapPublishedArg strips interface/type wrappers (`any(x)`) so publishing a shareable value
// (e.g. `sync.Map.Store("k", 1)` or `context.WithValue(ctx, key{}, "id")`) sees the concrete
// shareable payload type rather than the outer `any` interface type.
func unwrapPublishedArg(v ssa.Value) ssa.Value {
	for range 16 {
		switch x := v.(type) {
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		default:
			return v
		}
	}
	return v
}

type sharedLoadOrigin struct {
	desc  string
	pos   token.Pos
	muted bool
}

// checkSharedLoadWrites enforces the read-only `\imm` capability on values loaded from shared
// atomic containers (`(*sync/atomic.Pointer[T]).Load`/`Swap`, `(*sync/atomic.Value).Load`/`Swap`).
// Reading through the loaded value is safe; unguarded mutation of non-shareable memory reachable
// from the loaded value violates SAFE005.
func (st *state) checkSharedLoadWrites(fns []*ssa.Function) {
	shared := map[*ssa.Function]map[ssa.Value]sharedLoadOrigin{}
	var queue []*ssa.Function
	enqueued := map[*ssa.Function]bool{}

	// A value is muted only while every origin that reaches it is muted: a //gosree:unsafe on one
	// Load never covers what another, unhatched Load reaches. An origin therefore changes at most
	// once (muted to unmuted), so each (function, value) pair is queued at most twice and the
	// worklist below terminates without a step cap.
	add := func(fn *ssa.Function, v ssa.Value, orig sharedLoadOrigin) {
		if fn == nil || v == nil || st.cls.Shareable(v.Type()) {
			return
		}
		if shared[fn] == nil {
			shared[fn] = map[ssa.Value]sharedLoadOrigin{}
		}
		if prev, exists := shared[fn][v]; exists && (!prev.muted || orig.muted) {
			return
		}
		shared[fn][v] = orig
		if !enqueued[fn] {
			enqueued[fn] = true
			queue = append(queue, fn)
		}
	}

	for _, fn := range fns {
		if len(fn.Blocks) == 0 {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			callIns, ok := ins.(*ssa.Call)
			if !ok {
				return
			}
			var c capContract
			var has bool
			if callIns.Call.IsInvoke() {
				c, has = contractForInvoke(&callIns.Call)
			} else {
				callee, _ := directCallee(callIns.Call.Value)
				if callee == nil {
					callee = callIns.Call.StaticCallee()
				}
				c, has = capContractFor(callee)
				if !has && callee != nil {
					if fwdCC, ok := forwarderCall(callee, &callIns.Call); ok {
						if fwdCC.IsInvoke() {
							c, has = contractForInvoke(fwdCC)
						} else {
							callee2, _ := directCallee(fwdCC.Value)
							if callee2 == nil {
								callee2 = fwdCC.StaticCallee()
							}
							c, has = capContractFor(callee2)
						}
					}
				}
			}
			if !has || !c.sharedReadOnlyLoad || st.cls.Shareable(callIns.Type()) {
				return
			}
			origin := sharedLoadOrigin{
				desc:  c.displayName,
				pos:   callIns.Pos(),
				muted: st.ix.Suppressed(callIns.Pos()),
			}
			if callIns.Call.Signature() != nil && callIns.Call.Signature().Results().Len() > 1 && callIns.Referrers() != nil {
				for _, r := range *callIns.Referrers() {
					if ext, ok := r.(*ssa.Extract); ok && ext.Index == 0 {
						add(fn, ext, origin)
					}
				}
			} else {
				add(fn, callIns, origin)
			}
		})
	}
	if len(queue) == 0 {
		return
	}

	// A line is reported once per mutedness, so a muted report made before an unmuted origin
	// arrived does not swallow the unmuted one.
	type lineKey struct {
		file  string
		line  int
		muted bool
	}
	reported := map[lineKey]bool{}
	once := func(ins ssa.Instruction, muted bool) bool {
		p := st.pass.Fset.Position(posOf(ins, ins))
		k := lineKey{p.Filename, p.Line, muted}
		if reported[k] {
			return false
		}
		reported[k] = true
		return true
	}

	emit := func(ins ssa.Instruction, orig sharedLoadOrigin, detail string) {
		if !once(ins, orig.muted) {
			return
		}
		prevMuted, prevAt := st.muted, st.transferAt
		before := st.mutedFindings
		st.muted, st.transferAt = orig.muted, orig.pos
		st.report(posOf(ins, ins), Imm, detail)
		st.muted, st.transferAt = prevMuted, prevAt
		if orig.muted && st.mutedFindings > before {
			st.ix.Mute(orig.pos)
		}
	}

	// Phase 1: Fixed-point propagation. Propagate origins across functions and bindings
	// until queue is empty. Writes are NOT checked here to ensure hop-distance independence.
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		enqueued[fn] = false
		origins := shared[fn]
		roots := make(map[ssa.Value]string, len(origins))
		unmuted := make(map[ssa.Value]string, len(origins))
		for v, o := range origins {
			roots[v] = o.desc
			if !o.muted {
				unmuted[v] = o.desc
			}
		}
		rootOf := func(v ssa.Value) (ssa.Value, bool) {
			if r, ok := st.sharedLoadRoot(v, unmuted); ok {
				return r, true
			}
			return st.sharedLoadRoot(v, roots)
		}

		forEachInstr(fn, func(ins ssa.Instruction) {
			if _, ok := spawnOf(ins); ok {
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			if cc.IsInvoke() {
				return
			}
			callee, bindings := directCallee(cc.Value)
			if callee == nil {
				return
			}
			for _, a := range cc.Args {
				if mc := resolveClosureVal(a, 0); mc != nil {
					if argFn, ok := mc.Fn.(*ssa.Function); ok && len(argFn.Blocks) > 0 && st.followable(argFn) {
						for j, b := range mc.Bindings {
							if root, ok := rootOf(b); ok && j < len(argFn.FreeVars) {
								add(argFn, argFn.FreeVars[j], origins[root])
							}
						}
					}
				}
			}
			if len(callee.Blocks) == 0 || !st.followable(callee) {
				return
			}
			for i, a := range cc.Args {
				if i >= len(callee.Params) {
					break
				}
				if root, ok := rootOf(a); ok {
					add(callee, callee.Params[i], origins[root])
				}
			}
			for j, b := range bindings {
				if root, ok := rootOf(b); ok && j < len(callee.FreeVars) {
					add(callee, callee.FreeVars[j], origins[root])
				}
			}
		})
	}

	// Phase 2: Converged write checking. Only after queue is completely drained,
	// inspect writes and external callee summary writes on the converged state.
	for fn, origins := range shared {
		if len(origins) == 0 || len(fn.Blocks) == 0 {
			continue
		}
		roots := make(map[ssa.Value]string, len(origins))
		unmuted := make(map[ssa.Value]string, len(origins))
		for v, o := range origins {
			roots[v] = o.desc
			if !o.muted {
				unmuted[v] = o.desc
			}
		}
		rootOf := func(v ssa.Value) (ssa.Value, bool) {
			if r, ok := st.sharedLoadRoot(v, unmuted); ok {
				return r, true
			}
			return st.sharedLoadRoot(v, roots)
		}

		forEachInstr(fn, func(ins ssa.Instruction) {
			if _, ok := spawnOf(ins); ok {
				return
			}
			if w, ok := writeOf(ins); ok {
				if _, guarded := st.slotOf(w.addr); guarded {
					return
				}
				if root, ok := rootOf(w.addr); ok {
					orig := origins[root]
					emit(ins, orig, fmt.Sprintf(
						"write through shared value loaded from %s%s; published atomic values are read-only once shared",
						orig.desc, st.at(orig.pos)))
				}
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			if cc.IsInvoke() {
				return
			}
			callee, _ := directCallee(cc.Value)
			if callee == nil {
				return
			}
			if len(callee.Blocks) == 0 || !st.followable(callee) {
				sum := st.summary(callee)
				if sum.pessimistic {
					return
				}
				for i, a := range cc.Args {
					if !sum.writes(i) || st.cls.Shareable(a.Type()) {
						continue
					}
					if _, guarded := st.slotOf(a); guarded {
						continue
					}
					if root, ok := rootOf(a); ok {
						orig := origins[root]
						emit(ins, orig, fmt.Sprintf(
							"shared value loaded from %s%s is passed to %s, which writes it; published atomic values are read-only once shared",
							orig.desc, st.at(orig.pos), calleeName(callee)))
						return
					}
				}
			}
		})
	}
}

func (st *state) sharedLoadRoot(v ssa.Value, roots map[ssa.Value]string) (ssa.Value, bool) {
	// If v passes through an interface assertion/extraction to a shareable or //gosree:imm type,
	// stop here: shareable payloads are safe to use concurrently and checkImmStores already owns
	// //gosree:imm field checks.
	cur := v
	for range 16 {
		if cur == nil {
			break
		}
		switch cur.(type) {
		case *ssa.TypeAssert, *ssa.ChangeInterface, *ssa.Extract:
			if st.cls.Shareable(cur.Type()) {
				return nil, false
			}
		}
		switch x := cur.(type) {
		case *ssa.FieldAddr:
			if _, guarded := st.guardedAt(x); guarded {
				return nil, false
			}
			cur = x.X
		case *ssa.IndexAddr:
			cur = x.X
		case *ssa.Slice:
			cur = x.X
		case *ssa.TypeAssert:
			cur = x.X
		case *ssa.ChangeInterface:
			cur = x.X
		case *ssa.Extract:
			cur = x.Tuple
		default:
			cur = nil
		}
	}
	return st.sharedRoot(v, roots)
}
