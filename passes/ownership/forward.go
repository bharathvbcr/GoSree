package ownership

import (
	"fmt"
	"go/token"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// go/ssa synthesises functions that only forward a call: a bound-method wrapper behind a method
// value, a thunk behind a method expression, a wrapper for a promoted method. Their instructions
// have no source position, and one wrapper serves every call of that method in the package. A check
// that follows a value into one and reports there reports at no position, and reports once for all
// of those calls. The shared-load check (SAFE005) therefore checks what a wrapper forwards to at
// each call that enters it, where there is a position, and says nothing inside the wrapper.

// forwarded is the one call a synthesised wrapper makes.
type forwarded struct {
	call   ssa.CallInstruction
	callee *ssa.Function
	// params are the wrapper's parameters and free variables, as the roots an argument of call is
	// walked back to; outer is what the wrapper's caller passed for each.
	params map[ssa.Value]string
	outer  map[ssa.Value]ssa.Value
}

// forwardedCall returns the call wrapper forwards to, when wrapper is a synthesised function whose
// body makes exactly one call and that call is static. args and bindings are what the caller passed
// for its parameters and free variables; either may be shorter than the wrapper's own list (a
// closure handed on as an argument is called later, with arguments the caller does not have).
func forwardedCall(wrapper *ssa.Function, args, bindings []ssa.Value) (forwarded, bool) {
	if wrapper == nil || wrapper.Synthetic == "" || len(wrapper.Blocks) == 0 {
		return forwarded{}, false
	}
	var call ssa.CallInstruction
	for _, b := range wrapper.Blocks {
		for _, ins := range b.Instrs {
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				continue
			}
			if call != nil {
				return forwarded{}, false
			}
			call = c
		}
	}
	if call == nil || call.Common().IsInvoke() {
		return forwarded{}, false
	}
	callee := call.Common().StaticCallee()
	if callee == nil {
		return forwarded{}, false
	}
	fw := forwarded{call: call, callee: callee, params: map[ssa.Value]string{}, outer: map[ssa.Value]ssa.Value{}}
	for i, p := range wrapper.Params {
		if i < len(args) {
			fw.params[p], fw.outer[p] = "", args[i]
		}
	}
	for j, fv := range wrapper.FreeVars {
		if j < len(bindings) {
			fw.params[fv], fw.outer[fv] = "", bindings[j]
		}
	}
	return fw, true
}

// forwardShared checks, for the call at ins that enters wrapper, the call the wrapper forwards to:
// a shared value ins passes on is passed on to that callee, and a finding is reported at ins. args
// and bindings are what ins passes for the wrapper's parameters and free variables. The forwarded
// call is remembered, so the scan of the wrapper's own body does not report it again where it has
// no position (sharedLoadPos).
func (st *state) forwardShared(
	ins ssa.Instruction,
	wrapper *ssa.Function,
	args, bindings []ssa.Value,
	origins map[ssa.Value]sharedLoadOrigin,
	rootOf func(ssa.Value) (ssa.Value, bool),
	add func(*ssa.Function, ssa.Value, sharedLoadOrigin),
	emit func(ssa.Instruction, sharedLoadOrigin, string),
) {
	fw, ok := forwardedCall(wrapper, args, bindings)
	if !ok {
		return
	}
	if st.forwardedCalls == nil {
		st.forwardedCalls = map[ssa.Instruction]bool{}
	}
	st.forwardedCalls[fw.call] = true
	// An argument of the forwarded call is rooted where the wrapper parameter it comes from is: in
	// what the caller passed for that parameter.
	inner := func(v ssa.Value) (ssa.Value, bool) {
		p, ok := st.sharedLoadWalk().root(v, fw.params)
		if !ok {
			return nil, false
		}
		return rootOf(fw.outer[p])
	}
	st.propagateSharedToCallee(ins, fw.call.Common(), fw.callee, nil, origins, inner, add, emit)
}

// sharedLoadPos is where a SAFE005 finding at ins is reported, and whether it is reported at all.
// An instruction with no position that a wrapper forwards is reported at each call that enters the
// wrapper (forwardShared), so not here. Any other instruction with no position is reported at the
// Load its value came from: never at no position, where nobody could find it and every such finding
// in the package would be taken for one. located is false for that last resort, whose position does
// not tell two findings apart.
func (st *state) sharedLoadPos(ins ssa.Instruction, orig sharedLoadOrigin) (pos token.Pos, located, report bool) {
	if p := posOf(ins, ins); p.IsValid() {
		return p, true, true
	}
	if st.forwardedCalls[ins] {
		return token.NoPos, false, false
	}
	return orig.pos, false, true
}

// sharedLoadWalk is the walk the shared-load check follows a value back with. It reads no callee
// summaries: the result of a call into another package ends the walk.
func (st *state) sharedLoadWalk() *rootWalk {
	if st.loadWalk == nil {
		st.loadWalk = newRootWalk(nil)
	}
	return st.loadWalk
}

// sharedLoadCut is called before the shared-load check looks at ins, and returns what to run after:
// where a walk back from a value ins uses ran out of budget, the check did not find a shared Load
// behind it and did not rule one out, and that is reported at ins instead of passed as clean.
func (st *state) sharedLoadCut(ins ssa.Instruction) func() {
	w := st.sharedLoadWalk()
	w.cut = false
	return func() {
		if w.cut {
			st.report(posOf(ins, ins), ImmCut, fmt.Sprintf(
				"where a value used here comes from could not be followed to the end (more than %d values lead to it), so a write here through a value loaded from a shared container may be unreported",
				rootWalkBudget))
		}
	}
}

// propagateSharedToMethods passes on the shared values that the interface method call at ins hands
// over, as receiver or as argument, to the method of each type its receiver may be, so that a write
// in the method is found as it is behind a direct call. The types are the ones a trace of the
// receiver finds (resolveInterfaceTypes). A trace cut by its depth or its budget is reported, as it
// is for a called function value; a receiver whose types cannot be found for another reason (a
// parameter of a function with callers outside the package, say) is passed without a word, as a
// function value of unknown origin is.
func (st *state) propagateSharedToMethods(
	ins ssa.Instruction,
	fn *ssa.Function,
	cc *ssa.CallCommon,
	origins map[ssa.Value]sharedLoadOrigin,
	rootOf func(ssa.Value) (ssa.Value, bool),
	add func(*ssa.Function, ssa.Value, sharedLoadOrigin),
	emit func(ssa.Instruction, sharedLoadOrigin, string),
) {
	// The method takes its receiver first.
	args := append([]ssa.Value{cc.Value}, cc.Args...)
	// Only a call that hands over something shared needs its receiver traced.
	if !slices.ContainsFunc(args, func(a ssa.Value) bool { _, shared := rootOf(a); return shared }) {
		return
	}
	q := st.ask()
	concretes, _ := q.resolveInterfaceTypes(fn, cc.Value)
	if q.cut {
		// The methods found are checked below; those of the types the trace did not get to are not.
		st.report(posOf(ins, ins), ImmCut, fmt.Sprintf(
			"which types the receiver of this call may be could not be followed to the end (it comes through more than %d steps or from more than %d values), so what the methods not reached write through the shared value passed to them is not checked",
			originDepth, originBudget))
	}
	prog := st.ssa.Pkg.Prog
	for _, ct := range concretes {
		sel := prog.MethodSets.MethodSet(ct).Lookup(cc.Method.Pkg(), cc.Method.Name())
		if sel == nil {
			continue
		}
		if method := prog.MethodValue(sel); method != nil {
			st.propagateSharedToCallee(ins, &ssa.CallCommon{Value: method, Args: args}, method, nil, origins, rootOf, add, emit)
		}
	}
}
