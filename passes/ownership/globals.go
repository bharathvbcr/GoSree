package ownership

import (
	"fmt"
	"go/token"

	"golang.org/x/tools/go/ssa"
)

// checkGlobals reports SAFE006: a package variable that is written outside initialisation and is
// read or written by a function that runs on a goroutine started in this package, by a go statement
// or a callback spawner (spawn.go). The goroutine call graph follows static calls within the package
// only.
func (st *state) checkGlobals(fns []*ssa.Function) {
	inInit := map[*ssa.Function]bool{}
	for _, fn := range fns {
		if isInit(fn) {
			inInit[fn] = true
		}
	}
	if pkgInit := st.ssa.Pkg.Func("init"); pkgInit != nil {
		inInit[pkgInit] = true
	}

	written := map[*ssa.Global]token.Pos{}
	for _, fn := range fns {
		if inInit[fn] {
			continue
		}
		forEachInstr(fn, func(ins ssa.Instruction) {
			if g, ok := writtenGlobal(ins); ok && g.Pkg == st.ssa.Pkg {
				if _, seen := written[g]; !seen {
					written[g] = ins.Pos()
				}
			}
		})
	}
	if len(written) == 0 {
		return
	}

	// Functions that run on a goroutine, with the go statement that starts them.
	entry := map[*ssa.Function]token.Pos{}
	var queue []*ssa.Function
	enqueue := func(fn *ssa.Function, pos token.Pos) {
		if fn == nil || fn.Pkg != st.ssa.Pkg {
			return
		}
		if o := fn.Origin(); o != nil {
			fn = o
		}
		if _, seen := entry[fn]; seen {
			return
		}
		entry[fn] = pos
		queue = append(queue, fn)
	}
	prog := st.ssa.Pkg.Prog
	for _, fn := range fns {
		forEachInstr(fn, func(ins ssa.Instruction) {
			if sp, ok := spawnOf(ins); ok {
				for _, f := range sp.spawnedFuncs(prog) {
					enqueue(f, ins.Pos())
				}
			}
		})
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		start := entry[fn]
		forEachInstr(fn, func(ins ssa.Instruction) {
			switch ins := ins.(type) {
			case ssa.CallInstruction:
				if _, isGo := ins.(*ssa.Go); !isGo {
					enqueue(ins.Common().StaticCallee(), start)
				}
			case *ssa.MakeClosure:
				enqueue(ins.Fn.(*ssa.Function), start)
			}
		})
	}

	type key struct {
		fn *ssa.Function
		g  *ssa.Global
	}
	done := map[key]bool{}
	for fn, start := range entry {
		forEachInstr(fn, func(ins ssa.Instruction) {
			g, ok := accessedGlobal(ins)
			if !ok {
				return
			}
			wpos, mutable := written[g]
			if !mutable || done[key{fn, g}] {
				return
			}
			done[key{fn, g}] = true
			st.report(posOf(ins, ins), GlobalUse, fmt.Sprintf(
				"package variable %s is written outside init%s and accessed here on a goroutine started%s",
				g.Name(), st.at(wpos), st.at(start)))
		})
	}
}

func isInit(fn *ssa.Function) bool {
	for f := fn; f != nil; f = f.Parent() {
		if f.Name() == "init" && f.Signature.Recv() == nil && f.Parent() == nil {
			return true
		}
		if len(f.Name()) > 5 && f.Name()[:5] == "init#" {
			return true
		}
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

// globalRoot follows field, index, slice and load steps back to a package variable.
func globalRoot(v ssa.Value) (*ssa.Global, bool) {
	for range 64 {
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
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return nil, false
			}
			v = x.X
		default:
			return nil, false
		}
	}
	return nil, false
}

func writtenGlobal(ins ssa.Instruction) (*ssa.Global, bool) {
	switch ins := ins.(type) {
	case *ssa.Store:
		return globalRoot(ins.Addr)
	case *ssa.MapUpdate:
		return globalRoot(ins.Map)
	case *ssa.Call:
		if b, ok := ins.Call.Value.(*ssa.Builtin); ok && len(ins.Call.Args) > 0 {
			switch b.Name() {
			case "copy", "delete", "clear":
				return globalRoot(ins.Call.Args[0])
			}
		}
	}
	return nil, false
}

func accessedGlobal(ins ssa.Instruction) (*ssa.Global, bool) {
	if g, ok := writtenGlobal(ins); ok {
		return g, true
	}
	switch ins := ins.(type) {
	case *ssa.UnOp:
		if ins.Op == token.MUL {
			return globalRoot(ins.X)
		}
	case *ssa.Lookup:
		return globalRoot(ins.X)
	case *ssa.Range:
		return globalRoot(ins.X)
	}
	return nil, false
}
