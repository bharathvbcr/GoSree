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
		publishArgs: []int{1, 2},
		what:        "published via (*sync.Map).LoadOrStore",
		displayName: "(*sync.Map).LoadOrStore",
	},
	"(*sync.Map).Swap": {
		publishArgs: []int{1, 2},
		what:        "published via (*sync.Map).Swap",
		displayName: "(*sync.Map).Swap",
	},
	"(*sync.Map).CompareAndSwap": {
		publishArgs: []int{3},
		what:        "published via (*sync.Map).CompareAndSwap",
		displayName: "(*sync.Map).CompareAndSwap",
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
	obj, ok := fn.Object().(*types.Func)
	if !ok {
		return capContract{}, false
	}
	c, ok := capContracts[obj.Origin().FullName()]
	return c, ok
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
			callee := callIns.Call.StaticCallee()
			c, has := capContractFor(callee)
			if !has || !c.sharedReadOnlyLoad || st.cls.Shareable(callIns.Type()) {
				return
			}
			add(fn, callIns, sharedLoadOrigin{
				desc:  c.displayName,
				pos:   callIns.Pos(),
				muted: st.ix.Suppressed(callIns.Pos()),
			})
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

	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		enqueued[fn] = false
		origins := shared[fn]
		roots := make(map[ssa.Value]string, len(origins))
		for v, o := range origins {
			roots[v] = o.desc
		}

		forEachInstr(fn, func(ins ssa.Instruction) {
			if _, ok := spawnOf(ins); ok {
				return
			}
			if w, ok := writeOf(ins); ok {
				if _, guarded := st.slotOf(w.addr); guarded {
					return
				}
				if root, ok := st.sharedLoadRoot(w.addr, roots); ok {
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
			callee, bindings := directCallee(cc.Value)
			if callee == nil {
				return
			}
			for _, a := range cc.Args {
				if mc := resolveClosureVal(a, 0); mc != nil {
					if argFn, ok := mc.Fn.(*ssa.Function); ok && len(argFn.Blocks) > 0 && st.followable(argFn) {
						for j, b := range mc.Bindings {
							if root, ok := st.sharedLoadRoot(b, roots); ok && j < len(argFn.FreeVars) {
								add(argFn, argFn.FreeVars[j], origins[root])
							}
						}
					}
				}
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
					if root, ok := st.sharedLoadRoot(a, roots); ok {
						orig := origins[root]
						emit(ins, orig, fmt.Sprintf(
							"shared value loaded from %s%s is passed to %s, which writes it; published atomic values are read-only once shared",
							orig.desc, st.at(orig.pos), calleeName(callee)))
						return
					}
				}
				return
			}
			for i, a := range cc.Args {
				if i >= len(callee.Params) {
					break
				}
				if root, ok := st.sharedLoadRoot(a, roots); ok {
					add(callee, callee.Params[i], origins[root])
				}
			}
			for j, b := range bindings {
				if root, ok := st.sharedLoadRoot(b, roots); ok && j < len(callee.FreeVars) {
					add(callee, callee.FreeVars[j], origins[root])
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
	return sharedRoot(v, roots)
}
