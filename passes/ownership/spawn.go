package ownership

import (
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// A spawn site is where a function value starts running on another goroutine: a go statement, or a
// call to a function that runs its callback on another goroutine (time.AfterFunc, WaitGroup.Go, an
// HTTP handler registration). Every rule that cares where goroutines start reads spawn sites, so a
// callback is a goroutine boundary everywhere a go statement is.
type spawnSite struct {
	ins ssa.Instruction // *ssa.Go or *ssa.Call
	fn  ssa.Value       // the function (or, for a handler, the value) that runs on the other goroutine
	// arg is the index of fn in the call's arguments, or -1 for a go statement.
	arg int
	// args are the arguments the parent itself passes to fn (go statements only).
	args []ssa.Value
	// via names the spawning function, or is "" for a go statement.
	via string
	spawner
}

// spawner describes a function that runs one of its arguments on another goroutine.
type spawner struct {
	// arg indexes the call's arguments; a static method call's receiver is argument 0.
	arg int
	// repeated: the callback may run many times, concurrently with itself (an HTTP handler). It
	// never owns what it captured and never hands anything back.
	repeated bool
	// handler: the argument is an http.Handler value rather than a function.
	handler bool
	// wait is the receiver method that returns only after every callback started on the same
	// receiver has returned, or "".
	wait string
}

// spawners lists the functions known to run a callback on another goroutine, by types.Func
// FullName. A function that runs its callback before returning (sync.Once.Do, testing.T.Run without
// Parallel, slices.SortFunc) is not listed: it is an ordinary call. Listing is an assumption about
// the callee, like the type overrides in internal/kinds.
var spawners = map[string]spawner{
	"time.AfterFunc":                            {arg: 1},
	"context.AfterFunc":                         {arg: 1},
	"(*sync.WaitGroup).Go":                      {arg: 1, wait: "(*sync.WaitGroup).Wait"},
	"(*golang.org/x/sync/errgroup.Group).Go":    {arg: 1, wait: "(*golang.org/x/sync/errgroup.Group).Wait"},
	"(*golang.org/x/sync/errgroup.Group).TryGo": {arg: 1, wait: "(*golang.org/x/sync/errgroup.Group).Wait"},
	"net/http.HandleFunc":                       {arg: 1, repeated: true},
	"(*net/http.ServeMux).HandleFunc":           {arg: 2, repeated: true},
	"net/http.Handle":                           {arg: 1, repeated: true, handler: true},
	"(*net/http.ServeMux).Handle":               {arg: 2, repeated: true, handler: true},
}

// spawnOf returns the spawn site ins is, if any.
func spawnOf(ins ssa.Instruction) (spawnSite, bool) {
	switch ins := ins.(type) {
	case *ssa.Go:
		return spawnSite{ins: ins, fn: ins.Call.Value, arg: -1, args: ins.Call.Args}, true
	case *ssa.Call:
		cc := ins.Common()
		if cc.IsInvoke() {
			return spawnSite{}, false
		}
		callee := cc.StaticCallee()
		if callee == nil {
			return spawnSite{}, false
		}
		obj, ok := callee.Object().(*types.Func)
		if !ok {
			return spawnSite{}, false
		}
		name := obj.FullName()
		sp, ok := spawners[name]
		if !ok || sp.arg >= len(cc.Args) {
			return spawnSite{}, false
		}
		return spawnSite{ins: ins, fn: cc.Args[sp.arg], arg: sp.arg, via: name, spawner: sp}, true
	}
	return spawnSite{}, false
}

// receiver returns the value a method spawner was called on.
func (sp spawnSite) receiver() ssa.Value {
	if c, ok := sp.ins.(*ssa.Call); ok && sp.wait != "" && len(c.Call.Args) > 0 {
		return c.Call.Args[0]
	}
	return nil
}

// what describes the move of v at this spawn site, for diagnostics.
func (sp spawnSite) what(v ssa.Value) string {
	_, closure := v.(*ssa.MakeClosure)
	switch {
	case sp.via == "":
		return "" // go statements keep their own wording (graph.goStmt)
	case sp.handler:
		return "registered as a handler with " + sp.via
	case sp.repeated && closure:
		return "captured by a handler registered with " + sp.via
	case sp.repeated:
		return "registered as a handler with " + sp.via
	case closure:
		return "captured by a goroutine started by " + sp.via
	}
	return "started on a goroutine by " + sp.via
}

// closure returns the function literal this site starts and its closure value, when the closure is
// used by this site alone (as the function to run, not as an argument too).
func (sp spawnSite) closure() (*ssa.Function, *ssa.MakeClosure) {
	mc, ok := sp.fn.(*ssa.MakeClosure)
	if !ok || soleSpawn(mc, false) != sp.ins {
		return nil, nil
	}
	return mc.Fn.(*ssa.Function), mc
}

// soleSpawn returns the spawn site that starts mc when that is mc's only use. With once set it also
// requires the site to be outside any loop, so each run of the parent starts exactly one instance.
func soleSpawn(mc *ssa.MakeClosure, once bool) ssa.Instruction {
	var start ssa.Instruction
	for _, r := range *mc.Referrers() {
		if _, dbg := r.(*ssa.DebugRef); dbg {
			continue
		}
		sp, ok := spawnOf(r)
		if !ok || start != nil || sp.fn != mc || usedAsArg(r, mc, sp.arg) {
			return nil
		}
		start = r
	}
	if start == nil || (once && reachableAfter(start)(start)) {
		return nil
	}
	return start
}

// usedAsArg reports whether v is passed to ins in any argument position other than skip.
func usedAsArg(ins ssa.Instruction, v ssa.Value, skip int) bool {
	c, ok := ins.(ssa.CallInstruction)
	if !ok {
		return false
	}
	for i, a := range c.Common().Args {
		if i != skip && a == v {
			return true
		}
	}
	return false
}

// spawnedFunc returns the function a spawned value runs: the closure or function started, or a
// handler value's ServeHTTP method.
func spawnedFunc(prog *ssa.Program, v ssa.Value, handler bool) *ssa.Function {
	// Every turn moves to the operand of a conversion or a boxing, which is defined before it, so the
	// loop ends without a count.
	for {
		switch x := v.(type) {
		case *ssa.ChangeType:
			v = x.X
		case *ssa.MakeInterface:
			if !handler {
				return nil
			}
			// http.HandlerFunc(f) boxes a converted function; any other handler runs its ServeHTTP.
			if _, isFunc := x.X.Type().Underlying().(*types.Signature); isFunc {
				v = x.X
				continue
			}
			return serveHTTP(prog, x.X.Type())
		case *ssa.MakeClosure:
			return x.Fn.(*ssa.Function)
		case *ssa.Function:
			return x
		default:
			return nil
		}
	}
}

func serveHTTP(prog *ssa.Program, t types.Type) *ssa.Function {
	sel := prog.MethodSets.MethodSet(t).Lookup(nil, "ServeHTTP")
	if sel == nil {
		return nil
	}
	return prog.MethodValue(sel)
}
