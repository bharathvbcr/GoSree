package ownership

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
)

func indexIn(ins ssa.Instruction) int {
	for i, x := range ins.Block().Instrs {
		if x == ins {
			return i
		}
	}
	return -1
}

// reachableAfter returns a predicate reporting whether an instruction can execute after s on some
// path (including later iterations of a loop that contains s).
func reachableAfter(s ssa.Instruction) func(ssa.Instruction) bool {
	start := s.Block()
	sIdx := indexIn(s)
	seen := map[*ssa.BasicBlock]bool{}
	stack := append([]*ssa.BasicBlock(nil), start.Succs...)
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[b] {
			continue
		}
		seen[b] = true
		stack = append(stack, b.Succs...)
	}
	return func(t ssa.Instruction) bool {
		if seen[t.Block()] {
			return true
		}
		return t.Block() == start && indexIn(t) > sIdx
	}
}

func usesValue(ins ssa.Instruction, w ssa.Value) bool {
	for _, op := range ins.Operands(nil) {
		if op != nil && *op == w {
			return true
		}
	}
	return false
}

// syncNames are calls after which another goroutine's earlier writes are visible.
var syncNames = map[string]bool{
	"(*sync.WaitGroup).Wait": true,
	"(*sync.Mutex).Lock":     true,
	"(*sync.RWMutex).Lock":   true,
	"(*sync.RWMutex).RLock":  true,
	"(*sync.Cond).Wait":      true,
}

func isSync(ins ssa.Instruction) bool {
	switch ins := ins.(type) {
	case *ssa.UnOp:
		return ins.Op == token.ARROW
	case *ssa.Select:
		return true
	case *ssa.Call:
		if fn := ins.Common().StaticCallee(); fn != nil {
			if obj, ok := fn.Object().(*types.Func); ok {
				return syncNames[obj.FullName()]
			}
		}
	}
	return false
}

type use struct {
	at     ssa.Instruction
	pos    token.Pos
	synced bool
}

// useAfter finds a use of w that can execute after s without w being redefined first. It prefers a
// path with no synchronisation (channel receive, select, WaitGroup.Wait, Lock) between s and the use;
// if every such use is preceded by synchronisation, the returned use has synced set.
func useAfter(s ssa.Instruction, w ssa.Value) (use, bool) {
	def, _ := w.(ssa.Instruction)
	type state struct {
		b      *ssa.BasicBlock
		synced bool
	}
	var found *use
	visited := map[state]bool{}
	type item struct {
		b      *ssa.BasicBlock
		from   int
		synced bool
	}
	queue := []item{{s.Block(), indexIn(s) + 1, false}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		synced := it.synced
		killed := false
		for i := it.from; i < len(it.b.Instrs); i++ {
			ins := it.b.Instrs[i]
			if ins == def {
				killed = true
				break
			}
			if _, isPhi := ins.(*ssa.Phi); !isPhi {
				if _, dbg := ins.(*ssa.DebugRef); !dbg && usesValue(ins, w) {
					u := use{at: ins, pos: posOf(ins, s), synced: synced}
					if !synced {
						return u, true
					}
					if found == nil {
						found = &u
					}
					killed = true
					break
				}
			}
			if isSync(ins) {
				synced = true
			}
		}
		if killed {
			continue
		}
		for _, succ := range it.b.Succs {
			// A phi in the successor reads w on the edge from this block.
			for pi, pred := range succ.Preds {
				if pred != it.b {
					continue
				}
				for _, ins := range succ.Instrs {
					phi, ok := ins.(*ssa.Phi)
					if !ok {
						break
					}
					if phi.Edges[pi] == w {
						u := use{at: phi, pos: posOf(lastInstr(it.b), s), synced: synced}
						if !synced {
							return u, true
						}
						if found == nil {
							found = &u
						}
					}
				}
			}
			st := state{succ, synced}
			if !visited[st] {
				visited[st] = true
				queue = append(queue, item{succ, 0, synced})
			}
		}
	}
	if found != nil {
		return *found, true
	}
	return use{}, false
}

func lastInstr(b *ssa.BasicBlock) ssa.Instruction { return b.Instrs[len(b.Instrs)-1] }

// posOf finds a source position for ins: its own, else the next positioned instruction in its block,
// else fallback's.
func posOf(ins ssa.Instruction, fallback ssa.Instruction) token.Pos {
	if p := ins.Pos(); p.IsValid() {
		return p
	}
	if b := ins.Block(); b != nil {
		for _, x := range b.Instrs[indexIn(ins)+1:] {
			if p := x.Pos(); p.IsValid() {
				return p
			}
		}
	}
	return fallback.Pos()
}

// finalCells returns the captured variables that hold a shareable value and are never written after
// a closure captures them: sharing such a variable is sharing an immutable value.
func finalCells(st *state, fn *ssa.Function) map[*ssa.Alloc]bool {
	out := map[*ssa.Alloc]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			a, ok := ins.(*ssa.Alloc)
			if !ok || !a.Heap {
				continue
			}
			elem := a.Type().(*types.Pointer).Elem()
			if !st.cls.Shareable(elem) {
				continue
			}
			if cellIsFinal(a) {
				out[a] = true
			}
		}
	}
	return out
}

func cellIsFinal(a *ssa.Alloc) bool {
	var captures []*ssa.MakeClosure
	var stores []*ssa.Store
	for _, r := range *a.Referrers() {
		switch r := r.(type) {
		case *ssa.MakeClosure:
			captures = append(captures, r)
			for j, bnd := range r.Bindings {
				if bnd == a && freeVarWritten(r.Fn.(*ssa.Function).FreeVars[j]) {
					return false
				}
			}
		case *ssa.Store:
			if r.Addr != a {
				return false
			}
			stores = append(stores, r)
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return false
			}
		case *ssa.DebugRef:
		default:
			// The variable's address goes somewhere that may write through it.
			return false
		}
	}
	if len(captures) == 0 {
		return false
	}
	for _, mc := range captures {
		after := reachableAfter(mc)
		for _, s := range stores {
			if after(s) {
				return false
			}
		}
	}
	return true
}

func freeVarWritten(fv *ssa.FreeVar) bool {
	for _, r := range *fv.Referrers() {
		switch r := r.(type) {
		case *ssa.UnOp:
			if r.Op != token.MUL {
				return true
			}
		case *ssa.MakeClosure:
			for j, bnd := range r.Bindings {
				if bnd == fv && freeVarWritten(r.Fn.(*ssa.Function).FreeVars[j]) {
					return true
				}
			}
		case *ssa.DebugRef:
		default:
			return true
		}
	}
	return false
}

// closureIsGoroutineBody reports whether fn is an anonymous function whose every closure value is
// started directly by a go statement. Such a closure owns what it captured: the parent's transfer
// check proves the parent no longer touches it.
func closureIsGoroutineBody(fn *ssa.Function) bool {
	parent := fn.Parent()
	if parent == nil || len(fn.FreeVars) == 0 {
		return false
	}
	seen := false
	for _, b := range parent.Blocks {
		for _, ins := range b.Instrs {
			mc, ok := ins.(*ssa.MakeClosure)
			if !ok || mc.Fn != fn {
				continue
			}
			seen = true
			for _, r := range *mc.Referrers() {
				switch r := r.(type) {
				case *ssa.Go:
					if r.Call.Value != mc {
						return false
					}
				case *ssa.DebugRef:
				default:
					return false
				}
			}
		}
	}
	return seen
}
