package ownership

import (
	"fmt"
	"go/token"
	"go/types"
	"slices"
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
	// e.g. (*sync/atomic.Pointer[T]).Load, (*sync/atomic.Value).Load, (*sync.Map).Load): reading is
	// safe, while unguarded writes through non-shareable memory rooted at the loaded value violate SAFE005.
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
	"(*sync.Map).Load": {
		sharedReadOnlyLoad: true,
		displayName:        "(*sync.Map).Load",
	},
	"(*sync.Map).LoadAndDelete": {
		sharedReadOnlyLoad: true,
		displayName:        "(*sync.Map).LoadAndDelete",
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

// capMethod says what a call of an interface method could do: under capContracts, or by what a
// method of this package does in its own body.
type capMethod struct {
	known     bool // some entry or method with a contract could stand behind the call
	publishes bool // one that could publishes an argument
	loads     bool // one that could returns memory loaded from a container
}

// capMethodKeys lists the keys of capContracts' method entries by method name.
var capMethodKeys = func() map[string][]string {
	out := map[string][]string{}
	for full := range capContracts {
		if strings.HasPrefix(full, "(") {
			name := full[strings.LastIndex(full, ".")+1:]
			out[name] = append(out[name], full)
		}
	}
	return out
}()

// capMethodFor says what a call of method name on a value of the interface type recv could do. Such
// a call gets its contract through the method of that name on each type the receiver may be
// (evalInterfaceMethodContract): from capContracts, or for a method of this package from what the
// index recorded of its body. A type's method stands behind the interface's only with the same
// signature. So a capContracts entry or a method of this package counts if it has that name and,
// where the two signatures can be compared, that signature; a call nothing counts for has no
// contract whatever its receiver is.
func (st *state) capMethodFor(recv types.Type, name string) capMethod {
	var m capMethod
	var want *types.Signature
	if recv != nil {
		if iface, ok := recv.Underlying().(*types.Interface); ok {
			for i := range iface.NumMethods() {
				if im := iface.Method(i); im.Name() == name {
					want, _ = im.Type().(*types.Signature)
				}
			}
		}
	}
	for _, full := range capMethodKeys[name] {
		if want != nil && !st.capMethodMayHave(full, name, want) {
			continue
		}
		c := capContracts[full]
		m.known = true
		m.publishes = m.publishes || len(c.publishArgs) > 0
		m.loads = m.loads || c.sharedReadOnlyLoad
	}
	if ci := st.contracts; ci != nil {
		for _, fn := range ci.methodsNamed[name] {
			if want != nil && !sameSignature(fn.Signature, want, fn.TypeParams().Len() > 0 || fn.Signature.RecvTypeParams().Len() > 0) {
				continue
			}
			// What the method hands on to a function or method it is given counts as what that
			// one may do: it is not known here.
			if len(ci.directPublish[fn]) > 0 || len(ci.callForwards[fn]) > 0 {
				m.known, m.publishes = true, true
			}
			if ci.directSharedRet[fn] != "" || len(ci.retForwards[fn]) > 0 {
				m.known, m.loads = true, true
			}
		}
	}
	return m
}

// sameSignature reports whether a method with signature have can stand behind an interface method
// with signature want. For a method of a type with a type parameter, whose types depend on the
// instance, only the number of parameters and results is compared.
func sameSignature(have, want *types.Signature, generic bool) bool {
	if generic {
		return have.Params().Len() == want.Params().Len() && have.Results().Len() == want.Results().Len()
	}
	return have.Variadic() == want.Variadic() && types.Identical(have.Params(), want.Params()) && types.Identical(have.Results(), want.Results())
}

// capMethodMayHave reports whether the method that the capContracts key full names may have the
// signature want. It answers no only where it found the method, in a package this one imports,
// and the signatures differ: in the types of parameters and results, or for a method of a type
// with a type parameter, whose types depend on the instance, in their number.
func (st *state) capMethodMayHave(full, name string, want *types.Signature) bool {
	recv := strings.TrimPrefix(full[:strings.LastIndex(full, ")")], "(*")
	generic := strings.Contains(recv, "[")
	if generic {
		recv = recv[:strings.Index(recv, "[")]
	}
	dot := strings.LastIndex(recv, ".")
	if dot < 0 {
		return true
	}
	// Only what this package imports itself is looked at: that list is the same under every
	// driver, where the imports of imported packages are not.
	if st.contracts == nil {
		return true
	}
	pkg := st.contracts.directImports[recv[:dot]]
	if pkg == nil {
		return true
	}
	tn, ok := pkg.Scope().Lookup(recv[dot+1:]).(*types.TypeName)
	if !ok {
		return true
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return true
	}
	for i := range named.NumMethods() {
		meth := named.Method(i)
		if meth.Name() != name {
			continue
		}
		have, ok := meth.Type().(*types.Signature)
		if !ok {
			return true
		}
		return sameSignature(have, want, generic)
	}
	return true
}

func capContractNames() []string {
	out := make([]string, 0, len(capContracts))
	for name := range capContracts {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func isBoundMethodWrapper(fn *ssa.Function) bool {
	return fn != nil && strings.HasPrefix(fn.Synthetic, "bound method wrapper")
}

func isMethodThunk(fn *ssa.Function) bool {
	return fn != nil && (strings.HasPrefix(fn.Synthetic, "thunk") || strings.HasSuffix(fn.Name(), "$thunk"))
}

func canonicalBodyFn(fn *ssa.Function) *ssa.Function {
	if fn != nil && fn.Origin() != nil {
		return fn.Origin()
	}
	return fn
}

func shiftContractPublishArgs(c capContract, shift int) capContract {
	if shift == 0 || len(c.publishArgs) == 0 {
		return c
	}
	shifted := make([]int, 0, len(c.publishArgs))
	for _, idx := range c.publishArgs {
		if idx >= shift {
			shifted = append(shifted, idx-shift)
		}
	}
	c.publishArgs = shifted
	return c
}

func capContractFor(fn *ssa.Function) (capContract, bool) {
	if fn == nil {
		return capContract{}, false
	}
	bound := isBoundMethodWrapper(fn)
	thunk := isMethodThunk(fn)
	if o := fn.Origin(); o != nil {
		fn = o
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok {
		return capContract{}, false
	}
	if (bound || thunk) && obj.Signature().Recv() != nil && types.IsInterface(obj.Signature().Recv().Type()) {
		return capContract{}, false
	}
	c, ok := capContracts[obj.Origin().FullName()]
	if !ok {
		return capContract{}, false
	}
	if bound {
		c = shiftContractPublishArgs(c, 1)
	}
	return c, true
}

func isCapContractTransferWhat(what string) bool {
	return strings.HasPrefix(what, "published via ") ||
		strings.HasPrefix(what, "returned to ") ||
		strings.HasPrefix(what, "stored in shared ")
}

// unwrapPublishedArg strips interface/type wrappers (`any(x)`) so publishing a shareable value
// (e.g. `sync.Map.Store("k", 1)` or `context.WithValue(ctx, key{}, "id")`) sees the concrete
// shareable payload type rather than the outer `any` interface type. Every wrapper is stripped
// (each step goes to an operand, defined strictly earlier); stopping after 16 left a shareable
// value behind more conversions than that looking like an interface value that is published.
func unwrapPublishedArg(v ssa.Value) ssa.Value {
	for {
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
}

type pubArgSpec struct {
	argIdx             int
	what               string
	freezesToImm       bool
	delegatedToCallers bool
}

type resolvedCallContract struct {
	publishArgs        []pubArgSpec
	freshResult        bool
	sharedReadOnlyLoad bool
	suppressWrite      bool
	displayName        string
	// escapeArgs are the arguments some callee may let escape by something its publish does not
	// account for: a callable or receiver it hands the argument to that has no publishing answer
	// at this call, or something else its body does with it. It is a fact about any one callee,
	// like a publish, and matters for an argument the contract publishes: the publish then does
	// not stand for all that is done with it.
	escapeArgs []int
}

func (rc resolvedCallContract) alsoEscapes(i int) bool { return slices.Contains(rc.escapeArgs, i) }

type indexedCallSite struct {
	caller *ssa.Function
	ins    ssa.CallInstruction
}

type callForwardRule struct {
	targetParam int
	methodName  string
	slot        int
	argParam    int
}

type retForwardRule struct {
	targetParam int
	methodName  string
}

type structFieldKey struct {
	typ   string
	field int
}

type indexedValue struct {
	fn *ssa.Function
	v  ssa.Value
}

func derefStructTypeKey(ptrType types.Type) (string, bool) {
	if ptrType == nil {
		return "", false
	}
	p, ok := ptrType.Underlying().(*types.Pointer)
	if !ok {
		return "", false
	}
	elem := types.Unalias(p.Elem())
	if _, ok := elem.Underlying().(*types.Struct); !ok {
		return "", false
	}
	return types.TypeString(elem, nil), true
}

func structValueTypeKey(valType types.Type) (string, bool) {
	if valType == nil {
		return "", false
	}
	elem := types.Unalias(valType)
	if _, ok := elem.Underlying().(*types.Struct); !ok {
		return "", false
	}
	return types.TypeString(elem, nil), true
}

type contractIndex struct {
	closuresOf        map[*ssa.Function][]*ssa.MakeClosure
	callSitesOf       map[*ssa.Function][]indexedCallSite
	openCallers       map[*ssa.Function]bool
	globalStores      map[*ssa.Global][]ssa.Value
	globalFieldStores map[*ssa.Global]map[int][]ssa.Value
	structFieldStores map[structFieldKey][]indexedValue
	callForwards      map[*ssa.Function][]callForwardRule
	retForwards       map[*ssa.Function][]retForwardRule
	directPublish     map[*ssa.Function][]pubArgSpec
	directSharedRet   map[*ssa.Function]string
	directFreshRet    map[*ssa.Function]bool
	// retFreshButForwards holds the functions whose every returned value is fresh or the result
	// of one of their retForwards (retFreshness). A function in directFreshRet needs no entry.
	retFreshButForwards map[*ssa.Function]bool
	directImports       map[string]*types.Package
	localNamedTypes     []*types.Named
	boxedTypes          []types.Type

	// structConv links the struct types this package converts between, values or pointers, by
	// type key and in both directions (fieldStores).
	structConv map[string][]string

	// methodsNamed lists this package's methods that have a body, by name: the methods an
	// interface call may reach whose facts this index holds (capMethodFor).
	methodsNamed map[string][]*ssa.Function
}

// fieldStores returns every value stored into the given field of the struct type with key typ, and
// of every struct type this package converts to or from it, directly or through others. A
// conversion between two struct types keeps the fields, so what was stored through the one is read
// through the other, and a pointer converted the same way reaches the same memory from both sides.
func (ci *contractIndex) fieldStores(typ string, field int) []indexedValue {
	if len(ci.structConv[typ]) == 0 {
		return ci.structFieldStores[structFieldKey{typ: typ, field: field}]
	}
	var out []indexedValue
	seen := map[string]bool{typ: true}
	for work := []string{typ}; len(work) > 0; work = work[1:] {
		out = append(out, ci.structFieldStores[structFieldKey{typ: work[0], field: field}]...)
		for _, other := range ci.structConv[work[0]] {
			if !seen[other] {
				seen[other] = true
				work = append(work, other)
			}
		}
	}
	return out
}

func (st *state) buildContractIndex(fns []*ssa.Function) *contractIndex {
	ci := &contractIndex{
		closuresOf:        map[*ssa.Function][]*ssa.MakeClosure{},
		callSitesOf:       map[*ssa.Function][]indexedCallSite{},
		openCallers:       map[*ssa.Function]bool{},
		globalStores:      map[*ssa.Global][]ssa.Value{},
		globalFieldStores: map[*ssa.Global]map[int][]ssa.Value{},
		structFieldStores: map[structFieldKey][]indexedValue{},
		structConv:        map[string][]string{},
		callForwards:      map[*ssa.Function][]callForwardRule{},
		retForwards:       map[*ssa.Function][]retForwardRule{},
		directPublish:     map[*ssa.Function][]pubArgSpec{},
		directSharedRet:   map[*ssa.Function]string{},
		directFreshRet:    map[*ssa.Function]bool{},

		retFreshButForwards: map[*ssa.Function]bool{},
		directImports:       map[string]*types.Package{},
		methodsNamed:        map[string][]*ssa.Function{},
	}
	st.contracts = ci
	if st.pass != nil && st.pass.Pkg != nil {
		for _, imp := range st.pass.Pkg.Imports() {
			ci.directImports[imp.Path()] = imp
		}
		scope := st.pass.Pkg.Scope()
		for _, name := range scope.Names() {
			if tn, ok := scope.Lookup(name).(*types.TypeName); ok {
				if named, ok := types.Unalias(tn.Type()).(*types.Named); ok && named.TypeParams() == nil && !types.IsInterface(named) {
					ci.localNamedTypes = append(ci.localNamedTypes, named)
				}
			}
		}
	}

	addBoxedType := func(t types.Type) {
		if t == nil || types.IsInterface(t) {
			return
		}
		for _, ex := range ci.boxedTypes {
			if types.Identical(ex, t) {
				return
			}
		}
		ci.boxedTypes = append(ci.boxedTypes, t)
	}

	scanFns := fns
	if st.ssa != nil && st.ssa.Pkg != nil {
		if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil {
			scanFns = append(append([]*ssa.Function(nil), fns...), pkgInit)
		}
	}

	var opBuf [8]*ssa.Value
	for _, fn := range scanFns {
		bodyFn := canonicalBodyFn(fn)
		if bodyFn.Parent() == nil && token.IsExported(bodyFn.Name()) && (st.pass == nil || st.pass.Pkg == nil || st.pass.Pkg.Name() != "main") {
			ci.openCallers[bodyFn] = true
		}
		if name := bodyFn.Name(); bodyFn.Signature.Recv() != nil && len(bodyFn.Blocks) > 0 && !slices.Contains(ci.methodsNamed[name], bodyFn) {
			ci.methodsNamed[name] = append(ci.methodsNamed[name], bodyFn)
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			switch x := ins.(type) {
			case *ssa.ChangeType:
				from, okFrom := structValueTypeKey(x.X.Type())
				to, okTo := structValueTypeKey(x.Type())
				if !okFrom || !okTo {
					from, okFrom = derefStructTypeKey(x.X.Type())
					to, okTo = derefStructTypeKey(x.Type())
				}
				if okFrom && okTo && from != to {
					if !slices.Contains(ci.structConv[from], to) {
						ci.structConv[from] = append(ci.structConv[from], to)
						ci.structConv[to] = append(ci.structConv[to], from)
					}
				}
			case *ssa.MakeClosure:
				if mcFn, ok := x.Fn.(*ssa.Function); ok {
					ci.closuresOf[canonicalBodyFn(mcFn)] = append(ci.closuresOf[canonicalBodyFn(mcFn)], x)
				}
			case *ssa.MakeInterface:
				if x.X != nil {
					addBoxedType(x.X.Type())
				}
			case *ssa.Store:
				if g, ok := x.Addr.(*ssa.Global); ok {
					ci.globalStores[g] = append(ci.globalStores[g], x.Val)
				} else if fa, ok := x.Addr.(*ssa.FieldAddr); ok {
					if g, ok := fa.X.(*ssa.Global); ok {
						if ci.globalFieldStores[g] == nil {
							ci.globalFieldStores[g] = map[int][]ssa.Value{}
						}
						ci.globalFieldStores[g][fa.Field] = append(ci.globalFieldStores[g][fa.Field], x.Val)
					}
					if keyStr, ok := derefStructTypeKey(fa.X.Type()); ok {
						fk := structFieldKey{typ: keyStr, field: fa.Field}
						ci.structFieldStores[fk] = append(ci.structFieldStores[fk], indexedValue{fn: bodyFn, v: x.Val})
					}
				}
			case ssa.CallInstruction:
				cc := x.Common()
				if !cc.IsInvoke() {
					if callee, _ := directCallee(cc.Value); callee != nil {
						target := canonicalBodyFn(callee)
						ci.callSitesOf[target] = append(ci.callSitesOf[target], indexedCallSite{
							caller: bodyFn,
							ins:    x,
						})
					}
				}
			}
			var directFn *ssa.Function
			if c, ok := ins.(ssa.CallInstruction); ok && !c.Common().IsInvoke() {
				if f, ok := c.Common().Value.(*ssa.Function); ok {
					directFn = canonicalBodyFn(f)
				}
			}
			for _, op := range ins.Operands(opBuf[:0]) {
				if op == nil {
					continue
				}
				if f, ok := (*op).(*ssa.Function); ok {
					bf := canonicalBodyFn(f)
					if bf != directFn {
						ci.openCallers[bf] = true
					}
				}
			}
		})
	}

	addCallForward := func(fn *ssa.Function, r callForwardRule) bool {
		for _, ex := range ci.callForwards[fn] {
			if ex == r {
				return false
			}
		}
		ci.callForwards[fn] = append(ci.callForwards[fn], r)
		return true
	}
	addRetForward := func(fn *ssa.Function, r retForwardRule) bool {
		for _, ex := range ci.retForwards[fn] {
			if ex == r {
				return false
			}
		}
		ci.retForwards[fn] = append(ci.retForwards[fn], r)
		return true
	}
	addDirectPublish := func(fn *ssa.Function, spec pubArgSpec) bool {
		specs := ci.directPublish[fn]
		for i := range specs {
			if specs[i].argIdx == spec.argIdx {
				if specs[i].freezesToImm && !spec.freezesToImm {
					specs[i].freezesToImm = false
					return true
				}
				return false
			}
		}
		ci.directPublish[fn] = append(specs, pubArgSpec{
			argIdx:       spec.argIdx,
			what:         spec.what,
			freezesToImm: spec.freezesToImm,
		})
		return true
	}

	// Base forwarding rules inside each function body.
	for _, fn := range fns {
		bodyFn := canonicalBodyFn(fn)
		if len(bodyFn.Blocks) == 0 {
			continue
		}
		retCalls := returnedCallsOf(bodyFn)
		forEachInstr(bodyFn, func(ins ssa.Instruction) {
			cIns, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := cIns.Common()
			_, isRetCall := retCalls[cIns]
			if isRetCall {
				if rule, ok := retForwardOf(cc, bodyFn); ok {
					addRetForward(bodyFn, rule)
				}
			}
			if cc.IsInvoke() {
				pRecv := paramFlowIndex(cc.Value, bodyFn)
				if pRecv >= 0 {
					mName := cc.Method.Name()
					for slot, a := range cc.Args {
						if pArg := paramFlowIndex(a, bodyFn); pArg >= 0 {
							addCallForward(bodyFn, callForwardRule{
								targetParam: pRecv,
								methodName:  mName,
								slot:        slot,
								argParam:    pArg,
							})
						}
					}
				}
				return
			}
			if mc, ok := cc.Value.(*ssa.MakeClosure); ok {
				if mcFn, ok := mc.Fn.(*ssa.Function); ok && isBoundMethodWrapper(mcFn) && len(mc.Bindings) == 1 {
					if mObj, ok := mcFn.Object().(*types.Func); ok && mObj.Signature().Recv() != nil && types.IsInterface(mObj.Signature().Recv().Type()) {
						if pRecv := paramFlowIndex(mc.Bindings[0], bodyFn); pRecv >= 0 {
							mName := mObj.Name()
							for slot, a := range cc.Args {
								if pArg := paramFlowIndex(a, bodyFn); pArg >= 0 {
									addCallForward(bodyFn, callForwardRule{
										targetParam: pRecv,
										methodName:  mName,
										slot:        slot,
										argParam:    pArg,
									})
								}
							}
						}
						return
					}
				}
			}
			if pFn := paramFlowIndex(cc.Value, bodyFn); pFn >= 0 {
				for slot, a := range cc.Args {
					if pArg := paramFlowIndex(a, bodyFn); pArg >= 0 {
						addCallForward(bodyFn, callForwardRule{
							targetParam: pFn,
							methodName:  "",
							slot:        slot,
							argParam:    pArg,
						})
					}
				}
			}
			if isRetCall {
				if callee := cc.StaticCallee(); callee != nil {
					if contract, has := capContractFor(callee); has && contract.sharedReadOnlyLoad {
						ci.directSharedRet[bodyFn] = contract.displayName
					}
				}
			}
		})
	}

	// Fixpoint propagation across multi-hop higher-order helpers, closures, method values, and interfaces.
	// The rounds repeat until one adds nothing, with no limit on their number: every change adds a
	// rule to a list that holds each rule once, sets a flag that is never cleared, or clears
	// freezesToImm, which is never set again, so there are finitely many. With a limit (four rounds
	// per function plus 16) a helper that needs one round per parameter was left half described.
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			bodyFn := canonicalBodyFn(fn)
			if len(bodyFn.Blocks) == 0 {
				continue
			}
			retCalls := returnedCallsOf(bodyFn)
			forEachInstr(bodyFn, func(ins ssa.Instruction) {
				cIns, ok := ins.(ssa.CallInstruction)
				if !ok {
					return
				}
				cc := cIns.Common()
				_, isRetCall := retCalls[cIns]
				targetIsParam := paramFlowIndex(cc.Value, bodyFn) >= 0
				if !targetIsParam {
					if mc, ok := cc.Value.(*ssa.MakeClosure); ok {
						if mcFn, ok := mc.Fn.(*ssa.Function); ok && isBoundMethodWrapper(mcFn) && len(mc.Bindings) == 1 {
							targetIsParam = paramFlowIndex(mc.Bindings[0], bodyFn) >= 0
						}
					}
				}
				if rc, hasContract := st.ask().resolveCallContract(bodyFn, cc); hasContract {
					for _, spec := range rc.publishArgs {
						if spec.delegatedToCallers || spec.argIdx < 0 || spec.argIdx >= len(cc.Args) {
							continue
						}
						if pArg := paramFlowIndex(unwrapPublishedArg(cc.Args[spec.argIdx]), bodyFn); pArg >= 0 {
							if addDirectPublish(bodyFn, pubArgSpec{
								argIdx:       pArg,
								what:         spec.what,
								freezesToImm: spec.freezesToImm,
							}) {
								changed = true
							}
						}
					}
					if isRetCall && !targetIsParam && rc.sharedReadOnlyLoad && ci.directSharedRet[bodyFn] == "" {
						ci.directSharedRet[bodyFn] = rc.displayName
						changed = true
					}
				}
				if cc.IsInvoke() {
					return
				}
				callee, _ := directCallee(cc.Value)
				if callee == nil {
					return
				}
				calleeBody := canonicalBodyFn(callee)
				for _, cf := range ci.callForwards[calleeBody] {
					if cf.targetParam >= len(cc.Args) || cf.argParam >= len(cc.Args) {
						continue
					}
					outerTP := paramFlowIndex(cc.Args[cf.targetParam], bodyFn)
					outerAP := paramFlowIndex(cc.Args[cf.argParam], bodyFn)
					if outerTP >= 0 && outerAP >= 0 {
						if addCallForward(bodyFn, callForwardRule{
							targetParam: outerTP,
							methodName:  cf.methodName,
							slot:        cf.slot,
							argParam:    outerAP,
						}) {
							changed = true
						}
					}
				}
				if isRetCall {
					for _, rf := range ci.retForwards[calleeBody] {
						if rf.targetParam >= len(cc.Args) {
							continue
						}
						if outerTP := paramFlowIndex(cc.Args[rf.targetParam], bodyFn); outerTP >= 0 {
							if addRetForward(bodyFn, retForwardRule{
								targetParam: outerTP,
								methodName:  rf.methodName,
							}) {
								changed = true
							}
						}
					}
					if desc := ci.directSharedRet[calleeBody]; desc != "" && ci.directSharedRet[bodyFn] == "" {
						ci.directSharedRet[bodyFn] = desc
						changed = true
					}
				}
			})
			if !ci.directFreshRet[bodyFn] {
				// Both sets only grow. A function is put in retFreshButForwards after its own
				// forwards are recorded (above, in this round), and a caller leans on that entry
				// only once it is there, so no caller is judged against a list still growing.
				switch fnReturnFreshness(st, bodyFn) {
				case retFresh:
					ci.directFreshRet[bodyFn] = true
					changed = true
				case retFreshButForwards:
					if !ci.retFreshButForwards[bodyFn] {
						ci.retFreshButForwards[bodyFn] = true
						changed = true
					}
				}
			}
		}
	}

	return ci
}

// retFreshness is how far a function is known to return a fresh value.
type retFreshness uint8

const (
	retNotFresh retFreshness = iota
	// retFreshButForwards: every value the function may return is fresh, provided the calls of, or
	// on, its parameters that produce some of them return fresh values. Those calls are the
	// function's retForwards, and whether they do is known only at a call of the function.
	retFreshButForwards
	retFresh
)

// retForwardOf is the rule for a call whose target is one of bodyFn's parameters: a method called
// on it, a method value bound to it, or the parameter called as a function.
func retForwardOf(cc *ssa.CallCommon, bodyFn *ssa.Function) (retForwardRule, bool) {
	if cc.IsInvoke() {
		if pRecv := paramFlowIndex(cc.Value, bodyFn); pRecv >= 0 {
			return retForwardRule{targetParam: pRecv, methodName: cc.Method.Name()}, true
		}
		return retForwardRule{}, false
	}
	if mc, ok := cc.Value.(*ssa.MakeClosure); ok {
		if mcFn, ok := mc.Fn.(*ssa.Function); ok && isBoundMethodWrapper(mcFn) && len(mc.Bindings) == 1 {
			if mObj, ok := mcFn.Object().(*types.Func); ok && mObj.Signature().Recv() != nil && types.IsInterface(mObj.Signature().Recv().Type()) {
				if pRecv := paramFlowIndex(mc.Bindings[0], bodyFn); pRecv >= 0 {
					return retForwardRule{targetParam: pRecv, methodName: mObj.Name()}, true
				}
				return retForwardRule{}, false
			}
		}
	}
	if pFn := paramFlowIndex(cc.Value, bodyFn); pFn >= 0 {
		return retForwardRule{targetParam: pFn, methodName: ""}, true
	}
	return retForwardRule{}, false
}

// forwardsLandOnParams reports whether cc, in bodyFn, calls a function whose result is fresh but
// for its forwards, with each forwarded target one of bodyFn's own parameters. The question of
// whether the result is fresh then moves to the callers of bodyFn, whose retForwards hold the
// same rules against its parameters (the index adds them where it sees this call returned).
func (ci *contractIndex) forwardsLandOnParams(cc *ssa.CallCommon, bodyFn *ssa.Function) bool {
	if cc.IsInvoke() {
		return false
	}
	callee, _ := directCallee(cc.Value)
	if callee == nil {
		return false
	}
	calleeBody := canonicalBodyFn(callee)
	rules := ci.retForwards[calleeBody]
	if !ci.retFreshButForwards[calleeBody] || len(rules) == 0 {
		return false
	}
	for _, rf := range rules {
		if rf.targetParam >= len(cc.Args) {
			return false
		}
		outer := paramFlowIndex(cc.Args[rf.targetParam], bodyFn)
		if outer < 0 || !slices.Contains(ci.retForwards[bodyFn], retForwardRule{targetParam: outer, methodName: rf.methodName}) {
			return false
		}
	}
	return true
}

// fnReturnFreshness is the least fresh of the values bodyFn returns.
func fnReturnFreshness(st *state, bodyFn *ssa.Function) retFreshness {
	if bodyFn == nil || len(bodyFn.Blocks) == 0 {
		return retNotFresh
	}
	sawRet := false
	least := retFresh
	forEachInstr(bodyFn, func(ins ssa.Instruction) {
		ret, ok := ins.(*ssa.Return)
		if !ok {
			return
		}
		sawRet = true
		if len(ret.Results) == 0 {
			least = retNotFresh
			return
		}
		least = min(least, returnedValueFreshness(st, bodyFn, ret.Results[0], map[ssa.Value]bool{}))
	})
	if !sawRet {
		return retNotFresh
	}
	return least
}

// returnedValueFreshness visits each value once (seen), which is what ends it; it used to give up
// 13 steps in as well, and a fresh value returned through more conversions was not fresh.
func returnedValueFreshness(st *state, bodyFn *ssa.Function, v ssa.Value, seen map[ssa.Value]bool) retFreshness {
	if v == nil || seen[v] {
		return retNotFresh
	}
	seen[v] = true
	switch x := v.(type) {
	case *ssa.Call:
		cc := x.Common()
		if rule, ok := retForwardOf(cc, bodyFn); ok {
			if slices.Contains(st.contracts.retForwards[bodyFn], rule) {
				return retFreshButForwards
			}
			return retNotFresh
		}
		if mc, ok := cc.Value.(*ssa.MakeClosure); ok {
			if mcFn, ok := mc.Fn.(*ssa.Function); ok && isBoundMethodWrapper(mcFn) && len(mc.Bindings) == 1 && paramFlowIndex(mc.Bindings[0], bodyFn) >= 0 {
				return retNotFresh
			}
		}
		if rc, ok := st.ask().resolveCallContract(bodyFn, cc); ok && rc.freshResult {
			return retFresh
		}
		if st.contracts.forwardsLandOnParams(cc, bodyFn) {
			return retFreshButForwards
		}
		return retNotFresh
	case *ssa.Extract:
		if x.Index == 0 {
			return returnedValueFreshness(st, bodyFn, x.Tuple, seen)
		}
		return retNotFresh
	case *ssa.MakeInterface:
		return returnedValueFreshness(st, bodyFn, x.X, seen)
	case *ssa.ChangeInterface:
		return returnedValueFreshness(st, bodyFn, x.X, seen)
	case *ssa.ChangeType:
		return returnedValueFreshness(st, bodyFn, x.X, seen)
	case *ssa.TypeAssert:
		return returnedValueFreshness(st, bodyFn, x.X, seen)
	case *ssa.Phi:
		if len(x.Edges) == 0 {
			return retNotFresh
		}
		least := retFresh
		for _, e := range x.Edges {
			least = min(least, returnedValueFreshness(st, bodyFn, e, seen))
		}
		return least
	default:
		return retNotFresh
	}
}

func returnedCallsOf(fn *ssa.Function) map[ssa.CallInstruction]bool {
	out := map[ssa.CallInstruction]bool{}
	forEachInstr(fn, func(ins ssa.Instruction) {
		ret, ok := ins.(*ssa.Return)
		if !ok || len(ret.Results) == 0 {
			return
		}
		collectReturnedCalls(ret.Results[0], out, 0, map[ssa.Value]bool{})
	})
	return out
}

// collectReturnedCalls visits each value once (seen), which is what ends it; it used to give up 13
// steps in as well, and a call whose result is returned through more conversions was not found.
func collectReturnedCalls(v ssa.Value, out map[ssa.CallInstruction]bool, depth int, seen map[ssa.Value]bool) {
	if v == nil || seen[v] {
		return
	}
	seen[v] = true
	switch x := v.(type) {
	case *ssa.Call:
		out[x] = true
	case *ssa.Extract:
		if x.Index == 0 {
			collectReturnedCalls(x.Tuple, out, depth+1, seen)
		}
	case *ssa.MakeInterface:
		collectReturnedCalls(x.X, out, depth+1, seen)
	case *ssa.ChangeInterface:
		collectReturnedCalls(x.X, out, depth+1, seen)
	case *ssa.ChangeType:
		collectReturnedCalls(x.X, out, depth+1, seen)
	case *ssa.TypeAssert:
		collectReturnedCalls(x.X, out, depth+1, seen)
	case *ssa.Phi:
		for _, e := range x.Edges {
			collectReturnedCalls(e, out, depth+1, seen)
		}
	}
}

func paramFlowIndex(v ssa.Value, fn *ssa.Function) int {
	if fn == nil || v == nil {
		return -1
	}
	return paramFlowIndexSeen(v, fn, 0, map[ssa.Value]bool{})
}

// paramFlowIndexSeen visits each value once (seen), which is what ends it. It used to give up after
// 16 steps along one chain and 13 variables deep as well, and a parameter handed on through more
// than that was not seen to be the parameter.
func paramFlowIndexSeen(v ssa.Value, fn *ssa.Function, depth int, seen map[ssa.Value]bool) int {
	for {
		if v == nil || seen[v] {
			return -1
		}
		seen[v] = true
		for i, p := range fn.Params {
			if v == p {
				return i
			}
		}
		switch x := v.(type) {
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeInterface:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		case *ssa.TypeAssert:
			v = x.X
		case *ssa.Convert:
			v = x.X
		case *ssa.Phi:
			idx := -1
			for _, e := range x.Edges {
				ei := paramFlowIndexSeen(e, fn, depth+1, seen)
				if ei < 0 || (idx >= 0 && ei != idx) {
					return -1
				}
				idx = ei
			}
			return idx
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return -1
			}
			alloc, ok := x.X.(*ssa.Alloc)
			if !ok || alloc.Referrers() == nil {
				return -1
			}
			idx := -1
			for _, ref := range *alloc.Referrers() {
				stIns, ok := ref.(*ssa.Store)
				if !ok || stIns.Addr != alloc {
					continue
				}
				si := paramFlowIndexSeen(stIns.Val, fn, depth+1, seen)
				if si < 0 || (idx >= 0 && si != idx) {
					return -1
				}
				idx = si
			}
			return idx
		default:
			return -1
		}
	}
}

type valVisitKey struct {
	fn *ssa.Function
	v  ssa.Value
}

// question is one thing a check asks about where a value comes from or what a call does. Answering
// it may take many traces, nested and each with its own visited set; every one of them marks the
// question when it stops at a limit (originBudget, originDepth) and not at the end of what there
// is. The check that asked reads cut once, afterwards.
//
// The traces and the contract resolvers that start them are methods of question, so none can run
// without one. cut used to be a field of state that a check cleared before tracing and read
// after: a check that forgot to clear it read another check's cut, and one that did not read it
// had nothing to show that it had decided not to.
type question struct {
	*state
	cut bool
}

// ask starts a question.
func (st *state) ask() *question { return &question{state: st} }

// originBudget is the number of values one trace of where a value comes from (traceSSAOrigins,
// tracePtrBaseOrigins) may visit. A trace that spends it answers "incomplete" and marks its
// question as cut, so that a check that cannot act on "incomplete" can still say it was cut.
const originBudget = 96

// originDepth is how many steps deep those traces and traceStructFieldValueOrigins go. It is what
// ends one cycle that the visited set does not (a field of a struct value, read through a pointer
// whose trace starts a new visited set, read from a stored struct value again), so it stays. A
// trace that reaches it answers "incomplete" and marks its question as cut, like one out of budget.
const originDepth = 14

type leafValue struct {
	fn *ssa.Function
	v  ssa.Value
}

func (st *question) traceSSAOrigins(fn *ssa.Function, v ssa.Value, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	if v == nil {
		return nil, true
	}
	if depth > originDepth {
		st.cut = true
		return nil, true
	}
	bodyFn := canonicalBodyFn(fn)
	key := valVisitKey{fn: bodyFn, v: v}
	if seen[key] {
		return nil, false
	}
	if len(seen) >= originBudget {
		// Out of budget: what v may be is not known. This used to answer "nothing more, and that
		// is all", which made a callee chosen among more values than the budget look fully known.
		st.cut = true
		return nil, true
	}
	seen[key] = true

	switch x := v.(type) {
	case *ssa.Const:
		return nil, true
	case *ssa.Function, *ssa.MakeClosure, *ssa.MakeInterface:
		return []leafValue{{fn: bodyFn, v: x}}, false
	case *ssa.ChangeType:
		return st.traceSSAOrigins(bodyFn, x.X, depth+1, seen)
	case *ssa.ChangeInterface:
		return st.traceSSAOrigins(bodyFn, x.X, depth+1, seen)
	case *ssa.TypeAssert:
		return st.traceSSAOrigins(bodyFn, x.X, depth+1, seen)
	case *ssa.Phi:
		var out []leafValue
		incomplete := len(x.Edges) == 0
		for _, e := range x.Edges {
			sub, inc := st.traceSSAOrigins(bodyFn, e, depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.FreeVar:
		if st.contracts == nil || bodyFn == nil {
			return nil, true
		}
		fvIdx := -1
		for i, fv := range bodyFn.FreeVars {
			if fv == x {
				fvIdx = i
				break
			}
		}
		if fvIdx < 0 {
			return nil, true
		}
		mcs := st.contracts.closuresOf[bodyFn]
		if len(mcs) == 0 {
			return nil, true
		}
		var out []leafValue
		incomplete := false
		for _, mc := range mcs {
			if fvIdx < len(mc.Bindings) {
				sub, inc := st.traceSSAOrigins(mc.Parent(), mc.Bindings[fvIdx], depth+1, seen)
				out = append(out, sub...)
				if inc {
					incomplete = true
				}
			} else {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.Parameter:
		if st.contracts == nil || bodyFn == nil {
			return nil, true
		}
		pIdx := -1
		for i, p := range bodyFn.Params {
			if p == x {
				pIdx = i
				break
			}
		}
		if pIdx < 0 {
			return nil, true
		}
		sites := st.contracts.callSitesOf[bodyFn]
		if len(sites) == 0 {
			return nil, true
		}
		incomplete := st.contracts.openCallers[bodyFn]
		var out []leafValue
		for _, site := range sites {
			cc := site.ins.Common()
			argIdx := pIdx
			if _, isMC := cc.Value.(*ssa.MakeClosure); isMC && isBoundMethodWrapper(cc.StaticCallee()) {
				if pIdx == 0 {
					incomplete = true
					continue
				}
				argIdx = pIdx - 1
			}
			if argIdx < 0 || argIdx >= len(cc.Args) {
				incomplete = true
				continue
			}
			sub, inc := st.traceSSAOrigins(site.caller, cc.Args[argIdx], depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.UnOp:
		switch x.Op {
		case token.MUL:
			return st.traceDerefOrigins(bodyFn, x.X, depth+1, seen)
		case token.ARROW:
			return st.traceChanRecvOrigins(bodyFn, x.X, depth+1, seen)
		}
		return nil, true
	case *ssa.Field:
		return st.traceStructFieldValueOrigins(bodyFn, x.X, x.Field, depth+1, seen)
	case *ssa.Lookup:
		return st.traceMapLookupOrigins(bodyFn, x.X, depth+1, seen)
	case *ssa.Extract:
		if lk, ok := x.Tuple.(*ssa.Lookup); ok && x.Index == 0 {
			return st.traceMapLookupOrigins(bodyFn, lk.X, depth+1, seen)
		}
		if recv, ok := x.Tuple.(*ssa.UnOp); ok && recv.Op == token.ARROW && x.Index == 0 {
			return st.traceChanRecvOrigins(bodyFn, recv.X, depth+1, seen)
		}
		if callIns, ok := x.Tuple.(*ssa.Call); ok {
			return st.traceCallResultOrigins(bodyFn, callIns, x.Index, depth+1, seen)
		}
		return nil, true
	case *ssa.Call:
		return st.traceCallResultOrigins(bodyFn, x, 0, depth+1, seen)
	default:
		return nil, true
	}
}

func (st *question) tracePtrBaseOrigins(fn *ssa.Function, v ssa.Value, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	if v == nil {
		return nil, true
	}
	if depth > originDepth {
		st.cut = true
		return nil, true
	}
	bodyFn := canonicalBodyFn(fn)
	key := valVisitKey{fn: bodyFn, v: v}
	if seen[key] {
		return nil, false
	}
	if len(seen) >= originBudget {
		st.cut = true // as in traceSSAOrigins: out of budget is not "that is all"
		return nil, true
	}
	seen[key] = true

	switch x := v.(type) {
	case *ssa.Const:
		return nil, true
	case *ssa.Alloc, *ssa.Global:
		return []leafValue{{fn: bodyFn, v: x}}, false
	case *ssa.ChangeType:
		return st.tracePtrBaseOrigins(bodyFn, x.X, depth+1, seen)
	case *ssa.TypeAssert:
		return st.tracePtrBaseOrigins(bodyFn, x.X, depth+1, seen)
	case *ssa.Phi:
		var out []leafValue
		incomplete := len(x.Edges) == 0
		for _, e := range x.Edges {
			sub, inc := st.tracePtrBaseOrigins(bodyFn, e, depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.FreeVar:
		if st.contracts == nil || bodyFn == nil {
			return nil, true
		}
		fvIdx := -1
		for i, fv := range bodyFn.FreeVars {
			if fv == x {
				fvIdx = i
				break
			}
		}
		if fvIdx < 0 {
			return nil, true
		}
		mcs := st.contracts.closuresOf[bodyFn]
		if len(mcs) == 0 {
			return nil, true
		}
		var out []leafValue
		incomplete := false
		for _, mc := range mcs {
			if fvIdx < len(mc.Bindings) {
				sub, inc := st.tracePtrBaseOrigins(mc.Parent(), mc.Bindings[fvIdx], depth+1, seen)
				out = append(out, sub...)
				if inc {
					incomplete = true
				}
			} else {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.Parameter:
		if st.contracts == nil || bodyFn == nil {
			return nil, true
		}
		pIdx := -1
		for i, p := range bodyFn.Params {
			if p == x {
				pIdx = i
				break
			}
		}
		if pIdx < 0 {
			return nil, true
		}
		sites := st.contracts.callSitesOf[bodyFn]
		if len(sites) == 0 {
			return nil, true
		}
		incomplete := st.contracts.openCallers[bodyFn]
		var out []leafValue
		for _, site := range sites {
			cc := site.ins.Common()
			argIdx := pIdx
			if _, isMC := cc.Value.(*ssa.MakeClosure); isMC && isBoundMethodWrapper(cc.StaticCallee()) {
				if pIdx == 0 {
					incomplete = true
					continue
				}
				argIdx = pIdx - 1
			}
			if argIdx < 0 || argIdx >= len(cc.Args) {
				incomplete = true
				continue
			}
			sub, inc := st.tracePtrBaseOrigins(site.caller, cc.Args[argIdx], depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.UnOp:
		if x.Op == token.MUL {
			if alloc, ok := x.X.(*ssa.Alloc); ok && alloc.Referrers() != nil {
				var out []leafValue
				incomplete := false
				for _, ref := range *alloc.Referrers() {
					if stIns, ok := ref.(*ssa.Store); ok && stIns.Addr == alloc {
						pFn := stIns.Parent()
						if pFn == nil {
							pFn = bodyFn
						}
						sub, inc := st.tracePtrBaseOrigins(pFn, stIns.Val, depth+1, seen)
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
			if g, ok := x.X.(*ssa.Global); ok && st.contracts != nil {
				vals := st.contracts.globalStores[g]
				incomplete := len(vals) == 0 || token.IsExported(g.Name())
				var out []leafValue
				for _, sv := range vals {
					var parentFn *ssa.Function
					if ins, ok := sv.(ssa.Instruction); ok {
						parentFn = ins.Parent()
					}
					sub, inc := st.tracePtrBaseOrigins(parentFn, sv, depth+1, seen)
					out = append(out, sub...)
					if inc {
						incomplete = true
					}
				}
				return out, incomplete
			}
		}
		return nil, true
	case *ssa.Extract:
		if callIns, ok := x.Tuple.(*ssa.Call); ok {
			return st.tracePtrCallResultOrigins(bodyFn, callIns, x.Index, depth+1, seen)
		}
		return nil, true
	case *ssa.Call:
		return st.tracePtrCallResultOrigins(bodyFn, x, 0, depth+1, seen)
	default:
		return nil, true
	}
}

func (st *question) tracePtrCallResultOrigins(caller *ssa.Function, callIns *ssa.Call, resIdx int, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
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
			sub, inc := st.tracePtrBaseOrigins(caller, callIns.Call.Args[pIdx], depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
			return
		}
		sub, inc := st.tracePtrBaseOrigins(bodyFn, rv, depth+1, seen)
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

func (st *question) readFieldFromBasePtrLeaf(lf leafValue, field int, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	switch base := lf.v.(type) {
	case *ssa.Alloc:
		if base.Referrers() == nil {
			return nil, true
		}
		var out []leafValue
		incomplete := false
		for _, ref := range *base.Referrers() {
			if fa, ok := ref.(*ssa.FieldAddr); ok && fa.Field == field && fa.Referrers() != nil {
				for _, r2 := range *fa.Referrers() {
					if stIns, ok := r2.(*ssa.Store); ok && stIns.Addr == fa {
						pFn := stIns.Parent()
						if pFn == nil {
							pFn = lf.fn
						}
						sub, inc := st.traceSSAOrigins(pFn, stIns.Val, depth+1, seen)
						out = append(out, sub...)
						if inc {
							incomplete = true
						}
					}
				}
			}
			if stIns, ok := ref.(*ssa.Store); ok && stIns.Addr == base {
				pFn := stIns.Parent()
				if pFn == nil {
					pFn = lf.fn
				}
				sub, inc := st.traceStructFieldValueOrigins(pFn, stIns.Val, field, depth+1, seen)
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
	case *ssa.Global:
		if st.contracts == nil {
			return nil, true
		}
		vals := st.contracts.globalFieldStores[base][field]
		wholeVals := st.contracts.globalStores[base]
		incomplete := (len(vals) == 0 && len(wholeVals) == 0) || token.IsExported(base.Name())
		var out []leafValue
		for _, sv := range vals {
			var parentFn *ssa.Function
			if ins, ok := sv.(ssa.Instruction); ok {
				parentFn = ins.Parent()
			}
			sub, inc := st.traceSSAOrigins(parentFn, sv, depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		for _, wv := range wholeVals {
			var parentFn *ssa.Function
			if ins, ok := wv.(ssa.Instruction); ok {
				parentFn = ins.Parent()
			}
			sub, inc := st.traceStructFieldValueOrigins(parentFn, wv, field, depth+1, seen)
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

func (st *question) traceStructPtrFieldOrigins(fn *ssa.Function, ptr ssa.Value, field int, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	bases, incomplete := st.tracePtrBaseOrigins(fn, ptr, depth+1, map[valVisitKey]bool{})
	var out []leafValue
	for _, b := range bases {
		sub, inc := st.readFieldFromBasePtrLeaf(b, field, depth+1, seen)
		out = append(out, sub...)
		if inc {
			incomplete = true
		}
	}
	if len(out) == 0 && st.contracts != nil && ptr != nil {
		if keyStr, ok := derefStructTypeKey(ptr.Type()); ok {
			for _, iv := range st.contracts.fieldStores(keyStr, field) {
				sub, _ := st.traceSSAOrigins(iv.fn, iv.v, depth+1, seen)
				out = append(out, sub...)
			}
			incomplete = true
		}
	}
	if len(out) == 0 {
		incomplete = true
	}
	return out, incomplete
}

func (st *question) traceDerefOrigins(fn *ssa.Function, addr ssa.Value, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	switch a := addr.(type) {
	case *ssa.Alloc:
		if a.Referrers() == nil {
			return nil, true
		}
		var out []leafValue
		incomplete := false
		for _, ref := range *a.Referrers() {
			if stIns, ok := ref.(*ssa.Store); ok && stIns.Addr == a {
				pFn := stIns.Parent()
				if pFn == nil {
					pFn = fn
				}
				sub, inc := st.traceSSAOrigins(pFn, stIns.Val, depth+1, seen)
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
	case *ssa.FreeVar:
		// Captured mutable local cell (`*T`): follow closure binding back to parent's `*ssa.Alloc`.
		if st.contracts == nil || fn == nil {
			return nil, true
		}
		fvIdx := -1
		for i, fv := range fn.FreeVars {
			if fv == a {
				fvIdx = i
				break
			}
		}
		if fvIdx < 0 {
			return nil, true
		}
		var out []leafValue
		incomplete := len(st.contracts.closuresOf[fn]) == 0
		for _, mc := range st.contracts.closuresOf[fn] {
			if fvIdx < len(mc.Bindings) {
				sub, inc := st.traceDerefOrigins(mc.Parent(), mc.Bindings[fvIdx], depth+1, seen)
				out = append(out, sub...)
				if inc {
					incomplete = true
				}
			} else {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.Global:
		if st.contracts == nil {
			return nil, true
		}
		vals := st.contracts.globalStores[a]
		incomplete := len(vals) == 0 || token.IsExported(a.Name())
		var out []leafValue
		for _, sv := range vals {
			var parentFn *ssa.Function
			if ins, ok := sv.(ssa.Instruction); ok {
				parentFn = ins.Parent()
			}
			sub, inc := st.traceSSAOrigins(parentFn, sv, depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.FieldAddr:
		return st.traceStructPtrFieldOrigins(fn, a.X, a.Field, depth, seen)
	case *ssa.IndexAddr:
		base := a.X
		if sl, ok := base.(*ssa.Slice); ok {
			base = sl.X
		}
		if baseAlloc, ok := base.(*ssa.Alloc); ok && baseAlloc.Referrers() != nil {
			var out []leafValue
			collectIndexStores := func(refs *[]ssa.Instruction) {
				if refs == nil {
					return
				}
				for _, r := range *refs {
					if ia, ok := r.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
						for _, r2 := range *ia.Referrers() {
							if stIns, ok := r2.(*ssa.Store); ok && stIns.Addr == ia {
								sub, _ := st.traceSSAOrigins(fn, stIns.Val, depth+1, seen)
								out = append(out, sub...)
							}
						}
					}
					if sl, ok := r.(*ssa.Slice); ok {
						for _, sr := range *sl.Referrers() {
							if ia, ok := sr.(*ssa.IndexAddr); ok && ia.Referrers() != nil {
								for _, r2 := range *ia.Referrers() {
									if stIns, ok := r2.(*ssa.Store); ok && stIns.Addr == ia {
										sub, _ := st.traceSSAOrigins(fn, stIns.Val, depth+1, seen)
										out = append(out, sub...)
									}
								}
							}
						}
					}
				}
			}
			collectIndexStores(baseAlloc.Referrers())
			return out, true
		}
	}
	return nil, true
}

func (st *question) traceStructFieldValueOrigins(fn *ssa.Function, structVal ssa.Value, field int, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	if structVal == nil {
		return nil, true
	}
	if depth > originDepth {
		st.cut = true
		return nil, true
	}
	bodyFn := canonicalBodyFn(fn)
	switch sv := structVal.(type) {
	case *ssa.ChangeType:
		// A struct converted from another struct type has the same fields in the same order.
		return st.traceStructFieldValueOrigins(bodyFn, sv.X, field, depth+1, seen)
	case *ssa.UnOp:
		if sv.Op == token.MUL {
			return st.traceStructPtrFieldOrigins(bodyFn, sv.X, field, depth, seen)
		}
	case *ssa.Phi:
		var out []leafValue
		incomplete := len(sv.Edges) == 0
		for _, e := range sv.Edges {
			sub, inc := st.traceStructFieldValueOrigins(bodyFn, e, field, depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
		}
		return out, incomplete
	case *ssa.Parameter:
		if st.contracts != nil && bodyFn != nil {
			pIdx := -1
			for i, p := range bodyFn.Params {
				if p == sv {
					pIdx = i
					break
				}
			}
			if pIdx >= 0 && len(st.contracts.callSitesOf[bodyFn]) > 0 {
				incomplete := st.contracts.openCallers[bodyFn]
				var out []leafValue
				for _, site := range st.contracts.callSitesOf[bodyFn] {
					cc := site.ins.Common()
					if pIdx < len(cc.Args) {
						sub, inc := st.traceStructFieldValueOrigins(site.caller, cc.Args[pIdx], field, depth+1, seen)
						out = append(out, sub...)
						if inc {
							incomplete = true
						}
					} else {
						incomplete = true
					}
				}
				if len(out) > 0 {
					return out, incomplete
				}
			}
		}
	}
	if st.contracts != nil {
		if keyStr, ok := structValueTypeKey(structVal.Type()); ok {
			var out []leafValue
			for _, iv := range st.contracts.fieldStores(keyStr, field) {
				sub, _ := st.traceSSAOrigins(iv.fn, iv.v, depth+1, seen)
				out = append(out, sub...)
			}
			if len(out) > 0 {
				return out, true
			}
		}
	}
	return nil, true
}

func (st *question) traceMapLookupOrigins(fn *ssa.Function, m ssa.Value, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	mm, ok := m.(*ssa.MakeMap)
	if !ok || mm.Referrers() == nil {
		return nil, true
	}
	var out []leafValue
	for _, ref := range *mm.Referrers() {
		if mu, ok := ref.(*ssa.MapUpdate); ok && mu.Map == mm {
			sub, _ := st.traceSSAOrigins(fn, mu.Value, depth+1, seen)
			out = append(out, sub...)
		}
	}
	return out, true
}

func (st *question) traceChanRecvOrigins(fn *ssa.Function, ch ssa.Value, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
	mc, ok := ch.(*ssa.MakeChan)
	if !ok || mc.Referrers() == nil {
		return nil, true
	}
	var out []leafValue
	incomplete := false
	for _, ref := range *mc.Referrers() {
		if snd, ok := ref.(*ssa.Send); ok && snd.Chan == mc {
			sub, inc := st.traceSSAOrigins(snd.Parent(), snd.X, depth+1, seen)
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

func (st *question) traceCallResultOrigins(caller *ssa.Function, callIns *ssa.Call, resIdx int, depth int, seen map[valVisitKey]bool) ([]leafValue, bool) {
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
			sub, inc := st.traceSSAOrigins(caller, callIns.Call.Args[pIdx], depth+1, seen)
			out = append(out, sub...)
			if inc {
				incomplete = true
			}
			return
		}
		sub, inc := st.traceSSAOrigins(bodyFn, rv, depth+1, seen)
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

func (st *question) resolveInterfaceTypes(fn *ssa.Function, v ssa.Value, depth int, seen map[valVisitKey]bool) ([]types.Type, bool) {
	leaves, incomplete := st.traceSSAOrigins(fn, v, depth, seen)
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

// openStdlibInterfaceCandidates returns stdlib concurrent container pointer types that implement
// ifaceType when an interface value has open provenance (e.g. an uncalled function taking an
// interface parameter). When a local type in the package also implements ifaceType, stdlib
// container candidates are retained if and only if they are actually boxed into an interface in
// this package (avoiding false positives on pure local user interfaces while catching coexisting
// mock + real stdlib container boxing).
func (st *state) openStdlibInterfaceCandidates(t types.Type) []types.Type {
	if st.contracts == nil || t == nil {
		return nil
	}
	iface, ok := t.Underlying().(*types.Interface)
	if !ok || iface.NumMethods() == 0 {
		return nil
	}
	hasLocalImpl := false
	for _, localNamed := range st.contracts.localNamedTypes {
		if types.Implements(localNamed, iface) || types.Implements(types.NewPointer(localNamed), iface) {
			hasLocalImpl = true
			break
		}
	}
	var stdlibCands []types.Type
	if syncPkg := st.contracts.directImports["sync"]; syncPkg != nil {
		for _, name := range []string{"Map", "Pool"} {
			if obj, ok := syncPkg.Scope().Lookup(name).(*types.TypeName); ok {
				pt := types.NewPointer(obj.Type())
				if types.Implements(pt, iface) {
					stdlibCands = append(stdlibCands, pt)
				}
			}
		}
	}
	if atomicPkgObj := st.contracts.directImports["sync/atomic"]; atomicPkgObj != nil {
		if valObj, ok := atomicPkgObj.Scope().Lookup("Value").(*types.TypeName); ok {
			pt := types.NewPointer(valObj.Type())
			if types.Implements(pt, iface) {
				stdlibCands = append(stdlibCands, pt)
			}
		}
		if ptrObj, ok := atomicPkgObj.Scope().Lookup("Pointer").(*types.TypeName); ok {
			if ptrNamed, ok := types.Unalias(ptrObj.Type()).(*types.Named); ok && ptrNamed.TypeParams() != nil && ptrNamed.TypeParams().Len() == 1 {
				if elem := inferAtomicPointerElem(iface); elem != nil {
					if inst, err := types.Instantiate(nil, ptrNamed, []types.Type{elem}, false); err == nil {
						pt := types.NewPointer(inst)
						if types.Implements(pt, iface) {
							stdlibCands = append(stdlibCands, pt)
						}
					}
				}
			}
		}
	}
	if !hasLocalImpl {
		return stdlibCands
	}
	var out []types.Type
	for _, pt := range stdlibCands {
		for _, bt := range st.contracts.boxedTypes {
			if types.Identical(bt, pt) {
				out = append(out, pt)
				break
			}
		}
	}
	return out
}

func inferAtomicPointerElem(iface *types.Interface) types.Type {
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig := m.Type().(*types.Signature)
		switch m.Name() {
		case "Store":
			if sig.Params().Len() == 1 {
				if p, ok := sig.Params().At(0).Type().Underlying().(*types.Pointer); ok {
					return p.Elem()
				}
			}
		case "Load":
			if sig.Results().Len() == 1 {
				if p, ok := sig.Results().At(0).Type().Underlying().(*types.Pointer); ok {
					return p.Elem()
				}
			}
		case "Swap":
			if sig.Params().Len() == 1 {
				if p, ok := sig.Params().At(0).Type().Underlying().(*types.Pointer); ok {
					return p.Elem()
				}
			}
		case "CompareAndSwap":
			if sig.Params().Len() == 2 {
				if p, ok := sig.Params().At(0).Type().Underlying().(*types.Pointer); ok {
					return p.Elem()
				}
			}
		}
	}
	return nil
}

type candOutcome struct {
	escapeArgs         []int
	publishArgs        []pubArgSpec
	freshResult        bool
	sharedReadOnlyLoad bool
	isAtomic           bool
	displayName        string
	matched            bool
}

func mergeCandidateOutcomes(cands []candOutcome, incomplete bool) (resolvedCallContract, bool) {
	if len(cands) == 0 {
		return resolvedCallContract{}, false
	}
	anyMatched := false
	allFresh := !incomplete
	allAtomic := !incomplete
	sharedLoad := false
	var displayName string
	var escapes []int

	type pubAcc struct {
		what         string
		freezesToImm bool
	}
	pubByIdx := map[int]pubAcc{}
	var pubOrder []int

	for _, c := range cands {
		if c.matched {
			anyMatched = true
		}
		for _, idx := range c.escapeArgs {
			if !slices.Contains(escapes, idx) {
				escapes = append(escapes, idx)
			}
		}
		if !c.freshResult {
			allFresh = false
		}
		if !c.isAtomic {
			allAtomic = false
		}
		if c.sharedReadOnlyLoad {
			sharedLoad = true
			if displayName == "" {
				displayName = c.displayName
			}
		}
		if displayName == "" && c.displayName != "" {
			displayName = c.displayName
		}
		for _, p := range c.publishArgs {
			if prev, exists := pubByIdx[p.argIdx]; exists {
				prev.freezesToImm = prev.freezesToImm && p.freezesToImm
				pubByIdx[p.argIdx] = prev
			} else {
				pubByIdx[p.argIdx] = pubAcc{what: p.what, freezesToImm: p.freezesToImm}
				pubOrder = append(pubOrder, p.argIdx)
			}
		}
	}
	if !anyMatched {
		return resolvedCallContract{}, false
	}
	sort.Ints(pubOrder)
	specs := make([]pubArgSpec, 0, len(pubOrder))
	for _, idx := range pubOrder {
		acc := pubByIdx[idx]
		specs = append(specs, pubArgSpec{
			argIdx: idx,
			what:   acc.what,
			// Frozen only if every callee freezes it, and not every callee is known.
			freezesToImm: acc.freezesToImm && !incomplete,
		})
	}
	return resolvedCallContract{
		publishArgs:        specs,
		freshResult:        allFresh,
		sharedReadOnlyLoad: sharedLoad,
		suppressWrite:      allAtomic,
		displayName:        displayName,
		escapeArgs:         escapes,
	}, true
}

func outcomeFromCapContract(c capContract, pkgPath string) candOutcome {
	specs := make([]pubArgSpec, 0, len(c.publishArgs))
	for _, idx := range c.publishArgs {
		specs = append(specs, pubArgSpec{
			argIdx:       idx,
			what:         c.what,
			freezesToImm: c.freezesToImm,
		})
	}
	return candOutcome{
		publishArgs:        specs,
		freshResult:        c.freshResult,
		sharedReadOnlyLoad: c.sharedReadOnlyLoad,
		isAtomic:           pkgPath == atomicPkg,
		displayName:        c.displayName,
		matched:            true,
	}
}

// evalInterfaceMethodContract resolves a call of method methodName on the interface value recvVal:
// each type the receiver may be answers through its method of that name, by its capContracts entry
// or, for a method of this package, by what a direct call of it would get (evalFunctionOutcome).
// callArgs are the call's arguments, in caller, or nil where they are not at hand; with shift 1
// they leave the receiver out, with shift 0 it is the first of them.
func (st *question) evalInterfaceMethodContract(caller *ssa.Function, recvVal ssa.Value, methodName string, callArgs []ssa.Value, shift int) (candOutcome, bool) {
	if st.ssa == nil || st.ssa.Pkg == nil || st.ssa.Pkg.Prog == nil {
		return candOutcome{}, false
	}
	var recvType types.Type
	if recvVal != nil {
		recvType = recvVal.Type()
	}
	if !st.capMethodFor(recvType, methodName).known {
		// Nothing has a contract for a method of this name and signature, so there is none to
		// find and the receiver need not be traced. (A trace that is cut is reported at the call;
		// one that cannot change the answer must not be run for that reason alone.)
		return candOutcome{}, false
	}
	concretes, incomplete := st.resolveInterfaceTypes(caller, recvVal, 0, map[valVisitKey]bool{})
	if len(concretes) == 0 && recvVal != nil {
		concretes = st.openStdlibInterfaceCandidates(recvVal.Type())
		incomplete = true
	}
	if len(concretes) == 0 {
		return candOutcome{}, false
	}
	var sub []candOutcome
	for _, ct := range concretes {
		sel := st.ssa.Pkg.Prog.MethodSets.MethodSet(ct).Lookup(st.pass.Pkg, methodName)
		if sel == nil {
			sub = append(sub, candOutcome{})
			continue
		}
		mObj, ok := sel.Obj().(*types.Func)
		if !ok {
			sub = append(sub, candOutcome{})
			continue
		}
		origObj := mObj.Origin()
		if c, has := capContracts[origObj.FullName()]; has {
			c = shiftContractPublishArgs(c, shift)
			pkgPath := ""
			if origObj.Pkg() != nil {
				pkgPath = origObj.Pkg().Path()
			}
			sub = append(sub, outcomeFromCapContract(c, pkgPath))
		} else if fn := st.ssa.Pkg.Prog.FuncValue(origObj); fn != nil {
			// The method as declared, which for one promoted from an embedded field is not the
			// wrapper the outer type's method set holds: the index has the declared one's facts.
			sub = append(sub, st.evalFunctionOutcome(caller, fn, callArgs, shift == 1, 0))
		} else {
			sub = append(sub, candOutcome{})
		}
	}
	rc, ok := mergeCandidateOutcomes(sub, incomplete)
	if !ok {
		return candOutcome{}, false
	}
	return candOutcome{
		escapeArgs:         rc.escapeArgs,
		publishArgs:        rc.publishArgs,
		freshResult:        rc.freshResult,
		sharedReadOnlyLoad: rc.sharedReadOnlyLoad,
		isAtomic:           rc.suppressWrite,
		displayName:        rc.displayName,
		matched:            true,
	}, true
}

func (st *question) evalValueAsCallable(caller *ssa.Function, fnVal ssa.Value, callArgs []ssa.Value, depth int) (resolvedCallContract, bool) {
	if fnVal == nil {
		return resolvedCallContract{}, false
	}
	if depth > 6 {
		// A callable handed on through this many callables is not worked out. That is a cut like
		// a trace out of budget, and is said the same way; it used to read as "has no contract".
		st.cut = true
		return resolvedCallContract{}, false
	}
	leaves, incomplete := st.traceSSAOrigins(caller, fnVal, 0, map[valVisitKey]bool{})
	if len(leaves) == 0 {
		return resolvedCallContract{}, false
	}
	var cands []candOutcome
	for _, lf := range leaves {
		switch x := lf.v.(type) {
		case *ssa.Function:
			cands = append(cands, st.evalFunctionOutcome(caller, x, callArgs, false, depth))
		case *ssa.MakeClosure:
			mcFn, ok := x.Fn.(*ssa.Function)
			if !ok || mcFn == nil {
				cands = append(cands, candOutcome{})
				continue
			}
			if isBoundMethodWrapper(mcFn) && len(x.Bindings) == 1 {
				if mObj, ok := mcFn.Object().(*types.Func); ok && mObj.Signature().Recv() != nil && types.IsInterface(mObj.Signature().Recv().Type()) {
					// The arguments are values of caller; the receiver was bound where the leaf is.
					var args []ssa.Value
					if lf.fn == caller {
						args = callArgs
					}
					if out, ok := st.evalInterfaceMethodContract(lf.fn, x.Bindings[0], mObj.Name(), args, 1); ok {
						cands = append(cands, out)
					} else {
						cands = append(cands, candOutcome{})
					}
					continue
				}
			}
			cands = append(cands, st.evalFunctionOutcome(caller, mcFn, callArgs, isBoundMethodWrapper(mcFn), depth))
		default:
			incomplete = true
		}
	}
	return mergeCandidateOutcomes(cands, incomplete)
}

// forwardKeeps reports whether the callable target, handed an argument in slot, may keep it. It
// answers no only when every function target may be is known, each has a summary of its own body,
// and none of them leaks, takes ownership of or starts a goroutine with that parameter. A target
// that could not be followed to the end, a bound method or anything else answers yes.
func (st *question) forwardKeeps(caller *ssa.Function, target ssa.Value, slot int) bool {
	leaves, incomplete := st.traceSSAOrigins(caller, target, 0, map[valVisitKey]bool{})
	if incomplete || len(leaves) == 0 {
		return true
	}
	for _, lf := range leaves {
		var fn *ssa.Function
		switch x := lf.v.(type) {
		case *ssa.Function:
			fn = x
		case *ssa.MakeClosure:
			fn, _ = x.Fn.(*ssa.Function)
		}
		if fn == nil || isBoundMethodWrapper(fn) || isMethodThunk(fn) {
			return true
		}
		s := st.summary(fn)
		if s.leaks(slot) || s.isIso(slot) || s.spawns(slot) {
			return true
		}
	}
	return false
}

func (st *question) evalFunctionOutcome(caller *ssa.Function, fn *ssa.Function, callArgs []ssa.Value, bound bool, depth int) candOutcome {
	if fn == nil {
		return candOutcome{}
	}
	if isMethodThunk(fn) {
		if mObj, ok := fn.Object().(*types.Func); ok && mObj.Signature().Recv() != nil && types.IsInterface(mObj.Signature().Recv().Type()) {
			if len(callArgs) > 0 {
				if out, ok := st.evalInterfaceMethodContract(caller, callArgs[0], mObj.Name(), callArgs, 0); ok {
					return out
				}
			}
			return candOutcome{}
		}
	}
	if c, has := capContractFor(fn); has {
		pkgPath := ""
		if orig := fn.Origin(); orig != nil {
			if obj, ok := orig.Object().(*types.Func); ok && obj.Origin().Pkg() != nil {
				pkgPath = obj.Origin().Pkg().Path()
			}
		} else if obj, ok := fn.Object().(*types.Func); ok && obj.Origin().Pkg() != nil {
			pkgPath = obj.Origin().Pkg().Path()
		}
		return outcomeFromCapContract(c, pkgPath)
	}

	bodyFn := canonicalBodyFn(fn)
	if st.contracts == nil || bodyFn == nil {
		return candOutcome{}
	}

	var out candOutcome
	// A result that comes from the function's forwards is fresh only if every value the function
	// may return is fresh or a forward's result (retFreshButForwards), and every forward returns
	// a fresh value at this call. A forward that has no answer here counts against it.
	forwardsFresh := false
	// Forwarded higher-order/interface parameter contracts evaluated against caller's actual arguments.
	if len(callArgs) > 0 {
		for _, cf := range st.contracts.callForwards[bodyFn] {
			tIdx := cf.targetParam
			aIdx := cf.argParam
			if bound {
				tIdx--
				aIdx--
			}
			if tIdx < 0 || tIdx >= len(callArgs) || aIdx < 0 || aIdx >= len(callArgs) {
				if aIdx >= 0 && aIdx < len(callArgs) {
					out.escapeArgs = append(out.escapeArgs, aIdx)
				}
				continue
			}
			var inner resolvedCallContract
			var hasInner bool
			if cf.methodName != "" {
				if io, ok := st.evalInterfaceMethodContract(caller, callArgs[tIdx], cf.methodName, nil, 1); ok {
					inner, hasInner = mergeCandidateOutcomes([]candOutcome{io}, false)
				}
			} else {
				inner, hasInner = st.evalValueAsCallable(caller, callArgs[tIdx], nil, depth+1)
			}
			// keeps: the target has no publishing answer here, and may keep the argument. A
			// receiver's method is not looked into; a callable is, through its summary. When
			// the target and the argument are both parameters of caller, and the index holds
			// the rule that follows for caller, the question is not caller's to answer: each
			// call of caller answers it for its own target, by that rule.
			keeps := func() bool {
				cb := canonicalBodyFn(caller)
				if outerT, outerA := paramFlowIndex(callArgs[tIdx], cb), paramFlowIndex(callArgs[aIdx], cb); outerT >= 0 && outerA >= 0 &&
					slices.Contains(st.contracts.callForwards[cb], callForwardRule{targetParam: outerT, methodName: cf.methodName, slot: cf.slot, argParam: outerA}) {
					return false
				}
				return cf.methodName != "" || st.forwardKeeps(caller, callArgs[tIdx], cf.slot)
			}
			if !hasInner {
				if keeps() {
					out.escapeArgs = append(out.escapeArgs, aIdx)
				}
				continue
			}
			publishes := false
			for _, p := range inner.publishArgs {
				if p.argIdx == cf.slot {
					out.publishArgs = append(out.publishArgs, pubArgSpec{
						argIdx:       aIdx,
						what:         p.what,
						freezesToImm: p.freezesToImm,
					})
					out.matched = true
					publishes = true
				}
			}
			if inner.alsoEscapes(cf.slot) || (!publishes && keeps()) {
				out.escapeArgs = append(out.escapeArgs, aIdx)
			}
		}
		forwardsFresh = st.contracts.retFreshButForwards[bodyFn] && len(st.contracts.retForwards[bodyFn]) > 0
		for _, rf := range st.contracts.retForwards[bodyFn] {
			tIdx := rf.targetParam
			if bound {
				tIdx--
			}
			if tIdx < 0 || tIdx >= len(callArgs) {
				forwardsFresh = false
				continue
			}
			var inner resolvedCallContract
			var hasInner bool
			if rf.methodName != "" {
				if io, ok := st.evalInterfaceMethodContract(caller, callArgs[tIdx], rf.methodName, nil, 1); ok {
					inner, hasInner = mergeCandidateOutcomes([]candOutcome{io}, false)
				}
			} else {
				inner, hasInner = st.evalValueAsCallable(caller, callArgs[tIdx], nil, depth+1)
			}
			if !hasInner {
				forwardsFresh = false
				continue
			}
			if inner.sharedReadOnlyLoad {
				out.sharedReadOnlyLoad = true
				if out.displayName == "" {
					out.displayName = inner.displayName
				}
				out.matched = true
			}
			if !inner.freshResult {
				forwardsFresh = false
			}
		}
	}
	if forwardsFresh {
		out.freshResult = true
		out.matched = true
	}
	for _, dp := range st.contracts.directPublish[bodyFn] {
		idx := dp.argIdx
		if bound {
			idx--
		}
		if idx < 0 {
			continue
		}
		out.publishArgs = append(out.publishArgs, pubArgSpec{
			argIdx:       idx,
			what:         dp.what,
			freezesToImm: dp.freezesToImm,
		})
		out.matched = true
	}
	// What the body does with a published argument besides, for a callee reached as a value: a
	// direct call reads the same from the summary. There are no summaries while the index is built.
	if s := st.sums[bodyFn]; s != nil {
		for _, p := range out.publishArgs {
			param := p.argIdx
			if bound {
				param++
			}
			if s.leaksBesidesContract(param) && !slices.Contains(out.escapeArgs, p.argIdx) {
				out.escapeArgs = append(out.escapeArgs, p.argIdx)
			}
		}
	}
	if desc := st.contracts.directSharedRet[bodyFn]; desc != "" {
		out.sharedReadOnlyLoad = true
		if out.displayName == "" {
			out.displayName = desc
		}
		out.matched = true
	}
	if st.contracts.directFreshRet[bodyFn] {
		out.freshResult = true
		out.matched = true
	} else if bodyFn.Signature != nil && bodyFn.Signature.Results() != nil && bodyFn.Signature.Results().Len() > 0 {
		if s := st.sums[bodyFn]; s != nil && !s.pessimistic && s.fresh && len(s.links) == 0 {
			out.freshResult = true
		}
	}
	return out
}

// originCutNote is how a report says which bounds cut a trace.
func originCutNote() string {
	return fmt.Sprintf("a function or interface value comes through more than %d steps or from more than %d values", originDepth, originBudget)
}

// resolveCallContractCut is resolveCallContract, and whether a trace it ran was cut by its depth or
// its budget (originDepth, originBudget). The contract then speaks for the callees the trace
// reached only. What needs every callee (a fresh result, an atomic call, a value frozen when
// published) is withheld by mergeCandidateOutcomes; what any one callee may do (publish an
// argument, return loaded memory) is missing for the ones not reached, and whoever acts on the
// contract has to say so at the call.
func (st *state) resolveCallContractCut(caller *ssa.Function, cc *ssa.CallCommon) (rc resolvedCallContract, ok, cut bool) {
	q := st.ask()
	rc, ok = q.resolveCallContract(caller, cc)
	return rc, ok, q.cut
}

func (st *question) resolveCallContract(caller *ssa.Function, cc *ssa.CallCommon) (resolvedCallContract, bool) {
	if cc == nil {
		return resolvedCallContract{}, false
	}
	callerBody := canonicalBodyFn(caller)
	var rc resolvedCallContract
	var ok bool
	var targetFromParam bool

	if cc.IsInvoke() {
		targetFromParam = paramFlowIndex(cc.Value, callerBody) >= 0
		out, has := st.evalInterfaceMethodContract(callerBody, cc.Value, cc.Method.Name(), cc.Args, 1)
		if !has {
			return resolvedCallContract{}, false
		}
		rc, ok = mergeCandidateOutcomes([]candOutcome{out}, false)
	} else {
		targetFromParam = paramFlowIndex(cc.Value, callerBody) >= 0
		rc, ok = st.evalValueAsCallable(callerBody, cc.Value, cc.Args, 0)
	}
	if !ok {
		return resolvedCallContract{}, false
	}

	if targetFromParam && st.contracts != nil && callerBody != nil && len(st.contracts.callSitesOf[callerBody]) > 0 {
		for i := range rc.publishArgs {
			argIdx := rc.publishArgs[i].argIdx
			if argIdx >= 0 && argIdx < len(cc.Args) && paramFlowIndex(cc.Args[argIdx], callerBody) >= 0 {
				rc.publishArgs[i].delegatedToCallers = true
			}
		}
	}
	return rc, true
}

type sharedLoadOrigin struct {
	desc  string
	pos   token.Pos
	muted bool
}

// checkSharedLoadWrites enforces the read-only `\imm` capability on values loaded from shared
// concurrent containers (`(*sync/atomic.Pointer[T]).Load`/`Swap`, `(*sync/atomic.Value).Load`/`Swap`,
// `(*sync.Map).Load`/`LoadAndDelete`/`LoadOrStore`/`Swap`) across static, closure, higher-order,
// and interface dispatch.
func (st *state) checkSharedLoadWrites(fns []*ssa.Function) {
	shared := map[*ssa.Function]map[ssa.Value]sharedLoadOrigin{}
	var order []*ssa.Function // the functions that hold shared values, as they first did
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
			order = append(order, fn)
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

	seedCallResult := func(fn *ssa.Function, callIns *ssa.Call, orig sharedLoadOrigin) {
		if _, isTuple := callIns.Type().(*types.Tuple); isTuple {
			if callIns.Referrers() == nil {
				return
			}
			for _, ref := range *callIns.Referrers() {
				if ext, ok := ref.(*ssa.Extract); ok && ext.Tuple == callIns && ext.Index == 0 {
					add(fn, ext, orig)
				}
			}
			return
		}
		add(fn, callIns, orig)
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
			rc, has, cut := st.resolveCallContractCut(fn, &callIns.Call)
			if !has || !rc.sharedReadOnlyLoad {
				// None of the callees found returns loaded memory; one the trace did not reach
				// may, and a write through the result is then not seen.
				res := callIns.Call.Signature().Results()
				if cc := &callIns.Call; cc.IsInvoke() && !st.capMethodFor(cc.Value.Type(), cc.Method.Name()).loads {
					cut = false // no method that could stand behind it returns loaded memory
				}
				if cut && res.Len() > 0 && !st.cls.Shareable(res.At(0).Type()) {
					st.report(posOf(callIns, callIns), ImmCut, fmt.Sprintf(
						"what this call may run could not be followed to the end (%s), so whether its result is memory loaded from a shared container is not known, and writes through the result are not checked",
						originCutNote()))
				}
				return
			}
			desc := rc.displayName
			if desc == "" {
				desc = "shared atomic load"
			}
			seedCallResult(fn, callIns, sharedLoadOrigin{
				desc:  desc,
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
	once := func(pos token.Pos, muted bool) bool {
		p := st.pass.Fset.Position(pos)
		k := lineKey{p.Filename, p.Line, muted}
		if reported[k] {
			return false
		}
		reported[k] = true
		return true
	}

	emit := func(ins ssa.Instruction, orig sharedLoadOrigin, detail string) {
		// Where ins has no position (sharedLoadPos), the finding is reported at the Load, and that
		// line does not tell two findings apart: their messages do (reportRelated).
		pos, located, ok := st.sharedLoadPos(ins, orig)
		if !ok || (located && !once(pos, orig.muted)) {
			return
		}
		prevMuted, prevAt := st.muted, st.transferAt
		before := st.mutedFindings
		st.muted, st.transferAt = orig.muted, orig.pos
		st.report(pos, Imm, detail)
		st.muted, st.transferAt = prevMuted, prevAt
		if orig.muted && st.mutedFindings > before {
			st.ix.Mute(orig.pos)
		}
	}

	// Findings are emitted only after the origins reach their fixpoint: a hatch is recorded as used
	// by a muted finding, and a muted finding made while an unhatched origin was still on its way
	// (it arrives later through a longer call chain) would count the hatch as used for a write
	// that is then reported anyway. So a function is processed in two passes, and each does one
	// thing: the first hands shared values on (add) and reports nothing, the second reports (emit)
	// and hands nothing on. Everything process calls takes the two as arguments, so the helpers in
	// forward.go follow the pass they are called in.
	process := func(fn *ssa.Function, emitting bool) {
		add, emit, seedCallResult := add, emit, seedCallResult
		if emitting {
			add = func(*ssa.Function, ssa.Value, sharedLoadOrigin) {}
			seedCallResult = func(*ssa.Function, *ssa.Call, sharedLoadOrigin) {}
		} else {
			emit = func(ssa.Instruction, sharedLoadOrigin, string) {}
		}
		origins := shared[fn]
		roots := make(map[ssa.Value]string, len(origins))
		unmuted := make(map[ssa.Value]string, len(origins))
		for v, o := range origins {
			roots[v] = o.desc
			if !o.muted {
				unmuted[v] = o.desc
			}
		}
		// rootOf prefers an unmuted origin: a value that several Loads reach (through a phi, or a
		// cell stored on two paths) is muted only when no unhatched Load reaches it.
		rootOf := func(v ssa.Value) (ssa.Value, bool) {
			if r, ok := st.sharedLoadRoot(v, unmuted); ok {
				return r, true
			}
			return st.sharedLoadRoot(v, roots)
		}

		bodyFn := canonicalBodyFn(fn)
		hasRetForward := st.contracts != nil && len(st.contracts.retForwards[bodyFn]) > 0

		forEachInstr(fn, func(ins ssa.Instruction) {
			defer st.sharedLoadCut(ins)()
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
			if ret, ok := ins.(*ssa.Return); ok && len(ret.Results) > 0 && !hasRetForward && st.contracts != nil {
				if root, ok := rootOf(ret.Results[0]); ok {
					orig := origins[root]
					for _, site := range st.contracts.callSitesOf[bodyFn] {
						if callIns, ok := site.ins.(*ssa.Call); ok {
							seedCallResult(site.caller, callIns, orig)
						}
					}
				}
				return
			}
			c, ok := ins.(ssa.CallInstruction)
			if !ok {
				return
			}
			cc := c.Common()
			for _, a := range cc.Args {
				if mc := resolveClosureVal(a, 0); mc != nil {
					if argFn, ok := mc.Fn.(*ssa.Function); ok && len(argFn.Blocks) > 0 && st.followable(argFn) {
						st.forwardShared(ins, argFn, nil, mc.Bindings, origins, rootOf, add, emit)
						for j, b := range mc.Bindings {
							if root, ok := rootOf(b); ok && j < len(argFn.FreeVars) {
								add(argFn, argFn.FreeVars[j], origins[root])
							}
						}
					}
				}
			}
			if cc.IsInvoke() {
				st.propagateSharedToMethods(ins, fn, cc, origins, rootOf, add, emit)
				return
			}
			callee, bindings := directCallee(cc.Value)
			if callee != nil {
				st.propagateSharedToCallee(ins, cc, callee, bindings, origins, rootOf, add, emit)
				return
			}
			q := st.ask()
			leaves, _ := q.traceSSAOrigins(fn, cc.Value, 0, map[valVisitKey]bool{})
			if q.cut && slices.ContainsFunc(cc.Args, func(a ssa.Value) bool { _, shared := rootOf(a); return shared }) {
				// The callees found are checked below; the ones the trace did not get to are not.
				st.report(posOf(ins, ins), ImmCut, fmt.Sprintf(
					"which functions the value called here may be could not be followed to the end (it comes through more than %d steps or from more than %d values), so what the ones not reached write through the shared value passed to them is not checked",
					originDepth, originBudget))
			}
			for _, lf := range leaves {
				switch x := lf.v.(type) {
				case *ssa.Function:
					st.propagateSharedToCallee(ins, cc, x, nil, origins, rootOf, add, emit)
				case *ssa.MakeClosure:
					if mcFn, ok := x.Fn.(*ssa.Function); ok {
						st.propagateSharedToCallee(ins, cc, mcFn, x.Bindings, origins, rootOf, add, emit)
					}
				}
			}
		})
	}

	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		enqueued[fn] = false
		process(fn, false)
	}
	// Every function that holds shared values is reported on, in the order they first held one:
	// that includes closures and the like that are not in fns.
	for _, fn := range order {
		process(fn, true)
	}
}

func (st *state) propagateSharedToCallee(
	ins ssa.Instruction,
	cc *ssa.CallCommon,
	callee *ssa.Function,
	bindings []ssa.Value,
	origins map[ssa.Value]sharedLoadOrigin,
	rootOf func(ssa.Value) (ssa.Value, bool),
	add func(*ssa.Function, ssa.Value, sharedLoadOrigin),
	emit func(ssa.Instruction, sharedLoadOrigin, string),
) {
	callee = canonicalBodyFn(callee)
	if callee == nil {
		return
	}
	// What a synthesised wrapper forwards to is checked here, at the call that enters it.
	st.forwardShared(ins, callee, cc.Args, bindings, origins, rootOf, add, emit)
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
	return st.sharedLoadWalk().root(v, roots)
}
